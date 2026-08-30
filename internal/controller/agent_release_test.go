package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/protocol"
)

func TestAgentReleaseReturnsChecksumAndAvailability(t *testing.T) {
	dir := t.TempDir()
	asset := filepath.Join(dir, "cdt-relay-agent-linux-amd64")
	if err := os.WriteFile(asset, []byte("agent-binary"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "checksums.txt"), []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  cdt-relay-agent-linux-amd64\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "install.sh"), []byte("#!/bin/sh\n"), 0700); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateEnrollmentToken(context.Background(), "release-token", 0); err != nil {
		t.Fatal(err)
	}
	enrolled, err := store.EnrollAgent(context.Background(), protocol.AgentEnrollmentRequest{Token: "release-token", NodeName: "relay"})
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(store, ServerOptions{AdminToken: "admin", AgentInstallerPath: filepath.Join(dir, "install.sh"), AgentVersion: "test-release"})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	request, _ := http.NewRequest(http.MethodGet, httpServer.URL+"/api/v2/agents/"+enrolled.AgentID+"/release?arch=amd64&sha256=bbbb", nil)
	request.Header.Set("Authorization", "Bearer "+enrolled.Secret)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %d", response.StatusCode)
	}
	var release protocol.AgentRelease
	if err := json.NewDecoder(response.Body).Decode(&release); err != nil {
		t.Fatal(err)
	}
	if !release.Available || release.SHA256 != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" || release.Version != "test-release" || release.URL != "/agent/cdt-relay-agent-linux-amd64" {
		t.Fatalf("unexpected release: %+v", release)
	}
}
