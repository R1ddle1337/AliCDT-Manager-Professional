package controller

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

const maxAgentUpdateErrorRunes = 500

func normalizeAgentUpdateState(status, updateErr string) (string, string, error) {
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "" {
		status = "idle"
	}
	if !oneOf(status, "idle", "draining", "updating", "failed") {
		return "", "", errors.New("invalid agent update status")
	}
	updateErr = strings.TrimSpace(updateErr)
	runes := []rune(updateErr)
	if len(runes) > maxAgentUpdateErrorRunes {
		updateErr = string(runes[:maxAgentUpdateErrorRunes])
	}
	return status, updateErr, nil
}

func (s *Store) SetAgentUpdateState(ctx context.Context, id, status, updateErr string) error {
	status, updateErr, err := normalizeAgentUpdateState(status, updateErr)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE relay_nodes SET update_status=?,update_error=?,update_at=? WHERE id=?`, status, updateErr, time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}

// RequestLegacyAgentUpgrades queues only Agents that do not advertise the
// capabilities required by the current controller. New Agents can update
// themselves through force_update; old Agents need the host-side SSH bridge.
func (s *Store) RequestLegacyAgentUpgrades(ctx context.Context) ([]RelayNode, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM relay_nodes WHERE COALESCE(capabilities_json,'[]') NOT LIKE '%shared_meters_v1%' OR COALESCE(capabilities_json,'[]') NOT LIKE '%quota_leases_v1%'`)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []RelayNode{}, nil
	}
	return s.RequestAgentUpgrades(ctx, ids, "管理员已请求宿主机兼容通道升级 Agent")
}

// RequestAgentUpgrades atomically queues a known set of Agents. Keeping the
// batch in one transaction prevents a partially queued fleet when a database
// error occurs midway through an upgrade-all request.
func (s *Store) RequestAgentUpgrades(ctx context.Context, ids []string, eventMessage string) ([]RelayNode, error) {
	if len(ids) == 0 {
		return []RelayNode{}, nil
	}
	uniqueIDs := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		uniqueIDs = append(uniqueIDs, id)
	}
	if len(uniqueIDs) == 0 {
		return []RelayNode{}, nil
	}
	if strings.TrimSpace(eventMessage) == "" {
		eventMessage = "管理员已请求远程升级 Agent"
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	for _, id := range uniqueIDs {
		result, err := tx.ExecContext(ctx, `UPDATE relay_nodes SET update_status='requested',update_error='',update_at=?,desired_revision=desired_revision+1 WHERE id=?`, now.Format(time.RFC3339Nano), id)
		if err != nil {
			return nil, err
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			return nil, sql.ErrNoRows
		}
		if err := insertEvent(ctx, tx, id, "info", "agent_update", eventMessage, now); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	all, err := s.ListRelayNodes(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]RelayNode, len(all))
	for _, node := range all {
		byID[node.ID] = node
	}
	result := make([]RelayNode, 0, len(uniqueIDs))
	for _, id := range uniqueIDs {
		if node, exists := byID[id]; exists {
			result = append(result, node)
		}
	}
	return result, nil
}

func (s *Store) MarkAgentUpgradeFailed(ctx context.Context, id, message string) error {
	_, message, _ = normalizeAgentUpdateState("failed", message)
	if message == "" {
		message = "宿主机 Agent 升级失败"
	}
	result, err := s.db.ExecContext(ctx, `UPDATE relay_nodes SET update_status='failed',update_error=?,update_at=? WHERE id=?`, message, time.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}
