package controller

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/dnsprovider"
)

var dnsSyncMu sync.Mutex

// SyncDNSProvider reconciles only records declared in dns_managed_records. It
// never performs a broad zone cleanup, so existing unrelated DNS records are
// left untouched.
func (s *Store) SyncDNSProvider(ctx context.Context, providerID string) (dnsprovider.SyncResult, error) {
	if !dnsSyncMu.TryLock() {
		return dnsprovider.SyncResult{}, errors.New("DNS synchronization is already running")
	}
	defer dnsSyncMu.Unlock()
	provider, err := s.DNSProvider(ctx, providerID)
	if err != nil {
		return dnsprovider.SyncResult{}, err
	}
	providerInfo, err := s.GetDNSProvider(ctx, providerID)
	if err != nil {
		return dnsprovider.SyncResult{}, err
	}
	if !providerInfo.Enabled {
		return dnsprovider.SyncResult{}, errors.New("DNS provider is disabled")
	}
	records, err := s.ListDNSRecords(ctx, providerID)
	if err != nil {
		return dnsprovider.SyncResult{}, err
	}
	scopes := make([]dnsprovider.RecordScope, 0, len(records))
	desired := make([]dnsprovider.DesiredRecord, 0, len(records))
	deletedTombstones := 0
	for _, record := range records {
		scopes = append(scopes, dnsprovider.RecordScope{Name: record.Name, Type: record.Type})
		if record.Enabled {
			desired = append(desired, dnsprovider.DesiredRecord{Name: record.Name, Type: record.Type, Value: record.Value, TTL: record.TTL, ProviderRecordID: record.ProviderRecordID})
			continue
		}
		deleting := record.Status == "deleting" && record.PoolID == "" && !record.DesiredEnabled
		if record.ProviderRecordID == "" {
			if deleting {
				if _, deleteErr := s.db.ExecContext(ctx, `DELETE FROM dns_managed_records WHERE id=? AND status='deleting' AND COALESCE(pool_id,'')='' AND desired_enabled=0 AND NOT EXISTS(SELECT 1 FROM dns_managed_record_pools WHERE record_id=dns_managed_records.id)`, record.ID); deleteErr != nil {
					return dnsprovider.SyncResult{}, deleteErr
				}
				deletedTombstones++
			}
			continue
		}
		if deleteErr := provider.DeleteRecord(ctx, record.ProviderRecordID, record.Name); deleteErr != nil {
			if deleting {
				_, _ = s.db.ExecContext(ctx, `UPDATE dns_managed_records SET status='deleting',last_error=?,updated_at=? WHERE id=?`, deleteErr.Error(), time.Now().UTC().Format(time.RFC3339Nano), record.ID)
			} else {
				_ = s.UpdateDNSRecordSync(ctx, record.ID, record.ProviderRecordID, "error", deleteErr.Error(), false)
			}
			return dnsprovider.SyncResult{}, deleteErr
		}
		if deleting {
			if _, deleteErr := s.db.ExecContext(ctx, `DELETE FROM dns_managed_records WHERE id=? AND status='deleting' AND COALESCE(pool_id,'')='' AND desired_enabled=0 AND NOT EXISTS(SELECT 1 FROM dns_managed_record_pools WHERE record_id=dns_managed_records.id)`, record.ID); deleteErr != nil {
				return dnsprovider.SyncResult{}, deleteErr
			}
			deletedTombstones++
		} else {
			_ = s.UpdateDNSRecordSync(ctx, record.ID, "", "disabled", "", true)
		}
	}
	result, err := provider.EnsureRecords(ctx, providerInfo.Zone, scopes, desired)
	if err != nil {
		_ = s.MarkDNSProviderSync(ctx, providerID, err)
		for _, record := range records {
			if record.Status == "deleting" && record.PoolID == "" && !record.DesiredEnabled {
				_, _ = s.db.ExecContext(ctx, `UPDATE dns_managed_records SET status='deleting',desired_enabled=0,last_error=?,updated_at=? WHERE id=?`, err.Error(), time.Now().UTC().Format(time.RFC3339Nano), record.ID)
				continue
			}
			_ = s.UpdateDNSRecordSync(ctx, record.ID, record.ProviderRecordID, "error", err.Error(), false)
		}
		return result, err
	}
	byKey := make(map[string]dnsprovider.Record, len(result.Records))
	for _, record := range result.Records {
		byKey[recordKey(record.Name, record.Type, record.Value)] = record
	}
	for _, record := range records {
		if !record.Enabled {
			if record.Status == "deleting" && record.PoolID == "" && !record.DesiredEnabled {
				// The tombstone was removed above after the provider-side delete.
				continue
			}
			_ = s.UpdateDNSRecordSync(ctx, record.ID, "", "disabled", "", true)
			continue
		}
		providerRecord, ok := byKey[recordKey(dnsprovider.FQDN(record.Name, providerInfo.Zone), record.Type, record.Value)]
		if !ok {
			_ = s.UpdateDNSRecordSync(ctx, record.ID, record.ProviderRecordID, "error", "provider did not return the managed record", false)
			continue
		}
		_ = s.UpdateDNSRecordSync(ctx, record.ID, providerRecord.ID, "synced", "", true)
	}
	_ = s.MarkDNSProviderSync(ctx, providerID, nil)
	result.Deleted += deletedTombstones
	return result, nil
}

