package controller

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSystemUpdateRequestAndStatus(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	directory := t.TempDir()
	requestPath := filepath.Join(directory, "update.request")
	statusPath := filepath.Join(directory, "update.status.json")
	server, err := NewServer(store, ServerOptions{AdminToken: "admin", UpdateRequestFile: requestPath, UpdateStatusFile: statusPath})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	request, err := http.NewRequest(http.MethodPost, httpServer.URL+"/api/v2/system/update", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer admin")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusAccepted {
		response.Body.Close()
		t.Fatalf("expected accepted update request, got %d", response.StatusCode)
	}
	var pending UpdateStatus
	if err := json.NewDecoder(response.Body).Decode(&pending); err != nil {
		response.Body.Close()
		t.Fatal(err)
	}
	response.Body.Close()
	if pending.Status != "pending" || pending.RequestID == "" {
		t.Fatalf("unexpected pending status: %+v", pending)
	}
	data, err := os.ReadFile(requestPath)
	if err != nil {
		t.Fatal(err)
	}
	var marker updateRequest
	if err := json.Unmarshal(data, &marker); err != nil || marker.RequestID != pending.RequestID {
		t.Fatalf("request marker mismatch: data=%s marker=%+v pending=%+v", data, marker, pending)
	}

	statusData, _ := json.Marshal(UpdateStatus{Status: "running", Message: "building"})
	if err := os.WriteFile(statusPath, statusData, 0600); err != nil {
		t.Fatal(err)
	}
	request, _ = http.NewRequest(http.MethodGet, httpServer.URL+"/api/v2/system/update/status", nil)
	request.Header.Set("Authorization", "Bearer admin")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var running UpdateStatus
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&running) != nil {
		response.Body.Close()
		t.Fatalf("failed to read update status: %d", response.StatusCode)
	}
	response.Body.Close()
	if running.Status != "running" || running.Message != "building" {
		t.Fatalf("unexpected running status: %+v", running)
	}

	request, _ = http.NewRequest(http.MethodPost, httpServer.URL+"/api/v2/system/update", bytes.NewReader([]byte(`{}`)))
	request.Header.Set("Authorization", "Bearer admin")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("expected conflict while updating, got %d", response.StatusCode)
	}
}

func TestSystemUpdateStatusDefaultsToIdle(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server, err := NewServer(store, ServerOptions{AdminToken: "admin", UpdateStatusFile: filepath.Join(t.TempDir(), "missing-status.json")})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v2/system/update/status", nil)
	request.Header.Set("Authorization", "Bearer admin")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	var status UpdateStatus
	if err := json.Unmarshal(recorder.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.Status != "idle" {
		t.Fatalf("expected idle status, got %+v", status)
	}
}
