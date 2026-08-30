package controller

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/protocol"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type ServerOptions struct {
	AdminToken         string
	FrontendDir        string
	AgentInstallerPath string
	UpdateRequestFile  string
	UpdateStatusFile   string
}

type Server struct {
	store              *Store
	adminToken         string
	frontendDir        string
	agentInstallerPath string
	agentAssetsDir     string
	updateRequestFile  string
	updateStatusFile   string
	cloud              *CloudService
	router             chi.Router
}

func NewServer(store *Store, opts ServerOptions) (*Server, error) {
	if store == nil {
		return nil, errors.New("store is required")
	}
	if strings.TrimSpace(opts.AdminToken) == "" {
		return nil, errors.New("admin token is required")
	}
	server := &Server{store: store, adminToken: opts.AdminToken, frontendDir: opts.FrontendDir, agentInstallerPath: opts.AgentInstallerPath, updateRequestFile: opts.UpdateRequestFile, updateStatusFile: opts.UpdateStatusFile, cloud: NewCloudService(store)}
	if opts.AgentInstallerPath != "" {
		server.agentAssetsDir = filepath.Dir(opts.AgentInstallerPath)
	}
	server.router = server.routes()
	return server, nil
}

func (s *Server) Handler() http.Handler { return s.router }

func (s *Server) routes() chi.Router {
	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(middleware.RealIP)
	router.Use(middleware.Recoverer)
	router.Use(middleware.Timeout(30 * time.Second))
	router.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]interface{}{"status": "ok", "service": "alicdt-controller"})
	})
	if s.agentInstallerPath != "" {
		router.Get("/agent/install.sh", s.serveAgentInstaller)
		router.Get("/agent/{asset}", s.serveAgentAsset)
	}
	router.Get("/api/v2/auth/initialized", s.adminInitialized)
	router.Post("/api/v2/auth/init", s.initAdmin)
	router.Post("/api/v2/auth/login", s.loginAdmin)
	router.Get("/api/auth/initialized", s.adminInitialized)
	router.Post("/api/auth/init", s.initAdmin)
	router.Post("/api/auth/login", s.loginAdmin)

	router.Route("/api/v2/agents", func(router chi.Router) {
		router.Post("/enroll", s.enrollAgent)
		router.Group(func(router chi.Router) {
			router.Use(s.agentAuth)
			router.Get("/{agentID}/config", s.agentConfig)
			router.Post("/{agentID}/heartbeat", s.agentHeartbeat)
		})
	})

	router.Group(func(router chi.Router) {
		router.Use(s.adminAuth)
		router.Post("/api/v2/enrollment-tokens", s.createEnrollmentToken)
		router.Get("/api/v2/relay-nodes", s.listRelayNodes)
		router.Get("/api/v2/landing-nodes", s.listLandingNodes)
		router.Get("/api/v2/landing-nodes/{landingID}/relay-links", s.landingRelayLinks)
		router.Post("/api/v2/landing-nodes", s.createLandingNode)
		router.Put("/api/v2/landing-nodes/{landingID}", s.updateLandingNode)
		router.Delete("/api/v2/landing-nodes/{landingID}", s.deleteLandingNode)
		router.Get("/api/v2/relay-services", s.listRelayServices)
		router.Post("/api/v2/relay-services", s.createRelayService)
		router.Put("/api/v2/relay-services/{serviceID}", s.updateRelayService)
		router.Delete("/api/v2/relay-services/{serviceID}", s.deleteRelayService)
		router.Get("/api/v2/relay-pools", s.listRelayPools)
		router.Post("/api/v2/relay-pools", s.createRelayPool)
		router.Put("/api/v2/relay-pools/{poolID}", s.updateRelayPool)
		router.Delete("/api/v2/relay-pools/{poolID}", s.deleteRelayPool)
		router.Get("/api/v2/relay-pools/{poolID}/relay-links", s.relayPoolLinks)
		router.Get("/api/v2/events", s.listEvents)
		router.Get("/api/v2/dns/providers", s.listDNSProviders)
		router.Post("/api/v2/dns/providers", s.createDNSProvider)
		router.Put("/api/v2/dns/providers/{providerID}", s.updateDNSProvider)
		router.Delete("/api/v2/dns/providers/{providerID}", s.deleteDNSProvider)
		router.Post("/api/v2/dns/providers/{providerID}/test", s.testDNSProvider)
		router.Post("/api/v2/dns/providers/{providerID}/sync", s.syncDNSProvider)
		router.Get("/api/v2/dns/records", s.listDNSRecords)
		router.Post("/api/v2/dns/records", s.createDNSRecord)
		router.Put("/api/v2/dns/records/{recordID}", s.updateDNSRecord)
		router.Delete("/api/v2/dns/records/{recordID}", s.deleteDNSRecord)
		router.Post("/api/v2/dns/sync", s.syncAllDNS)
		router.Get("/api/v2/cloud/overview", s.cloudOverview)
		router.Post("/api/v2/cloud/sync", s.syncCloud)
		router.Post("/api/v2/cloud/accounts", s.createCloudAccount)
		router.Put("/api/v2/cloud/accounts/{accountID}", s.updateCloudAccount)
		router.Delete("/api/v2/cloud/accounts/{accountID}", s.deleteCloudAccount)
		router.Post("/api/v2/cloud/instances/{instanceID}/start", s.startCloudInstance)
		router.Post("/api/v2/cloud/instances/{instanceID}/stop", s.stopCloudInstance)
		router.Post("/api/v2/system/update", s.requestSystemUpdate)
		router.Get("/api/v2/system/update/status", s.systemUpdateStatus)
	})
	router.Route("/api", func(router chi.Router) {
		router.Use(s.adminAuth)
		router.Get("/accounts", s.legacyListAccounts)
		router.Post("/accounts", s.legacyCreateAccount)
		router.Put("/accounts/{accountID}", s.legacyUpdateAccount)
		router.Delete("/accounts/{accountID}", s.legacyDeleteAccount)
		router.Get("/instances", s.legacyListInstances)
		router.Post("/instances/sync", s.legacySyncInstances)
		router.Post("/instances/{instanceID}/sync", s.legacySyncInstance)
		router.Patch("/instances/{instanceID}/rename", s.legacyRenameInstance)
		router.Post("/instances/{instanceID}/start", s.startCloudInstance)
		router.Post("/instances/{instanceID}/stop", s.stopCloudInstance)
		router.Delete("/instances/{instanceID}", s.legacyReleaseInstance)
		router.Get("/billing/{accountID}", s.legacyBilling)
		router.Get("/settings", s.legacyGetSettings)
		router.Post("/settings", s.legacyUpdateSettings)
		router.Post("/settings/test-tg", s.legacyTestTelegram)
		router.Post("/settings/test-daily-report", s.legacyTestDailyReport)
		router.Post("/settings/change-password", s.legacyChangePassword)
		router.Get("/version/check", s.legacyVersionCheck)
		router.Get("/logs", s.legacyListLogs)
		router.Delete("/logs", s.legacyClearLogs)
	})
	if s.frontendDir != "" {
		router.NotFound(s.serveFrontend)
	}
	return router
}