// cleanupDeletedPoolDNS removes provider-side records belonging only to a
// deleted pool. The database transaction intentionally completes before this
// network operation; a transient provider failure leaves a non-publishable
// `deleting` tombstone for the regular DNS scheduler to retry.
func (s *Store) cleanupDeletedPoolDNS(ctx context.Context, records []poolDNSCleanupRecord) {
	if len(records) == 0 {
		return
	}
	// A regular provider sync may already be in flight. Leave the tombstone for
	// that sync (or the next scheduler tick) instead of making a delete request
	// wait behind a potentially slow external API call.
	if !dnsSyncMu.TryLock() {
		return
	}
	defer dnsSyncMu.Unlock()

	providers := make(map[string]dnsprovider.Provider)
	for _, expected := range records {
		var providerID, providerRecordID, name, recordType, poolID, status string
		var desiredEnabled, hasPoolBinding int
		err := s.db.QueryRowContext(ctx, `SELECT provider_id,COALESCE(provider_record_id,''),name,type,COALESCE(pool_id,''),status,desired_enabled,EXISTS(SELECT 1 FROM dns_managed_record_pools WHERE record_id=dns_managed_records.id) FROM dns_managed_records WHERE id=?`, expected.ID).
			Scan(&providerID, &providerRecordID, &name, &recordType, &poolID, &status, &desiredEnabled, &hasPoolBinding)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil || poolID != "" || hasPoolBinding != 0 || status != "deleting" || desiredEnabled != 0 {
			continue
		}
		if providerRecordID == "" {
			_, _ = s.db.ExecContext(ctx, `DELETE FROM dns_managed_records WHERE id=? AND status='deleting' AND COALESCE(pool_id,'')='' AND desired_enabled=0 AND NOT EXISTS(SELECT 1 FROM dns_managed_record_pools WHERE record_id=dns_managed_records.id)`, expected.ID)
			continue
		}
		provider := providers[providerID]
		if provider == nil {
			provider, err = s.DNSProvider(ctx, providerID)
			if err != nil {
				_, _ = s.db.ExecContext(ctx, `UPDATE dns_managed_records SET status='deleting',last_error=?,updated_at=? WHERE id=?`, err.Error(), time.Now().UTC().Format(time.RFC3339Nano), expected.ID)
				continue
			}
			providers[providerID] = provider
		}
		if err := provider.DeleteRecord(ctx, providerRecordID, name); err != nil {
			_, _ = s.db.ExecContext(ctx, `UPDATE dns_managed_records SET status='deleting',last_error=?,updated_at=? WHERE id=?`, err.Error(), time.Now().UTC().Format(time.RFC3339Nano), expected.ID)
			continue
		}
		_, _ = s.db.ExecContext(ctx, `DELETE FROM dns_managed_records WHERE id=? AND status='deleting' AND COALESCE(pool_id,'')='' AND desired_enabled=0 AND NOT EXISTS(SELECT 1 FROM dns_managed_record_pools WHERE record_id=dns_managed_records.id)`, expected.ID)
	}
}

func (s *Store) SyncAllDNS(ctx context.Context) (map[string]dnsprovider.SyncResult, error) {
	providers, err := s.ListDNSProviders(ctx)
	if err != nil {
		return nil, err
	}
	result := make(map[string]dnsprovider.SyncResult)
	var firstErr error
	for _, provider := range providers {
		if !provider.Enabled {
			continue
		}
		item, syncErr := s.SyncDNSProvider(ctx, provider.ID)
		result[provider.ID] = item
		if syncErr != nil && firstErr == nil {
			firstErr = syncErr
		}
	}
	return result, firstErr
}

