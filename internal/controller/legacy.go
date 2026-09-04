package controller

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type SystemLog struct {
	ID        int64      `json:"id"`
	Level     string     `json:"level"`
	Category  string     `json:"category"`
	Message   string     `json:"message"`
	CreatedAt *time.Time `json:"created_at,omitempty"`
}

type SettingUpdate struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func (s *Store) ListSystemLogs(ctx context.Context, category string, limit int) ([]SystemLog, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query := `SELECT id,COALESCE(level,'info'),COALESCE(category,'system'),message,created_at FROM logs`
	args := []interface{}{}
	if strings.TrimSpace(category) != "" {
		query += ` WHERE category=?`
		args = append(args, strings.TrimSpace(category))
	}
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	logs := make([]SystemLog, 0)
	for rows.Next() {
		var item SystemLog
		var created sql.NullString
		if err := rows.Scan(&item.ID, &item.Level, &item.Category, &item.Message, &created); err != nil {
			return nil, err
		}
		if created.Valid {
			parsed := parseDatabaseTime(created.String)
			if !parsed.IsZero() {
				item.CreatedAt = &parsed
			}
		}
		logs = append(logs, item)
	}
	return logs, rows.Err()
}

func (s *Store) AddSystemLog(ctx context.Context, level, category, message string) error {
	level = strings.TrimSpace(level)
	if level == "" {
		level = "info"
	}
	category = strings.TrimSpace(category)
	if category == "" {
		category = "system"
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO logs(level,category,message,created_at) VALUES(?,?,?,?)`, level, category, message, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) ClearSystemLogs(ctx context.Context, category string) error {
	if strings.TrimSpace(category) == "" {
		_, err := s.db.ExecContext(ctx, `DELETE FROM logs`)
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM logs WHERE category=?`, strings.TrimSpace(category))
	return err
}

func (s *Store) GetPublicSettings(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key,COALESCE(value,'') FROM settings WHERE key NOT LIKE '%password_hash%' AND key NOT LIKE '%totp%' ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	settings := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		settings[key] = value
	}
	return settings, rows.Err()
}

func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(value,'') FROM settings WHERE key=?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}

func (s *Store) UpdateSettings(ctx context.Context, items []SettingUpdate) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	allowed := map[string]bool{"tg_bot_token": true, "tg_chat_id": true, "tg_daily_report": true}
	for _, item := range items {
		item.Key = strings.TrimSpace(item.Key)
		if !allowed[item.Key] {
			return errors.New("unsupported setting key")
		}
		if item.Key == "tg_daily_report" && item.Value != "0" && item.Value != "1" {
			return errors.New("tg_daily_report must be 0 or 1")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, item.Key, item.Value); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ChangeAdminPassword(ctx context.Context, password string) error {
	if len(password) < 8 {
		return errors.New("password must contain at least 8 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE settings SET value=? WHERE key='admin_password_hash'`, string(hash))
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return errors.New("administrator is not initialized")
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM admin_sessions`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) RenameCloudInstance(ctx context.Context, instanceID, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("instance name is required")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE instances SET instance_name=?,updated_at=? WHERE instance_id=?`, name, time.Now().UTC().Format(time.RFC3339Nano), instanceID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) RemoveCloudInstance(ctx context.Context, instanceID string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM instances WHERE instance_id=?`, instanceID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) UpdateCloudInstanceStatus(ctx context.Context, instanceID, status string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE instances SET status=?,updated_at=? WHERE instance_id=?`, status, time.Now().UTC().Format(time.RFC3339Nano), instanceID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) CloudInstanceStatus(ctx context.Context, instanceID string) (string, error) {
	var status string
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(status,'') FROM instances WHERE instance_id=?`, instanceID).Scan(&status)
	return status, err
}

func (s *Store) SetAccountManualStopped(ctx context.Context, accountID int64, stopped bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE accounts SET manual_stopped=? WHERE id=?`, boolInt(stopped), accountID)
	return err
}

// MarkRelayNodesForInstance immediately removes Relay nodes hosted by a
// stopped ECS instance from the online set. Heartbeats will promote the node
// back to online after the instance and its Agent have booted again. Without
// this transition, an entry pool can keep publishing a dead address for up to
// the stale-heartbeat timeout.
func (s *Store) MarkRelayNodesForInstance(ctx context.Context, instanceID, status string) error {
	instanceID = strings.TrimSpace(instanceID)
	if instanceID == "" {
		return nil
	}
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "stopped" || status == "offline" {
		_, err := s.db.ExecContext(ctx, `UPDATE relay_nodes SET status='offline' WHERE ecs_instance_id=?`, instanceID)
		return err
	}
	return nil
}

func (s *Store) SetAccountNoStockNotified(ctx context.Context, accountID int64, notified bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE accounts SET nostock_notified=? WHERE id=?`, boolInt(notified), accountID)
	return err
}

func (s *Store) MarkStaleRelayNodes(ctx context.Context, staleAfter time.Duration) (int, error) {
	if staleAfter <= 0 {
		staleAfter = 45 * time.Second
	}
	cutoff := time.Now().UTC().Add(-staleAfter).Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id,name FROM relay_nodes WHERE status='online' AND (last_seen_at IS NULL OR last_seen_at<?)`, cutoff)
	if err != nil {
		return 0, err
	}
	type staleNode struct{ id, name string }
	stale := make([]staleNode, 0)
	for rows.Next() {
		var node staleNode
		if err := rows.Scan(&node.id, &node.name); err != nil {
			rows.Close()
			return 0, err
		}
		stale = append(stale, node)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	if len(stale) == 0 {
		return 0, tx.Commit()
	}
	now := time.Now().UTC()
	for _, node := range stale {
		if _, err := tx.ExecContext(ctx, `UPDATE relay_nodes SET status='offline' WHERE id=? AND status='online'`, node.id); err != nil {
			return 0, err
		}
		if err := insertEvent(ctx, tx, node.id, "warning", "agent", fmt.Sprintf("Agent %s heartbeat timed out; marked offline", node.name), now); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(stale), nil
}
