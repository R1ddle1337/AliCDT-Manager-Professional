package controller

import (
	"context"
	"fmt"
	"time"
)

const (
	maintenanceInterval = 6 * time.Hour
)

type MaintenanceResult struct {
	AdminSessions   int64
	UserSessions    int64
	EnrollmentToken int64
}

func (s *Store) RunMaintenance(ctx context.Context, now time.Time) (MaintenanceResult, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MaintenanceResult{}, err
	}
	defer tx.Rollback()

	result := MaintenanceResult{}
	operations := []struct {
		name        string
		query       string
		args        []interface{}
		destination *int64
	}{
		{"expired admin sessions", `DELETE FROM admin_sessions WHERE expires_at<=?`, []interface{}{now.Format(time.RFC3339Nano)}, &result.AdminSessions},
		{"expired user sessions", `DELETE FROM user_sessions WHERE expires_at<=?`, []interface{}{now.Format(time.RFC3339Nano)}, &result.UserSessions},
		{"spent enrollment tokens", `DELETE FROM enrollment_tokens WHERE expires_at<=? OR (used_at IS NOT NULL AND used_at<=?)`, []interface{}{now.Format(time.RFC3339Nano), now.Add(-24 * time.Hour).Format(time.RFC3339Nano)}, &result.EnrollmentToken},
	}
	for _, operation := range operations {
		execution, err := tx.ExecContext(ctx, operation.query, operation.args...)
		if err != nil {
			return MaintenanceResult{}, fmt.Errorf("clean %s: %w", operation.name, err)
		}
		if affected, affectedErr := execution.RowsAffected(); affectedErr == nil {
			*operation.destination = affected
		}
	}
	if err := tx.Commit(); err != nil {
		return MaintenanceResult{}, err
	}
	if _, err := s.db.ExecContext(ctx, `PRAGMA optimize`); err != nil {
		return result, fmt.Errorf("optimize sqlite: %w", err)
	}
	return result, nil
}

func (s *Server) RunMaintenanceScheduler(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = maintenanceInterval
	}
	run := func() {
		maintenanceCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if result, err := s.store.RunMaintenance(maintenanceCtx, time.Now()); err != nil {
			_ = s.store.AddSystemLog(context.Background(), "warning", "maintenance", "数据库维护失败: "+err.Error())
		} else if result.AdminSessions+result.UserSessions+result.EnrollmentToken > 0 {
			_ = s.store.AddSystemLog(maintenanceCtx, "info", "maintenance", "已清理过期会话和注册令牌")
		}
	}
	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
