package controller

import (
	"context"
	"testing"
	"time"

	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/protocol"
)

func TestHeartbeatUsageLedgerIsIdempotentAcrossSharedPorts(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	user, err := store.CreateConsoleUser(ctx, ConsoleUserRequest{Username: "ledger-user", Password: "password-123", TrafficLimitGB: 10})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateEnrollmentToken(ctx, "ledger-enroll", time.Hour); err != nil {
		t.Fatal(err)
	}
	agent, err := store.EnrollAgent(ctx, protocol.AgentEnrollmentRequest{Token: "ledger-enroll", NodeName: "ledger-relay"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateHeartbeat(ctx, agent.AgentID, protocol.AgentHeartbeat{Capabilities: []string{"shared_meters_v1", "quota_leases_v1"}}); err != nil {
		t.Fatal(err)
	}
	landing, err := store.CreateLandingNode(ctx, CreateLandingNodeRequest{Name: "landing", Address: "127.0.0.1", Port: 443, Network: "tcp"})
	if err != nil {
		t.Fatal(err)
	}
	group, err := store.CreateUserEntryGroup(ctx, CreateUserEntryGroupRequest{
		UserID: user.ID, RelayNodeID: agent.AgentID, Name: "ledger ports", PortCount: 2, Network: "tcp", Mode: "failover", BillingMode: protocol.BillingModeBoth,
		Targets: []CreateServiceTarget{{LandingNodeID: landing.ID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	config, err := store.AgentConfig(ctx, agent.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	heartbeat := func(total uint64) protocol.AgentHeartbeat {
		statuses := make([]protocol.ServiceStatus, 0, len(group.Ports))
		for _, port := range group.Ports {
			statuses = append(statuses, protocol.ServiceStatus{ID: port.ServiceID, MeterKey: "user:1", BillingMode: protocol.BillingModeBoth, BillingEpoch: config.Services[0].BillingEpoch, BilledBytes: total})
		}
		return protocol.AgentHeartbeat{CurrentRevision: config.Revision, Services: statuses}
	}
	if err := store.UpdateHeartbeat(ctx, agent.AgentID, heartbeat(100)); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateHeartbeat(ctx, agent.AgentID, heartbeat(100)); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateHeartbeat(ctx, agent.AgentID, heartbeat(150)); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateHeartbeat(ctx, agent.AgentID, heartbeat(120)); err != nil {
		t.Fatal(err)
	}
	entries, err := store.ListUsageLedger(ctx, user.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].DeltaBytes != 50 || entries[0].TotalBytes != 150 || entries[1].DeltaBytes != 100 {
		t.Fatalf("heartbeat ledger is not idempotent: %+v", entries)
	}
}

func TestQuotaAdjustmentAndResetAreAudited(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	user, err := store.CreateConsoleUser(ctx, ConsoleUserRequest{Username: "quota-ledger", Password: "password-123", TrafficLimitGB: 10})
	if err != nil {
		t.Fatal(err)
	}
	adjusted, err := store.AdjustUserTrafficLimit(ctx, user.ID, UsageAdjustmentRequest{DeltaGB: 2.5, Note: "renewal"})
	if err != nil {
		t.Fatal(err)
	}
	if adjusted.TrafficLimitGB != 12.5 {
		t.Fatalf("adjusted limit = %f", adjusted.TrafficLimitGB)
	}
	entries, err := store.ListUsageLedger(ctx, user.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Kind != "quota_adjustment" || entries[0].DeltaBytes != int64(2.5*1024*1024*1024) || entries[0].Note != "renewal" {
		t.Fatalf("unexpected quota ledger: %+v", entries)
	}
	if _, err := store.AdjustUserTrafficLimit(ctx, user.ID, UsageAdjustmentRequest{DeltaGB: -20, Note: "invalid"}); err == nil {
		t.Fatal("expected adjustment below zero to be rejected")
	}
}
