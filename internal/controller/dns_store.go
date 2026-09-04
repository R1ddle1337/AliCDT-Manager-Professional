package controller

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/dnsprovider"
)

func (s *Store) ListDNSProviders(ctx context.Context) ([]DNSProvider, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,type,zone,zone_id,endpoint,access_key_id,access_key_secret,api_token,enabled,last_test_at,last_error,created_at,updated_at FROM dns_providers ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]DNSProvider, 0)
	for rows.Next() {
		var item DNSProvider
		var secret, token string
		var enabled int
		var lastTest, created, updated sql.NullString
		if err := rows.Scan(&item.ID, &item.Name, &item.Type, &item.Zone, &item.ZoneID, &item.Endpoint, &item.AccessKeyID, &secret, &token, &enabled, &lastTest, &item.LastError, &created, &updated); err != nil {
			return nil, err
		}
		item.Enabled = enabled != 0
		item.SecretConfigured = strings.TrimSpace(secret) != ""
		item.TokenConfigured = strings.TrimSpace(token) != ""
		item.CreatedAt = parseDatabaseTime(created.String)
		item.UpdatedAt = parseDatabaseTime(updated.String)
		if lastTest.Valid {
			parsed := parseDatabaseTime(lastTest.String)
			item.LastTestAt = &parsed
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) GetDNSProvider(ctx context.Context, id string) (DNSProvider, error) {
	var item DNSProvider
	var secret, token string
	var enabled int
	var lastTest, created, updated sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id,name,type,zone,zone_id,endpoint,access_key_id,access_key_secret,api_token,enabled,last_test_at,last_error,created_at,updated_at FROM dns_providers WHERE id=?`, id).
		Scan(&item.ID, &item.Name, &item.Type, &item.Zone, &item.ZoneID, &item.Endpoint, &item.AccessKeyID, &secret, &token, &enabled, &lastTest, &item.LastError, &created, &updated)
	if err != nil {
		return DNSProvider{}, err
	}
	item.Enabled = enabled != 0
	item.SecretConfigured = strings.TrimSpace(secret) != ""
	item.TokenConfigured = strings.TrimSpace(token) != ""
	item.CreatedAt = parseDatabaseTime(created.String)
	item.UpdatedAt = parseDatabaseTime(updated.String)
	if lastTest.Valid {
		parsed := parseDatabaseTime(lastTest.String)
		item.LastTestAt = &parsed
	}
	return item, nil
}

func (s *Store) dnsProviderConfig(ctx context.Context, id string) (dnsprovider.Config, error) {
	var cfg dnsprovider.Config
	err := s.db.QueryRowContext(ctx, `SELECT type,zone,zone_id,endpoint,access_key_id,access_key_secret,api_token,api_email FROM dns_providers WHERE id=?`, id).
		Scan(&cfg.Type, &cfg.Zone, &cfg.ZoneID, &cfg.Endpoint, &cfg.AccessKeyID, &cfg.AccessKeySecret, &cfg.APIToken, &cfg.APIEmail)
	if err != nil {
		return dnsprovider.Config{}, err
	}
	return cfg, nil
}

// ValidateDNSProviderRequest performs a real read-only request against the
// vendor before credentials are persisted. Existing secrets are merged for
// edits so leaving a secret field blank keeps the last working credential.
func (s *Store) ValidateDNSProviderRequest(ctx context.Context, existingID string, request CreateDNSProviderRequest) error {
	var existing dnsprovider.Config
	var err error
	if existingID != "" {
		existing, err = s.dnsProviderConfig(ctx, existingID)
		if err != nil {
			return err
		}
	}
	cfg := dnsprovider.Config{
		Type: strings.ToLower(strings.TrimSpace(request.Type)), Zone: strings.TrimSuffix(strings.TrimSpace(request.Zone), "."),
		ZoneID: strings.TrimSpace(request.ZoneID), Endpoint: strings.TrimSpace(request.Endpoint), AccessKeyID: strings.TrimSpace(request.AccessKeyID),
		AccessKeySecret: strings.TrimSpace(request.AccessKeySecret), APIToken: strings.TrimSpace(request.APIToken), APIEmail: strings.TrimSpace(request.APIEmail),
	}
	if cfg.Type == "" {
		cfg.Type = existing.Type
	}
	if cfg.Zone == "" {
		cfg.Zone = existing.Zone
	}
	if cfg.ZoneID == "" {
		cfg.ZoneID = existing.ZoneID
	}
	if cfg.AccessKeyID == "" {
		cfg.AccessKeyID = existing.AccessKeyID
	}
	if cfg.AccessKeySecret == "" {
		cfg.AccessKeySecret = existing.AccessKeySecret
	}
	if cfg.APIToken == "" {
		cfg.APIToken = existing.APIToken
	}
	if cfg.APIEmail == "" {
		cfg.APIEmail = existing.APIEmail
	}
	provider, err := dnsprovider.New(cfg)
	if err != nil {
		return err
	}
	return provider.Test(ctx)
}

func normalizeDNSProviderRequest(request CreateDNSProviderRequest, secretOptional bool) (CreateDNSProviderRequest, bool, error) {
	request.Name = strings.TrimSpace(request.Name)
	request.Type = strings.ToLower(strings.TrimSpace(request.Type))
	request.Zone = strings.TrimSuffix(strings.TrimSpace(request.Zone), ".")
	request.ZoneID = strings.TrimSpace(request.ZoneID)
	request.Endpoint = strings.TrimSpace(request.Endpoint)
	request.AccessKeyID = strings.TrimSpace(request.AccessKeyID)
	request.APIEmail = strings.TrimSpace(request.APIEmail)
	request.APIToken = strings.TrimSpace(request.APIToken)
	if request.Name == "" || request.Zone == "" {
		return request, false, errors.New("provider name and DNS zone are required")
	}
	if !oneOf(request.Type, "aliyun", "cloudflare") {
		return request, false, errors.New("DNS provider must be aliyun or cloudflare")
	}
	if request.Type == "aliyun" && request.AccessKeyID == "" && !secretOptional {
		return request, false, errors.New("aliyun AccessKey ID is required")
	}
	if request.Type == "aliyun" && strings.TrimSpace(request.AccessKeySecret) == "" && !secretOptional {
		return request, false, errors.New("aliyun AccessKey Secret is required")
	}
	if request.Type == "cloudflare" && request.APIToken == "" && !secretOptional {
		return request, false, errors.New("cloudflare API token is required")
	}
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	return request, enabled, nil
}

func (s *Store) CreateDNSProvider(ctx context.Context, request CreateDNSProviderRequest) (DNSProvider, error) {
	request, enabled, err := normalizeDNSProviderRequest(request, false)
	if err != nil {
		return DNSProvider{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	id := randomID("dns")
	_, err = s.db.ExecContext(ctx, `INSERT INTO dns_providers(id,name,type,zone,zone_id,endpoint,access_key_id,access_key_secret,api_token,api_email,enabled,last_error,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, request.Name, request.Type, request.Zone, request.ZoneID, request.Endpoint, request.AccessKeyID, request.AccessKeySecret, request.APIToken, request.APIEmail, boolInt(enabled), "", now, now)
	if err != nil {
		return DNSProvider{}, err
	}
	return s.GetDNSProvider(ctx, id)
}

func (s *Store) UpdateDNSProvider(ctx context.Context, id string, request CreateDNSProviderRequest) (DNSProvider, error) {
	var oldSecret, oldToken, oldType, oldAccessKeyID, oldZone, oldZoneID string
	if err := s.db.QueryRowContext(ctx, `SELECT type,zone,zone_id,access_key_id,access_key_secret,api_token FROM dns_providers WHERE id=?`, id).Scan(&oldType, &oldZone, &oldZoneID, &oldAccessKeyID, &oldSecret, &oldToken); err != nil {
		return DNSProvider{}, err
	}
	request.Type = strings.ToLower(strings.TrimSpace(request.Type))
	if request.Type == "" {
		request.Type = oldType
	}
	if request.AccessKeyID == "" {
		request.AccessKeyID = oldAccessKeyID
	}
	if request.ZoneID == "" {
		request.ZoneID = oldZoneID
	}
	if request.AccessKeySecret == "" {
		request.AccessKeySecret = oldSecret
	}
	if request.APIToken == "" {
		request.APIToken = oldToken
	}
	request, enabled, err := normalizeDNSProviderRequest(request, true)
	if err != nil {
		return DNSProvider{}, err
	}
	if request.Type == "aliyun" && (request.AccessKeyID == "" || request.AccessKeySecret == "") {
		return DNSProvider{}, errors.New("aliyun Access Key ID and Secret are required")
	}
	if request.Type == "cloudflare" && request.APIToken == "" {
		return DNSProvider{}, errors.New("cloudflare API token is required")
	}
	if request.Type != oldType || request.Zone != oldZone {
		var managedCount int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM dns_managed_records WHERE provider_id=?`, id).Scan(&managedCount); err != nil {
			return DNSProvider{}, err
		}
		if managedCount > 0 {
			return DNSProvider{}, errors.New("provider type or zone cannot change while managed records exist")
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = s.db.ExecContext(ctx, `UPDATE dns_providers SET name=?,type=?,zone=?,zone_id=?,endpoint=?,access_key_id=?,access_key_secret=?,api_token=?,api_email=?,enabled=?,updated_at=?,last_error='' WHERE id=?`, request.Name, request.Type, request.Zone, request.ZoneID, request.Endpoint, request.AccessKeyID, request.AccessKeySecret, request.APIToken, request.APIEmail, boolInt(enabled), now, id)
	if err != nil {
		return DNSProvider{}, err
	}
	return s.GetDNSProvider(ctx, id)
}

func (s *Store) DeleteDNSProvider(ctx context.Context, id string) error {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM dns_managed_records WHERE provider_id=?`, id).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return errors.New("remove managed DNS records before deleting this provider")
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM dns_providers WHERE id=?`, id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) MarkDNSProviderTest(ctx context.Context, id string, testErr error) error {
	message := ""
	if testErr != nil {
		message = testErr.Error()
	}
	_, err := s.db.ExecContext(ctx, `UPDATE dns_providers SET last_test_at=?,last_error=?,updated_at=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339Nano), message, time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}

