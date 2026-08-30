package controller

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

func normalizeRelayPoolRequest(request CreateRelayPoolRequest) (CreateRelayPoolRequest, bool, error) {
	request.Name = strings.TrimSpace(request.Name)
	request.Hostname = strings.TrimSuffix(strings.TrimSpace(request.Hostname), ".")
	request.Network = strings.ToLower(strings.TrimSpace(request.Network))
	request.Mode = strings.ToLower(strings.TrimSpace(request.Mode))
	request.DNSProviderID = strings.TrimSpace(request.DNSProviderID)
	request.DNSRecordName = strings.TrimSpace(request.DNSRecordName)
	if request.DNSRecordName == "" {
		request.DNSRecordName = request.Hostname
	}
	if request.Name == "" || request.Hostname == "" || request.ListenPort < 1 || request.ListenPort > 65535 {
		return request, false, errors.New("name, hostname and a valid listen port are required")
	}
	if strings.ContainsAny(request.Hostname, "/ :\\") {
		return request, false, errors.New("hostname must be a DNS name without a scheme or port")
	}
	if !oneOf(request.Network, "tcp", "udp", "tcp+udp") || !oneOf(request.Mode, "failover", "round_robin", "ip_hash", "weighted") {
		return request, false, errors.New("invalid network or mode")
	}
	if len(request.Members) == 0 {
		return request, false, errors.New("at least one relay member is required")
	}
	if len(request.Targets) == 0 {
		return request, false, errors.New("at least one landing target is required")
	}
	if request.DNSTTL < 30 {
		request.DNSTTL = 60
	}
	if request.DNSTTL > 86400 {
		return request, false, errors.New("DNS TTL must not exceed 86400 seconds")
	}
	if request.DialTimeoutMillis <= 0 {
		request.DialTimeoutMillis = 2500
	}
	if request.UDPIdleTimeoutSeconds <= 0 {
		request.UDPIdleTimeoutSeconds = 60
	}
	request.Health = normalizeHealth(request.Health)
	seenMembers := make(map[string]bool)
	for _, member := range request.Members {
		if strings.TrimSpace(member.RelayNodeID) == "" {
			return request, false, errors.New("relay member ID is required")
		}
		if seenMembers[member.RelayNodeID] {
			return request, false, errors.New("duplicate relay member")
		}
		seenMembers[member.RelayNodeID] = true
	}
	seenTargets := make(map[string]bool)
	for _, target := range request.Targets {
		if strings.TrimSpace(target.LandingNodeID) == "" {
			return request, false, errors.New("landing target ID is required")
		}
		if seenTargets[target.LandingNodeID] {
			return request, false, errors.New("duplicate landing target")
		}
		seenTargets[target.LandingNodeID] = true
	}
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	return request, enabled, nil
}

