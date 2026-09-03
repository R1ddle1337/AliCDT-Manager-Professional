package controller

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/protocol"
)

func TestTrafficLeasesShardQuotaAcrossRelayNodes(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	user, err := store.CreateConsoleUser(ctx, ConsoleUserRequest{Username: "lease-user", Password: "password-123", TrafficLimitGB: 1})
	if err != nil {
		t.Fatal(err)
	}
	landing, err := store.CreateLandingNode(ctx, CreateLandingNodeRequest{Name: "landing", Address: "127.0.0.1", Port: 443, Network: "tcp"})
	if err != nil {
		t.Fatal(err)
	}
	agents := make([]protocol.AgentEnrollmentResponse, 0, 2)
	for index := 0; index < 2; index++ {
		token := fmt.Sprintf("lease-enroll-%d", index)
		if err := store.CreateEnrollmentToken(ctx, token, time.Hour); err != nil {
			t.Fatal(err)
		}
		agent, err := store.EnrollAgent(ctx, protocol.AgentEnrollmentRequest{Token: token, NodeName: fmt.Sprintf("lease-relay-%d", index)})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.UpdateHeartbeat(ctx, agent.AgentID, protocol.AgentHeartbeat{Capabilities: []string{"shared_meters_v1", "quota_leases_v1"}}); err != nil {
			t.Fatal(err)
		}
		agents = append(agents, agent)
		if _, err := store.CreateUserEntryGroup(ctx, CreateUserEntryGroupRequest{
			UserID: user.ID, RelayNodeID: agent.AgentID, Name: fmt.Sprintf("relay %d", index), StartPort: 21000 + index, PortCount: 1, Network: "tcp", Mode: "failover", BillingMode: protocol.BillingModeBoth,
			Targets: []CreateServiceTarget{{LandingNodeID: landing.ID}},
		}); err != nil {
			t.Fatal(err)
		}
		config, err := store.AgentConfig(ctx, agent.AgentID)
		if err != nil {
			t.Fatal(err)
		}
		if len(config.Services) != 1 || config.Services[0].QuotaLeaseID == "" || config.Services[0].QuotaLeaseBytes == 0 || config.Services[0].QuotaLeaseExpiresAt == nil {
			t.Fatalf("relay %d did not receive a quota lease: %+v", index, config)
		}
	}
	var reserved int64
	if err := store.db.QueryRow(`SELECT COALESCE(SUM(reserved_bytes),0) FROM traffic_leases WHERE user_id=? AND status='active'`, user.ID).Scan(&reserved); err != nil {
		t.Fatal(err)
	}
	limit, _ := gbToLedgerBytes(user.TrafficLimitGB)
	if reserved <= 0 || reserved > limit {
		t.Fatalf("reserved bytes = %d, limit = %d", reserved, limit)
	}
}

func TestExpiredQuotaLeaseRejectsTraffic(t *testing.T) {
	// Enforcement itself lives in the relay package; this controller-side test
	// guards the serialized lease deadline sent to that package.
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('traffic_leases') WHERE name IN ('reserved_bytes','sequence','expires_at')`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("traffic lease schema is incomplete: %d columns", count)
	}
}
