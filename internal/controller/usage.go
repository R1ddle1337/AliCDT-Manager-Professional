package controller

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/protocol"
)

func boundedUsageBytes(value uint64) int64 {
	if value > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(value)
}

func gbToLedgerBytes(value float64) (int64, error) {
	bytes := value * float64(1024*1024*1024)
	if math.IsNaN(bytes) || math.IsInf(bytes, 0) || bytes > math.MaxInt64 || bytes < math.MinInt64 {
		return 0, errors.New("traffic adjustment is outside the supported range")
	}
	return int64(math.Round(bytes)), nil
}

// recordUsageHeartbeatTx converts cumulative Agent counters into append-only
// deltas. A repeated or out-of-order heartbeat cannot add usage twice.
func recordUsageHeartbeatTx(ctx context.Context, tx *sql.Tx, relayNodeID string, statuses []protocol.ServiceStatus, now time.Time) error {
	type meterReading struct {
		userID    int64
		meterKey  string
		epoch     int64
		direction string
		total     uint64
		bytesUp   uint64
		bytesDown uint64
		leaseID   string
		sequence  int64
	}
	readings := make(map[string]meterReading)
	for _, status := range statuses {
		if status.ID == "" || status.BillingMode == "" || status.BillingEpoch <= 0 {
			continue
		}
		var userID sql.NullInt64
		var configuredMode string
		var configuredEpoch int64
		if err := tx.QueryRowContext(ctx, `SELECT rs.user_id,COALESCE(u.billing_mode,rs.billing_mode,'both'),COALESCE(u.billing_epoch,rs.billing_epoch,1)
			FROM relay_services rs LEFT JOIN console_users u ON u.id=rs.user_id WHERE rs.id=? AND rs.relay_node_id=?`, status.ID, relayNodeID).Scan(&userID, &configuredMode, &configuredEpoch); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return err
		}
		if !userID.Valid || status.BillingEpoch != effectiveBillingEpoch(configuredEpoch) {
			continue
		}
		if status.QuotaLeaseID != "" {
			var leaseUserID int64
			var leaseRelayID, leaseStatus string
			var leaseEpoch, leaseSequence, leaseReserved int64
			leaseErr := tx.QueryRowContext(ctx, `SELECT user_id,relay_node_id,billing_epoch,sequence,reserved_bytes,status FROM traffic_leases WHERE id=?`, status.QuotaLeaseID).Scan(&leaseUserID, &leaseRelayID, &leaseEpoch, &leaseSequence, &leaseReserved, &leaseStatus)
			if leaseErr != nil || leaseStatus != "active" || leaseUserID != userID.Int64 || leaseRelayID != relayNodeID || leaseEpoch != status.BillingEpoch || status.QuotaLeaseSequence < leaseSequence {
				continue
			}
			total := billedBytesFromStatus(status, configuredMode)
			if total > uint64(maxInt64(leaseReserved, 0)) {
				continue
			}
		}
		meterKey := fmt.Sprintf("user:%d", userID.Int64)
		total := billedBytesFromStatus(status, configuredMode)
		key := fmt.Sprintf("%s/%d", meterKey, status.BillingEpoch)
		if previous, exists := readings[key]; !exists || total > previous.total {
			readings[key] = meterReading{userID: userID.Int64, meterKey: meterKey, epoch: status.BillingEpoch, direction: configuredMode, total: total, bytesUp: status.BytesUp, bytesDown: status.BytesDown, leaseID: status.QuotaLeaseID, sequence: status.QuotaLeaseSequence}
		}
	}
	for _, reading := range readings {
		var previous int64
		err := tx.QueryRowContext(ctx, `SELECT total_bytes FROM usage_meter_checkpoints WHERE relay_node_id=? AND meter_key=? AND billing_epoch=?`, relayNodeID, reading.meterKey, reading.epoch).Scan(&previous)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		current := boundedUsageBytes(reading.total)
		if err == nil && current <= previous {
			continue
		}
		delta := current
		if err == nil {
			delta -= previous
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO usage_meter_checkpoints(relay_node_id,meter_key,user_id,billing_epoch,total_bytes,bytes_up,bytes_down,lease_id,sequence,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(relay_node_id,meter_key,billing_epoch) DO UPDATE SET total_bytes=excluded.total_bytes,bytes_up=excluded.bytes_up,bytes_down=excluded.bytes_down,lease_id=excluded.lease_id,sequence=MAX(usage_meter_checkpoints.sequence,excluded.sequence),updated_at=excluded.updated_at`, relayNodeID, reading.meterKey, reading.userID, reading.epoch, current, boundedUsageBytes(reading.bytesUp), boundedUsageBytes(reading.bytesDown), reading.leaseID, reading.sequence, now.Format(time.RFC3339Nano)); err != nil {
			return err
		}
		if delta == 0 {
			continue
		}
		var aggregate int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(total_bytes),0) FROM usage_meter_checkpoints WHERE user_id=? AND billing_epoch=?`, reading.userID, reading.epoch).Scan(&aggregate); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO usage_ledger(id,user_id,meter_key,billing_epoch,direction,kind,delta_bytes,total_bytes,note,source,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, randomID("usage"), reading.userID, reading.meterKey, reading.epoch, reading.direction, "usage", delta, aggregate, "Agent heartbeat usage delta", relayNodeID, now.Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ListUsageLedger(ctx context.Context, userID int64, limit int) ([]UsageLedgerEntry, error) {
	if userID <= 0 {
		return nil, errors.New("invalid user id")
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,user_id,meter_key,billing_epoch,direction,kind,delta_bytes,total_bytes,note,source,created_at FROM usage_ledger WHERE user_id=? ORDER BY created_at DESC,id DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := make([]UsageLedgerEntry, 0)
	for rows.Next() {
		var entry UsageLedgerEntry
		var total int64
		var created string
		if err := rows.Scan(&entry.ID, &entry.UserID, &entry.MeterKey, &entry.BillingEpoch, &entry.Direction, &entry.Kind, &entry.DeltaBytes, &total, &entry.Note, &entry.Source, &created); err != nil {
			return nil, err
		}
		if total > 0 {
			entry.TotalBytes = uint64(total)
		}
		entry.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func (s *Store) AdjustUserTrafficLimit(ctx context.Context, userID int64, request UsageAdjustmentRequest) (ConsoleUser, error) {
	deltaBytes, err := gbToLedgerBytes(request.DeltaGB)
	if err != nil || deltaBytes == 0 {
		if err != nil {
			return ConsoleUser{}, err
		}
		return ConsoleUser{}, errors.New("quota adjustment must not be zero")
	}
	note := strings.TrimSpace(request.Note)
	if note == "" {
		return ConsoleUser{}, errors.New("an audit note is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ConsoleUser{}, err
	}
	defer tx.Rollback()
	var currentLimit float64
	var epoch int64
	if err := tx.QueryRowContext(ctx, `SELECT traffic_limit_gb,COALESCE(billing_epoch,1) FROM console_users WHERE id=?`, userID).Scan(&currentLimit, &epoch); err != nil {
		return ConsoleUser{}, err
	}
	newLimit := currentLimit + request.DeltaGB
	if newLimit <= 0 || newLimit > 1_000_000_000 {
		return ConsoleUser{}, errors.New("adjusted traffic limit must remain greater than zero")
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `UPDATE console_users SET traffic_limit_gb=?,updated_at=? WHERE id=?`, newLimit, now.Format(time.RFC3339Nano), userID); err != nil {
		return ConsoleUser{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE relay_services SET traffic_limit_gb=?,updated_at=? WHERE user_id=?`, newLimit, now.Format(time.RFC3339Nano), userID); err != nil {
		return ConsoleUser{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE relay_nodes SET desired_revision=desired_revision+1 WHERE id IN (SELECT relay_node_id FROM relay_services WHERE user_id=?)`, userID); err != nil {
		return ConsoleUser{}, err
	}
	limitBytes, err := gbToLedgerBytes(newLimit)
	if err != nil {
		return ConsoleUser{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO usage_ledger(id,user_id,meter_key,billing_epoch,direction,kind,delta_bytes,total_bytes,note,source,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, randomID("usage"), userID, fmt.Sprintf("user:%d", userID), effectiveBillingEpoch(epoch), "quota", "quota_adjustment", deltaBytes, limitBytes, note, "admin", now.Format(time.RFC3339Nano)); err != nil {
		return ConsoleUser{}, err
	}
	if err := tx.Commit(); err != nil {
		return ConsoleUser{}, err
	}
	return s.ConsoleUserOverview(ctx, userID)
}

func insertUsageResetTx(ctx context.Context, tx *sql.Tx, userID, epoch int64, note string, now time.Time) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO usage_ledger(id,user_id,meter_key,billing_epoch,direction,kind,delta_bytes,total_bytes,note,source,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, randomID("usage"), userID, fmt.Sprintf("user:%d", userID), effectiveBillingEpoch(epoch), "quota", "reset", 0, 0, note, "admin", now.Format(time.RFC3339Nano))
	return err
}
