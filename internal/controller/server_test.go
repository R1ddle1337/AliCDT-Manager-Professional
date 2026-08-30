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
