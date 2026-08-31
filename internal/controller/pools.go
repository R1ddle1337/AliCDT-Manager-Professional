package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// relayPoolHeartbeatFreshness is deliberately aligned with the controller's
// stale-agent window. A pool member must have checked in within this window
// before its address is published to managed DNS.
const relayPoolHeartbeatFreshness = 45 * time.Second

func normalizeRelayPoolRequest(request CreateRelayPoolRequest) (CreateRelayPoolRequest, bool, error) {
	normalized, enabled, _, err := normalizeRelayPoolRequestWithDrain(request)
	return normalized, enabled, err
}

func normalizeRelayPoolRequestWithDrain(request CreateRelayPoolRequest) (CreateRelayPoolRequest, bool, bool, error) {
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
		return request, false, false, errors.New("name, hostname and a valid listen port are required")
	}
	if strings.ContainsAny(request.Hostname, "/ :\\") {
		return request, false, false, errors.New("hostname must be a DNS name without a scheme or port")
	}
	if !oneOf(request.Network, "tcp", "udp", "tcp+udp") || !oneOf(request.Mode, "failover", "round_robin", "ip_hash", "weighted") {
		return request, false, false, errors.New("invalid network or mode")
	}
	if len(request.Members) == 0 {
		return request, false, false, errors.New("at least one relay member is required")
	}
	if len(request.Targets) == 0 {
		return request, false, false, errors.New("at least one landing target is required")
	}
	if request.DNSTTL < 30 {
		request.DNSTTL = 60
	}
	if request.DNSTTL > 86400 {
		return request, false, false, errors.New("DNS TTL must not exceed 86400 seconds")
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
			return request, false, false, errors.New("relay member ID is required")
		}
		if seenMembers[member.RelayNodeID] {
			return request, false, false, errors.New("duplicate relay member")
		}
		seenMembers[member.RelayNodeID] = true
	}
	seenTargets := make(map[string]bool)
	for _, target := range request.Targets {
		if strings.TrimSpace(target.LandingNodeID) == "" {
			return request, false, false, errors.New("landing target ID is required")
		}
		if seenTargets[target.LandingNodeID] {
			return request, false, false, errors.New("duplicate landing target")
		}
		seenTargets[target.LandingNodeID] = true
	}
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	autoDrain := true
	if request.AutoDrain != nil {
		autoDrain = *request.AutoDrain
	}
	return request, enabled, autoDrain, nil
}

