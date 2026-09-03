package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestRemoteUpgradeEndpointSignalsHostCompatibilityBridge(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.CreateEnrollmentToken(ctx, "bridge-enroll", time.Hour); err != nil {
		t.Fatal(err)
	}
	agent, err := store.EnrollAgent(ctx, protocol.AgentEnrollmentRequest{Token: "bridge-enroll", NodeName: "legacy-relay", PublicIP: "203.0.113.10"})
	if err != nil {
		t.Fatal(err)
	}
	requestFile := filepath.Join(t.TempDir(), "agent-upgrade.request")
	server, err := NewServer(store, ServerOptions{AdminToken: "admin", AgentUpgradeRequestFile: requestFile})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	requestJSON(t, httpServer.URL+"/api/v2/relay-nodes/"+agent.AgentID+"/upgrade", "admin", nil, http.StatusAccepted, nil)
	data, err := os.ReadFile(requestFile)
	if err != nil {
		t.Fatal(err)
	}
	var marker agentUpgradeRequest
	if err := json.Unmarshal(data, &marker); err != nil || marker.RequestID == "" {
		t.Fatalf("invalid host bridge marker: %s (%v)", data, err)
	}
}
