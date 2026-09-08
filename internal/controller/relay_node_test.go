package controller

import (
	"context"
	"testing"
	"time"

	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/protocol"
)

func TestDeleteRelayNodeRemovesOwnedConfiguration(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.CreateEnrollmentToken(ctx, "delete-node", time.Hour); err != nil {
		t.Fatal(err)
	}
	enrolled, err := store.EnrollAgent(ctx, protocol.AgentEnrollmentRequest{Token: "delete-node", NodeName: "stale-relay"})
	if err != nil {
		t.Fatal(err)
	}
	landing, err := store.CreateLandingNode(ctx, CreateLandingNodeRequest{Name: "target", Address: "127.0.0.1", Port: 443, Network: "tcp"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRelayService(ctx, CreateRelayServiceRequest{
		RelayNodeID: enrolled.AgentID, Name: "service", ListenPort: 18443, Network: "tcp", Mode: "failover",
		Targets: []CreateServiceTarget{{LandingNodeID: landing.ID}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteRelayNode(ctx, enrolled.AgentID); err != nil {
		t.Fatal(err)
	}
	for table := range map[string]string{"relay_nodes": "id", "relay_services": "relay_node_id", "relay_pool_members": "relay_node_id"} {
		var count int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE `+map[string]string{"relay_nodes": "id", "relay_services": "relay_node_id", "relay_pool_members": "relay_node_id"}[table]+`=?`, enrolled.AgentID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s retained rows for deleted node: %d", table, count)
		}
	}
	if _, err := store.ListRelayNodes(ctx); err != nil {
		t.Fatal(err)
	}
}
