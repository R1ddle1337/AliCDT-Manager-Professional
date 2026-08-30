package dnsprovider

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

type aliyunProvider struct {
	cfg    Config
	client *http.Client
}

func NewAliyun(cfg Config) Provider {
	if cfg.Endpoint == "" {
		cfg.Endpoint = "https://alidns.aliyuncs.com/"
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &aliyunProvider{cfg: cfg, client: client}
}

func (p *aliyunProvider) Type() string               { return "aliyun" }
func (p *aliyunProvider) Capabilities() Capabilities { return Capabilities{} }

func (p *aliyunProvider) Test(ctx context.Context) error {
	_, err := p.call(ctx, "DescribeDomainRecords", map[string]string{"DomainName": p.cfg.Zone, "PageSize": "1", "PageNumber": "1"})
	return err
}

func (p *aliyunProvider) ListRecords(ctx context.Context, zone, name, recordType string) ([]Record, error) {
	if strings.TrimSpace(zone) == "" {
		zone = p.cfg.Zone
	}
	params := map[string]string{"DomainName": zone, "PageSize": "500", "PageNumber": "1"}
	if name != "" {
		params["RRKeyWord"] = relativeName(name, zone)
	}
	if recordType != "" {
		params["TypeKeyWord"] = strings.ToUpper(recordType)
	}
	raw, err := p.call(ctx, "DescribeDomainRecords", params)
	if err != nil {
		return nil, err
	}
	return parseAliyunRecords(raw), nil
}

func (p *aliyunProvider) EnsureRecords(ctx context.Context, zone string, scopes []RecordScope, desired []DesiredRecord) (SyncResult, error) {
	if strings.TrimSpace(zone) == "" {
		zone = p.cfg.Zone
	}
	existing, err := p.ListRecords(ctx, zone, "", "")
	if err != nil {
		return SyncResult{}, err
	}
	managed := make(map[string]Record)
	for _, record := range existing {
		if !inScopes(record, zone, scopes) {
			continue
		}
		managed[recordKey(record.Name, record.Type, record.Value)] = record
	}
	result := SyncResult{Records: make([]Record, 0, len(desired))}
	for _, item := range desired {
		item.Type = strings.ToUpper(strings.TrimSpace(item.Type))
		item.Name = strings.TrimSpace(item.Name)
		item.Value = strings.TrimSpace(item.Value)
		if item.Type == "" || item.Name == "" || item.Value == "" {
			return result, errors.New("DNS record name, type and value are required")
		}
		if item.TTL <= 0 {
			item.TTL = 60
		}
		if item.ProviderRecordID != "" {
			if err := p.updateRecord(ctx, item.ProviderRecordID, relativeName(item.Name, zone), item.Type, item.Value, item.TTL); err != nil {
				return result, err
			}
			result.Updated++
			result.Records = append(result.Records, Record{ID: item.ProviderRecordID, Name: fqdn(item.Name, zone), Type: item.Type, Value: item.Value, TTL: item.TTL})
			continue
		}
		key := recordKey(fqdn(item.Name, zone), item.Type, item.Value)
		if current, ok := managed[key]; ok {
			if current.TTL != item.TTL {
				if err := p.updateRecord(ctx, current.ID, relativeName(item.Name, zone), item.Type, item.Value, item.TTL); err != nil {
					return result, err
				}
				result.Updated++
			} else {
				result.Skipped++
			}
			result.Records = append(result.Records, Record{ID: current.ID, Name: fqdn(item.Name, zone), Type: item.Type, Value: item.Value, TTL: item.TTL})
			continue
		}
		id, err := p.addRecord(ctx, zone, relativeName(item.Name, zone), item.Type, item.Value, item.TTL)
		if err != nil {
			return result, err
		}
		result.Created++
		result.Records = append(result.Records, Record{ID: id, Name: fqdn(item.Name, zone), Type: item.Type, Value: item.Value, TTL: item.TTL})
	}
	return result, nil
}

func (p *aliyunProvider) DeleteRecord(ctx context.Context, id, _ string) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("aliyun record id is required")
	}
	_, err := p.call(ctx, "DeleteDomainRecord", map[string]string{"RecordId": id})
	return err
}

func (p *aliyunProvider) addRecord(ctx context.Context, zone, rr, recordType, value string, ttl int) (string, error) {
	raw, err := p.call(ctx, "AddDomainRecord", map[string]string{"DomainName": zone, "RR": rr, "Type": recordType, "Value": value, "TTL": fmt.Sprint(ttl)})
	if err != nil {
		return "", err
	}
	var response struct {
		RecordID string `json:"RecordId"`
	}
	b, _ := json.Marshal(raw)
	_ = json.Unmarshal(b, &response)
	return response.RecordID, nil
}

