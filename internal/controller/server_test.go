package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/protocol"
)

func TestControllerAgentLifecycle(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateEnrollmentToken(context.Background(), "enroll-once", time.Hour); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(store, ServerOptions{AdminToken: "admin-secret"})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	var enrolled protocol.AgentEnrollmentResponse
	requestJSON(t, httpServer.URL+"/api/v2/agents/enroll", "", protocol.AgentEnrollmentRequest{
		Token: "enroll-once", NodeName: "relay-hk-01", Architecture: "amd64", OS: "linux", AgentVersion: "test",
	}, http.StatusCreated, &enrolled)
	if enrolled.AgentID == "" || enrolled.Secret == "" {
		t.Fatal("controller did not return credentials")
	}

	var landing LandingNode
	requestJSON(t, httpServer.URL+"/api/v2/landing-nodes", "admin-secret", CreateLandingNodeRequest{
		Name: "landing-a", Address: "127.0.0.1", Port: 443, Network: "tcp",
	}, http.StatusCreated, &landing)
	var service RelayService
	requestJSON(t, httpServer.URL+"/api/v2/relay-services", "admin-secret", CreateRelayServiceRequest{
		RelayNodeID: enrolled.AgentID,
		Name:        "reality-entry",
		ListenHost:  "0.0.0.0",
		ListenPort:  18443,
		Network:     "tcp",
		Mode:        "failover",
		Health:      HealthSettings{Enabled: true},
		Targets:     []CreateServiceTarget{{LandingNodeID: landing.ID, Weight: 1, Priority: 0}},
	}, http.StatusCreated, &service)
	if service.ID == "" {
		t.Fatal("service was not created")
	}

	var config protocol.AgentConfig
	getJSON(t, httpServer.URL+"/api/v2/agents/"+enrolled.AgentID+"/config?after=0", enrolled.Secret, http.StatusOK, &config)
	if config.Revision != 1 || len(config.Services) != 1 || config.Services[0].Targets[0].Address != "127.0.0.1:443" {
		t.Fatalf("unexpected config: %+v", config)
	}
	requestJSON(t, httpServer.URL+"/api/v2/agents/"+enrolled.AgentID+"/heartbeat", enrolled.Secret, protocol.AgentHeartbeat{
		AgentVersion: "test", CurrentRevision: config.Revision, StartedAt: time.Now(), Services: []protocol.ServiceStatus{{ID: service.ID, Listening: true}},
	}, http.StatusNoContent, nil)

	var nodes []RelayNode
	getJSON(t, httpServer.URL+"/api/v2/relay-nodes", "admin-secret", http.StatusOK, &nodes)
	if len(nodes) != 1 || nodes[0].CurrentRevision != 1 || nodes[0].Status != "online" {
		t.Fatalf("unexpected nodes: %+v", nodes)
	}
}

func TestDisabledLandingNodeIsExcludedFromAgentConfig(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateEnrollmentToken(context.Background(), "landing-disable", time.Hour); err != nil {
		t.Fatal(err)
	}
	enrolled, err := store.EnrollAgent(context.Background(), protocol.AgentEnrollmentRequest{Token: "landing-disable", NodeName: "relay"})
	if err != nil {
		t.Fatal(err)
	}
	landing, err := store.CreateLandingNode(context.Background(), CreateLandingNodeRequest{Name: "landing", Address: "127.0.0.1", Port: 443, Network: "tcp"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRelayService(context.Background(), CreateRelayServiceRequest{
		RelayNodeID: enrolled.AgentID, Name: "service", ListenPort: 18443, Network: "tcp", Mode: "failover",
		Targets: []CreateServiceTarget{{LandingNodeID: landing.ID, Enabled: boolPtr(true)}},
	}); err != nil {
		t.Fatal(err)
	}
	disabled := false
	if _, err := store.UpdateLandingNode(context.Background(), landing.ID, CreateLandingNodeRequest{Name: landing.Name, Address: landing.Address, Port: landing.Port, Network: landing.Network, Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	config, err := store.AgentConfig(context.Background(), enrolled.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Services) != 1 || len(config.Services[0].Targets) != 1 || config.Services[0].Targets[0].Enabled {
		t.Fatalf("disabled landing node remained eligible: %+v", config)
	}
}

func TestStaleAgentIsMarkedOffline(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateEnrollmentToken(context.Background(), "stale-agent", time.Hour); err != nil {
		t.Fatal(err)
	}
	enrolled, err := store.EnrollAgent(context.Background(), protocol.AgentEnrollmentRequest{Token: "stale-agent", NodeName: "relay"})
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-2 * time.Minute).Format(time.RFC3339Nano)
	if _, err := store.db.Exec(`UPDATE relay_nodes SET last_seen_at=? WHERE id=?`, old, enrolled.AgentID); err != nil {
		t.Fatal(err)
	}
	marked, err := store.MarkStaleRelayNodes(context.Background(), time.Minute)
	if err != nil || marked != 1 {
		t.Fatalf("stale agent was not marked: count=%d err=%v", marked, err)
	}
	nodes, err := store.ListRelayNodes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].Status != "offline" {
		t.Fatalf("unexpected stale node status: %+v", nodes)
	}
}

func TestRelayListenerRejectsTransportOverlap(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateEnrollmentToken(context.Background(), "listener-conflict", time.Hour); err != nil {
		t.Fatal(err)
	}
	agent, err := store.EnrollAgent(context.Background(), protocol.AgentEnrollmentRequest{Token: "listener-conflict", NodeName: "relay"})
	if err != nil {
		t.Fatal(err)
	}
	landing, err := store.CreateLandingNode(context.Background(), CreateLandingNodeRequest{Name: "landing", Address: "127.0.0.1", Port: 443, Network: "tcp"})
	if err != nil {
		t.Fatal(err)
	}
	request := CreateRelayServiceRequest{RelayNodeID: agent.AgentID, Name: "tcp", ListenPort: 18443, Network: "tcp", Mode: "failover", Targets: []CreateServiceTarget{{LandingNodeID: landing.ID}}}
	if _, err := store.CreateRelayService(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	request.Name = "tcp-udp"
	request.Network = "tcp+udp"
	if _, err := store.CreateRelayService(context.Background(), request); err == nil {
		t.Fatal("expected tcp+udp listener to conflict with existing tcp listener")
	}
}

func boolPtr(value bool) *bool { return &value }

func requestJSON(t *testing.T, url, token string, payload interface{}, expected int, output interface{}) {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	doJSON(t, request, expected, output)
}

func getJSON(t *testing.T, url, token string, expected int, output interface{}) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	doJSON(t, request, expected, output)
}

func doJSON(t *testing.T, request *http.Request, expected int, output interface{}) {
	t.Helper()
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != expected {
		var body map[string]interface{}
		_ = json.NewDecoder(response.Body).Decode(&body)
		t.Fatalf("expected status %d, got %d: %v", expected, response.StatusCode, body)
	}
	if output != nil {
		if err := json.NewDecoder(response.Body).Decode(output); err != nil {
			t.Fatal(err)
		}
	}
}
