package controller

import (
	"context"
	"testing"
	"time"

	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/protocol"
)

func TestRemoteAgentUpgradeRequestIsDeliveredThroughConfig(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.CreateEnrollmentToken(ctx, "upgrade-enroll", time.Hour); err != nil {
		t.Fatal(err)
	}
	agent, err := store.EnrollAgent(ctx, protocol.AgentEnrollmentRequest{Token: "upgrade-enroll", NodeName: "upgrade-relay"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RequestAgentUpgrade(ctx, agent.AgentID); err != nil {
		t.Fatal(err)
	}
	config, err := store.AgentConfig(ctx, agent.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if !config.ForceUpdate {
		t.Fatalf("agent config did not carry force update request: %+v", config)
	}
	if err := store.SetAgentUpdateState(ctx, agent.AgentID, "idle", ""); err != nil {
		t.Fatal(err)
	}
	config, err = store.AgentConfig(ctx, agent.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if config.ForceUpdate {
		t.Fatalf("force update remained after Agent acknowledged it: %+v", config)
	}
}
