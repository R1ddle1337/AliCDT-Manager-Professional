package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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

func TestAgentUpdateAvailabilityUsesArchitectureChecksum(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.CreateEnrollmentToken(ctx, "checksum-enroll", time.Hour); err != nil {
		t.Fatal(err)
	}
	agent, err := store.EnrollAgent(ctx, protocol.AgentEnrollmentRequest{Token: "checksum-enroll", NodeName: "current-relay", Architecture: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	assets := t.TempDir()
	assetName := "cdt-relay-agent-linux-amd64"
	assetData := []byte("current-agent-binary")
	if err := os.WriteFile(filepath.Join(assets, assetName), assetData, 0755); err != nil {
		t.Fatal(err)
	}
	expectedHash := sha256Hex(assetData)
	if err := os.WriteFile(filepath.Join(assets, "checksums.txt"), []byte(expectedHash+"  "+assetName+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	installer := filepath.Join(assets, "install-agent.sh")
	if err := os.WriteFile(installer, []byte("#!/bin/sh\n"), 0700); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(store, ServerOptions{AdminToken: "admin", AgentInstallerPath: installer, AgentReleaseSource: "embedded"})
	if err != nil {
		t.Fatal(err)
	}

	if err := store.UpdateHeartbeat(ctx, agent.AgentID, protocol.AgentHeartbeat{BinarySHA256: "old-hash", Capabilities: []string{"shared_meters_v1", "quota_leases_v1"}}); err != nil {
		t.Fatal(err)
	}
	nodes, err := store.ListRelayNodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	server.annotateAgentUpdates(nodes)
	if len(nodes) != 1 || !nodes[0].UpdateAvailable {
		t.Fatalf("old agent was not marked updateable: %+v", nodes)
	}

	if err := store.UpdateHeartbeat(ctx, agent.AgentID, protocol.AgentHeartbeat{BinarySHA256: expectedHash, Capabilities: []string{"shared_meters_v1", "quota_leases_v1"}}); err != nil {
		t.Fatal(err)
	}
	nodes, err = store.ListRelayNodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	server.annotateAgentUpdates(nodes)
	if nodes[0].UpdateAvailable {
		t.Fatalf("current agent was marked updateable: %+v", nodes[0])
	}
}

func TestBatchAgentUpgradeIsAtomic(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.CreateEnrollmentToken(ctx, "atomic-enroll", time.Hour); err != nil {
		t.Fatal(err)
	}
	agent, err := store.EnrollAgent(ctx, protocol.AgentEnrollmentRequest{Token: "atomic-enroll", NodeName: "atomic-relay"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RequestAgentUpgrades(ctx, []string{agent.AgentID, "missing-agent"}, "batch"); err == nil {
		t.Fatal("expected missing agent to abort batch")
	}
	config, err := store.AgentConfig(ctx, agent.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if config.ForceUpdate {
		t.Fatal("valid agent was queued despite atomic batch failure")
	}
}

func TestAgentUpdateStateIsValidatedAndBounded(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.CreateEnrollmentToken(ctx, "state-enroll", time.Hour); err != nil {
		t.Fatal(err)
	}
	agent, err := store.EnrollAgent(ctx, protocol.AgentEnrollmentRequest{Token: "state-enroll", NodeName: "state-relay"})
	if err != nil {
		t.Fatal(err)
	}
	longError := strings.Repeat("错", maxAgentUpdateErrorRunes+100)
	if err := store.SetAgentUpdateState(ctx, agent.AgentID, "FAILED", longError); err != nil {
		t.Fatal(err)
	}
	nodes, err := store.ListRelayNodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].UpdateStatus != "failed" || len([]rune(nodes[0].UpdateError)) != maxAgentUpdateErrorRunes || !utf8.ValidString(nodes[0].UpdateError) {
		t.Fatalf("update error was not safely normalized: %+v", nodes)
	}
	if err := store.UpdateHeartbeat(ctx, agent.AgentID, protocol.AgentHeartbeat{UpdateStatus: "unknown"}); err == nil {
		t.Fatal("invalid heartbeat update status was accepted")
	}
	nodes, err = store.ListRelayNodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if nodes[0].UpdateStatus != "failed" || nodes[0].UpdateError == "" {
		t.Fatalf("invalid heartbeat changed the last valid state: %+v", nodes[0])
	}
}