// RefreshRelayAgentDNSRecords derives the value and active state of records
// explicitly attached to a CDT Relay Agent. Pool records are handled by the
// pool reconciler and are intentionally skipped here.
func (s *Store) RefreshRelayAgentDNSRecords(ctx context.Context) error {
	records, err := s.ListDNSRecords(ctx, "")
	if err != nil {
		return err
	}
	for _, record := range records {
		if record.PoolID != "" || record.RelayNodeID == "" || record.Status == "deleting" {
			continue
		}
		var publicIP, status string
		var draining int
		err := s.db.QueryRowContext(ctx, `SELECT COALESCE(rn.public_ip,''),
			CASE WHEN ((COALESCE(a.protection_triggered,0)=1 OR COALESCE(a.protection_predictive,0)=1) AND (COALESCE(a.protection_mode,'alert_only')='drain_relay' OR EXISTS(
				SELECT 1 FROM relay_services ars JOIN relay_pools arp ON arp.id=ars.pool_id
				WHERE ars.relay_node_id=rn.id AND ars.enabled=1 AND COALESCE(arp.enabled,1)=1 AND COALESCE(arp.auto_drain,1)=1
			))) OR COALESCE(rn.update_status,'idle') IN ('draining','updating') THEN 'draining' ELSE rn.status END,
			CASE WHEN ((COALESCE(a.protection_triggered,0)=1 OR COALESCE(a.protection_predictive,0)=1) AND (COALESCE(a.protection_mode,'alert_only')='drain_relay' OR EXISTS(
				SELECT 1 FROM relay_services ars JOIN relay_pools arp ON arp.id=ars.pool_id
				WHERE ars.relay_node_id=rn.id AND ars.enabled=1 AND COALESCE(arp.enabled,1)=1 AND COALESCE(arp.auto_drain,1)=1
			))) OR COALESCE(rn.update_status,'idle') IN ('draining','updating') THEN 1 ELSE 0 END
			FROM relay_nodes rn LEFT JOIN accounts a ON a.id=rn.cloud_account_id OR (rn.cloud_account_id IS NULL AND rn.ecs_instance_id IN (SELECT instance_id FROM instances WHERE account_id=a.id)) WHERE rn.id=?`, record.RelayNodeID).Scan(&publicIP, &status, &draining)
		if err != nil {
			continue
		}
		active := record.DesiredEnabled && strings.EqualFold(status, "online") && draining == 0 && validRelayIP(publicIP)
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if publicIP != "" && publicIP != record.Value {
			_, err = s.db.ExecContext(ctx, `UPDATE dns_managed_records SET value=?,enabled=?,status=CASE WHEN enabled<>? OR value<>? THEN 'pending' ELSE status END,last_error='',updated_at=? WHERE id=?`, publicIP, boolInt(active), boolInt(active), publicIP, now, record.ID)
		} else {
			_, err = s.db.ExecContext(ctx, `UPDATE dns_managed_records SET enabled=?,status=CASE WHEN enabled<>? THEN 'pending' ELSE status END,last_error='',updated_at=? WHERE id=?`, boolInt(active), boolInt(active), now, record.ID)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) TestDNSProvider(ctx context.Context, id string) error {
	provider, err := s.DNSProvider(ctx, id)
	if err != nil {
		return err
	}
	err = provider.Test(ctx)
	_ = s.MarkDNSProviderTest(ctx, id, err)
	return err
}

func (s *Store) MarkDNSProviderSync(ctx context.Context, id string, syncErr error) error {
	message := ""
	if syncErr != nil {
		message = syncErr.Error()
	}
	_, err := s.db.ExecContext(ctx, `UPDATE dns_providers SET last_error=?,updated_at=? WHERE id=?`, message, time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}

func recordKey(name, recordType, value string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), ".")) + "|" + strings.ToUpper(strings.TrimSpace(recordType)) + "|" + strings.TrimSpace(value)
}

// RunDNSScheduler periodically reconciles managed records. Thirty seconds is
// short enough to make a protection/offline transition useful while still
// avoiding an unnecessarily aggressive provider polling loop; manual sync
// remains available from the UI.
func (s *Server) RunDNSScheduler(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	initialCtx, initialCancel := context.WithTimeout(ctx, 45*time.Second)
	_ = s.store.EnsureProtectionRevisions(initialCtx)
	_ = s.store.RefreshRelayAgentDNSRecords(initialCtx)
	_ = s.store.RefreshAllRelayPoolDNS(initialCtx)
	_, _ = s.store.SyncAllDNS(initialCtx)
	initialCancel()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			syncCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
			_ = s.store.EnsureProtectionRevisions(syncCtx)
			_ = s.store.RefreshRelayAgentDNSRecords(syncCtx)
			_ = s.store.RefreshAllRelayPoolDNS(syncCtx)
			_, _ = s.store.SyncAllDNS(syncCtx)
			cancel()
		}
	}
}
