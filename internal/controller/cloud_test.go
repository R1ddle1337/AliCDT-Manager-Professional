package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/aliyun"
	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/protocol"
	_ "modernc.org/sqlite"
)

func TestCloudOverviewKeepsAccountTrafficSeparateFromInstances(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	account, err := store.CreateCloudAccount(context.Background(), CloudAccountRequest{
		Name: "shared-account", AccessKeyID: "key", AccessKeySecret: "secret",
		RegionID: "cn-hongkong", SiteType: "china", TrafficLimitGB: 200, ThresholdPercent: 95,
	})
	if err != nil {
		t.Fatal(err)
	}
	instances := []CloudInstanceUpdate{
		{InstanceID: "i-one", InstanceName: "relay-one", RegionID: "cn-hongkong", Status: "Running", PublicIP: "203.0.113.1"},
		{InstanceID: "i-two", InstanceName: "relay-two", RegionID: "cn-hongkong", Status: "Running", PublicIP: "203.0.113.2"},
	}
	if err := store.SaveCloudSync(context.Background(), account, instances, true, "", 12.5, true, ""); err != nil {
		t.Fatal(err)
	}
	overview, err := store.CloudOverview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(overview.Instances) != 2 || len(overview.Traffic) != 1 {
		t.Fatalf("unexpected overview cardinality: %+v", overview)
	}
	if overview.Traffic[0].Scope != TrafficScopeAccount {
		t.Fatalf("expected account-scoped traffic snapshot, got %+v", overview.Traffic[0])
	}
	encoded, err := json.Marshal(overview)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Instances []map[string]interface{} `json:"instances"`
		Traffic   []map[string]interface{} `json:"traffic"`
	}
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatal(err)
	}
	for _, instance := range payload.Instances {
		if _, exists := instance["traffic_used_gb"]; exists {
			t.Fatalf("instance payload must not expose account traffic as per-instance usage: %v", instance)
		}
		if _, exists := instance["traffic_percent"]; exists {
			t.Fatalf("instance payload must not expose account traffic percent: %v", instance)
		}
	}
	if got := payload.Traffic[0]["scope"]; got != TrafficScopeAccount {
		t.Fatalf("traffic payload scope = %v, want %q", got, TrafficScopeAccount)
	}
}

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

