package controller

import (
	"context"
	"testing"
	"time"
)

func TestMaintenanceRemovesOnlyExpiredAuthenticationState(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	expired := now.Add(-time.Hour).Format(time.RFC3339Nano)
	oldUsed := now.Add(-48 * time.Hour).Format(time.RFC3339Nano)

	if _, err := store.db.ExecContext(ctx, `INSERT INTO admin_sessions(token_hash,username,expires_at,created_at) VALUES('expired-admin','admin',?,?)`, expired, expired); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO enrollment_tokens(token_hash,expires_at,used_at,created_at) VALUES('used-token',?,?,?)`, now.Add(time.Hour).Format(time.RFC3339Nano), oldUsed, oldUsed); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO admin_sessions(token_hash,username,expires_at,created_at) VALUES('live-admin','admin',?,?)`, now.Add(time.Hour).Format(time.RFC3339Nano), expired); err != nil {
		t.Fatal(err)
	}

	result, err := store.RunMaintenance(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.AdminSessions != 1 || result.EnrollmentToken != 1 {
		t.Fatalf("unexpected maintenance result: %+v", result)
	}
	var liveSessions int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_sessions WHERE token_hash='live-admin'`).Scan(&liveSessions); err != nil {
		t.Fatal(err)
	}
	if liveSessions != 1 {
		t.Fatal("maintenance deleted a live session")
	}
}

func TestScheduledCyclesSkipInsteadOfQueueing(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := NewCloudService(store)
	service.syncMu.Lock()
	if service.trySyncAll(context.Background()) {
		t.Fatal("scheduled sync ran while another sync held the gate")
	}
	service.syncMu.Unlock()
	service.automationMu.Lock()
	if service.tryAutomationCycle(context.Background(), time.Now()) {
		t.Fatal("automation cycle ran while another cycle held the gate")
	}
	service.automationMu.Unlock()
}
