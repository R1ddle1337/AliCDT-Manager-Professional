package agent

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/protocol"
	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/relay"
)

func TestRunRestoresCachedConfigWhenControllerIsUnavailable(t *testing.T) {
	dataDir := t.TempDir()
	credentialsJSON, err := json.Marshal(credentials{AgentID: "relay-test", Secret: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "credentials.json"), credentialsJSON, 0600); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.NotFoundHandler())
	controllerURL := server.URL
	server.Close()

	engine := relay.NewEngine()
	defer engine.Close()
	client, err := New(Options{
		ControllerURL:  controllerURL,
		DataDir:        dataDir,
		PollInterval:   time.Hour,
		HeartbeatEvery: time.Hour,
	}, engine)
	if err != nil {
		t.Fatal(err)
	}
	client.httpClient.Timeout = 100 * time.Millisecond
	config := testAgentConfig(7, "127.0.0.1:0")
	if err := client.saveCachedConfig(config); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- client.Run(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for engine.Revision() != config.Revision && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if engine.Revision() != config.Revision {
		cancel()
		<-done
		t.Fatalf("cached revision was not restored, got %d", engine.Revision())
	}
	if statuses := engine.Snapshot(); len(statuses) != 1 || statuses[0].ID != "service-test" {
		cancel()
		<-done
		t.Fatalf("cached service was not restored: %+v", statuses)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("agent returned an error after cancellation: %v", err)
	}
}

func TestDailyUpdateScheduleUsesBeijingTime(t *testing.T) {
	client, err := New(Options{ControllerURL: "https://controller.invalid", UpdateTime: "04:00", UpdateLocation: "Asia/Shanghai"}, relay.NewEngine())
	if err != nil {
		t.Fatal(err)
	}
	defer client.engine.Close()
	location, _ := time.LoadLocation("Asia/Shanghai")
	before := time.Date(2026, 8, 30, 3, 59, 0, 0, location)
	if got := client.durationUntilNextUpdate(before); got != time.Minute {
		t.Fatalf("expected one minute until 04:00, got %s", got)
	}
	after := time.Date(2026, 8, 30, 4, 1, 0, 0, location)
	if got := client.durationUntilNextUpdate(after); got < 23*time.Hour || got > 24*time.Hour {
		t.Fatalf("expected next day update, got %s", got)
	}
}

func TestDailyUpdateScheduleFallsBackForUnavailableTimezone(t *testing.T) {
	client, err := New(Options{
		ControllerURL:  "https://controller.invalid",
		UpdateTime:     "04:00",
		UpdateLocation: "Not/ARealTimezone",
	}, relay.NewEngine())
	if err != nil {
		t.Fatal(err)
	}
	defer client.engine.Close()

	// An invalid/missing zone must not leave a nil *time.Location and panic
	// while the Agent is constructing its daily update timer.
	beijing := time.FixedZone("Asia/Shanghai", 8*60*60)
	before := time.Date(2026, 8, 30, 3, 59, 0, 0, beijing)
	if got := client.durationUntilNextUpdate(before); got != time.Minute {
		t.Fatalf("expected fallback schedule one minute before 04:00, got %s", got)
	}
}

func TestPollConfigRollsBackWhenNewRevisionCannotBeApplied(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()

	oldConfig := testAgentConfig(11, "127.0.0.1:0")
	newConfig := testAgentConfig(12, occupied.Addr().String())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("unexpected authorization header %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(newConfig)
	}))
	defer server.Close()

	engine := relay.NewEngine()
	defer engine.Close()
	if err := engine.Apply(context.Background(), oldConfig); err != nil {
		t.Fatal(err)
	}
	client, err := New(Options{ControllerURL: server.URL, DataDir: t.TempDir()}, engine)
	if err != nil {
		t.Fatal(err)
	}
	client.creds = credentials{AgentID: "relay-test", Secret: "secret"}
	client.lastConfig = oldConfig
	client.hasConfig = true
	if err := client.saveCachedConfig(oldConfig); err != nil {
		t.Fatal(err)
	}

	if err := client.pollConfig(context.Background()); err == nil {
		t.Fatal("expected the occupied listen address to reject the new revision")
	}
	if engine.Revision() != oldConfig.Revision {
		t.Fatalf("expected revision %d after rollback, got %d", oldConfig.Revision, engine.Revision())
	}
	if statuses := engine.Snapshot(); len(statuses) != 1 || statuses[0].ID != "service-test" {
		t.Fatalf("old service was not restored after rollback: %+v", statuses)
	}
	data, err := os.ReadFile(client.configPath())
	if err != nil {
		t.Fatal(err)
	}
	var cached protocol.AgentConfig
	if err := json.Unmarshal(data, &cached); err != nil {
		t.Fatal(err)
	}
	if cached.Revision != oldConfig.Revision {
		t.Fatalf("failed revision replaced the cache: %+v", cached)
	}
}

