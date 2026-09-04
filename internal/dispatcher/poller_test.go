package dispatcher

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPollerRetainsThenDrainsStaleConfiguration(t *testing.T) {
	now := time.Date(2026, 8, 31, 2, 0, 0, 0, time.UTC)
	serveError := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveError {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_ = json.NewEncoder(w).Encode(Snapshot{PoolID: "pool", Revision: "r1", Network: "tcp", ListenPort: 443, Backends: []SnapshotBackend{{ID: "relay", Address: "127.0.0.1:443"}}, GeneratedAt: now})
	}))
	defer server.Close()
	clock := func() time.Time { return now }
	client, err := NewClient(ClientOptions{ControllerURL: server.URL, PoolID: "pool", Token: "token", Clock: clock, MaxSnapshotAge: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	engine := NewEngine()
	defer engine.Close()
	poller, err := NewPoller(client, engine, PollerOptions{Interval: time.Second, StaleAfter: 10 * time.Second, Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	if err := poller.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(engine.Config().Backends) != 1 || poller.State().Stale {
		t.Fatalf("initial sync not active: config=%+v state=%+v", engine.Config(), poller.State())
	}
	serveError = true
	now = now.Add(5 * time.Second)
	if err := poller.Sync(context.Background()); err == nil {
		t.Fatal("expected transient sync error")
	}
	if len(engine.Config().Backends) != 1 || poller.State().Stale {
		t.Fatalf("transient error should retain config: config=%+v state=%+v", engine.Config(), poller.State())
	}
	now = now.Add(6 * time.Second)
	if err := poller.Sync(context.Background()); err == nil {
		t.Fatal("expected stale sync error")
	}
	if len(engine.Config().Backends) != 0 || !poller.State().Stale {
		t.Fatalf("stale config was not drained: config=%+v state=%+v", engine.Config(), poller.State())
	}
	if engine.Stats().LastConfigError == "" {
		t.Fatalf("drain lost polling error: %+v", engine.Stats())
	}
}

func TestPollerSuccessfulEmptyPoolIsNotMarkedStale(t *testing.T) {
	now := time.Now().UTC()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(Snapshot{PoolID: "pool", Revision: "empty", Network: "tcp+udp", ListenPort: 443, Backends: []SnapshotBackend{}, GeneratedAt: now})
	}))
	defer server.Close()
	client, err := NewClient(ClientOptions{ControllerURL: server.URL, PoolID: "pool", Token: "token", MaxSnapshotAge: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	engine := NewEngine()
	defer engine.Close()
	poller, err := NewPoller(client, engine, PollerOptions{Interval: time.Second, StaleAfter: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if err := poller.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	state := poller.State()
	if state.Stale || state.LastSuccessAt.IsZero() || len(engine.Config().Backends) != 0 {
		t.Fatalf("empty but valid pool was treated as stale: state=%+v config=%+v", state, engine.Config())
	}
}

func TestPollerRejectsSnapshotWithUnboundTransport(t *testing.T) {
	now := time.Now().UTC()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(Snapshot{PoolID: "pool", Revision: "tcp-udp", Network: "tcp+udp", ListenPort: 443, Backends: []SnapshotBackend{{ID: "relay", Address: "127.0.0.1:443"}}, GeneratedAt: now})
	}))
	defer server.Close()
	client, err := NewClient(ClientOptions{ControllerURL: server.URL, PoolID: "pool", Token: "token"})
	if err != nil {
		t.Fatal(err)
	}
	engine := NewEngine()
	defer engine.Close()
	poller, err := NewPoller(client, engine, PollerOptions{ListenerNetwork: "tcp", StaleAfter: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if err := poller.Sync(context.Background()); err == nil {
		t.Fatal("expected transport mismatch")
	}
	if len(engine.Config().Backends) != 0 || poller.State().LastError == "" {
		t.Fatalf("mismatched snapshot was applied: config=%+v state=%+v", engine.Config(), poller.State())
	}
}
