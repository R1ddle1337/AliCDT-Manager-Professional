package controller

import (
	"context"
	"testing"
)

func TestListSystemLogsParsesLegacyUTCAndSkipsInvalidTimes(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if _, err := store.db.Exec(`INSERT INTO logs(level,category,message,created_at) VALUES(?,?,?,?)`,
		"warning", "system", "legacy", "2026-08-29 13:26:35.134312"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO logs(level,category,message,created_at) VALUES(?,?,?,?)`,
		"warning", "system", "invalid", "not-a-timestamp"); err != nil {
		t.Fatal(err)
	}

	logs, err := store.ListSystemLogs(context.Background(), "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 2 {
		t.Fatalf("unexpected logs: %+v", logs)
	}
	if logs[1].CreatedAt == nil || logs[1].CreatedAt.Format("2006-01-02T15:04:05.999999Z07:00") != "2026-08-29T13:26:35.134312Z" {
		t.Fatalf("legacy timestamp was not normalized: %+v", logs[1].CreatedAt)
	}
	if logs[0].CreatedAt != nil {
		t.Fatalf("invalid timestamp should be omitted, got %v", logs[0].CreatedAt)
	}
}
