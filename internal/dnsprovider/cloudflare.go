package dnsprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type cloudflareProvider struct {
	cfg    Config
	client *http.Client
}

func NewCloudflare(cfg Config) Provider {
	if cfg.Endpoint == "" {
		cfg.Endpoint = "https://api.cloudflare.com/client/v4"
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &cloudflareProvider{cfg: cfg, client: client}
}

func (p *cloudflareProvider) Type() string                   { return "cloudflare" }
func (p *cloudflareProvider) Capabilities() Capabilities     { return Capabilities{} }
func (p *cloudflareProvider) Test(ctx context.Context) error { _, err := p.zoneID(ctx); return err }

func (p *cloudflareProvider) ListRecords(ctx context.Context, zone, name, recordType string) ([]Record, error) {
	zid, err := p.zoneID(ctx)
	if err != nil {
		return nil, err
	}
	path := fmt.Sprintf("%s/zones/%s/dns_records?per_page=100", strings.TrimRight(p.cfg.Endpoint, "/"), zid)
	if name != "" {
		path += "&name=" + urlQuery(name)
	}
	if recordType != "" {
		path += "&type=" + urlQuery(strings.ToUpper(recordType))
	}
	var response struct {
		Success bool `json:"success"`
		Errors  []struct {
			Message string `json:"message"`
		} `json:"errors"`
		Result []struct {
			ID, Name, Type, Content string
			TTL                     int
		} `json:"result"`
	}
	if err := p.request(ctx, http.MethodGet, path, nil, &response); err != nil {
		return nil, err
	}
	result := make([]Record, 0, len(response.Result))
	for _, item := range response.Result {
		result = append(result, Record{ID: item.ID, Name: item.Name, Type: item.Type, Value: item.Content, TTL: item.TTL})
	}
	return result, nil
}

func (p *cloudflareProvider) EnsureRecords(ctx context.Context, zone string, scopes []RecordScope, desired []DesiredRecord) (SyncResult, error) {
	if strings.TrimSpace(zone) == "" {
		zone = p.cfg.Zone
	}
	zid, err := p.zoneID(ctx)
	if err != nil {
		return SyncResult{}, err
	}
	existing, err := p.ListRecords(ctx, zone, "", "")
	if err != nil {
		return SyncResult{}, err
	}
	managed := make(map[string]Record)
	for _, record := range existing {
		if inScopes(record, zone, scopes) {
			managed[recordKey(record.Name, record.Type, record.Value)] = record
		}
	}
	result := SyncResult{Records: make([]Record, 0, len(desired))}
	for _, item := range desired {
		item.Name = fqdn(item.Name, zone)
		item.Type = strings.ToUpper(strings.TrimSpace(item.Type))
		item.Value = strings.TrimSpace(item.Value)
		if item.TTL <= 0 {
			item.TTL = 60
		}
		if item.ProviderRecordID != "" {
			if err := p.update(ctx, item.ProviderRecordID, item); err != nil {
				// Provider-side records can be removed manually or by a prior
				// cleanup. Do not let one stale ID abort reconciliation for the
				// whole pool: fall through to the normal name/value lookup and
				// create a replacement when necessary.
				if !isCloudflareRecordNotFound(err) {
					return result, err
				}
			} else {
				result.Updated++
				result.Records = append(result.Records, Record{ID: item.ProviderRecordID, Name: item.Name, Type: item.Type, Value: item.Value, TTL: item.TTL})
				continue
			}
		}
		key := recordKey(item.Name, item.Type, item.Value)
		if current, ok := managed[key]; ok {
			if current.TTL != item.TTL {
				if err := p.update(ctx, current.ID, item); err != nil {
					return result, err
				}
				result.Updated++
			} else {
				result.Skipped++
			}
			result.Records = append(result.Records, Record{ID: current.ID, Name: item.Name, Type: item.Type, Value: item.Value, TTL: item.TTL})
			continue
		}
		created, err := p.create(ctx, zid, item)
		if err != nil {
			return result, err
		}
		result.Created++
		result.Records = append(result.Records, created)
	}
	return result, nil
}

func (p *cloudflareProvider) DeleteRecord(ctx context.Context, id, _ string) error {
	zid, err := p.zoneID(ctx)
	if err != nil {
		return err
	}
	err = p.request(ctx, http.MethodDelete, fmt.Sprintf("%s/zones/%s/dns_records/%s", strings.TrimRight(p.cfg.Endpoint, "/"), zid, id), nil, &struct {
		Success bool `json:"success"`
	}{})
	// A managed record may have been removed out-of-band (or by a previous
	// reconciliation attempt). Treat Cloudflare's record-not-found response as
	// success so one stale row cannot block synchronization of every other row.
	if isCloudflareRecordNotFound(err) {
		return nil
	}
	return err
}

func isCloudflareRecordNotFound(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "cloudflare dns http status 404") &&
		(strings.Contains(message, "81044") || strings.Contains(message, "record does not exist"))
}

