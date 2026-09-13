package dispatcher

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientFetchesDedicatedSnapshotAndBuildsConfig(t *testing.T) {
	now := time.Date(2026, 8, 31, 1, 2, 3, 0, time.UTC)
	var gotPath, gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(Snapshot{PoolID: "pool/unsafe", PoolName: "entry", ListenPort: 443, Network: "tcp+udp", SelectionMode: "quota_weighted", DialTimeoutMillis: 1500, UDPIdleTimeoutSeconds: 90, FailureThreshold: 3, FailureCooldownSeconds: 12, Backends: []SnapshotBackend{{ID: "relay-a", Name: "hk-a", Address: "203.0.113.10:443", Weight: 2, TrafficKnown: true, TrafficRemainingGB: 50, TrafficRateGBPerMinute: 1}}, GeneratedAt: now})
	}))
	defer server.Close()
	client, err := NewClient(ClientOptions{ControllerURL: server.URL + "/base/", PoolID: "pool/unsafe", Token: "dispatch-token", Clock: func() time.Time { return now }, MaxSnapshotAge: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := client.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/base/api/v2/dispatch/pools/pool%2Funsafe" {
		t.Fatalf("unexpected endpoint path %q", gotPath)
	}
	if gotAuth != "Bearer dispatch-token" {
		t.Fatalf("unexpected auth header %q", gotAuth)
	}
	if cfg.Revision == "" || cfg.Network != "tcp+udp" || cfg.SelectionMode != "quota_weighted" || len(cfg.Backends) != 1 || !cfg.Backends[0].Enabled || cfg.Backends[0].TrafficRemainingGB != 50 {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if cfg.DialTimeout != 1500*time.Millisecond || cfg.UDPIdleTimeout != 90*time.Second || cfg.FailureThreshold != 3 || cfg.FailureCooldown != 12*time.Second {
		t.Fatalf("snapshot settings were not converted: %+v", cfg)
	}
}

func TestClientRejectsStaleAndMismatchedSnapshots(t *testing.T) {
	now := time.Date(2026, 8, 31, 1, 0, 0, 0, time.UTC)
	responses := []Snapshot{{PoolID: "other", ListenPort: 443, GeneratedAt: now}, {PoolID: "pool", ListenPort: 443, GeneratedAt: now.Add(-2 * time.Minute)}}
	index := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(responses[index])
		index++
	}))
	defer server.Close()
	client, err := NewClient(ClientOptions{ControllerURL: server.URL, PoolID: "pool", Token: "token", Clock: func() time.Time { return now }, MaxSnapshotAge: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Fetch(context.Background()); err == nil || !strings.Contains(err.Error(), "pool ID") {
		t.Fatalf("expected pool mismatch, got %v", err)
	}
	if _, err := client.Fetch(context.Background()); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("expected stale snapshot, got %v", err)
	}
}

func TestClientRejectsHTTPErrorsAndTrailingJSON(t *testing.T) {
	mode := "status"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mode == "status" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":"do not expose this"}`)
			return
		}
		_, _ = io.WriteString(w, `{"pool_id":"pool","listen_port":443,"generated_at":"2026-08-31T01:00:00Z"}{"extra":true}`)
	}))
	defer server.Close()
	client, err := NewClient(ClientOptions{ControllerURL: server.URL, PoolID: "pool", Token: "token", Clock: func() time.Time { return time.Date(2026, 8, 31, 1, 0, 1, 0, time.UTC) }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Fetch(context.Background()); err == nil || strings.Contains(err.Error(), "do not expose") {
		t.Fatalf("HTTP error leaked body or was accepted: %v", err)
	}
	mode = "trailing"
	if _, err := client.Fetch(context.Background()); err == nil || !strings.Contains(err.Error(), "trailing") {
		t.Fatalf("expected trailing JSON rejection, got %v", err)
	}
}

func TestNewClientValidatesControllerURLAndCredentials(t *testing.T) {
	cases := []ClientOptions{{PoolID: "p", Token: "t"}, {ControllerURL: "not-a-url", PoolID: "p", Token: "t"}, {ControllerURL: "ftp://example.test", PoolID: "p", Token: "t"}, {ControllerURL: "https://user:pass@example.test", PoolID: "p", Token: "t"}, {ControllerURL: "https://example.test", Token: "t"}, {ControllerURL: "https://example.test", PoolID: "p"}}
	for _, options := range cases {
		if _, err := NewClient(options); err == nil {
			t.Fatalf("expected invalid options to fail: %+v", options)
		}
	}
}