func TestSaveCachedConfigUsesPrivateAtomicReplacement(t *testing.T) {
	dataDir := t.TempDir()
	engine := relay.NewEngine()
	defer engine.Close()
	client, err := New(Options{ControllerURL: "https://controller.invalid", DataDir: dataDir}, engine)
	if err != nil {
		t.Fatal(err)
	}

	if err := client.saveCachedConfig(testAgentConfig(1, "127.0.0.1:0")); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(client.configPath())
	if err != nil {
		t.Fatal(err)
	}
	if got := before.Mode().Perm(); got != 0600 {
		t.Fatalf("expected cache mode 0600, got %04o", got)
	}

	if err := client.saveCachedConfig(testAgentConfig(2, "127.0.0.1:0")); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(client.configPath())
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(before, after) {
		t.Fatal("cache was rewritten in place instead of atomically replaced")
	}
	if got := after.Mode().Perm(); got != 0600 {
		t.Fatalf("expected replacement mode 0600, got %04o", got)
	}
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != filepath.Base(client.configPath()) {
			t.Fatalf("unexpected temporary cache artifact %q", entry.Name())
		}
	}
}

func TestDiscoverAliyunMetadataUsesIMDSv2AndConcurrentFields(t *testing.T) {
	values := map[string]string{
		"instance-id": "i-test", "region-id": "cn-hongkong",
		"eipv4": "203.0.113.9", "public-ipv4": "198.51.100.7",
	}
	var mu sync.Mutex
	requested := make(map[string]bool)
	client := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method == http.MethodPut && request.URL.Path == "/latest/api/token" {
			if request.Header.Get("X-aliyun-ecs-metadata-token-ttl-seconds") == "" {
				t.Error("IMDSv2 token TTL header is missing")
			}
			return textResponse(http.StatusOK, "metadata-token"), nil
		}
		key := strings.TrimPrefix(request.URL.Path, "/latest/meta-data/")
		if request.Header.Get("X-aliyun-ecs-metadata-token") != "metadata-token" {
			t.Errorf("metadata token is missing for %q", key)
		}
		mu.Lock()
		requested[key] = true
		mu.Unlock()
		return textResponse(http.StatusOK, values[key]), nil
	})}

	metadata := discoverAliyunMetadata(context.Background(), client)
	if metadata == nil || metadata.InstanceID != "i-test" || metadata.RegionID != "cn-hongkong" || metadata.PublicIP != "203.0.113.9" {
		t.Fatalf("unexpected metadata: %+v", metadata)
	}
	for key := range values {
		if !requested[key] {
			t.Fatalf("metadata field %q was not requested", key)
		}
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (function roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func textResponse(status int, value string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(value)),
	}
}

func testAgentConfig(revision int64, listen string) protocol.AgentConfig {
	return protocol.AgentConfig{Revision: revision, Services: []protocol.ServiceConfig{{
		ID:      "service-test",
		Name:    "test service",
		Listen:  listen,
		Network: "tcp",
		Mode:    "failover",
		Enabled: true,
		Targets: []protocol.TargetConfig{{
			ID:      "target-test",
			Name:    "test target",
			Address: "127.0.0.1:9",
			Weight:  1,
			Enabled: true,
		}},
	}}}
}
