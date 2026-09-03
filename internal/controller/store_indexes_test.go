package controller

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestStoreCreatesHighFrequencyQueryIndexes(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	required := []string{
		"idx_instances_account",
		"idx_relay_nodes_cloud_account",
		"idx_relay_services_pool",
		"idx_service_targets_landing",
		"idx_dns_records_relay",
		"idx_dns_record_pools_pool",
		"idx_user_sessions_expiry",
		"idx_usage_checkpoints_user_epoch",
		"idx_relay_events_created",
	}
	for _, name := range required {
		var count int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, name).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Errorf("required index %s was not created", name)
		}
	}
}

func TestPerformanceIndexesMigrateLegacyDNSRecordSchema(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "legacy.db")
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.Exec(`CREATE TABLE dns_managed_records (
		id TEXT PRIMARY KEY,
		provider_id TEXT NOT NULL,
		name TEXT NOT NULL,
		type TEXT NOT NULL DEFAULT 'A',
		value TEXT NOT NULL,
		ttl INTEGER NOT NULL DEFAULT 60,
		enabled INTEGER NOT NULL DEFAULT 1,
		provider_record_id TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'pending',
		last_error TEXT NOT NULL DEFAULT '',
		last_synced_at TEXT,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		UNIQUE(provider_id,name,type,value)
	)`)
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := OpenStore(databasePath)
	if err != nil {
		t.Fatalf("legacy database migration failed: %v", err)
	}
	defer store.Close()
	for _, name := range []string{"idx_dns_records_relay", "idx_dns_records_pool"} {
		var count int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, name).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Errorf("legacy migration did not create %s", name)
		}
	}
}