func (s *Store) CreateRelayPool(ctx context.Context, request CreateRelayPoolRequest) (RelayPool, error) {
	request, enabled, err := normalizeRelayPoolRequest(request)
	if err != nil {
		return RelayPool{}, err
	}
	if request.DNSProviderID != "" {
		if _, err := s.GetDNSProvider(ctx, request.DNSProviderID); err != nil {
			return RelayPool{}, errors.New("DNS provider not found")
		}
	}
	now := time.Now().UTC()
	poolID := randomID("pool")
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RelayPool{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO relay_pools(id,name,hostname,listen_port,network,mode,enabled,dns_provider_id,dns_record_name,dns_ttl,dial_timeout_ms,udp_idle_timeout_seconds,health_enabled,health_interval_seconds,health_timeout_ms,failure_threshold,success_threshold,recovery_cooldown_seconds,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, poolID, request.Name, request.Hostname, request.ListenPort, request.Network, request.Mode, boolInt(enabled), nullIfEmpty(request.DNSProviderID), request.DNSRecordName, request.DNSTTL, request.DialTimeoutMillis, request.UDPIdleTimeoutSeconds, boolInt(request.Health.Enabled), request.Health.IntervalSeconds, request.Health.TimeoutMillis, request.Health.FailureThreshold, request.Health.SuccessThreshold, request.Health.RecoveryCooldownSecs, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		return RelayPool{}, err
	}
	for _, target := range request.Targets {
		if target.Weight <= 0 {
			target.Weight = 1
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO relay_pool_targets(id,pool_id,landing_node_id,weight,priority,enabled) VALUES(?,?,?,?,?,?)`, randomID("pooltarget"), poolID, target.LandingNodeID, target.Weight, target.Priority, boolInt(pointerBool(target.Enabled, true))); err != nil {
			return RelayPool{}, err
		}
	}
	changedNodes := make(map[string]bool)
	for _, member := range request.Members {
		var nodeName string
		if err := tx.QueryRowContext(ctx, `SELECT name FROM relay_nodes WHERE id=?`, member.RelayNodeID).Scan(&nodeName); err != nil {
			return RelayPool{}, errors.New("relay member not found")
		}
		memberEnabled := pointerBool(member.Enabled, true)
		if member.Weight <= 0 {
			member.Weight = 1
		}
		memberID := randomID("poolmember")
		serviceID, err := insertPoolServiceTx(ctx, tx, poolID, request, member.RelayNodeID, nodeName, memberEnabled && enabled)
		if err != nil {
			return RelayPool{}, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO relay_pool_members(id,pool_id,relay_node_id,service_id,weight,enabled,created_at) VALUES(?,?,?,?,?,?,?)`, memberID, poolID, member.RelayNodeID, serviceID, member.Weight, boolInt(memberEnabled), now.Format(time.RFC3339Nano)); err != nil {
			return RelayPool{}, err
		}
		changedNodes[member.RelayNodeID] = true
	}
	for nodeID := range changedNodes {
		if _, err := tx.ExecContext(ctx, `UPDATE relay_nodes SET desired_revision=desired_revision+1 WHERE id=?`, nodeID); err != nil {
			return RelayPool{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return RelayPool{}, err
	}
	_ = s.RefreshRelayPoolDNS(ctx, poolID)
	return s.GetRelayPool(ctx, poolID)
}

func insertPoolServiceTx(ctx context.Context, tx *sql.Tx, poolID string, request CreateRelayPoolRequest, relayNodeID, nodeName string, enabled bool) (string, error) {
	serviceID := randomID("service")
	serviceName := fmt.Sprintf("%s · %s", request.Name, nodeName)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `INSERT INTO relay_services(id,relay_node_id,name,listen_host,listen_port,network,mode,enabled,dial_timeout_ms,udp_idle_timeout_seconds,health_enabled,health_interval_seconds,health_timeout_ms,failure_threshold,success_threshold,recovery_cooldown_seconds,created_at,updated_at,pool_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, serviceID, relayNodeID, serviceName, "0.0.0.0", request.ListenPort, request.Network, request.Mode, boolInt(enabled), request.DialTimeoutMillis, request.UDPIdleTimeoutSeconds, boolInt(request.Health.Enabled), request.Health.IntervalSeconds, request.Health.TimeoutMillis, request.Health.FailureThreshold, request.Health.SuccessThreshold, request.Health.RecoveryCooldownSecs, now, now, poolID); err != nil {
		return "", err
	}
	for _, target := range request.Targets {
		if target.Weight <= 0 {
			target.Weight = 1
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO service_targets(id,service_id,landing_node_id,weight,priority,enabled) VALUES(?,?,?,?,?,?)`, randomID("target"), serviceID, target.LandingNodeID, target.Weight, target.Priority, boolInt(pointerBool(target.Enabled, true))); err != nil {
			return "", err
		}
	}
	return serviceID, nil
}

func (s *Store) UpdateRelayPool(ctx context.Context, id string, request CreateRelayPoolRequest) (RelayPool, error) {
	existing, err := s.GetRelayPool(ctx, id)
	if err != nil {
		return RelayPool{}, err
	}
	request, enabled, err := normalizeRelayPoolRequest(request)
	if err != nil {
		return RelayPool{}, err
	}
	if request.DNSProviderID != "" {
		if _, err := s.GetDNSProvider(ctx, request.DNSProviderID); err != nil {
			return RelayPool{}, errors.New("DNS provider not found")
		}
	}
	if existing.DNSProviderID != request.DNSProviderID {
		var managedRecords int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM dns_managed_records WHERE pool_id=?`, id).Scan(&managedRecords); err != nil {
			return RelayPool{}, err
		}
		if managedRecords > 0 {
			return RelayPool{}, errors.New("DNS provider cannot change while pool-managed records exist")
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RelayPool{}, err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `UPDATE relay_pools SET name=?,hostname=?,listen_port=?,network=?,mode=?,enabled=?,dns_provider_id=?,dns_record_name=?,dns_ttl=?,dial_timeout_ms=?,udp_idle_timeout_seconds=?,health_enabled=?,health_interval_seconds=?,health_timeout_ms=?,failure_threshold=?,success_threshold=?,recovery_cooldown_seconds=?,updated_at=? WHERE id=?`, request.Name, request.Hostname, request.ListenPort, request.Network, request.Mode, boolInt(enabled), nullIfEmpty(request.DNSProviderID), request.DNSRecordName, request.DNSTTL, request.DialTimeoutMillis, request.UDPIdleTimeoutSeconds, request.Health.Enabled, request.Health.IntervalSeconds, request.Health.TimeoutMillis, request.Health.FailureThreshold, request.Health.SuccessThreshold, request.Health.RecoveryCooldownSecs, now, id); err != nil {
		return RelayPool{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM relay_pool_targets WHERE pool_id=?`, id); err != nil {
		return RelayPool{}, err
	}
	for _, target := range request.Targets {
		if target.Weight <= 0 {
			target.Weight = 1
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO relay_pool_targets(id,pool_id,landing_node_id,weight,priority,enabled) VALUES(?,?,?,?,?,?)`, randomID("pooltarget"), id, target.LandingNodeID, target.Weight, target.Priority, boolInt(pointerBool(target.Enabled, true))); err != nil {
			return RelayPool{}, err
		}
	}
	existingMembers := make(map[string]RelayPoolMember)
	for _, member := range existing.Members {
		existingMembers[member.RelayNodeID] = member
	}
	requested := make(map[string]bool)
	changed := make(map[string]bool)
	for _, member := range request.Members {
		requested[member.RelayNodeID] = true
		if member.Weight <= 0 {
			member.Weight = 1
		}
		memberEnabled := pointerBool(member.Enabled, true)
		if old, ok := existingMembers[member.RelayNodeID]; ok {
			if _, err := tx.ExecContext(ctx, `UPDATE relay_pool_members SET weight=?,enabled=? WHERE id=?`, member.Weight, boolInt(memberEnabled), old.ID); err != nil {
				return RelayPool{}, err
			}
			if err := updatePoolServiceTx(ctx, tx, old.ServiceID, request, memberEnabled && enabled); err != nil {
				return RelayPool{}, err
			}
		} else {
			var nodeName string
			if err := tx.QueryRowContext(ctx, `SELECT name FROM relay_nodes WHERE id=?`, member.RelayNodeID).Scan(&nodeName); err != nil {
				return RelayPool{}, errors.New("relay member not found")
			}
			serviceID, err := insertPoolServiceTx(ctx, tx, id, request, member.RelayNodeID, nodeName, memberEnabled && enabled)
			if err != nil {
				return RelayPool{}, err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO relay_pool_members(id,pool_id,relay_node_id,service_id,weight,enabled,created_at) VALUES(?,?,?,?,?,?,?)`, randomID("poolmember"), id, member.RelayNodeID, serviceID, member.Weight, boolInt(memberEnabled), now); err != nil {
				return RelayPool{}, err
			}
		}
		changed[member.RelayNodeID] = true
	}
	for nodeID, old := range existingMembers {
		if requested[nodeID] {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE relay_pool_members SET enabled=0 WHERE id=?`, old.ID); err != nil {
			return RelayPool{}, err
		}
		if err := updatePoolServiceTx(ctx, tx, old.ServiceID, request, false); err != nil {
			return RelayPool{}, err
		}
		changed[nodeID] = true
	}
	for nodeID := range changed {
		if _, err := tx.ExecContext(ctx, `UPDATE relay_nodes SET desired_revision=desired_revision+1 WHERE id=?`, nodeID); err != nil {
			return RelayPool{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return RelayPool{}, err
	}
	_ = s.RefreshRelayPoolDNS(ctx, id)
	return s.GetRelayPool(ctx, id)
}

func updatePoolServiceTx(ctx context.Context, tx *sql.Tx, serviceID string, request CreateRelayPoolRequest, enabled bool) error {
	if serviceID == "" {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var nodeName string
	_ = tx.QueryRowContext(ctx, `SELECT rn.name FROM relay_services rs JOIN relay_nodes rn ON rn.id=rs.relay_node_id WHERE rs.id=?`, serviceID).Scan(&nodeName)
	serviceName := request.Name
	if nodeName != "" {
		serviceName = fmt.Sprintf("%s · %s", request.Name, nodeName)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE relay_services SET name=?,listen_port=?,network=?,mode=?,enabled=?,dial_timeout_ms=?,udp_idle_timeout_seconds=?,health_enabled=?,health_interval_seconds=?,health_timeout_ms=?,failure_threshold=?,success_threshold=?,recovery_cooldown_seconds=?,updated_at=? WHERE id=?`, serviceName, request.ListenPort, request.Network, request.Mode, boolInt(enabled), request.DialTimeoutMillis, request.UDPIdleTimeoutSeconds, boolInt(request.Health.Enabled), request.Health.IntervalSeconds, request.Health.TimeoutMillis, request.Health.FailureThreshold, request.Health.SuccessThreshold, request.Health.RecoveryCooldownSecs, now, serviceID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM service_targets WHERE service_id=?`, serviceID); err != nil {
		return err
	}
	for _, target := range request.Targets {
		if target.Weight <= 0 {
			target.Weight = 1
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO service_targets(id,service_id,landing_node_id,weight,priority,enabled) VALUES(?,?,?,?,?,?)`, randomID("target"), serviceID, target.LandingNodeID, target.Weight, target.Priority, boolInt(pointerBool(target.Enabled, true))); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) DeleteRelayPool(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT relay_node_id,service_id FROM relay_pool_members WHERE pool_id=?`)
	if err != nil {
		return err
	}
	type member struct{ node, service string }
	members := make([]member, 0)
	for rows.Next() {
		var item member
		if err := rows.Scan(&item.node, &item.service); err != nil {
			rows.Close()
			return err
		}
		members = append(members, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE relay_pools SET enabled=0,updated_at=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339Nano), id); err != nil {
		return err
	}
	changed := make(map[string]bool)
	for _, item := range members {
		if _, err := tx.ExecContext(ctx, `UPDATE relay_pool_members SET enabled=0 WHERE pool_id=? AND relay_node_id=?`, id, item.node); err != nil {
			return err
		}
		if item.service != "" {
			if _, err := tx.ExecContext(ctx, `UPDATE relay_services SET enabled=0,updated_at=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339Nano), item.service); err != nil {
				return err
			}
		}
		changed[item.node] = true
	}
	for nodeID := range changed {
		if _, err := tx.ExecContext(ctx, `UPDATE relay_nodes SET desired_revision=desired_revision+1 WHERE id=?`, nodeID); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	_ = s.RefreshRelayPoolDNS(ctx, id)
	return nil
}