func (s *Store) CreateRelayPool(ctx context.Context, request CreateRelayPoolRequest) (RelayPool, error) {
	request, enabled, autoDrain, err := normalizeRelayPoolRequestWithDrain(request)
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
	if _, err := tx.ExecContext(ctx, `INSERT INTO relay_pools(id,name,hostname,listen_port,network,mode,enabled,auto_drain,dns_provider_id,dns_record_name,dns_ttl,dial_timeout_ms,udp_idle_timeout_seconds,health_enabled,health_interval_seconds,health_timeout_ms,failure_threshold,success_threshold,recovery_cooldown_seconds,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, poolID, request.Name, request.Hostname, request.ListenPort, request.Network, request.Mode, boolInt(enabled), boolInt(autoDrain), nullIfEmpty(request.DNSProviderID), request.DNSRecordName, request.DNSTTL, request.DialTimeoutMillis, request.UDPIdleTimeoutSeconds, boolInt(request.Health.Enabled), request.Health.IntervalSeconds, request.Health.TimeoutMillis, request.Health.FailureThreshold, request.Health.SuccessThreshold, request.Health.RecoveryCooldownSecs, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
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
	if err := validateListenConflictTx(ctx, tx, relayNodeID, "0.0.0.0", request.ListenPort, request.Network, ""); err != nil {
		return "", err
	}
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
	request, enabled, autoDrain, err := normalizeRelayPoolRequestWithDrain(request)
	if err != nil {
		return RelayPool{}, err
	}
	if request.AutoDrain == nil {
		autoDrain = existing.AutoDrain
	}
	if request.DNSProviderID != "" {
		if _, err := s.GetDNSProvider(ctx, request.DNSProviderID); err != nil {
			return RelayPool{}, errors.New("DNS provider not found")
		}
	}
	if existing.DNSProviderID != request.DNSProviderID {
		var managedRecords int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM dns_managed_records WHERE pool_id=? OR id IN (SELECT record_id FROM dns_managed_record_pools WHERE pool_id=?)`, id, id).Scan(&managedRecords); err != nil {
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
	if _, err := tx.ExecContext(ctx, `UPDATE relay_pools SET name=?,hostname=?,listen_port=?,network=?,mode=?,enabled=?,auto_drain=?,dns_provider_id=?,dns_record_name=?,dns_ttl=?,dial_timeout_ms=?,udp_idle_timeout_seconds=?,health_enabled=?,health_interval_seconds=?,health_timeout_ms=?,failure_threshold=?,success_threshold=?,recovery_cooldown_seconds=?,updated_at=? WHERE id=?`, request.Name, request.Hostname, request.ListenPort, request.Network, request.Mode, boolInt(enabled), boolInt(autoDrain), nullIfEmpty(request.DNSProviderID), request.DNSRecordName, request.DNSTTL, request.DialTimeoutMillis, request.UDPIdleTimeoutSeconds, request.Health.Enabled, request.Health.IntervalSeconds, request.Health.TimeoutMillis, request.Health.FailureThreshold, request.Health.SuccessThreshold, request.Health.RecoveryCooldownSecs, now, id); err != nil {
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
	if existing.AutoDrain != autoDrain || existing.Enabled != enabled {
		// A pool-level automatic drain toggle changes whether an already
		// triggered account may accept new connections. Re-publish the desired
		// revision for every member so Agents converge without waiting for the
		// next cloud poll.
		for _, member := range existing.Members {
			changed[member.RelayNodeID] = true
		}
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
	var relayNodeID string
	if err := tx.QueryRowContext(ctx, `SELECT relay_node_id FROM relay_services WHERE id=?`, serviceID).Scan(&relayNodeID); err != nil {
		return err
	}
	if err := validateListenConflictTx(ctx, tx, relayNodeID, "0.0.0.0", request.ListenPort, request.Network, serviceID); err != nil {
		return err
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
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT relay_node_id,service_id FROM relay_pool_members WHERE pool_id=?`, id)
	if err != nil {
		return fmt.Errorf("list members: %w", err)
	}
	type member struct{ node, service string }
	members := make([]member, 0)
	for rows.Next() {
		var item member
		if err := rows.Scan(&item.node, &item.service); err != nil {
			rows.Close()
			return fmt.Errorf("scan members: %w", err)
		}
		members = append(members, item)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close members: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE relay_pools SET enabled=0,updated_at=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339Nano), id); err != nil {
		return fmt.Errorf("disable pool: %w", err)
	}
	changed := make(map[string]bool)
	for _, item := range members {
		if _, err := tx.ExecContext(ctx, `UPDATE relay_pool_members SET enabled=0 WHERE pool_id=? AND relay_node_id=?`, id, item.node); err != nil {
			return fmt.Errorf("disable member: %w", err)
		}
		if item.service != "" {
			if _, err := tx.ExecContext(ctx, `UPDATE relay_services SET enabled=0,updated_at=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339Nano), item.service); err != nil {
				return fmt.Errorf("disable service: %w", err)
			}
		}
		changed[item.node] = true
	}
	for nodeID := range changed {
		if _, err := tx.ExecContext(ctx, `UPDATE relay_nodes SET desired_revision=desired_revision+1 WHERE id=?`, nodeID); err != nil {
			return fmt.Errorf("bump node: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM dns_managed_record_pools WHERE pool_id=?`, id); err != nil {
		return fmt.Errorf("remove pool DNS bindings: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE dns_managed_records SET enabled=0,status='disabled',updated_at=? WHERE pool_id=? AND NOT EXISTS(SELECT 1 FROM dns_managed_record_pools b WHERE b.record_id=dns_managed_records.id)`, time.Now().UTC().Format(time.RFC3339Nano), id); err != nil {
		return fmt.Errorf("disable orphaned pool DNS records: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	if err := s.RefreshRelayPoolDNS(ctx, id); err != nil {
		return fmt.Errorf("refresh DNS after pool disable: %w", err)
	}
	return nil
}

func (s *Store) ListRelayPools(ctx context.Context) ([]RelayPool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,hostname,listen_port,network,mode,enabled,COALESCE(auto_drain,1),COALESCE(dns_provider_id,''),dns_record_name,dns_ttl,dial_timeout_ms,udp_idle_timeout_seconds,health_enabled,health_interval_seconds,health_timeout_ms,failure_threshold,success_threshold,recovery_cooldown_seconds,created_at,updated_at FROM relay_pools ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	result := make([]RelayPool, 0)
	for rows.Next() {
		var p RelayPool
		var enabled, autoDrain, healthEnabled int
		var created, updated string
		if err := rows.Scan(&p.ID, &p.Name, &p.Hostname, &p.ListenPort, &p.Network, &p.Mode, &enabled, &autoDrain, &p.DNSProviderID, &p.DNSRecordName, &p.DNSTTL, &p.DialTimeoutMillis, &p.UDPIdleTimeoutSeconds, &healthEnabled, &p.Health.IntervalSeconds, &p.Health.TimeoutMillis, &p.Health.FailureThreshold, &p.Health.SuccessThreshold, &p.Health.RecoveryCooldownSecs, &created, &updated); err != nil {
			return nil, err
		}
		p.Enabled = enabled != 0
		p.AutoDrain = autoDrain != 0
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
	rows, err := s.db.QueryContext(ctx, `SELECT rpm.id,rpm.pool_id,rpm.relay_node_id,rn.name,COALESCE(rn.public_ip,''),rn.status,
		CASE WHEN (COALESCE(a.protection_triggered,0)=1 AND (COALESCE(a.protection_mode,'alert_only')='drain_relay' OR COALESCE(rp.auto_drain,1)=1)) OR COALESCE(rn.update_status,'idle') IN ('draining','updating') THEN 1 ELSE 0 END,
		COALESCE(rn.last_seen_at,''),rn.current_revision,rn.desired_revision,COALESCE(rn.service_status_json,'[]'),rpm.weight,rpm.enabled,COALESCE(rpm.service_id,''),
		 rn.cloud_account_id,COALESCE(a.name,''),COALESCE(ts.used_gb,0),CASE WHEN ts.account_id IS NOT NULL AND ts.synced_at IS NOT NULL THEN 1 ELSE 0 END,
		COALESCE(a.traffic_limit_gb,0),COALESCE(a.threshold_percent,0),COALESCE(a.protection_mode,''),COALESCE(a.protection_triggered,0),
		COALESCE(a.protection_action_completed,0),COALESCE(a.protection_triggered_at,'')
		FROM relay_pool_members rpm JOIN relay_nodes rn ON rn.id=rpm.relay_node_id JOIN relay_pools rp ON rp.id=rpm.pool_id
		LEFT JOIN accounts a ON a.id=rn.cloud_account_id OR (rn.cloud_account_id IS NULL AND rn.ecs_instance_id IN (SELECT instance_id FROM instances WHERE account_id=a.id))
		LEFT JOIN account_traffic_snapshots ts ON ts.account_id=a.id
		WHERE rpm.pool_id=? ORDER BY rn.name`, poolID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]RelayPoolMember, 0)
	now := time.Now().UTC()
	for rows.Next() {
		var m RelayPoolMember
		var enabled, draining, trafficKnown, protectionTriggered, protectionActionCompleted int
		var rawStatus, lastSeen, serviceStatusJSON, protectionTriggeredAt string
		var cloudAccountID sql.NullInt64
		var currentRevision, desiredRevision int64
		if err := rows.Scan(&m.ID, &m.PoolID, &m.RelayNodeID, &m.RelayNodeName, &m.PublicIP, &rawStatus, &draining,
			&lastSeen, &currentRevision, &desiredRevision, &serviceStatusJSON, &m.Weight, &enabled, &m.ServiceID,
			&cloudAccountID, &m.CloudAccountName, &m.TrafficUsedGB, &trafficKnown, &m.TrafficLimitGB,
			&m.TrafficThresholdPercent, &m.ProtectionMode, &protectionTriggered, &protectionActionCompleted, &protectionTriggeredAt); err != nil {
			return nil, err
		}
		m.Status = relayPoolMemberStatus(now, rawStatus, draining != 0, lastSeen, currentRevision, desiredRevision, m.ServiceID, serviceStatusJSON)
		m.Enabled = enabled != 0
		if cloudAccountID.Valid {
			id := cloudAccountID.Int64
			m.CloudAccountID = &id
		}
		m.TrafficKnown = trafficKnown != 0
		m.ProtectionTriggered = protectionTriggered != 0
		m.ProtectionActionCompleted = protectionActionCompleted != 0
		if m.TrafficKnown && m.TrafficLimitGB > 0 {
			m.TrafficPercent = m.TrafficUsedGB / m.TrafficLimitGB * 100
		}
		if protectionTriggeredAt != "" {
			value := parseDatabaseTime(protectionTriggeredAt)
			if !value.IsZero() {
				m.ProtectionTriggeredAt = &value
			}
		}
		result = append(result, m)
	}
	return result, rows.Err()
}

func relayPoolMemberStatus(now time.Time, rawStatus string, draining bool, lastSeen string, currentRevision, desiredRevision int64, serviceID, serviceStatusJSON string) string {
	if draining {
		return "draining"
	}
	if !strings.EqualFold(strings.TrimSpace(rawStatus), "online") {
		return "offline"
	}
	seenAt := parseDatabaseTime(lastSeen)
	if seenAt.IsZero() || now.Sub(seenAt) > relayPoolHeartbeatFreshness {
		return "offline"
	}
	if currentRevision != desiredRevision || !relayPoolServiceListening(serviceID, serviceStatusJSON) {
		return "pending"
	}
	return "online"
}

func relayPoolServiceListening(serviceID, raw string) bool {
	if strings.TrimSpace(serviceID) == "" {
		return false
	}
	var services []struct {
		ID        string `json:"id"`
		Listening bool   `json:"listening"`
	}
	if err := json.Unmarshal([]byte(raw), &services); err != nil {
		return false
	}
	for _, service := range services {
		if service.ID == serviceID && service.Listening {
			return true
		}
	}
	return false
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
	bindings, err := s.poolDNSBindings(ctx, poolID)
	if err != nil {
		return err
	}
	if !pool.Enabled {
		for recordID := range bindings {
			if err := s.unbindPoolDNSRecord(ctx, recordID, pool.ID); err != nil {
				return err
			}
			stillUsed, err := s.recordHasPoolBinding(ctx, recordID)
			if err != nil {
				return err
			}
			if !stillUsed {
				if _, err := s.db.ExecContext(ctx, `UPDATE dns_managed_records SET enabled=0,status='disabled',updated_at=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339Nano), recordID); err != nil {
					return err
				}
			}
		}
		return nil
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
			// Match by provider/name/type/member even when another pool owns the
			// row. This lets port-specific pools share one RR and prevents an IP
			// change from creating a stale duplicate row.
			if records[i].ProviderID == pool.DNSProviderID && strings.EqualFold(records[i].Name, recordName) && records[i].Type == "A" && records[i].RelayNodeID == member.RelayNodeID {
				copy := records[i]
				existing = &copy
				break
			}
		}
		if !validRelayIP(member.PublicIP) {
			if existing != nil {
				if err := s.bindPoolDNSRecord(ctx, existing.ID, pool.ID); err != nil {
					return err
				}
				if _, err := s.db.ExecContext(ctx, `UPDATE dns_managed_records SET enabled=0,status=CASE WHEN enabled<>0 THEN 'pending' ELSE status END,last_error='',updated_at=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339Nano), existing.ID); err != nil {
					return err
				}
			}
			continue
		}
		if existing == nil {
			recordID := randomID("record")
			_, err := s.db.ExecContext(ctx, `INSERT INTO dns_managed_records(id,provider_id,pool_id,relay_node_id,name,type,value,ttl,enabled,desired_enabled,status,last_error,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,'pending','',?,?)`, recordID, pool.DNSProviderID, pool.ID, member.RelayNodeID, recordName, "A", member.PublicIP, pool.DNSTTL, boolInt(enabled), 1, time.Now().UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano))
			if err != nil {
				if !isUniqueConstraint(err) {
					return err
				}
				for i := range records {
					if records[i].ProviderID == pool.DNSProviderID && strings.EqualFold(records[i].Name, recordName) && records[i].Type == "A" && records[i].RelayNodeID == member.RelayNodeID && records[i].Value == member.PublicIP {
						copy := records[i]
						existing = &copy
						break
					}
				}
				if existing == nil {
					return err
				}
			}
			if existing == nil {
				if err := s.bindPoolDNSRecord(ctx, recordID, pool.ID); err != nil {
					return err
				}
				continue
			}
		}
		if err := s.bindPoolDNSRecord(ctx, existing.ID, pool.ID); err != nil {
			return err
		}
		sharedEnabled, err := s.sharedPoolRecordEligible(ctx, existing.ID, pool.ID, enabled)
		if err != nil {
			return err
		}
		enabled = sharedEnabled
		_, err = s.db.ExecContext(ctx, `UPDATE dns_managed_records SET provider_id=?,pool_id=CASE WHEN COALESCE(pool_id,'')='' THEN ? ELSE pool_id END,name=?,type='A',value=?,ttl=?,enabled=?,desired_enabled=1,status=CASE WHEN enabled<>? OR value<>? THEN 'pending' ELSE status END,last_error='',updated_at=? WHERE id=?`, pool.DNSProviderID, pool.ID, recordName, member.PublicIP, pool.DNSTTL, boolInt(enabled), boolInt(enabled), member.PublicIP, time.Now().UTC().Format(time.RFC3339Nano), existing.ID)
		if err != nil {
			return err
		}
	}
	for _, record := range records {
		belongs := bindings[record.ID] || record.PoolID == pool.ID
		nameChanged := record.ProviderID != pool.DNSProviderID || record.Type != "A" || !strings.EqualFold(record.Name, recordName)
		if belongs && (!seen[record.RelayNodeID] || nameChanged) {
			if err := s.unbindPoolDNSRecord(ctx, record.ID, pool.ID); err != nil {
				return err
			}
			stillUsed, err := s.recordHasPoolBinding(ctx, record.ID)
			if err != nil {
				return err
			}
			if !stillUsed {
				_, _ = s.db.ExecContext(ctx, `UPDATE dns_managed_records SET enabled=0,status='disabled',updated_at=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339Nano), record.ID)
			}
		}
	}
	return nil
}

func (s *Store) poolDNSBindings(ctx context.Context, poolID string) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT record_id FROM dns_managed_record_pools WHERE pool_id=?`, poolID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result[id] = true
	}
	return result, rows.Err()
}

func (s *Store) bindPoolDNSRecord(ctx context.Context, recordID, poolID string) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO dns_managed_record_pools(record_id,pool_id) VALUES(?,?)`, recordID, poolID)
	return err
}

func (s *Store) unbindPoolDNSRecord(ctx context.Context, recordID, poolID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM dns_managed_record_pools WHERE record_id=? AND pool_id=?`, recordID, poolID)
	return err
}

func (s *Store) recordHasPoolBinding(ctx context.Context, recordID string) (bool, error) {
	var result int
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM dns_managed_record_pools WHERE record_id=?)`, recordID).Scan(&result)
	return result != 0, err
}

func isUniqueConstraint(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique") || strings.Contains(message, "constraint")
}

// sharedPoolRecordEligible applies a conservative AND policy to a shared RR:
// every port-specific pool using the address must have an eligible member.
func (s *Store) sharedPoolRecordEligible(ctx context.Context, recordID, currentPoolID string, currentEligible bool) (bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT b.pool_id,rp.enabled,rpm.enabled,rn.status,
		CASE WHEN (COALESCE(a.protection_triggered,0)=1 AND (COALESCE(a.protection_mode,'alert_only')='drain_relay' OR COALESCE(rp.auto_drain,1)=1)) OR COALESCE(rn.update_status,'idle') IN ('draining','updating') THEN 1 ELSE 0 END,
		COALESCE(rn.public_ip,''),COALESCE(rn.last_seen_at,''),rn.current_revision,rn.desired_revision,COALESCE(rn.service_status_json,'[]'),COALESCE(rpm.service_id,'')
		FROM dns_managed_record_pools b JOIN relay_pools rp ON rp.id=b.pool_id
		JOIN dns_managed_records dr ON dr.id=b.record_id
		JOIN relay_pool_members rpm ON rpm.pool_id=b.pool_id AND rpm.relay_node_id=dr.relay_node_id
		JOIN relay_nodes rn ON rn.id=rpm.relay_node_id LEFT JOIN accounts a ON a.id=rn.cloud_account_id OR (rn.cloud_account_id IS NULL AND rn.ecs_instance_id IN (SELECT instance_id FROM instances WHERE account_id=a.id))
		WHERE b.record_id=?`, recordID)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	eligible := currentEligible
	now := time.Now().UTC()
	for rows.Next() {
		var bindingPoolID, rawStatus, publicIP, lastSeen, serviceJSON, serviceID string
		var poolEnabled, memberEnabled, draining int
		var currentRevision, desiredRevision int64
		if err := rows.Scan(&bindingPoolID, &poolEnabled, &memberEnabled, &rawStatus, &draining, &publicIP, &lastSeen, &currentRevision, &desiredRevision, &serviceJSON, &serviceID); err != nil {
			return false, err
		}
		if bindingPoolID == currentPoolID {
			continue
		}
		status := relayPoolMemberStatus(now, rawStatus, draining != 0, lastSeen, currentRevision, desiredRevision, serviceID, serviceJSON)
		if poolEnabled == 0 || memberEnabled == 0 || status != "online" || !validRelayIP(publicIP) {
			eligible = false
		}
	}
	return eligible, rows.Err()
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
