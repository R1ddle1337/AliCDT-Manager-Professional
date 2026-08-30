package controller

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/protocol"
	_ "modernc.org/sqlite"
)

func TestCloudTrafficFailurePreservesLastValidValue(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	account, err := store.CreateCloudAccount(context.Background(), CloudAccountRequest{
		Name: "test", AccessKeyID: "key", AccessKeySecret: "secret",
		RegionID: "cn-hongkong", SiteType: "china", TrafficLimitGB: 200, ThresholdPercent: 95,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveCloudSync(context.Background(), account, nil, false, "", 123.456, true, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveCloudSync(context.Background(), account, nil, false, "", 0, false, "temporary timeout"); err != nil {
		t.Fatal(err)
	}
	overview, err := store.CloudOverview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(overview.Traffic) != 1 {
		t.Fatalf("unexpected traffic snapshots: %+v", overview.Traffic)
	}
	if overview.Traffic[0].UsedGB != 123.456 {
		t.Fatalf("last valid traffic was overwritten: %+v", overview.Traffic[0])
	}
	if overview.Traffic[0].LastError != "temporary timeout" {
		t.Fatalf("expected visible sync error, got %+v", overview.Traffic[0])
	}
}

func TestCloudInstanceFailurePreservesLastValidInventory(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	account, err := store.CreateCloudAccount(context.Background(), CloudAccountRequest{
		Name: "test", AccessKeyID: "key", AccessKeySecret: "secret",
		RegionID: "cn-hongkong", SiteType: "china", TrafficLimitGB: 200, ThresholdPercent: 95,
	})
	if err != nil {
		t.Fatal(err)
	}
	instances := []CloudInstanceUpdate{{InstanceID: "i-test", InstanceName: "relay", RegionID: "cn-hongkong", Status: "Running", PublicIP: "1.2.3.4"}}
	if err := store.SaveCloudSync(context.Background(), account, instances, true, "", 0, false, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveCloudSync(context.Background(), account, nil, false, "temporary timeout", 0, false, ""); err != nil {
		t.Fatal(err)
	}
	overview, err := store.CloudOverview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(overview.Instances) != 1 || overview.Instances[0].InstanceID != "i-test" {
		t.Fatalf("last valid instance inventory was lost: %+v", overview.Instances)
	}
}

func TestLegacyDatabaseMigrationPreservesCloudDataAndTraffic(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "legacy.db")
	legacy, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	statements := []string{
		`CREATE TABLE accounts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,name TEXT NOT NULL,access_key_id TEXT NOT NULL,
			access_key_secret TEXT NOT NULL,region_id TEXT NOT NULL,site_type TEXT DEFAULT 'international',
			instance_id TEXT,traffic_limit_gb REAL DEFAULT 200,threshold_percent REAL DEFAULT 95,
			outstanding_threshold REAL DEFAULT 0,shutdown_mode TEXT DEFAULT 'StopCharging',keep_alive INTEGER DEFAULT 0,
			auto_start_time TEXT,auto_stop_time TEXT,manual_stopped INTEGER DEFAULT 0,nostock_notified INTEGER DEFAULT 0,
			enabled INTEGER DEFAULT 1,created_at TEXT
		)`,
		`CREATE TABLE instances (
			id INTEGER PRIMARY KEY AUTOINCREMENT,account_id INTEGER NOT NULL,instance_id TEXT NOT NULL UNIQUE,
			instance_name TEXT,region_id TEXT,status TEXT DEFAULT 'Unknown',public_ip TEXT,instance_type TEXT,
			bandwidth_mbps INTEGER DEFAULT 0,traffic_used_gb REAL DEFAULT 0,traffic_percent REAL DEFAULT 0,
			is_spot INTEGER DEFAULT 0,last_synced TEXT,updated_at TEXT
		)`,
		`CREATE TABLE settings (key TEXT PRIMARY KEY,value TEXT)`,
		`INSERT INTO accounts(id,name,access_key_id,access_key_secret,region_id,site_type,traffic_limit_gb,threshold_percent,enabled)
			VALUES(42,'legacy account','legacy-id','legacy-secret','cn-hongkong','china',200,95,1)`,
		`INSERT INTO instances(account_id,instance_id,instance_name,region_id,status,public_ip,instance_type,bandwidth_mbps,traffic_used_gb,last_synced)
			VALUES(42,'i-legacy','legacy relay','cn-hongkong','Running','203.0.113.8','ecs.t6-c1m1.large',100,87.5,'2026-08-29 12:00:00')`,
	}
	for _, statement := range statements {
		if _, err := legacy.Exec(statement); err != nil {
			legacy.Close()
			t.Fatal(err)
		}
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := OpenStore(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	overview, err := store.CloudOverview(context.Background())
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	if len(overview.Accounts) != 1 || overview.Accounts[0].ID != 42 || overview.Accounts[0].AccessKeySecret != "" {
		store.Close()
		t.Fatalf("legacy account was not safely preserved: %+v", overview.Accounts)
	}
	if len(overview.Instances) != 1 || overview.Instances[0].InstanceID != "i-legacy" {
		store.Close()
		t.Fatalf("legacy instance was not preserved: %+v", overview.Instances)
	}
	if len(overview.Traffic) != 1 || overview.Traffic[0].AccountID != 42 || overview.Traffic[0].UsedGB != 87.5 {
		store.Close()
		t.Fatalf("legacy traffic was not migrated: %+v", overview.Traffic)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopening runs migrations again and must not overwrite the seeded last
	// valid snapshot even if a legacy instance value later becomes zero.
	legacy, err = sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`UPDATE instances SET traffic_used_gb=0 WHERE instance_id='i-legacy'`); err != nil {
		legacy.Close()
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = OpenStore(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	overview, err = store.CloudOverview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(overview.Traffic) != 1 || overview.Traffic[0].UsedGB != 87.5 {
		t.Fatalf("idempotent migration overwrote the last valid traffic: %+v", overview.Traffic)
	}
}

func TestCloudOverviewUsesEmptyArraysInsteadOfNull(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	overview, err := store.CloudOverview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if overview.Accounts == nil || overview.Instances == nil || overview.Traffic == nil {
		t.Fatalf("cloud overview must expose JSON arrays: %+v", overview)
	}
}

func TestAgentEnrollmentImmediatelyAssociatesSyncedECS(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	account, err := store.CreateCloudAccount(context.Background(), CloudAccountRequest{
		Name: "test", AccessKeyID: "key", AccessKeySecret: "secret",
		RegionID: "cn-hongkong", SiteType: "china", TrafficLimitGB: 200, ThresholdPercent: 95,
	})
	if err != nil {
		t.Fatal(err)
	}
	instances := []CloudInstanceUpdate{{InstanceID: "i-associated", InstanceName: "relay", RegionID: "cn-hongkong", Status: "Running", PublicIP: "203.0.113.20"}}
	if err := store.SaveCloudSync(context.Background(), account, instances, true, "", 12, true, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateEnrollmentToken(context.Background(), "associate-once", time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnrollAgent(context.Background(), protocol.AgentEnrollmentRequest{
		Token: "associate-once", NodeName: "relay", PublicIP: "203.0.113.20", ECSInstanceID: "i-associated",
		RegionID: "cn-hongkong", Architecture: "amd64", OS: "linux", AgentVersion: "test",
	}); err != nil {
		t.Fatal(err)
	}
	nodes, err := store.ListRelayNodes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].CloudAccountID == nil || *nodes[0].CloudAccountID != account.ID {
		t.Fatalf("agent was not associated during enrollment: %+v", nodes)
	}
	if err := store.DeleteCloudAccount(context.Background(), account.ID); err != nil {
		t.Fatal(err)
	}
	nodes, err = store.ListRelayNodes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].CloudAccountID != nil {
		t.Fatalf("deleted account left a dangling relay association: %+v", nodes)
	}
}