func (p *aliyunProvider) updateRecord(ctx context.Context, id, rr, recordType, value string, ttl int) error {
	_, err := p.call(ctx, "UpdateDomainRecord", map[string]string{"RecordId": id, "RR": rr, "Type": recordType, "Value": value, "TTL": fmt.Sprint(ttl)})
	return err
}

func (p *aliyunProvider) call(ctx context.Context, action string, extra map[string]string) (map[string]interface{}, error) {
	if strings.TrimSpace(p.cfg.AccessKeyID) == "" || strings.TrimSpace(p.cfg.AccessKeySecret) == "" {
		return nil, errors.New("aliyun access key id and secret are required")
	}
	params := map[string]string{
		"AccessKeyId": p.cfg.AccessKeyID, "Action": action, "Format": "JSON",
		"SignatureMethod": "HMAC-SHA1", "SignatureNonce": nonce(),
		"SignatureVersion": "1.0", "Timestamp": time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		"Version": "2015-01-09",
	}
	for key, value := range extra {
		params[key] = value
	}
	params["Signature"] = aliyunSignature(params, p.cfg.AccessKeySecret)
	query := url.Values{}
	for key, value := range params {
		query.Set(key, value)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.cfg.Endpoint+"?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("aliyun DNS HTTP status %d", resp.StatusCode)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	if code, ok := raw["Code"].(string); ok && code != "" {
		return nil, fmt.Errorf("aliyun DNS API [%s]: %v", code, raw["Message"])
	}
	return raw, nil
}

func parseAliyunRecords(raw map[string]interface{}) []Record {
	container, _ := raw["DomainRecords"].(map[string]interface{})
	items := container["Record"]
	if items == nil {
		return []Record{}
	}
	encoded, _ := json.Marshal(items)
	var list []struct {
		ID     string `json:"RecordId"`
		RR     string `json:"RR"`
		Domain string `json:"DomainName"`
		Type   string `json:"Type"`
		Value  string `json:"Value"`
		TTL    int    `json:"TTL"`
	}
	if len(encoded) > 0 && encoded[0] == '[' {
		_ = json.Unmarshal(encoded, &list)
	} else {
		var one struct {
			ID     string `json:"RecordId"`
			RR     string `json:"RR"`
			Domain string `json:"DomainName"`
			Type   string `json:"Type"`
			Value  string `json:"Value"`
			TTL    int    `json:"TTL"`
		}
		if json.Unmarshal(encoded, &one) == nil {
			list = append(list, one)
		}
	}
	result := make([]Record, 0, len(list))
	for _, item := range list {
		name := item.RR
		if item.Domain != "" {
			name = fqdn(item.RR, item.Domain)
		}
		result = append(result, Record{ID: item.ID, Name: name, Type: strings.ToUpper(item.Type), Value: item.Value, TTL: item.TTL})
	}
	return result
}

func aliyunSignature(params map[string]string, secret string) string {
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	canonical := make([]string, 0, len(keys))
	for _, key := range keys {
		canonical = append(canonical, percentEncode(key)+"="+percentEncode(params[key]))
	}
	stringToSign := "GET&%2F&" + percentEncode(strings.Join(canonical, "&"))
	mac := hmac.New(sha1.New, []byte(secret+"&"))
	_, _ = mac.Write([]byte(stringToSign))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func nonce() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprint(time.Now().UnixNano())
	}
	return fmt.Sprintf("%x", b)
}

func percentEncode(value string) string {
	return strings.NewReplacer("+", "%20", "*", "%2A", "%7E", "~").Replace(url.QueryEscape(value))
}
func relativeName(name, zone string) string {
	name = strings.TrimSuffix(strings.TrimSpace(name), ".")
	zone = strings.TrimSuffix(strings.TrimSpace(zone), ".")
	if name == zone {
		return "@"
	}
	suffix := "." + zone
	if strings.HasSuffix(name, suffix) {
		return strings.TrimSuffix(strings.TrimSuffix(name, suffix), ".")
	}
	return name
}
func recordKey(name, recordType, value string) string {
	return strings.ToLower(strings.TrimSuffix(name, ".")) + "|" + strings.ToUpper(recordType) + "|" + strings.TrimSpace(value)
}