func (s *Store) ListRelayPools(ctx context.Context) ([]RelayPool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,hostname,listen_port,network,mode,enabled,COALESCE(dns_provider_id,''),dns_record_name,dns_ttl,dial_timeout_ms,udp_idle_timeout_seconds,health_enabled,health_interval_seconds,health_timeout_ms,failure_threshold,success_threshold,recovery_cooldown_seconds,created_at,updated_at FROM relay_pools ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	result := make([]RelayPool, 0)
	for rows.Next() {
		var p RelayPool
		var enabled, healthEnabled int
		var created, updated string
		if err := rows.Scan(&p.ID, &p.Name, &p.Hostname, &p.ListenPort, &p.Network, &p.Mode, &enabled, &p.DNSProviderID, &p.DNSRecordName, &p.DNSTTL, &p.DialTimeoutMillis, &p.UDPIdleTimeoutSeconds, &healthEnabled, &p.Health.IntervalSeconds, &p.Health.TimeoutMillis, &p.Health.FailureThreshold, &p.Health.SuccessThreshold, &p.Health.RecoveryCooldownSecs, &created, &updated); err != nil {
			return nil, err
		}
		p.Enabled = enabled != 0
		p.Health.Enabled = healthEnabled != 0
		p.CreatedAt = parseDatabaseTime(created)
		p.UpdatedAt = parseDatabaseTime(updated)
		result = append(result, p)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for index := range result {
		members, err := s.listRelayPoolMembers(ctx, result[index].ID)
		if err != nil {
			return nil, err
		}
		targets, err := s.listRelayPoolTargets(ctx, result[index].ID)
		if err != nil {
			return nil, err
		}
		result[index].Members = members
		result[index].Targets = targets
	}
	return result, rows.Err()
}

func (s *Store) GetRelayPool(ctx context.Context, id string) (RelayPool, error) {
	pools, err := s.ListRelayPools(ctx)
	if err != nil {
		return RelayPool{}, err
	}
	for _, p := range pools {
		if p.ID == id {
			return p, nil
		}
	}
	return RelayPool{}, sql.ErrNoRows
}

func (s *Store) listRelayPoolMembers(ctx context.Context, poolID string) ([]RelayPoolMember, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT rpm.id,rpm.pool_id,rpm.relay_node_id,rn.name,COALESCE(rn.public_ip,''),CASE WHEN COALESCE(a.protection_triggered,0)=1 AND COALESCE(a.protection_mode,'alert_only')='drain_relay' THEN 'draining' ELSE rn.status END,rpm.weight,rpm.enabled,COALESCE(rpm.service_id,'') FROM relay_pool_members rpm JOIN relay_nodes rn ON rn.id=rpm.relay_node_id LEFT JOIN accounts a ON a.id=rn.cloud_account_id WHERE rpm.pool_id=? ORDER BY rn.name`, poolID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]RelayPoolMember, 0)
	for rows.Next() {
		var m RelayPoolMember
		var enabled int
		if err := rows.Scan(&m.ID, &m.PoolID, &m.RelayNodeID, &m.RelayNodeName, &m.PublicIP, &m.Status, &m.Weight, &enabled, &m.ServiceID); err != nil {
			return nil, err
		}
		m.Enabled = enabled != 0
		result = append(result, m)
	}
	return result, rows.Err()
}
func (s *Store) listRelayPoolTargets(ctx context.Context, poolID string) ([]ServiceTarget, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT rpt.id,rpt.landing_node_id,ln.name,ln.address,ln.port,rpt.weight,rpt.priority,rpt.enabled,ln.enabled FROM relay_pool_targets rpt JOIN landing_nodes ln ON ln.id=rpt.landing_node_id WHERE rpt.pool_id=? ORDER BY rpt.priority,rpt.id`, poolID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]ServiceTarget, 0)
	for rows.Next() {
		var t ServiceTarget
		var enabled, landingEnabled int
		if err := rows.Scan(&t.ID, &t.LandingNodeID, &t.Name, &t.Address, &t.Port, &t.Weight, &t.Priority, &enabled, &landingEnabled); err != nil {
			return nil, err
		}
		t.Enabled = enabled != 0 && landingEnabled != 0
		result = append(result, t)
	}
	return result, rows.Err()
}

