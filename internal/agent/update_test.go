package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/protocol"
	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/relay"
)

func TestInstallAndConfirmPendingUpdate(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "agent-bin")
	if err := os.WriteFile(binary, []byte("old-agent"), 0755); err != nil {
		t.Fatal(err)
	}
	client, err := New(Options{ControllerURL: "https://controller.invalid", DataDir: dir, BinaryPath: binary}, relay.NewEngine())
	if err != nil {
		t.Fatal(err)
	}
	defer client.engine.Close()
	newData := []byte("new-agent")
	sum := sha256Hex(newData)
	if err := client.installUpdate(newData, sum); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new-agent" {
		t.Fatalf("new binary was not installed: %q", data)
	}
	client.setUpdateState("updating", "")
	if err := client.recoverPendingUpdate(); err != nil {
		t.Fatal(err)
	}
	if err := client.confirmPendingUpdate(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(client.updateMarkerPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("update marker remained: %v", err)
	}
	status, updateErr := client.currentUpdateState()
	if status != "idle" || updateErr != "" {
		t.Fatalf("unexpected update state: %s/%s", status, updateErr)
	}
}

func TestCheckForUpdateDownloadsAndVerifiesRelease(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "agent-bin")
	if err := os.WriteFile(binary, []byte("old-agent"), 0755); err != nil {
		t.Fatal(err)
	}
	newData := []byte("new-agent-from-controller")
	checksum := sha256Hex(newData)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/release"):
			_ = json.NewEncoder(w).Encode(protocol.AgentRelease{Available: true, Version: "next", Architecture: "amd64", SHA256: checksum, URL: "/agent/asset", Size: int64(len(newData))})
		case r.URL.Path == "/agent/asset":
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(newData)
		case strings.HasSuffix(r.URL.Path, "/update/state"):
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := New(Options{ControllerURL: server.URL, DataDir: dir, BinaryPath: binary, UpdateCheckInterval: 1, AutoUpdate: true, AutoUpdateSet: true}, relay.NewEngine())
	if err != nil {
		t.Fatal(err)
	}
	defer client.engine.Close()
	client.creds = credentials{AgentID: "relay-test", Secret: "secret"}
	err = client.checkForUpdate(context.Background())
	if !errors.Is(err, ErrRestartRequested) {
		t.Fatalf("expected restart request, got %v", err)
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(newData) {
		t.Fatalf("downloaded binary was not installed: %q", data)
	}
}

func sha256Hex(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
