package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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
			_ = json.NewEncoder(w).Encode(protocol.AgentRelease{Available: true, Version: "next", Architecture: runtime.GOARCH, SHA256: checksum, URL: "/agent/cdt-relay-agent-linux-" + runtime.GOARCH, Size: int64(len(newData))})
		case r.URL.Path == "/agent/cdt-relay-agent-linux-"+runtime.GOARCH:
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

func TestResolveReleaseURLStaysOnExactControllerAsset(t *testing.T) {
	client, err := New(Options{ControllerURL: "https://controller.example/base"}, relay.NewEngine())
	if err != nil {
		t.Fatal(err)
	}
	defer client.engine.Close()
	assetPath := "/agent/cdt-relay-agent-linux-" + runtime.GOARCH
	valid := []string{assetPath, "https://controller.example" + assetPath}
	for _, raw := range valid {
		resolved, err := client.resolveReleaseURL(raw)
		if err != nil || resolved != "https://controller.example"+assetPath {
			t.Errorf("valid release URL %q resolved to %q: %v", raw, resolved, err)
		}
	}
	invalid := []string{
		"//attacker.example" + assetPath,
		"https://attacker.example" + assetPath,
		"/agent/../private",
		assetPath + "?download=1",
		assetPath + "#fragment",
		"https://user@controller.example" + assetPath,
		"/agent/cdt-relay-agent-linux-other",
	}
	for _, raw := range invalid {
		if resolved, err := client.resolveReleaseURL(raw); err == nil {
			t.Errorf("unsafe release URL %q was accepted as %q", raw, resolved)
		}
	}
}

func TestAgentHTTPClientRejectsCrossOriginRedirect(t *testing.T) {
	var leakedAuthorization string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leakedAuthorization = r.Header.Get("Authorization")
		_, _ = w.Write([]byte("untrusted"))
	}))
	defer target.Close()
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusFound)
	}))
	defer controller.Close()
	client, err := New(Options{ControllerURL: controller.URL}, relay.NewEngine())
	if err != nil {
		t.Fatal(err)
	}
	defer client.engine.Close()
	client.creds = credentials{AgentID: "relay-test", Secret: "agent-secret"}
	assetPath := "/agent/cdt-relay-agent-linux-" + runtime.GOARCH
	if _, err := client.downloadRelease(context.Background(), controller.URL+assetPath, 16); err == nil || !strings.Contains(err.Error(), "redirect changed origin") {
		t.Fatalf("expected cross-origin redirect rejection, got %v", err)
	}
	if leakedAuthorization != "" {
		t.Fatalf("agent credential leaked across redirect: %q", leakedAuthorization)
	}
}

func TestDecodeAgentReleaseBoundsAndRejectsTrailingJSON(t *testing.T) {
	wanted := protocol.AgentRelease{Available: true, Architecture: runtime.GOARCH, SHA256: strings.Repeat("a", 64), URL: "/agent/cdt-relay-agent-linux-" + runtime.GOARCH, Size: 42}
	data, err := json.Marshal(wanted)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeAgentRelease(bytes.NewReader(data))
	if err != nil || got.URL != wanted.URL || got.Size != wanted.Size {
		t.Fatalf("valid release was not decoded: %+v, %v", got, err)
	}
	if _, err := decodeAgentRelease(bytes.NewReader(append(data, []byte(`{"extra":true}`)...))); err == nil || !strings.Contains(err.Error(), "trailing") {
		t.Fatalf("expected trailing JSON rejection, got %v", err)
	}
	oversized := strings.NewReader(`{"padding":"` + strings.Repeat("x", maxAgentReleaseResponseBytes) + `"}`)
	if _, err := decodeAgentRelease(oversized); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("expected oversized response rejection, got %v", err)
	}
}

func TestUpdateEndpointErrorDoesNotLeakResponseBody(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "agent-bin")
	if err := os.WriteFile(binary, []byte("old-agent"), 0755); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("upstream-secret-must-not-enter-agent-status"))
	}))
	defer server.Close()
	client, err := New(Options{ControllerURL: server.URL, DataDir: dir, BinaryPath: binary}, relay.NewEngine())
	if err != nil {
		t.Fatal(err)
	}
	defer client.engine.Close()
	client.creds = credentials{AgentID: "relay-test", Secret: "secret"}
	err = client.checkForUpdate(context.Background())
	if err == nil || strings.Contains(err.Error(), "upstream-secret") {
		t.Fatalf("release error body leaked or request unexpectedly passed: %v", err)
	}
}

func TestRecoverPendingUpdateRollsBackAfterRepeatedFailedStarts(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "agent-bin")
	backup := filepath.Join(dir, "agent-backup.bin")
	oldData := []byte("known-good-agent")
	newData := []byte("broken-new-agent")
	if err := os.WriteFile(binary, newData, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backup, oldData, 0700); err != nil {
		t.Fatal(err)
	}
	client, err := New(Options{ControllerURL: "https://controller.invalid", DataDir: dir, BinaryPath: binary}, relay.NewEngine())
	if err != nil {
		t.Fatal(err)
	}
	defer client.engine.Close()
	marker := pendingUpdate{BackupPath: backup, TargetSHA256: sha256Hex(newData), PreparedAt: time.Now().UTC()}
	encoded, _ := json.Marshal(marker)
	if err := os.WriteFile(client.updateMarkerPath(), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 3; attempt++ {
		if err := client.recoverPendingUpdate(); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, oldData) {
		t.Fatalf("known-good binary was not restored: %q", data)
	}
	if _, err := os.Stat(client.updateMarkerPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rollback marker remained after restore: %v", err)
	}
	status, updateErr := client.currentUpdateState()
	if status != "failed" || !strings.Contains(updateErr, "rolled back") {
		t.Fatalf("rollback state was not exposed: %s/%s", status, updateErr)
	}
}

func sha256Hex(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