func (s *Store) RefreshRelayPoolDNS(ctx context.Context, poolID string) error {
	pool, err := s.GetRelayPool(ctx, poolID)
	if err != nil {
		return err
	}
	if pool.DNSProviderID == "" {
		return nil
	}
	records, err := s.ListDNSRecords(ctx, pool.DNSProviderID)
	if err != nil {
		return err
	}
	seen := make(map[string]bool)
	recordName := pool.DNSRecordName
	if recordName == "" {
		recordName = pool.Hostname
	}
	for _, member := range pool.Members {
		key := member.RelayNodeID
		seen[key] = true
		enabled := pool.Enabled && member.Enabled && strings.EqualFold(member.Status, "online") && validRelayIP(member.PublicIP)
		var existing *DNSManagedRecord
		for i := range records {
			if records[i].PoolID == pool.ID && records[i].RelayNodeID == member.RelayNodeID {
				copy := records[i]
				existing = &copy
				break
			}
		}
		if existing == nil {
			if member.PublicIP == "" {
				continue
			}
			_, err := s.db.ExecContext(ctx, `INSERT INTO dns_managed_records(id,provider_id,pool_id,relay_node_id,name,type,value,ttl,enabled,desired_enabled,status,last_error,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,'pending','',?,?)`, randomID("record"), pool.DNSProviderID, pool.ID, member.RelayNodeID, recordName, "A", member.PublicIP, pool.DNSTTL, boolInt(enabled), 1, time.Now().UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano))
			if err != nil {
				return err
			}
			continue
		}
		_, err = s.db.ExecContext(ctx, `UPDATE dns_managed_records SET provider_id=?,name=?,type='A',value=?,ttl=?,enabled=?,status=CASE WHEN enabled<>? OR value<>? THEN 'pending' ELSE status END,last_error='',updated_at=? WHERE id=?`, pool.DNSProviderID, recordName, member.PublicIP, pool.DNSTTL, boolInt(enabled), boolInt(enabled), member.PublicIP, time.Now().UTC().Format(time.RFC3339Nano), existing.ID)
		if err != nil {
			return err
		}
	}
	for _, record := range records {
		if record.PoolID == pool.ID && !seen[record.RelayNodeID] {
			_, _ = s.db.ExecContext(ctx, `UPDATE dns_managed_records SET enabled=0,status='disabled',updated_at=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339Nano), record.ID)
		}
	}
	return nil
}

func (s *Store) RefreshAllRelayPoolDNS(ctx context.Context) error {
	pools, err := s.ListRelayPools(ctx)
	if err != nil {
		return err
	}
	var first error
	for _, pool := range pools {
		if err := s.RefreshRelayPoolDNS(ctx, pool.ID); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func validRelayIP(value string) bool {
	ip := net.ParseIP(strings.TrimSpace(value))
	return ip != nil && !ip.IsUnspecified()
}
func pointerBool(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}