func (s *Server) serveAgentInstaller(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, s.agentInstallerPath)
}

func (s *Server) serveAgentAsset(w http.ResponseWriter, r *http.Request) {
	asset := chi.URLParam(r, "asset")
	allowed := map[string]string{
		"cdt-relay-agent-linux-amd64": "application/octet-stream",
		"cdt-relay-agent-linux-arm64": "application/octet-stream",
		"checksums.txt":               "text/plain; charset=utf-8",
	}
	contentType, ok := allowed[asset]
	if !ok || s.agentAssetsDir == "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=300")
	http.ServeFile(w, r, filepath.Join(s.agentAssetsDir, asset))
}

func (s *Server) serveFrontend(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeError(w, http.StatusNotFound, errors.New("API route not found"))
		return
	}
	clean := strings.TrimPrefix(filepath.Clean(r.URL.Path), string(filepath.Separator))
	if clean == "." || clean == "" {
		clean = "index.html"
	}
	candidate := filepath.Join(s.frontendDir, clean)
	relative, err := filepath.Rel(s.frontendDir, candidate)
	if err == nil && !strings.HasPrefix(relative, "..") {
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			http.ServeFile(w, r, candidate)
			return
		}
	}
	http.ServeFile(w, r, filepath.Join(s.frontendDir, "index.html"))
}

func (s *Server) adminAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		staticValid := subtle.ConstantTimeCompare([]byte(token), []byte(s.adminToken)) == 1
		if !staticValid && s.store.AuthenticateAdminSession(r.Context(), token) != nil {
			writeError(w, http.StatusUnauthorized, errors.New("invalid admin token"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) adminInitialized(w http.ResponseWriter, r *http.Request) {
	initialized, err := s.store.IsAdminInitialized(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"initialized": initialized})
}

func (s *Server) initAdmin(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	token, err := s.store.InitAdmin(r.Context(), request.Username, request.Password)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"token": token, "username": request.Username})
}

func (s *Server) loginAdmin(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	token, err := s.store.LoginAdmin(r.Context(), request.Username, request.Password)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": token, "username": request.Username})
}