func (s *Store) ListDNSRecords(ctx context.Context, providerID string) ([]DNSManagedRecord, error) {
	query := `SELECT id,provider_id,COALESCE(pool_id,''),COALESCE(relay_node_id,''),name,type,value,ttl,enabled,desired_enabled,provider_record_id,status,last_error,last_synced_at,created_at,updated_at FROM dns_managed_records`
	args := []interface{}{}
	if strings.TrimSpace(providerID) != "" {
		query += ` WHERE provider_id=?`
		args = append(args, providerID)
	}
	query += ` ORDER BY name,type,value`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]DNSManagedRecord, 0)
	for rows.Next() {
		var item DNSManagedRecord
		var enabled, desiredEnabled int
		var synced, created, updated sql.NullString
		if err := rows.Scan(&item.ID, &item.ProviderID, &item.PoolID, &item.RelayNodeID, &item.Name, &item.Type, &item.Value, &item.TTL, &enabled, &desiredEnabled, &item.ProviderRecordID, &item.Status, &item.LastError, &synced, &created, &updated); err != nil {
			return nil, err
		}
		item.Enabled = enabled != 0
		item.DesiredEnabled = desiredEnabled != 0
		item.CreatedAt = parseDatabaseTime(created.String)
		item.UpdatedAt = parseDatabaseTime(updated.String)
		if synced.Valid {
			parsed := parseDatabaseTime(synced.String)
			item.LastSyncedAt = &parsed
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) CreateDNSRecord(ctx context.Context, request CreateDNSRecordRequest) (DNSManagedRecord, error) {
	request.ProviderID = strings.TrimSpace(request.ProviderID)
	request.Name = strings.TrimSpace(request.Name)
	request.Type = strings.ToUpper(strings.TrimSpace(request.Type))
	request.Value = strings.TrimSpace(request.Value)
	if request.Type == "" {
		request.Type = "A"
	}
	if request.TTL <= 0 {
		request.TTL = 60
	}
	if request.ProviderID == "" || request.Name == "" {
		return DNSManagedRecord{}, errors.New("provider and record name are required")
	}
	relayNodeIDs := normalizeDNSRelayNodeIDs(request)
	type recordValue struct {
		relayNodeID string
		value       string
	}
	valueCapacity := len(relayNodeIDs)
	if valueCapacity == 0 {
		valueCapacity = 1
	}
	values := make([]recordValue, 0, valueCapacity)
	if len(relayNodeIDs) > 0 {
		if !oneOf(request.Type, "A", "AAAA") {
			return DNSManagedRecord{}, errors.New("a relay agent source requires an A or AAAA record")
		}
		seenValues := make(map[string]string, len(relayNodeIDs))
		for _, relayNodeID := range relayNodeIDs {
			var publicIP string
			if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(public_ip,'') FROM relay_nodes WHERE id=?`, relayNodeID).Scan(&publicIP); err != nil {
				return DNSManagedRecord{}, fmt.Errorf("relay node %q was not found", relayNodeID)
			}
			publicIP = strings.TrimSpace(publicIP)
			if publicIP == "" {
				return DNSManagedRecord{}, fmt.Errorf("relay node %q has not reported a public IP", relayNodeID)
			}
			if other, duplicate := seenValues[publicIP]; duplicate {
				return DNSManagedRecord{}, fmt.Errorf("relay nodes %q and %q report the same public IP", other, relayNodeID)
			}
			seenValues[publicIP] = relayNodeID
			values = append(values, recordValue{relayNodeID: relayNodeID, value: publicIP})
		}
	} else if request.Value == "" {
		return DNSManagedRecord{}, errors.New("provider, record name and value are required")
	} else {
		values = append(values, recordValue{value: request.Value})
	}
	if !oneOf(request.Type, "A", "AAAA", "CNAME", "TXT") {
		return DNSManagedRecord{}, errors.New("supported DNS record types are A, AAAA, CNAME and TXT")
	}
	providerInfo, err := s.GetDNSProvider(ctx, request.ProviderID)
	if err != nil {
		return DNSManagedRecord{}, errors.New("DNS provider not found")
	}
	request.TTL, err = normalizeDNSProviderTTL(request.TTL, providerInfo.Type)
	if err != nil {
		return DNSManagedRecord{}, err
	}
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DNSManagedRecord{}, err
	}
	defer tx.Rollback()
	firstID := ""
	for _, item := range values {
		id := randomID("record")
		if _, err := tx.ExecContext(ctx, `INSERT INTO dns_managed_records(id,provider_id,relay_node_id,name,type,value,ttl,enabled,desired_enabled,status,last_error,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,'pending','',?,?)`, id, request.ProviderID, nullIfEmpty(item.relayNodeID), request.Name, request.Type, item.value, request.TTL, boolInt(enabled), boolInt(enabled), now, now); err != nil {
			return DNSManagedRecord{}, fmt.Errorf("create managed DNS record: %w", err)
		}
		if firstID == "" {
			firstID = id
		}
	}
	if err := tx.Commit(); err != nil {
		return DNSManagedRecord{}, err
	}
	return s.GetDNSRecord(ctx, firstID)
}

func normalizeDNSRelayNodeIDs(request CreateDNSRecordRequest) []string {
	result := make([]string, 0, len(request.RelayNodeIDs)+1)
	seen := make(map[string]bool, len(request.RelayNodeIDs)+1)
	for _, raw := range append(request.RelayNodeIDs, request.RelayNodeID) {
		id := strings.TrimSpace(raw)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		result = append(result, id)
	}
	return result
}

func (s *Store) UpdateDNSRecord(ctx context.Context, id string, request CreateDNSRecordRequest) (DNSManagedRecord, error) {
	var oldProviderID, oldPoolID string
	if err := s.db.QueryRowContext(ctx, `SELECT provider_id,COALESCE(pool_id,'') FROM dns_managed_records WHERE id=?`, id).Scan(&oldProviderID, &oldPoolID); err != nil {
		return DNSManagedRecord{}, err
	}
	if oldPoolID != "" {
		return DNSManagedRecord{}, errors.New("pool-managed DNS records must be changed from the relay pool")
	}
	request.RelayNodeID = strings.TrimSpace(request.RelayNodeID)
	request.Name = strings.TrimSpace(request.Name)
	request.Type = strings.ToUpper(strings.TrimSpace(request.Type))
	request.Value = strings.TrimSpace(request.Value)
	if request.Type == "" {
		request.Type = "A"
	}
	if request.TTL <= 0 {
		request.TTL = 60
	}
	if request.ProviderID == "" {
		return DNSManagedRecord{}, errors.New("provider is required")
	}
	if request.RelayNodeID != "" {
		if !oneOf(request.Type, "A", "AAAA") {
			return DNSManagedRecord{}, errors.New("a relay agent source requires an A or AAAA record")
		}
		var publicIP string
		if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(public_ip,'') FROM relay_nodes WHERE id=?`, request.RelayNodeID).Scan(&publicIP); err != nil {
			return DNSManagedRecord{}, errors.New("relay node not found")
		}
		request.Value = strings.TrimSpace(publicIP)
		if request.Value == "" {
			return DNSManagedRecord{}, errors.New("selected relay node has not reported a public IP")
		}
	} else if request.Value == "" {
		return DNSManagedRecord{}, errors.New("record value is required when no relay agent is selected")
	}
	if !oneOf(request.Type, "A", "AAAA", "CNAME", "TXT") {
		return DNSManagedRecord{}, errors.New("supported DNS record types are A, AAAA, CNAME and TXT")
	}
	providerInfo, err := s.GetDNSProvider(ctx, request.ProviderID)
	if err != nil {
		return DNSManagedRecord{}, errors.New("DNS provider not found")
	}
	request.TTL, err = normalizeDNSProviderTTL(request.TTL, providerInfo.Type)
	if err != nil {
		return DNSManagedRecord{}, err
	}
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	providerRecordID := ""
	if oldProviderID == request.ProviderID {
		_ = s.db.QueryRowContext(ctx, `SELECT provider_record_id FROM dns_managed_records WHERE id=?`, id).Scan(&providerRecordID)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE dns_managed_records SET provider_id=?,relay_node_id=?,name=?,type=?,value=?,ttl=?,enabled=?,desired_enabled=?,provider_record_id=?,status='pending',last_error='',updated_at=? WHERE id=?`, request.ProviderID, nullIfEmpty(request.RelayNodeID), request.Name, request.Type, request.Value, request.TTL, boolInt(enabled), boolInt(enabled), providerRecordID, now, id)
	if err != nil {
		return DNSManagedRecord{}, err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return DNSManagedRecord{}, sql.ErrNoRows
	}
	return s.GetDNSRecord(ctx, id)
}

func (s *Store) DeleteDNSRecord(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM dns_managed_records WHERE id=?`, id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) GetDNSRecord(ctx context.Context, id string) (DNSManagedRecord, error) {
	records, err := s.ListDNSRecords(ctx, "")
	if err != nil {
		return DNSManagedRecord{}, err
	}
	for _, item := range records {
		if item.ID == id {
			return item, nil
		}
	}
	return DNSManagedRecord{}, sql.ErrNoRows
}

func (s *Store) UpdateDNSRecordSync(ctx context.Context, id, providerRecordID, status, lastError string, synced bool) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if synced {
		_, err := s.db.ExecContext(ctx, `UPDATE dns_managed_records SET provider_record_id=?,status=?,last_error=?,last_synced_at=?,updated_at=? WHERE id=?`, providerRecordID, status, lastError, now, now, id)
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE dns_managed_records SET status=?,last_error=?,updated_at=? WHERE id=?`, status, lastError, now, id)
	return err
}

func (s *Store) DNSProvider(ctx context.Context, id string) (dnsprovider.Provider, error) {
	cfg, err := s.dnsProviderConfig(ctx, id)
	if err != nil {
		return nil, err
	}
	return dnsprovider.New(cfg)
}
