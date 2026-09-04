package dispatcher

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthHandlerReadinessAndMetrics(t *testing.T) {
	engine := NewEngine()
	defer engine.Close()
	handler := HealthHandler{Engine: engine, ListenersReady: func() bool { return true }}.Handler()
	request := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("empty engine should not be ready: %d", recorder.Code)
	}
	if err := engine.Apply(Config{Network: "tcp", Backends: []Backend{{ID: "relay", Address: "127.0.0.1:443", Enabled: true}}}); err != nil {
		t.Fatal(err)
	}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("configured engine should be ready: %d %s", recorder.Code, recorder.Body.String())
	}
	metrics := httptest.NewRecorder()
	handler.ServeHTTP(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if metrics.Code != http.StatusOK || !strings.Contains(metrics.Body.String(), "alicdt_dispatcher_backend_count 1") {
		t.Fatalf("metrics response missing backend count: %d %s", metrics.Code, metrics.Body.String())
	}
}