func (s *Server) agentAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := s.store.AuthenticateAgent(r.Context(), chi.URLParam(r, "agentID"), bearerToken(r)); err != nil {
			writeError(w, http.StatusUnauthorized, errors.New("invalid agent credentials"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) enrollAgent(w http.ResponseWriter, r *http.Request) {
	var request protocol.AgentEnrollmentRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	response, err := s.store.EnrollAgent(r.Context(), request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, response)
}

func (s *Server) agentConfig(w http.ResponseWriter, r *http.Request) {
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	config, err := s.store.AgentConfig(r.Context(), chi.URLParam(r, "agentID"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if config.Revision <= after {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, config)
}

func (s *Server) agentHeartbeat(w http.ResponseWriter, r *http.Request) {
	var heartbeat protocol.AgentHeartbeat
	if err := decodeJSON(r, &heartbeat); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.store.UpdateHeartbeat(r.Context(), chi.URLParam(r, "agentID"), heartbeat); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) createEnrollmentToken(w http.ResponseWriter, r *http.Request) {
	var request struct {
		TTLMinutes int `json:"ttl_minutes"`
	}
	if r.ContentLength > 0 {
		if err := decodeJSON(r, &request); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	if request.TTLMinutes <= 0 {
		request.TTLMinutes = 30
	}
	raw := randomSecret(24)
	if err := s.store.CreateEnrollmentToken(r.Context(), raw, time.Duration(request.TTLMinutes)*time.Minute); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]interface{}{"token": raw, "expires_in_minutes": request.TTLMinutes})
}

func (s *Server) listRelayNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := s.store.ListRelayNodes(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	now := time.Now().UTC()
	for index := range nodes {
		if nodes[index].LastSeenAt == nil || now.Sub(*nodes[index].LastSeenAt) > 35*time.Second {
			nodes[index].Status = "offline"
		}
	}
	writeJSON(w, http.StatusOK, nodes)
}

func (s *Server) createLandingNode(w http.ResponseWriter, r *http.Request) {
	var request CreateLandingNodeRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	node, err := s.store.CreateLandingNode(r.Context(), request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, node)
}

func (s *Server) landingRelayLinks(w http.ResponseWriter, r *http.Request) {
	links, err := s.store.LandingRelayLinks(r.Context(), chi.URLParam(r, "landingID"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, links)
}

func (s *Server) listLandingNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := s.store.ListLandingNodes(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, nodes)
}

func (s *Server) updateLandingNode(w http.ResponseWriter, r *http.Request) {
	var request CreateLandingNodeRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	node, err := s.store.UpdateLandingNode(r.Context(), chi.URLParam(r, "landingID"), request)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeStoreError(w, err)
		} else {
			writeError(w, http.StatusBadRequest, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, node)
}

func (s *Server) deleteLandingNode(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteLandingNode(r.Context(), chi.URLParam(r, "landingID")); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) createRelayService(w http.ResponseWriter, r *http.Request) {
	var request CreateRelayServiceRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	service, err := s.store.CreateRelayService(r.Context(), request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, service)
}

func (s *Server) listRelayServices(w http.ResponseWriter, r *http.Request) {
	services, err := s.store.ListRelayServices(r.Context(), r.URL.Query().Get("relay_node_id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	filtered := services[:0]
	for _, service := range services {
		if service.PoolID == "" {
			filtered = append(filtered, service)
		}
	}
	writeJSON(w, http.StatusOK, filtered)
}

func (s *Server) updateRelayService(w http.ResponseWriter, r *http.Request) {
	var request CreateRelayServiceRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	service, err := s.store.UpdateRelayService(r.Context(), chi.URLParam(r, "serviceID"), request)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeStoreError(w, err)
		} else {
			writeError(w, http.StatusBadRequest, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, service)
}

func (s *Server) deleteRelayService(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteRelayService(r.Context(), chi.URLParam(r, "serviceID")); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listEvents(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	events, err := s.store.ListEvents(r.Context(), limit)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, events)
}

func (s *Server) cloudOverview(w http.ResponseWriter, r *http.Request) {
	overview, err := s.store.CloudOverview(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, overview)
}

func (s *Server) syncCloud(w http.ResponseWriter, r *http.Request) {
	results, err := s.cloud.SyncAll(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, results)
}

func (s *Server) createCloudAccount(w http.ResponseWriter, r *http.Request) {
	var request CloudAccountRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	account, err := s.store.CreateCloudAccount(r.Context(), request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.syncAccountAsync(account.ID)
	writeJSON(w, http.StatusCreated, account)
}

func (s *Server) updateCloudAccount(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "accountID"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid account id"))
		return
	}
	var request CloudAccountRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	account, err := s.store.UpdateCloudAccount(r.Context(), id, request)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeStoreError(w, err)
		} else {
			writeError(w, http.StatusBadRequest, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, account)
}

func (s *Server) deleteCloudAccount(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "accountID"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid account id"))
		return
	}
	if err := s.store.DeleteCloudAccount(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) startCloudInstance(w http.ResponseWriter, r *http.Request) {
	if err := s.cloud.StartInstance(r.Context(), chi.URLParam(r, "instanceID")); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"message": "start command sent"})
}

func (s *Server) stopCloudInstance(w http.ResponseWriter, r *http.Request) {
	if err := s.cloud.StopInstance(r.Context(), chi.URLParam(r, "instanceID")); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"message": "stop command sent"})
}

func (s *Server) RunCloudScheduler(ctx context.Context, interval time.Duration) {
	s.cloud.RunScheduler(ctx, interval)
}

func bearerToken(r *http.Request) string {
	parts := strings.SplitN(r.Header.Get("Authorization"), " ", 2)
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return strings.TrimSpace(parts[1])
	}
	return ""
}

func decodeJSON(r *http.Request, destination interface{}) error {
	decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func writeStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, errors.New("resource not found"))
		return
	}
	writeError(w, http.StatusInternalServerError, err)
}

func WithContext(ctx context.Context, handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r.WithContext(ctx))
	})
}