func TestCloudSyncUpdatesRelayPublicIPWhenECSAddressChanges(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	account, err := store.CreateCloudAccount(context.Background(), CloudAccountRequest{
		Name: "ip-change", AccessKeyID: "key", AccessKeySecret: "secret", RegionID: "cn-hongkong", SiteType: "china",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateEnrollmentToken(context.Background(), "ip-change-token", time.Hour, account.ID); err != nil {
		t.Fatal(err)
	}
	agent, err := store.EnrollAgent(context.Background(), protocol.AgentEnrollmentRequest{Token: "ip-change-token", NodeName: "relay", ECSInstanceID: "i-ip", PublicIP: "203.0.113.60"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveCloudSync(context.Background(), account, []CloudInstanceUpdate{{InstanceID: "i-ip", PublicIP: "203.0.113.61", RegionID: "cn-hongkong", Status: "Running"}}, true, "", 0, false, ""); err != nil {
		t.Fatal(err)
	}
	nodes, err := store.ListRelayNodes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].ID != agent.AgentID || nodes[0].PublicIP != "203.0.113.61" {
		t.Fatalf("relay public IP was not refreshed: %+v", nodes)
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
	if len(overview.Accounts) != 1 || overview.Accounts[0].ID != 42 || overview.Accounts[0].AccessKeySecret != "" || overview.Accounts[0].ProtectionMode != ProtectionAlertOnly || overview.Accounts[0].ProtectionTriggered {
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

func TestScheduledPowerUpdatesInstanceAndRelayProjection(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	account, err := store.CreateCloudAccount(ctx, CloudAccountRequest{
		Name: "scheduled", AccessKeyID: "key", AccessKeySecret: "secret", RegionID: "cn-hongkong", SiteType: "china",
		ProtectedInstanceID: "i-scheduled", AutoStopTime: "02:00", AutoStartTime: "03:00", ShutdownMode: "StopCharging",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveCloudSync(ctx, account, []CloudInstanceUpdate{{InstanceID: "i-scheduled", InstanceName: "edge", RegionID: "cn-hongkong", Status: "Running", PublicIP: "203.0.113.40"}}, true, "", 0, false, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateEnrollmentToken(ctx, "scheduled-agent", time.Hour); err != nil {
		t.Fatal(err)
	}
	enrolled, err := store.EnrollAgent(ctx, protocol.AgentEnrollmentRequest{Token: "scheduled-agent", NodeName: "edge", ECSInstanceID: "i-scheduled", PublicIP: "203.0.113.40"})
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeCloudClient{}
	service := NewCloudService(store)
	service.clientFor = func(CloudAccount) cloudClient { return fake }

	service.runScheduledPower(ctx, "02:00")
	if fake.stopCalls != 1 {
		t.Fatalf("scheduled stop calls=%d, want 1", fake.stopCalls)
	}
	overview, err := store.CloudOverview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(overview.Instances) != 1 || overview.Instances[0].Status != "Stopped" {
		t.Fatalf("scheduled stop did not update instance projection: %+v", overview.Instances)
	}
	nodes, err := store.ListRelayNodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].ID != enrolled.AgentID || nodes[0].Status != "offline" {
		t.Fatalf("scheduled stop did not remove relay from online set: %+v", nodes)
	}

	service.runScheduledPower(ctx, "03:00")
	if fake.startCalls != 1 {
		t.Fatalf("scheduled start calls=%d, want 1", fake.startCalls)
	}
	accounts, err := store.ListCloudAccounts(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 || accounts[0].ManualStopped {
		t.Fatalf("scheduled start did not clear manual stop marker: %+v", accounts)
	}
	overview, err = store.CloudOverview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if overview.Instances[0].Status != "Running" {
		t.Fatalf("scheduled start did not update instance projection: %+v", overview.Instances)
	}
	// A replayed scheduler tick in the same minute must be harmless.
	service.runScheduledPower(ctx, "03:00")
	if fake.startCalls != 1 {
		t.Fatalf("scheduled start was not idempotent, calls=%d", fake.startCalls)
	}
}

func TestScheduledPowerMinutesReplaysShortTickerGap(t *testing.T) {
	service := &CloudService{}
	location := time.FixedZone("Asia/Shanghai", 8*60*60)
	first := time.Date(2026, 9, 2, 2, 0, 30, 0, location)
	if got := service.scheduledPowerMinutes(first); len(got) != 1 || got[0] != "02:00" {
		t.Fatalf("unexpected initial scheduler minute: %v", got)
	}
	if got := service.scheduledPowerMinutes(first.Add(3 * time.Minute)); len(got) != 3 || got[0] != "02:01" || got[1] != "02:02" || got[2] != "02:03" {
		t.Fatalf("short ticker gap was not replayed: %v", got)
	}
	if got := service.scheduledPowerMinutes(first.Add(20 * time.Minute)); len(got) != 1 || got[0] != "02:20" {
		t.Fatalf("long scheduler gap should run only the current minute: %v", got)
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

func TestAccountEnrollmentTokenAssociatesAgentWithoutECSMetadata(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	account, err := store.CreateCloudAccount(context.Background(), CloudAccountRequest{
		Name: "account-bound", AccessKeyID: "key", AccessKeySecret: "secret", RegionID: "cn-hongkong", SiteType: "china",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateEnrollmentToken(context.Background(), "account-token", time.Hour, account.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnrollAgent(context.Background(), protocol.AgentEnrollmentRequest{Token: "account-token", NodeName: "relay-no-metadata", Architecture: "amd64", OS: "linux"}); err != nil {
		t.Fatal(err)
	}
	accounts, err := store.ListCloudAccounts(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 || !accounts[0].AgentInstalled || accounts[0].AgentCount != 1 || accounts[0].OnlineAgentCount != 1 {
		t.Fatalf("account did not report its enrolled Agent: %+v", accounts)
	}
}

func TestDrainRelayProtectionIsIdempotentAndRecovers(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	account, nodeID := createProtectedRelay(t, store, ProtectionDrainRelay)

	initial, err := store.AgentConfig(context.Background(), nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if len(initial.Services) != 1 || !initial.Services[0].Enabled {
		t.Fatalf("expected enabled service before protection: %+v", initial)
	}
	decision, err := store.ApplyTrafficProtection(context.Background(), account.ID, 190)
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Changed || !decision.Triggered || decision.Mode != ProtectionDrainRelay {
		t.Fatalf("unexpected protection decision: %+v", decision)
	}
	suspended, err := store.AgentConfig(context.Background(), nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if len(suspended.Services) != 1 || suspended.Services[0].Enabled || suspended.Revision != initial.Revision+1 {
		t.Fatalf("relay was not suspended with a new revision: initial=%+v suspended=%+v", initial, suspended)
	}

	decision, err = store.ApplyTrafficProtection(context.Background(), account.ID, 191)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Changed {
		t.Fatalf("repeated over-threshold sync created another transition: %+v", decision)
	}
	repeated, err := store.AgentConfig(context.Background(), nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if repeated.Revision != suspended.Revision {
		t.Fatalf("repeated sync changed desired revision: %d -> %d", suspended.Revision, repeated.Revision)
	}

	// A failed traffic request saves the error but never clears active protection.
	if err := store.SaveCloudSync(context.Background(), account, nil, false, "", 0, false, "temporary CDT failure"); err != nil {
		t.Fatal(err)
	}
	accounts, err := store.ListCloudAccounts(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 || !accounts[0].ProtectionTriggered {
		t.Fatalf("CDT failure cleared protection: %+v", accounts)
	}

	decision, err = store.ApplyTrafficProtection(context.Background(), account.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Changed || decision.Triggered {
		t.Fatalf("expected protection recovery: %+v", decision)
	}
	recovered, err := store.AgentConfig(context.Background(), nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered.Services) != 1 || !recovered.Services[0].Enabled || recovered.Revision != suspended.Revision+1 {
		t.Fatalf("relay did not recover: %+v", recovered)
	}
	events, err := store.ListEvents(context.Background(), 50)
	if err != nil {
		t.Fatal(err)
	}
	transitions := 0
	for _, event := range events {
		if event.Category == "traffic_protection" {
			transitions++
		}
	}
	if transitions != 2 {
		t.Fatalf("expected one trigger and one recovery event, got %d: %+v", transitions, events)
	}
}

func TestTrafficRateTreatsCounterResetAsNewBillingPeriod(t *testing.T) {
	now := time.Now().UTC()
	previousAt := now.Add(-2 * time.Minute).Format(time.RFC3339Nano)
	rate, reset := trafficRate(120, now, sql.NullFloat64{Float64: 100, Valid: true}, sql.NullString{String: previousAt, Valid: true})
	if reset || rate < 9.9 || rate > 10.1 {
		t.Fatalf("unexpected traffic rate: rate=%f reset=%v", rate, reset)
	}
	rate, reset = trafficRate(80, now, sql.NullFloat64{Float64: 100, Valid: true}, sql.NullString{String: previousAt, Valid: true})
	if !reset || rate != 0 {
		t.Fatalf("counter reset was not detected: rate=%f reset=%v", rate, reset)
	}
}

func TestPredictiveTrafficProtectionDrainsBeforeHardThreshold(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	account, nodeID := createProtectedRelay(t, store, ProtectionDrainRelay)
	now := time.Now().UTC()
	// Simulate a previous successful sample so the next cloud response has a
	// meaningful rate. 170 GB is below the 180 GB hard threshold, but at the
	// observed rate it crosses that threshold inside a two-minute window.
	if _, err := store.db.Exec(`UPDATE account_traffic_snapshots
		SET used_gb=?,synced_at=?,previous_used_gb=?,previous_synced_at=? WHERE account_id=?`,
		160, now.Add(-2*time.Minute).Format(time.RFC3339Nano), 140, now.Add(-4*time.Minute).Format(time.RFC3339Nano), account.ID); err != nil {
		t.Fatal(err)
	}
	before, err := store.AgentConfig(context.Background(), nodeID)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := store.ApplyTrafficProtectionWithWindow(context.Background(), account.ID, 170, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Triggered || !decision.Predictive || decision.Percent >= 90 || decision.RateGBPerMinute < 7.4 || decision.ProjectedGB < 184 {
		t.Fatalf("unexpected predictive decision: %+v", decision)
	}
	protected, err := store.ListCloudAccounts(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(protected) != 1 || !protected[0].ProtectionTriggered || !protected[0].ProtectionPredictive {
		t.Fatalf("predictive marker was not persisted: %+v", protected)
	}
	after, err := store.AgentConfig(context.Background(), nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != before.Revision+1 || len(after.Services) != 1 || after.Services[0].Enabled {
		t.Fatalf("predictive protection did not drain the relay: before=%+v after=%+v", before, after)
	}

	// A later sample below the hard threshold must not reopen the relay merely
	// because the instantaneous rate fell to zero.
	decision, err = store.ApplyTrafficProtectionWithWindow(context.Background(), account.ID, 171, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Triggered || !decision.Predictive {
		t.Fatalf("predictive protection was not sticky: %+v", decision)
	}

	// A cumulative counter decrease represents a new billing period and allows
	// the relay to recover.
	decision, err = store.ApplyTrafficProtectionWithWindow(context.Background(), account.ID, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Triggered || decision.Predictive || !decision.Changed {
		t.Fatalf("counter reset did not recover protection: %+v", decision)
	}
	recovered, err := store.ListCloudAccounts(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered) != 1 || recovered[0].ProtectionTriggered || recovered[0].ProtectionPredictive {
		t.Fatalf("recovered account retained protection: %+v", recovered)
	}
}

func TestCloudOverviewExposesRecentTrafficRate(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	account, err := store.CreateCloudAccount(context.Background(), CloudAccountRequest{
		Name: "rate", AccessKeyID: "key", AccessKeySecret: "secret", RegionID: "cn-hongkong", SiteType: "china",
		TrafficLimitGB: 200, ThresholdPercent: 95,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.SaveCloudSync(context.Background(), account, nil, false, "", 120, true, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE account_traffic_snapshots SET used_gb=?,synced_at=?,previous_used_gb=?,previous_synced_at=? WHERE account_id=?`,
		120, now.Format(time.RFC3339Nano), 100, now.Add(-2*time.Minute).Format(time.RFC3339Nano), account.ID); err != nil {
		t.Fatal(err)
	}
	overview, err := store.CloudOverview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(overview.Traffic) != 1 || overview.Traffic[0].RateGBPerMinute < 9.9 || overview.Traffic[0].RateGBPerMinute > 10.1 {
		t.Fatalf("unexpected account traffic rate: %+v", overview.Traffic)
	}
	if overview.Traffic[0].MinutesToThreshold == nil || *overview.Traffic[0].MinutesToThreshold < 6.9 || *overview.Traffic[0].MinutesToThreshold > 7.1 {
		t.Fatalf("unexpected threshold estimate: %+v", overview.Traffic[0])
	}
}

func TestCloudServiceUsesTrafficSafetyWindowForPrediction(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	account, _ := createProtectedRelay(t, store, ProtectionDrainRelay)
	// The helper creates the first valid snapshot. Make it old enough to form a
	// rate on the next sync and use a fake CDT client for deterministic input.
	old := time.Now().UTC().Add(-2 * time.Minute).Format(time.RFC3339Nano)
	if _, err := store.db.Exec(`UPDATE account_traffic_snapshots SET used_gb=?,synced_at=? WHERE account_id=?`, 160, old, account.ID); err != nil {
		t.Fatal(err)
	}
	fake := &fakeCloudClient{traffic: 171}
	service := NewCloudService(store)
	service.SetTrafficSafetyWindow(2 * time.Minute)
	service.clientFor = func(CloudAccount) cloudClient { return fake }
	result := service.syncAccount(context.Background(), account)
	if result.Error != "" || !result.ProtectionTriggered || !result.ProtectionPredictive || result.ProtectionAction != "drain_relay_predictive" {
		t.Fatalf("cloud sync did not apply predictive drain: %+v", result)
	}
}

func TestTriggeredAccountPublishesCatchupDrainRevisionOnce(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	account, nodeID := createProtectedRelay(t, store, ProtectionDrainRelay)
	if _, err := store.db.Exec(`UPDATE accounts SET protection_triggered=1,protection_triggered_at=?,protection_drain_published=0 WHERE id=?`, time.Now().UTC().Format(time.RFC3339Nano), account.ID); err != nil {
		t.Fatal(err)
	}
	before, err := store.AgentConfig(context.Background(), nodeID)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := store.ApplyTrafficProtection(context.Background(), account.ID, 190)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Changed {
		t.Fatalf("pre-existing trigger should not create a new transition: %+v", decision)
	}
	after, err := store.AgentConfig(context.Background(), nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != before.Revision+1 {
		t.Fatalf("missing catch-up drain revision: before=%+v after=%+v", before, after)
	}
	if _, err := store.ApplyTrafficProtection(context.Background(), account.ID, 191); err != nil {
		t.Fatal(err)
	}
	repeated, err := store.AgentConfig(context.Background(), nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if repeated.Revision != after.Revision {
		t.Fatalf("catch-up marker caused repeated revisions: after=%d repeated=%d", after.Revision, repeated.Revision)
	}
}

func TestEnsureProtectionRevisionsRepairsTriggeredPoolAfterRestart(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	account, nodeID := createProtectedRelay(t, store, ProtectionAlertOnly)
	// Attach the existing relay service to a pool so the pool's default
	// auto-drain policy applies even though the account itself is alert-only.
	landing, err := store.CreateLandingNode(context.Background(), CreateLandingNodeRequest{Name: "pool-landing", Address: "127.0.0.1", Port: 9443, Network: "tcp"})
	if err != nil {
		t.Fatal(err)
	}
	// The helper service is intentionally left standalone; create a dedicated
	// pool on another port for the restart-repair assertion.
	pool, err := store.CreateRelayPool(context.Background(), CreateRelayPoolRequest{
		Name: "restart", Hostname: "relay.example.com", ListenPort: 18447, Network: "tcp", Mode: "failover",
		Members: []CreateRelayPoolMember{{RelayNodeID: nodeID}}, Targets: []CreateServiceTarget{{LandingNodeID: landing.ID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE accounts SET protection_triggered=1,protection_triggered_at=?,protection_drain_published=0 WHERE id=?`, time.Now().UTC().Format(time.RFC3339Nano), account.ID); err != nil {
		t.Fatal(err)
	}
	before, err := store.AgentConfig(context.Background(), nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureProtectionRevisions(context.Background()); err != nil {
		t.Fatal(err)
	}
	after, err := store.AgentConfig(context.Background(), nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != before.Revision+1 {
		t.Fatalf("restart repair did not publish revision: before=%+v after=%+v pool=%+v", before, after, pool)
	}
	if err := store.EnsureProtectionRevisions(context.Background()); err != nil {
		t.Fatal(err)
	}
	repeated, err := store.AgentConfig(context.Background(), nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if repeated.Revision != after.Revision {
		t.Fatalf("restart repair published duplicate revision: after=%d repeated=%d", after.Revision, repeated.Revision)
	}
}

func TestStopECSProtectionRetriesUntilActionIsRecorded(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	account, err := store.CreateCloudAccount(context.Background(), CloudAccountRequest{
		Name: "stop protected", AccessKeyID: "key", AccessKeySecret: "secret", RegionID: "cn-hongkong", SiteType: "china",
		ProtectedInstanceID: "i-stop", TrafficLimitGB: 200, ThresholdPercent: 90, ShutdownMode: "StopCharging", ProtectionMode: ProtectionStopECS,
	})
	if err != nil {
		t.Fatal(err)
	}
	decision, err := store.ApplyTrafficProtection(context.Background(), account.ID, 190)
	if err != nil {
		t.Fatal(err)
	}
	if !decision.NeedsStop || decision.InstanceID != "i-stop" {
		t.Fatalf("expected stop action: %+v", decision)
	}
	actionError := errors.New("temporary stop API failure")
	if err := store.MarkTrafficProtectionAction(context.Background(), account.ID, actionError); err != nil {
		t.Fatal(err)
	}
	decision, err = store.ApplyTrafficProtection(context.Background(), account.ID, 191)
	if err != nil {
		t.Fatal(err)
	}
	if !decision.NeedsStop || decision.Changed {
		t.Fatalf("failed stop action was not retried idempotently: %+v", decision)
	}
	if err := store.MarkTrafficProtectionAction(context.Background(), account.ID, nil); err != nil {
		t.Fatal(err)
	}
	decision, err = store.ApplyTrafficProtection(context.Background(), account.ID, 192)
	if err != nil {
		t.Fatal(err)
	}
	if decision.NeedsStop {
		t.Fatalf("completed stop action was requested again: %+v", decision)
	}
	accounts, err := store.ListCloudAccounts(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 || !accounts[0].ProtectionTriggered || !accounts[0].ProtectionActionCompleted || accounts[0].ProtectionLastError != "" {
		t.Fatalf("unexpected recorded protection state: %+v", accounts)
	}
	if _, err := store.ApplyTrafficProtection(context.Background(), account.ID, 1); err != nil {
		t.Fatal(err)
	}
	accounts, err = store.ListCloudAccounts(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if accounts[0].ProtectionTriggered || accounts[0].ProtectionActionCompleted {
		t.Fatalf("monthly recovery did not reset stop action state: %+v", accounts[0])
	}
}

func TestStopProtectionAuthorizationIsCancelledByAccountUpdate(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	account, err := store.CreateCloudAccount(context.Background(), CloudAccountRequest{
		Name: "cancellable stop", AccessKeyID: "key", AccessKeySecret: "secret", RegionID: "cn-hongkong", SiteType: "china",
		ProtectedInstanceID: "i-cancel", TrafficLimitGB: 200, ThresholdPercent: 90, ShutdownMode: "StopCharging", ProtectionMode: ProtectionStopECS,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyTrafficProtection(context.Background(), account.ID, 190); err != nil {
		t.Fatal(err)
	}
	authorized, err := store.ConfirmTrafficProtectionStop(context.Background(), account.ID, "i-cancel")
	if err != nil || !authorized {
		t.Fatalf("expected pending stop authorization, authorized=%v err=%v", authorized, err)
	}
	if _, err := store.UpdateCloudAccount(context.Background(), account.ID, CloudAccountRequest{
		Name: account.Name, AccessKeyID: account.AccessKeyID, RegionID: account.RegionID, SiteType: account.SiteType,
		TrafficLimitGB: account.TrafficLimitGB, ThresholdPercent: account.ThresholdPercent, ShutdownMode: account.ShutdownMode,
		ProtectionMode: ProtectionAlertOnly,
	}); err != nil {
		t.Fatal(err)
	}
	authorized, err = store.ConfirmTrafficProtectionStop(context.Background(), account.ID, "i-cancel")
	if err != nil {
		t.Fatal(err)
	}
	if authorized {
		t.Fatal("stop authorization remained active after protection mode changed")
	}
}

func TestCloudServiceExecutesStopProtectionOnce(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	account, err := store.CreateCloudAccount(context.Background(), CloudAccountRequest{
		Name: "automatic stop", AccessKeyID: "key", AccessKeySecret: "secret", RegionID: "cn-hongkong", SiteType: "china",
		ProtectedInstanceID: "i-stop", TrafficLimitGB: 200, ThresholdPercent: 90, ShutdownMode: "StopCharging", ProtectionMode: ProtectionStopECS,
	})
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeCloudClient{
		traffic:   190,
		instances: []aliyun.Instance{{InstanceID: "i-stop", InstanceName: "relay", RegionID: "cn-hongkong", Status: "Running"}},
	}
	service := NewCloudService(store)
	service.clientFor = func(CloudAccount) cloudClient { return fake }
	first := service.syncAccount(context.Background(), account)
	if first.Error != "" || first.ProtectionAction != "stop_ecs_sent" || fake.stopCalls != 1 {
		t.Fatalf("unexpected first protection sync: result=%+v calls=%d", first, fake.stopCalls)
	}
	second := service.syncAccount(context.Background(), account)
	if second.Error != "" || fake.stopCalls != 1 {
		t.Fatalf("completed protection action repeated: result=%+v calls=%d", second, fake.stopCalls)
	}
}

func TestCloudServiceRecordsAlreadyStoppedInstanceWithoutAPICall(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	account, err := store.CreateCloudAccount(context.Background(), CloudAccountRequest{
		Name: "already stopped", AccessKeyID: "key", AccessKeySecret: "secret", RegionID: "cn-hongkong", SiteType: "china",
		ProtectedInstanceID: "i-stopped", TrafficLimitGB: 200, ThresholdPercent: 90, ShutdownMode: "StopCharging", ProtectionMode: ProtectionStopECS,
	})
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeCloudClient{
		traffic:   190,
		instances: []aliyun.Instance{{InstanceID: "i-stopped", InstanceName: "relay", RegionID: "cn-hongkong", Status: "Stopped"}},
	}
	service := NewCloudService(store)
	service.clientFor = func(CloudAccount) cloudClient { return fake }
	result := service.syncAccount(context.Background(), account)
	if result.Error != "" || result.ProtectionAction != "stop_ecs_already_stopped" || fake.stopCalls != 0 {
		t.Fatalf("already stopped instance caused an API call: result=%+v calls=%d", result, fake.stopCalls)
	}
}

func TestChangingActiveDrainModeImmediatelyRefreshesAgentConfig(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	account, nodeID := createProtectedRelay(t, store, ProtectionDrainRelay)
	if _, err := store.ApplyTrafficProtection(context.Background(), account.ID, 190); err != nil {
		t.Fatal(err)
	}
	suspended, err := store.AgentConfig(context.Background(), nodeID)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := store.UpdateCloudAccount(context.Background(), account.ID, CloudAccountRequest{
		Name: account.Name, AccessKeyID: account.AccessKeyID, RegionID: account.RegionID, SiteType: account.SiteType,
		TrafficLimitGB: account.TrafficLimitGB, ThresholdPercent: account.ThresholdPercent, ShutdownMode: account.ShutdownMode,
		ProtectionMode: ProtectionAlertOnly,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !updated.ProtectionTriggered || updated.ProtectionMode != ProtectionAlertOnly {
		t.Fatalf("unexpected updated account state: %+v", updated)
	}
	resumed, err := store.AgentConfig(context.Background(), nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if len(resumed.Services) != 1 || !resumed.Services[0].Enabled || resumed.Revision != suspended.Revision+1 {
		t.Fatalf("mode change did not refresh and resume relay: suspended=%+v resumed=%+v", suspended, resumed)
	}
}

func TestDisablingProtectedCloudAccountReleasesRelay(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	account, nodeID := createProtectedRelay(t, store, ProtectionDrainRelay)
	if _, err := store.ApplyTrafficProtection(context.Background(), account.ID, 190); err != nil {
		t.Fatal(err)
	}
	suspended, err := store.AgentConfig(context.Background(), nodeID)
	if err != nil {
		t.Fatal(err)
	}
	disabled := false
	updated, err := store.UpdateCloudAccount(context.Background(), account.ID, CloudAccountRequest{
		Name: account.Name, AccessKeyID: account.AccessKeyID, RegionID: account.RegionID, SiteType: account.SiteType,
		TrafficLimitGB: account.TrafficLimitGB, ThresholdPercent: account.ThresholdPercent, ShutdownMode: account.ShutdownMode,
		ProtectionMode: ProtectionDrainRelay, Enabled: &disabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Enabled || updated.ProtectionTriggered {
		t.Fatalf("disabled account retained active protection: %+v", updated)
	}
	resumed, err := store.AgentConfig(context.Background(), nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if len(resumed.Services) != 1 || !resumed.Services[0].Enabled || resumed.Revision != suspended.Revision+1 {
		t.Fatalf("disabling account did not release relay: suspended=%+v resumed=%+v", suspended, resumed)
	}
}

type fakeCloudClient struct {
	instances  []aliyun.Instance
	traffic    float64
	stopErr    error
	startErr   error
	startCalls int
	stopCalls  int
}

func (client *fakeCloudClient) GetInstances(context.Context) ([]aliyun.Instance, error) {
	return client.instances, nil
}

func (client *fakeCloudClient) GetCDTTraffic(context.Context) (float64, error) {
	return client.traffic, nil
}

func (client *fakeCloudClient) StartInstance(context.Context, string) error {
	client.startCalls++
	return client.startErr
}

func (client *fakeCloudClient) StopInstance(context.Context, string, string) error {
	client.stopCalls++
	return client.stopErr
}

func createProtectedRelay(t *testing.T, store *Store, protectionMode string) (CloudAccount, string) {
	t.Helper()
	account, err := store.CreateCloudAccount(context.Background(), CloudAccountRequest{
		Name: "protected", AccessKeyID: "key", AccessKeySecret: "secret", RegionID: "cn-hongkong", SiteType: "china",
		TrafficLimitGB: 200, ThresholdPercent: 90, ShutdownMode: "StopCharging", ProtectionMode: protectionMode,
	})
	if err != nil {
		t.Fatal(err)
	}
	instances := []CloudInstanceUpdate{{InstanceID: "i-protected", InstanceName: "relay", RegionID: "cn-hongkong", Status: "Running", PublicIP: "203.0.113.30"}}
	if err := store.SaveCloudSync(context.Background(), account, instances, true, "", 10, true, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateEnrollmentToken(context.Background(), "protect-once", time.Hour); err != nil {
		t.Fatal(err)
	}
	enrolled, err := store.EnrollAgent(context.Background(), protocol.AgentEnrollmentRequest{
		Token: "protect-once", NodeName: "relay", PublicIP: "203.0.113.30", ECSInstanceID: "i-protected",
		RegionID: "cn-hongkong", Architecture: "amd64", OS: "linux", AgentVersion: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	landing, err := store.CreateLandingNode(context.Background(), CreateLandingNodeRequest{
		Name: "landing", Address: "127.0.0.1", Port: 443, Network: "tcp",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRelayService(context.Background(), CreateRelayServiceRequest{
		RelayNodeID: enrolled.AgentID, Name: "reality", ListenHost: "0.0.0.0", ListenPort: 18443,
		Network: "tcp", Mode: "failover", Health: HealthSettings{Enabled: true},
		Targets: []CreateServiceTarget{{LandingNodeID: landing.ID, Weight: 1, Priority: 0}},
	}); err != nil {
		t.Fatal(err)
	}
	return account, enrolled.AgentID
}
