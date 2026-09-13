package dispatcher

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// HealthHandler exposes only operational counters. It intentionally omits
// backend addresses and pool credentials from the public-facing health port.
type HealthHandler struct {
	Engine         *Engine
	Poller         *Poller
	ListenersReady func() bool
}

func (h HealthHandler) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", h.healthz)
	mux.HandleFunc("/readyz", h.readyz)
	mux.HandleFunc("/stats", h.stats)
	mux.HandleFunc("/metrics", h.metrics)
	return mux
}

func (h HealthHandler) healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	h.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "ok", "service": "alicdt-dispatcher"})
}

func (h HealthHandler) readyz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	ready := h.Engine != nil
	var stats Stats
	if h.Engine != nil {
		stats = h.Engine.Stats()
		ready = ready && stats.HealthyBackendCount > 0 && stats.BackendCount > 0
	}
	if h.Poller != nil {
		state := h.Poller.State()
		ready = ready && !state.Stale && !state.LastSuccessAt.IsZero()
	}
	if h.ListenersReady != nil {
		ready = ready && h.ListenersReady()
	}
	status := http.StatusOK
	state := "ready"
	if !ready {
		status = http.StatusServiceUnavailable
		state = "not_ready"
	}
	h.writeJSON(w, status, map[string]interface{}{"status": state, "stats": stats})
}

func (h HealthHandler) stats(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if h.Engine == nil {
		h.writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "engine unavailable"})
		return
	}
	payload := map[string]interface{}{"stats": h.Engine.Stats()}
	if h.Poller != nil {
		payload["poller"] = h.Poller.State()
	}
	h.writeJSON(w, http.StatusOK, payload)
}

func (h HealthHandler) metrics(w http.ResponseWriter, _ *http.Request) {
	if h.Engine == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	stats := h.Engine.Stats()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	lines := []string{
		"# HELP alicdt_dispatcher_active_connections Active TCP connections and UDP sessions.",
		"# TYPE alicdt_dispatcher_active_connections gauge",
		"alicdt_dispatcher_active_connections " + strconv.FormatInt(stats.ActiveConnections, 10),
		"# HELP alicdt_dispatcher_total_connections Total accepted TCP connections and UDP sessions.",
		"# TYPE alicdt_dispatcher_total_connections counter",
		"alicdt_dispatcher_total_connections " + strconv.FormatUint(stats.TotalConnections, 10),
		"# HELP alicdt_dispatcher_bytes_up Bytes sent to CDT Relays.",
		"# TYPE alicdt_dispatcher_bytes_up counter",
		"alicdt_dispatcher_bytes_up " + strconv.FormatUint(stats.BytesUp, 10),
		"# HELP alicdt_dispatcher_bytes_down Bytes received from CDT Relays.",
		"# TYPE alicdt_dispatcher_bytes_down counter",
		"alicdt_dispatcher_bytes_down " + strconv.FormatUint(stats.BytesDown, 10),
		"# HELP alicdt_dispatcher_rejected Rejected connections or datagrams.",
		"# TYPE alicdt_dispatcher_rejected counter",
		"alicdt_dispatcher_rejected " + strconv.FormatUint(stats.Rejected, 10),
		"# HELP alicdt_dispatcher_backend_failures Backend dial/write failures.",
		"# TYPE alicdt_dispatcher_backend_failures counter",
		"alicdt_dispatcher_backend_failures " + strconv.FormatUint(stats.BackendFailures, 10),
		"# HELP alicdt_dispatcher_backend_count Configured enabled backends.",
		"# TYPE alicdt_dispatcher_backend_count gauge",
		"alicdt_dispatcher_backend_count " + strconv.Itoa(stats.BackendCount),
		"# HELP alicdt_dispatcher_healthy_backend_count Backends outside failure cooldown.",
		"# TYPE alicdt_dispatcher_healthy_backend_count gauge",
		"alicdt_dispatcher_healthy_backend_count " + strconv.Itoa(stats.HealthyBackendCount),
		"# HELP alicdt_dispatcher_udp_session_count Active UDP sessions.",
		"# TYPE alicdt_dispatcher_udp_session_count gauge",
		"alicdt_dispatcher_udp_session_count " + strconv.Itoa(stats.UDPSessionCount),
	}
	if h.Poller != nil {
		state := h.Poller.State()
		pollReady := !state.Stale && !state.LastSuccessAt.IsZero()
		value := 0
		if pollReady {
			value = 1
		}
		lines = append(lines, "# HELP alicdt_dispatcher_config_ready Whether a fresh controller snapshot is active.", "# TYPE alicdt_dispatcher_config_ready gauge", "alicdt_dispatcher_config_ready "+strconv.Itoa(value))
	}
	_, _ = w.Write([]byte(joinLines(lines)))
}

func joinLines(lines []string) string {
	var result strings.Builder
	for _, line := range lines {
		result.WriteString(line)
		result.WriteByte('\n')
	}
	return result.String()
}

func (h HealthHandler) writeJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
