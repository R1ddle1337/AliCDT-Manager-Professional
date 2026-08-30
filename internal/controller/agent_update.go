package controller

import (
	"context"
	"errors"
	"strings"
	"time"
)

func (s *Store) SetAgentUpdateState(ctx context.Context, id, status, updateErr string) error {
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "" {
		status = "idle"
	}
	if !oneOf(status, "idle", "draining", "updating", "failed") {
		return errors.New("invalid agent update status")
	}
	_, err := s.db.ExecContext(ctx, `UPDATE relay_nodes SET update_status=?,update_error=?,update_at=? WHERE id=?`, status, strings.TrimSpace(updateErr), time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}
