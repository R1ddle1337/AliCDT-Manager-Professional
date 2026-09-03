package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/protocol"
)

const (
	trafficLeaseDuration    = 5 * time.Minute
	trafficLeaseRenewWindow = 2 * time.Minute
	minimumLeaseChunk       = int64(256 * 1024 * 1024)
	maximumLeaseChunk       = int64(5 * 1024 * 1024 * 1024)
)

func trafficLeaseChunk(limit int64) int64 {
	chunk := limit / 4
	if chunk < minimumLeaseChunk {
		chunk = minimumLeaseChunk
	}
	if chunk > maximumLeaseChunk {
		chunk = maximumLeaseChunk
	}
	if chunk > limit {
		chunk = limit
	}
	return chunk
}

func ensureTrafficLeaseTx(ctx context.Context, tx *sql.Tx, userID int64, relayNodeID string, now time.Time) (TrafficLease, bool, error) {
	var limitGB float64
	var configuredEpoch int64
	if err := tx.QueryRowContext(ctx, `SELECT traffic_limit_gb,COALESCE(billing_epoch,1) FROM console_users WHERE id=?`, userID).Scan(&limitGB, &configuredEpoch); err != nil {
		return TrafficLease{}, false, err
	}
	epoch := effectiveBillingEpoch(configuredEpoch)
	limit, err := gbToLedgerBytes(limitGB)
	if err != nil {
		return TrafficLease{}, false, err
	}
	var localUsed, globalUsed int64
	_ = tx.QueryRowContext(ctx, `SELECT COALESCE(total_bytes,0) FROM usage_meter_checkpoints WHERE relay_node_id=? AND meter_key=? AND billing_epoch=?`, relayNodeID, fmt.Sprintf("user:%d", userID), epoch).Scan(&localUsed)
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(total_bytes),0) FROM usage_meter_checkpoints WHERE user_id=? AND billing_epoch=?`, userID, epoch).Scan(&globalUsed); err != nil {
		return TrafficLease{}, false, err
	}
	var otherReserved int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(MAX(tl.reserved_bytes-COALESCE(cp.total_bytes,0),0)),0)
		FROM traffic_leases tl LEFT JOIN usage_meter_checkpoints cp ON cp.relay_node_id=tl.relay_node_id AND cp.meter_key=tl.meter_key AND cp.billing_epoch=tl.billing_epoch
		WHERE tl.user_id=? AND tl.billing_epoch=? AND tl.relay_node_id<>? AND tl.status='active' AND tl.expires_at>?`, userID, epoch, relayNodeID, now.Format(time.RFC3339Nano)).Scan(&otherReserved); err != nil {
		return TrafficLease{}, false, err
	}
	var lease TrafficLease
	var reserved int64
	var expires, created, updated string
	err = tx.QueryRowContext(ctx, `SELECT id,user_id,meter_key,relay_node_id,billing_epoch,reserved_bytes,sequence,status,expires_at,created_at,updated_at FROM traffic_leases WHERE user_id=? AND relay_node_id=? AND billing_epoch=?`, userID, relayNodeID, epoch).Scan(&lease.ID, &lease.UserID, &lease.MeterKey, &lease.RelayNodeID, &lease.BillingEpoch, &reserved, &lease.Sequence, &lease.Status, &expires, &created, &updated)
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return TrafficLease{}, false, err
	}
	currentExpiry := parseDatabaseTime(expires)
	currentActive := exists && lease.Status == "active" && currentExpiry.After(now)
	currentRemaining := reserved - localUsed
	if currentRemaining < 0 {
		currentRemaining = 0
	}
	chunk := trafficLeaseChunk(limit)
	if currentActive && currentExpiry.After(now.Add(trafficLeaseRenewWindow)) && currentRemaining > chunk/4 {
		lease.ReservedBytes = uint64(maxInt64(reserved, 0))
		lease.UsedBytes = uint64(maxInt64(localUsed, 0))
		lease.ExpiresAt, lease.CreatedAt, lease.UpdatedAt = currentExpiry, parseDatabaseTime(created), parseDatabaseTime(updated)
		return lease, false, nil
	}
	available := limit - globalUsed - otherReserved
	if available < 0 {
		available = 0
	}
	// Never reclaim an unexpired reservation. The Agent may still be serving
	// the previous signed-off ceiling until its local lease deadline. Let that
	// lease expire first, then issue a smaller slice without creating overlap.
	if currentActive && currentRemaining > available {
		lease.ReservedBytes = uint64(maxInt64(reserved, 0))
		lease.UsedBytes = uint64(maxInt64(localUsed, 0))
		lease.ExpiresAt, lease.CreatedAt, lease.UpdatedAt = currentExpiry, parseDatabaseTime(created), parseDatabaseTime(updated)
		return lease, false, nil
	}
	allocation := int64(0)
	if currentActive {
		allocation = currentRemaining
	}
	if !currentActive || currentRemaining <= chunk/4 {
		allocation += chunk
	}
	if allocation > available {
		allocation = available
	}
	newReserved := localUsed + allocation
	newExpiry := now.Add(trafficLeaseDuration)
	meterKey := fmt.Sprintf("user:%d", userID)
	if !exists {
		lease = TrafficLease{ID: randomID("lease"), UserID: userID, MeterKey: meterKey, RelayNodeID: relayNodeID, BillingEpoch: epoch, Sequence: 1, Status: "active", CreatedAt: now}
		if _, err := tx.ExecContext(ctx, `INSERT INTO traffic_leases(id,user_id,meter_key,relay_node_id,billing_epoch,reserved_bytes,sequence,status,expires_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, lease.ID, userID, meterKey, relayNodeID, epoch, newReserved, lease.Sequence, "active", newExpiry.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
			return TrafficLease{}, false, err
		}
	} else {
		lease.Sequence++
		if _, err := tx.ExecContext(ctx, `UPDATE traffic_leases SET reserved_bytes=?,sequence=?,status='active',expires_at=?,updated_at=? WHERE id=?`, newReserved, lease.Sequence, newExpiry.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), lease.ID); err != nil {
			return TrafficLease{}, false, err
		}
	}
	lease.MeterKey = meterKey
	lease.ReservedBytes = uint64(maxInt64(newReserved, 0))
	lease.UsedBytes = uint64(maxInt64(localUsed, 0))
	lease.Status = "active"
	lease.ExpiresAt = newExpiry
	lease.UpdatedAt = now
	if lease.CreatedAt.IsZero() {
		lease.CreatedAt = parseDatabaseTime(created)
	}
	return lease, true, nil
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

func (s *Store) EnsureTrafficLeases(ctx context.Context, relayNodeID string, services []RelayService) (map[int64]TrafficLease, error) {
	users := make(map[int64]struct{})
	for _, service := range services {
		if service.UserID != nil && service.Enabled && service.UserEnabled {
			users[*service.UserID] = struct{}{}
		}
	}
	result := make(map[int64]TrafficLease, len(users))
	var capabilities string
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(capabilities_json,'[]') FROM relay_nodes WHERE id=?`, relayNodeID).Scan(&capabilities); err != nil {
		return nil, err
	}
	if !containsJSONText(capabilities, "quota_leases_v1") {
		return result, nil
	}
	changed := false
	for userID := range users {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		lease, leaseChanged, err := ensureTrafficLeaseTx(ctx, tx, userID, relayNodeID, time.Now().UTC())
		if err != nil {
			tx.Rollback()
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		result[userID] = lease
		changed = changed || leaseChanged
	}
	if changed {
		if _, err := s.db.ExecContext(ctx, `UPDATE relay_nodes SET desired_revision=desired_revision+1 WHERE id=?`, relayNodeID); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func renewTrafficLeasesTx(ctx context.Context, tx *sql.Tx, relayNodeID string, statuses []protocol.ServiceStatus, now time.Time) error {
	supported, err := relaySupportsQuotaLeasesTx(ctx, tx, relayNodeID)
	if err != nil {
		return err
	}
	if !supported {
		return nil
	}
	users := make(map[int64]struct{})
	for _, status := range statuses {
		var userID sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT rs.user_id FROM relay_services rs LEFT JOIN console_users u ON u.id=rs.user_id WHERE rs.id=? AND rs.relay_node_id=? AND rs.enabled=1 AND COALESCE(u.enabled,1)=1`, status.ID, relayNodeID).Scan(&userID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return err
		}
		if userID.Valid {
			users[userID.Int64] = struct{}{}
		}
	}
	changed := false
	for userID := range users {
		_, leaseChanged, err := ensureTrafficLeaseTx(ctx, tx, userID, relayNodeID, now)
		if err != nil {
			return err
		}
		changed = changed || leaseChanged
	}
	if changed {
		_, err := tx.ExecContext(ctx, `UPDATE relay_nodes SET desired_revision=desired_revision+1 WHERE id=?`, relayNodeID)
		return err
	}
	return nil
}

func releaseUserTrafficLeasesTx(ctx context.Context, tx *sql.Tx, userID int64, now time.Time) error {
	_, err := tx.ExecContext(ctx, `UPDATE traffic_leases SET status='released',updated_at=? WHERE user_id=? AND status='active'`, now.Format(time.RFC3339Nano), userID)
	return err
}

func relaySupportsQuotaLeasesTx(ctx context.Context, tx *sql.Tx, relayNodeID string) (bool, error) {
	return relaySupportsCapabilityTx(ctx, tx, relayNodeID, "quota_leases_v1")
}

func relaySupportsCapabilityTx(ctx context.Context, tx *sql.Tx, relayNodeID, capability string) (bool, error) {
	var raw string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(capabilities_json,'[]') FROM relay_nodes WHERE id=?`, relayNodeID).Scan(&raw); err != nil {
		return false, err
	}
	return containsJSONText(raw, capability), nil
}

func containsJSONText(raw, expected string) bool {
	var values []string
	if jsonErr := json.Unmarshal([]byte(raw), &values); jsonErr != nil {
		return false
	}
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