func (p *cloudflareProvider) zoneID(ctx context.Context) (string, error) {
	if strings.TrimSpace(p.cfg.ZoneID) != "" {
		return strings.TrimSpace(p.cfg.ZoneID), nil
	}
	var response struct {
		Success bool `json:"success"`
		Errors  []struct {
			Message string `json:"message"`
		} `json:"errors"`
		Result []struct{ ID, Name string } `json:"result"`
	}
	path := strings.TrimRight(p.cfg.Endpoint, "/") + "/zones?name=" + urlQuery(p.cfg.Zone) + "&per_page=1"
	if err := p.request(ctx, http.MethodGet, path, nil, &response); err != nil {
		return "", err
	}
	if len(response.Result) == 0 {
		return "", errors.New("cloudflare zone not found")
	}
	return response.Result[0].ID, nil
}
func (p *cloudflareProvider) create(ctx context.Context, zid string, item DesiredRecord) (Record, error) {
	var response struct {
		Result struct {
			ID, Name, Type, Content string
			TTL                     int
		} `json:"result"`
	}
	payload := map[string]interface{}{"type": item.Type, "name": item.Name, "content": item.Value, "ttl": item.TTL, "proxied": false}
	path := fmt.Sprintf("%s/zones/%s/dns_records", strings.TrimRight(p.cfg.Endpoint, "/"), zid)
	if err := p.request(ctx, http.MethodPost, path, payload, &response); err != nil {
		return Record{}, err
	}
	return Record{ID: response.Result.ID, Name: response.Result.Name, Type: response.Result.Type, Value: response.Result.Content, TTL: response.Result.TTL}, nil
}
func (p *cloudflareProvider) update(ctx context.Context, id string, item DesiredRecord) error {
	zid, err := p.zoneID(ctx)
	if err != nil {
		return err
	}
	payload := map[string]interface{}{"type": item.Type, "name": fqdn(item.Name, p.cfg.Zone), "content": item.Value, "ttl": item.TTL, "proxied": false}
	return p.request(ctx, http.MethodPut, fmt.Sprintf("%s/zones/%s/dns_records/%s", strings.TrimRight(p.cfg.Endpoint, "/"), zid, id), payload, &struct {
		Success bool `json:"success"`
	}{})
}
func (p *cloudflareProvider) request(ctx context.Context, method, path string, payload interface{}, out interface{}) error {
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(ctx, method, path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.cfg.APIToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("cloudflare DNS HTTP status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var envelope struct {
		Success bool `json:"success"`
		Errors  []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return err
	}
	if !envelope.Success {
		if len(envelope.Errors) > 0 {
			return errors.New(envelope.Errors[0].Message)
		}
		return errors.New("cloudflare DNS API request failed")
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}
func urlQuery(value string) string { return url.QueryEscape(value) }

func inScopes(record Record, zone string, scopes []RecordScope) bool {
	for _, scope := range scopes {
		if strings.EqualFold(record.Type, scope.Type) && strings.EqualFold(strings.TrimSuffix(record.Name, "."), strings.TrimSuffix(fqdn(scope.Name, zone), ".")) {
			return true
		}
	}
	return false
}
