package controller

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/protocol"
)

func TestDecodeJSONRejectsTrailingValue(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"name":"first"} {"name":"second"}`))
	var payload struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(request, &payload); err == nil {
		t.Fatal("expected multiple JSON values to be rejected")
	}
}

func TestFrontendCachingAndCompression(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	frontendDir := t.TempDir()
	assetsDir := filepath.Join(frontendDir, "assets")
	if err := os.MkdirAll(assetsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(frontendDir, "index.html"), []byte("<html><body>console</body></html>"), 0600); err != nil {
		t.Fatal(err)
	}
	javascript := bytes.Repeat([]byte("const status = 'healthy';\n"), 100)
	if err := os.WriteFile(filepath.Join(assetsDir, "index-contenthash.js"), javascript, 0600); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(store, ServerOptions{AdminToken: "admin", FrontendDir: frontendDir})
	if err != nil {
		t.Fatal(err)
	}

	indexRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(indexRecorder, httptest.NewRequest(http.MethodGet, "/dns", nil))
	if indexRecorder.Code != http.StatusOK {
		t.Fatalf("SPA fallback returned %d", indexRecorder.Code)
	}
	if got := indexRecorder.Header().Get("Cache-Control"); got != "no-cache, must-revalidate" {
		t.Fatalf("unexpected index cache policy %q", got)
	}

	assetRequest := httptest.NewRequest(http.MethodGet, "/assets/index-contenthash.js", nil)
	assetRequest.Header.Set("Accept-Encoding", "gzip")
	assetRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(assetRecorder, assetRequest)
	if got := assetRecorder.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("unexpected asset cache policy %q", got)
	}
	if got := assetRecorder.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("expected gzip content encoding, got %q", got)
	}
	reader, err := gzip.NewReader(assetRecorder.Body)
	if err != nil {
		t.Fatal(err)
	}
	decompressed, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decompressed, javascript) {
		t.Fatal("compressed asset content changed")
	}
}

func TestHealthChecksStorageAndInternalErrorsAreRedacted(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(store, ServerOptions{AdminToken: "admin"})
	if err != nil {
		t.Fatal(err)
	}

	healthRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(healthRecorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if healthRecorder.Code != http.StatusOK {
		t.Fatalf("healthy storage returned %d: %s", healthRecorder.Code, healthRecorder.Body.String())
	}

	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	healthRecorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(healthRecorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if healthRecorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("closed storage returned %d: %s", healthRecorder.Code, healthRecorder.Body.String())
	}

	errorRecorder := httptest.NewRecorder()
	writeStoreError(errorRecorder, errors.New("secret database path /private/controller.db"))
	if errorRecorder.Code != http.StatusInternalServerError {
		t.Fatalf("storage error returned %d", errorRecorder.Code)
	}
	if bytes.Contains(errorRecorder.Body.Bytes(), []byte("/private/controller.db")) {
		t.Fatalf("storage error leaked internal details: %s", errorRecorder.Body.String())
	}
	if !bytes.Contains(errorRecorder.Body.Bytes(), []byte("internal server error")) {
		t.Fatalf("storage error response was not normalized: %s", errorRecorder.Body.String())
	}
}

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

func TestAgentReenrollmentReusesExistingECSNode(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.CreateEnrollmentToken(ctx, "first-enroll", time.Hour); err != nil {
		t.Fatal(err)
	}
	first, err := store.EnrollAgent(ctx, protocol.AgentEnrollmentRequest{
		Token: "first-enroll", NodeName: "edge-old", ECSInstanceID: "i-edge", RegionID: "cn-hongkong", PublicIP: "203.0.113.10",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateEnrollmentToken(ctx, "replacement-enroll", time.Hour); err != nil {
		t.Fatal(err)
	}
	second, err := store.EnrollAgent(ctx, protocol.AgentEnrollmentRequest{
		Token: "replacement-enroll", NodeName: "edge-reinstalled", ECSInstanceID: "i-edge", RegionID: "cn-hongkong", PublicIP: "203.0.113.11",
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.AgentID != first.AgentID {
		t.Fatalf("re-enrollment created a new relay node: first=%+v second=%+v", first, second)
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM relay_nodes WHERE ecs_instance_id=?`, "i-edge").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected one relay node for ECS instance, got %d", count)
	}
	if err := store.AuthenticateAgent(ctx, first.AgentID, first.Secret); err == nil {
		t.Fatal("old Agent secret remained valid after re-enrollment")
	}
	if err := store.AuthenticateAgent(ctx, second.AgentID, second.Secret); err != nil {
		t.Fatalf("new Agent secret was not accepted: %v", err)
	}
	nodes, err := store.ListRelayNodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].Name != "edge-reinstalled" || nodes[0].PublicIP != "203.0.113.11" {
		t.Fatalf("existing relay node metadata was not refreshed: %+v", nodes)
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

func TestDispatcherSnapshotUsesDedicatedReadOnlyToken(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	account, err := store.CreateCloudAccount(context.Background(), CloudAccountRequest{
		Name: "dispatch-account", AccessKeyID: "key", AccessKeySecret: "secret", RegionID: "cn-hongkong", SiteType: "china",
		TrafficLimitGB: 200, ThresholdPercent: 95,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateEnrollmentToken(context.Background(), "dispatch-enroll", time.Hour, account.ID); err != nil {
		t.Fatal(err)
	}
	agent, err := store.EnrollAgent(context.Background(), protocol.AgentEnrollmentRequest{Token: "dispatch-enroll", NodeName: "relay", PublicIP: "203.0.113.77"})
	if err != nil {
		t.Fatal(err)
	}
	landing, err := store.CreateLandingNode(context.Background(), CreateLandingNodeRequest{Name: "landing", Address: "127.0.0.1", Port: 9443, Network: "tcp"})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := store.CreateRelayPool(context.Background(), CreateRelayPoolRequest{
		Name: "dispatch", Hostname: "entry.example.com", ListenPort: 18450, Network: "tcp", Mode: "failover",
		Members: []CreateRelayPoolMember{{RelayNodeID: agent.AgentID}}, Targets: []CreateServiceTarget{{LandingNodeID: landing.ID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	config, err := store.AgentConfig(context.Background(), agent.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateHeartbeat(context.Background(), agent.AgentID, protocol.AgentHeartbeat{
		CurrentRevision: config.Revision,
		Services:        []protocol.ServiceStatus{{ID: config.Services[0].ID, Listening: true}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.RefreshRelayPoolDNS(context.Background(), pool.ID); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(store, ServerOptions{AdminToken: "admin", DispatchToken: "dispatch-only"})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	getJSON(t, httpServer.URL+"/api/v2/dispatch/pools/"+pool.ID, "admin", http.StatusNotFound, nil)
	var snapshot DispatcherPoolSnapshot
	getJSON(t, httpServer.URL+"/api/v2/dispatch/pools/"+pool.ID, "dispatch-only", http.StatusOK, &snapshot)
	if snapshot.PoolID != pool.ID || snapshot.Revision == "" || snapshot.SelectionMode != "quota_weighted" || len(snapshot.Backends) != 1 || snapshot.Backends[0].Address != "203.0.113.77:18450" {
		t.Fatalf("unexpected dispatcher snapshot: %+v", snapshot)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("secret")) || bytes.Contains(encoded, []byte("share_uri")) {
		t.Fatalf("dispatcher snapshot leaked sensitive landing/account data: %s", encoded)
	}
	if _, err := store.db.Exec(`UPDATE accounts SET protection_triggered=1 WHERE id=?`, account.ID); err != nil {
		t.Fatal(err)
	}
	snapshot, err = store.DispatcherPoolSnapshot(context.Background(), pool.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Backends) != 0 {
		t.Fatalf("draining account remained in dispatcher backends: %+v", snapshot.Backends)
	}
	if snapshot.Revision == "" {
		t.Fatal("disabled/drained snapshot did not receive a revision")
	}
}

func TestServerRejectsDispatchTokenReuse(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := NewServer(store, ServerOptions{AdminToken: "same", DispatchToken: "same"}); err == nil {
		t.Fatal("expected dispatch token reuse to be rejected")
	}
}

func TestDispatcherInstallerAndEmbeddedAssetAreServed(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	directory := t.TempDir()
	installer := filepath.Join(directory, "install-agent.sh")
	if err := os.WriteFile(installer, []byte("#!/bin/sh\necho agent\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "install-dispatcher.sh"), []byte("#!/bin/sh\necho dispatcher\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "cdt-dispatcher-linux-amd64"), []byte("binary"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "checksums.txt"), []byte("deadbeef  cdt-dispatcher-linux-amd64\n"), 0600); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(store, ServerOptions{AdminToken: "admin", AgentInstallerPath: installer, AgentReleaseSource: "embedded"})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/dispatcher/install.sh", nil))
	if recorder.Code != http.StatusOK || !bytes.Contains(recorder.Body.Bytes(), []byte("dispatcher")) {
		t.Fatalf("dispatcher installer was not served: %d %q", recorder.Code, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/agent/cdt-dispatcher-linux-amd64", nil))
	if recorder.Code != http.StatusOK || recorder.Body.String() != "binary" {
		t.Fatalf("dispatcher asset was not served: %d %q", recorder.Code, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/dispatcher/checksums.txt", nil))
	if recorder.Code != http.StatusOK || !bytes.Contains(recorder.Body.Bytes(), []byte("cdt-dispatcher-linux-amd64")) {
		t.Fatalf("dispatcher checksum asset was not served: %d %q", recorder.Code, recorder.Body.String())
	}
}

func TestSingBoxLogCleanupAssetIsServed(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	directory := t.TempDir()
	installer := filepath.Join(directory, "install-agent.sh")
	if err := os.WriteFile(installer, []byte("#!/bin/sh\n"), 0700); err != nil {
		t.Fatal(err)
	}
	cleanup := filepath.Join(directory, "cdt-sing-box-log-cleanup.sh")
	if err := os.WriteFile(cleanup, []byte("#!/bin/sh\necho cleanup\n"), 0700); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(store, ServerOptions{AdminToken: "admin", AgentInstallerPath: installer})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/agent/cdt-sing-box-log-cleanup.sh", nil))
	if recorder.Code != http.StatusOK || recorder.Body.String() != "#!/bin/sh\necho cleanup\n" {
		t.Fatalf("sing-box cleanup asset was not served: %d %q", recorder.Code, recorder.Body.String())
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
