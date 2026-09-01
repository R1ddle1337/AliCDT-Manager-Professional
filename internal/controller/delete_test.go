package controller

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/protocol"
)

func TestDeleteRelayPoolRemovesPoolAndGeneratedServices(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := store.CreateEnrollmentToken(context.Background(), "delete-pool", time.Hour); err != nil {
		t.Fatal(err)
	}
	agent, err := store.EnrollAgent(context.Background(), protocol.AgentEnrollmentRequest{
		Token: "delete-pool", NodeName: "relay", PublicIP: "203.0.113.40",
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
	pool, err := store.CreateRelayPool(context.Background(), CreateRelayPoolRequest{
		Name: "temporary", Hostname: "temporary.example.com", ListenPort: 18443,
		Network: "tcp", Mode: "failover",
		Members: []CreateRelayPoolMember{{RelayNodeID: agent.AgentID}},
		Targets: []CreateServiceTarget{{LandingNodeID: landing.ID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.AgentConfig(context.Background(), agent.AgentID)
	if err != nil || len(before.Services) != 1 {
		t.Fatalf("unexpected pool service before delete: config=%+v err=%v", before, err)
	}

	if err := store.DeleteRelayPool(context.Background(), pool.ID); err != nil {
		t.Fatalf("delete relay pool: %v", err)
	}
	if _, err := store.GetRelayPool(context.Background(), pool.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted pool is still readable: err=%v", err)
	}
	services, err := store.ListRelayServices(context.Background(), agent.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 0 {
		t.Fatalf("pool-generated service was not removed: %+v", services)
	}
	var targetCount, memberCount int
	if err := store.db.QueryRow(`SELECT COUNT(1) FROM relay_pool_targets WHERE pool_id=?`, pool.ID).Scan(&targetCount); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(1) FROM relay_pool_members WHERE pool_id=?`, pool.ID).Scan(&memberCount); err != nil {
		t.Fatal(err)
	}
	if targetCount != 0 || memberCount != 0 {
		t.Fatalf("pool relationship rows remain: targets=%d members=%d", targetCount, memberCount)
	}
	after, err := store.AgentConfig(context.Background(), agent.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != before.Revision+1 {
		t.Fatalf("relay revision was not bumped after pool delete: before=%d after=%d", before.Revision, after.Revision)
	}
}

func TestDeleteLandingNodeDetachesRoutesBeforeDelete(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := store.CreateEnrollmentToken(context.Background(), "delete-landing", time.Hour); err != nil {
		t.Fatal(err)
	}
	agent, err := store.EnrollAgent(context.Background(), protocol.AgentEnrollmentRequest{
		Token: "delete-landing", NodeName: "relay", PublicIP: "203.0.113.41",
	})
	if err != nil {
		t.Fatal(err)
	}
	landing, err := store.CreateLandingNode(context.Background(), CreateLandingNodeRequest{
		Name: "remove-me", Address: "127.0.0.1", Port: 443, Network: "tcp",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRelayService(context.Background(), CreateRelayServiceRequest{
		RelayNodeID: agent.AgentID, Name: "standalone", ListenPort: 18444,
		Network: "tcp", Mode: "failover",
		Targets: []CreateServiceTarget{{LandingNodeID: landing.ID}},
	}); err != nil {
		t.Fatal(err)
	}
	pool, err := store.CreateRelayPool(context.Background(), CreateRelayPoolRequest{
		Name: "landing-pool", Hostname: "landing.example.com", ListenPort: 18445,
		Network: "tcp", Mode: "failover",
		Members: []CreateRelayPoolMember{{RelayNodeID: agent.AgentID}},
		Targets: []CreateServiceTarget{{LandingNodeID: landing.ID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.AgentConfig(context.Background(), agent.AgentID)
	if err != nil {
		t.Fatal(err)
	}

	if err := store.DeleteLandingNode(context.Background(), landing.ID); err != nil {
		t.Fatalf("delete referenced landing node: %v", err)
	}
	landingNodes, err := store.ListLandingNodes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range landingNodes {
		if node.ID == landing.ID {
			t.Fatalf("deleted landing node is still readable: %+v", node)
		}
	}
	services, err := store.ListRelayServices(context.Background(), agent.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	for _, service := range services {
		if len(service.Targets) != 0 {
			t.Fatalf("deleted landing target remains on service %s: %+v", service.ID, service.Targets)
		}
		if service.Enabled {
			t.Fatalf("targetless service remained enabled: %+v", service)
		}
	}
	updatedPool, err := store.GetRelayPool(context.Background(), pool.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updatedPool.Enabled || len(updatedPool.Targets) != 0 {
		t.Fatalf("pool was not safely disabled after its last target was deleted: %+v", updatedPool)
	}
	var serviceRefs, poolRefs int
	if err := store.db.QueryRow(`SELECT COUNT(1) FROM service_targets WHERE landing_node_id=?`, landing.ID).Scan(&serviceRefs); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(1) FROM relay_pool_targets WHERE landing_node_id=?`, landing.ID).Scan(&poolRefs); err != nil {
		t.Fatal(err)
	}
	if serviceRefs != 0 || poolRefs != 0 {
		t.Fatalf("foreign-key references remain after landing delete: services=%d pools=%d", serviceRefs, poolRefs)
	}
	after, err := store.AgentConfig(context.Background(), agent.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != before.Revision+1 {
		t.Fatalf("relay revision was not bumped after landing delete: before=%d after=%d", before.Revision, after.Revision)
	}
}
