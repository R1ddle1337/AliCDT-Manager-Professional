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
	"sync"
	"time"

	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/protocol"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type ServerOptions struct {
	AdminToken              string
	FrontendDir             string
	AgentInstallerPath      string
	AgentVersion            string
	AgentReleaseSource      string
	AgentReleaseRepo        string
	AgentReleaseChannel     string
	AgentReleaseCacheDir    string
	UpdateRequestFile       string
	UpdateStatusFile        string
	AgentUpgradeRequestFile string
	TrafficSafetyWindow     time.Duration
	TrafficSafetyWindowSet  bool
	DispatchToken           string
}

type Server struct {
	store                   *Store
	adminToken              string
	frontendDir             string
	agentInstallerPath      string
	agentAssetsDir          string
	updateRequestFile       string
	updateStatusFile        string
	agentUpgradeRequestFile string
	agentVersion            string
	agentReleaseSource      string
	agentReleaseRepo        string
	agentReleaseChannel     string
	agentReleaseCacheDir    string
	agentReleaseMu          sync.Mutex
	agentReleaseCheckedAt   time.Time
	agentReleaseVersion     string
	agentReleaseErr         error
	cloud                   *CloudService
	dispatchToken           string
	loginMu                 sync.Mutex
	loginFailures           map[string]loginFailureState
	router                  chi.Router
}

const (
	consoleRoleAdmin = "admin"
	consoleRoleUser  = "user"
)

type consolePrincipal struct {
	Role     string
	Username string
	User     *ConsoleUser
}

type consolePrincipalContextKey struct{}

func withConsolePrincipal(ctx context.Context, principal consolePrincipal) context.Context {
	return context.WithValue(ctx, consolePrincipalContextKey{}, principal)
}

func consolePrincipalFromContext(ctx context.Context) (consolePrincipal, bool) {
	principal, ok := ctx.Value(consolePrincipalContextKey{}).(consolePrincipal)
	return principal, ok
}

func NewServer(store *Store, opts ServerOptions) (*Server, error) {
	if store == nil {
		return nil, errors.New("store is required")
	}
	adminToken := strings.TrimSpace(opts.AdminToken)
	if adminToken == "" {
		return nil, errors.New("admin token is required")
	}
	dispatchToken := strings.TrimSpace(opts.DispatchToken)
	if dispatchToken != "" && subtle.ConstantTimeCompare([]byte(dispatchToken), []byte(adminToken)) == 1 {
		return nil, errors.New("dispatch token must be different from admin token")
	}
	agentVersion := strings.TrimSpace(opts.AgentVersion)
	if agentVersion == "" {
		agentVersion = "dev"
	}
	releaseSource := strings.ToLower(strings.TrimSpace(opts.AgentReleaseSource))
	if releaseSource == "" {
		releaseSource = "embedded"
	}
	releaseRepo := strings.TrimSpace(opts.AgentReleaseRepo)
	if releaseRepo == "" {
		releaseRepo = "R1ddle1337/AliCDT-Manager-Professional"
	}
	releaseChannel := strings.TrimSpace(opts.AgentReleaseChannel)
	if releaseChannel == "" {
		releaseChannel = "latest"
	}
	releaseCacheDir := strings.TrimSpace(opts.AgentReleaseCacheDir)
	if releaseCacheDir == "" {
		releaseCacheDir = "/app/data/agent-releases"
	}
	cloud := NewCloudService(store)
	if opts.TrafficSafetyWindowSet {
		// A zero value is meaningful when explicitly supplied, allowing
		// operators to disable forecasting without changing the default.
		cloud.SetTrafficSafetyWindow(opts.TrafficSafetyWindow)
	} else if opts.TrafficSafetyWindow > 0 {
		cloud.SetTrafficSafetyWindow(opts.TrafficSafetyWindow)
	}
	server := &Server{store: store, adminToken: adminToken, dispatchToken: dispatchToken, frontendDir: opts.FrontendDir, agentInstallerPath: opts.AgentInstallerPath, updateRequestFile: opts.UpdateRequestFile, updateStatusFile: opts.UpdateStatusFile, agentUpgradeRequestFile: opts.AgentUpgradeRequestFile, agentVersion: agentVersion, agentReleaseSource: releaseSource, agentReleaseRepo: releaseRepo, agentReleaseChannel: releaseChannel, agentReleaseCacheDir: releaseCacheDir, cloud: cloud, loginFailures: make(map[string]loginFailureState)}
	if releaseSource == "github" {
		if cachedVersion, err := os.ReadFile(filepath.Join(releaseCacheDir, "version")); err == nil {
			server.agentReleaseVersion = strings.TrimSpace(string(cachedVersion))
		}
	}
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
	router.Use(securityHeaders)
	router.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]interface{}{"status": "ok", "service": "alicdt-controller"})
	})
	if s.agentInstallerPath != "" {
		router.Get("/agent/install.sh", s.serveAgentInstaller)
		router.Get("/agent/upgrade.sh", s.serveAgentUpgrade)
		router.Get("/agent/cdt-sing-box-log-cleanup.sh", s.serveSingBoxLogCleanup)
		router.Get("/dispatcher/install.sh", s.serveDispatcherInstaller)
		router.Get("/dispatcher/{asset}", s.serveDispatcherAsset)
		router.Get("/agent/{asset}", s.serveAgentAsset)
	}
	router.Get("/api/v2/auth/initialized", s.adminInitialized)
	router.Post("/api/v2/auth/init", s.initAdmin)
	router.Post("/api/v2/auth/login", s.loginAdmin)
	router.Get("/api/auth/initialized", s.adminInitialized)
	router.Post("/api/auth/init", s.initAdmin)
	router.Post("/api/auth/login", s.loginAdmin)
	router.Group(func(router chi.Router) {
		router.Use(s.consoleAuth)
		router.Get("/api/v2/auth/me", s.currentIdentity)
		router.Post("/api/v2/auth/logout", s.logoutConsole)
		router.Get("/api/v2/user/overview", s.currentUserOverview)
		router.Get("/api/v2/user/usage-ledger", s.currentUserUsageLedger)
	})

	router.Route("/api/v2/agents", func(router chi.Router) {
		router.Post("/enroll", s.enrollAgent)
		router.Group(func(router chi.Router) {
			router.Use(s.agentAuth)
			router.Get("/{agentID}/config", s.agentConfig)
			router.Get("/{agentID}/release", s.agentRelease)
			router.Post("/{agentID}/update/state", s.agentUpdateState)
			router.Post("/{agentID}/heartbeat", s.agentHeartbeat)
		})
	})
	router.Get("/api/v2/dispatch/pools/{poolID}", s.dispatchPoolSnapshot)

	router.Group(func(router chi.Router) {
		router.Use(s.adminAuth)
		router.Get("/api/v2/security/sessions", s.listAdminSessions)
		router.Delete("/api/v2/security/sessions/{sessionID}", s.revokeAdminSession)
		router.Post("/api/v2/security/sessions/revoke-others", s.revokeOtherAdminSessions)
		router.Post("/api/v2/security/admin-password", s.changeAdminPassword)
		router.Get("/api/v2/security/2fa", s.adminTwoFAStatus)
		router.Post("/api/v2/security/2fa/setup", s.beginAdminTwoFA)
		router.Post("/api/v2/security/2fa/confirm", s.confirmAdminTwoFA)
		router.Delete("/api/v2/security/2fa", s.disableAdminTwoFA)
		router.Get("/api/v2/users", s.listConsoleUsers)
		router.Post("/api/v2/users", s.createConsoleUser)
		router.Put("/api/v2/users/{userID}", s.updateConsoleUser)
		router.Delete("/api/v2/users/{userID}", s.deleteConsoleUser)
		router.Get("/api/v2/users/{userID}/usage-ledger", s.userUsageLedger)
		router.Post("/api/v2/users/{userID}/quota/adjust", s.adjustUserQuota)
		router.Get("/api/v2/entry-groups", s.listEntryGroups)
		router.Post("/api/v2/entry-groups", s.createEntryGroup)
		router.Put("/api/v2/entry-groups/{groupID}", s.updateEntryGroup)
		router.Delete("/api/v2/entry-groups/{groupID}", s.deleteEntryGroup)
		router.Post("/api/v2/enrollment-tokens", s.createEnrollmentToken)
		router.Get("/api/v2/relay-nodes", s.listRelayNodes)
		router.Post("/api/v2/relay-nodes/upgrade-all", s.requestAllAgentUpgrades)
		router.Post("/api/v2/relay-nodes/{agentID}/upgrade", s.requestAgentUpgrade)
		router.Get("/api/v2/landing-nodes", s.listLandingNodes)
		router.Get("/api/v2/landing-nodes/{landingID}/relay-links", s.landingRelayLinks)
		router.Post("/api/v2/landing-nodes", s.createLandingNode)
		router.Put("/api/v2/landing-nodes/{landingID}", s.updateLandingNode)
		router.Delete("/api/v2/landing-nodes/{landingID}", s.deleteLandingNode)
		router.Get("/api/v2/relay-services", s.listRelayServices)
		router.Post("/api/v2/relay-services", s.createRelayService)
		router.Put("/api/v2/relay-services/{serviceID}", s.updateRelayService)
		router.Post("/api/v2/relay-services/{serviceID}/traffic/reset", s.resetRelayServiceTraffic)
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

func (s *Server) serveAgentUpgrade(w http.ResponseWriter, r *http.Request) {
	if s.agentAssetsDir == "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, filepath.Join(s.agentAssetsDir, "upgrade-agent.sh"))
}

func (s *Server) serveSingBoxLogCleanup(w http.ResponseWriter, r *http.Request) {
	if s.agentAssetsDir == "" {
		http.NotFound(w, r)
		return
	}
	path := filepath.Join(s.agentAssetsDir, "cdt-sing-box-log-cleanup.sh")
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, path)
}

func (s *Server) serveDispatcherInstaller(w http.ResponseWriter, r *http.Request) {
	if s.agentAssetsDir == "" {
		http.NotFound(w, r)
		return
	}
	path := filepath.Join(s.agentAssetsDir, "install-dispatcher.sh")
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, path)
}

func (s *Server) serveDispatcherAsset(w http.ResponseWriter, r *http.Request) {
	asset := chi.URLParam(r, "asset")
	if asset != "checksums.txt" && !validDispatcherAsset(asset) {
		http.NotFound(w, r)
		return
	}
	if s.agentAssetsDir == "" {
		http.NotFound(w, r)
		return
	}
	path := filepath.Join(s.agentAssetsDir, asset)
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	contentType := "application/octet-stream"
	if asset == "checksums.txt" {
		contentType = "text/plain; charset=utf-8"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=300")
	http.ServeFile(w, r, path)
}

func (s *Server) serveAgentAsset(w http.ResponseWriter, r *http.Request) {
	asset := chi.URLParam(r, "asset")
	allowed := map[string]string{
		"cdt-relay-agent-linux-amd64": "application/octet-stream",
		"cdt-relay-agent-linux-arm64": "application/octet-stream",
		"cdt-dispatcher-linux-amd64":  "application/octet-stream",
		"cdt-dispatcher-linux-arm64":  "application/octet-stream",
		"checksums.txt":               "text/plain; charset=utf-8",
	}
	contentType, ok := allowed[asset]
	if !ok {
		http.NotFound(w, r)
		return
	}
	if asset != "checksums.txt" {
		_ = s.refreshAgentRelease(r.Context())
	}
	path := ""
	if asset == "checksums.txt" {
		if s.agentReleaseSource == "embedded" {
			path = filepath.Join(s.agentAssetsDir, asset)
		} else {
			if s.agentReleaseCacheDir != "" {
				candidate := filepath.Join(s.agentReleaseCacheDir, asset)
				if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
					path = candidate
				}
			}
			if path == "" && s.agentAssetsDir != "" {
				path = filepath.Join(s.agentAssetsDir, asset)
			}
		}
	} else {
		path, _ = s.agentAssetPath(asset)
	}
	if path == "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=300")
	http.ServeFile(w, r, path)
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
		principal, err := s.authenticateConsoleRequest(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, errors.New("invalid admin token"))
			return
		}
		if principal.Role != consoleRoleAdmin {
			writeError(w, http.StatusForbidden, errors.New("administrator access is required"))
			return
		}
		next.ServeHTTP(w, r.WithContext(withConsolePrincipal(r.Context(), principal)))
	})
}

func (s *Server) consoleAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, err := s.authenticateConsoleRequest(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, errors.New("invalid console token"))
			return
		}
		next.ServeHTTP(w, r.WithContext(withConsolePrincipal(r.Context(), principal)))
	})
}

func (s *Server) authenticateConsoleRequest(r *http.Request) (consolePrincipal, error) {
	token := bearerToken(r)
	if subtle.ConstantTimeCompare([]byte(token), []byte(s.adminToken)) == 1 {
		username, _ := s.store.GetSetting(r.Context(), "admin_username")
		return consolePrincipal{Role: consoleRoleAdmin, Username: username}, nil
	}
	if username, err := s.store.AdminSessionUsername(r.Context(), token); err == nil {
		return consolePrincipal{Role: consoleRoleAdmin, Username: username}, nil
	}
	user, err := s.store.AuthenticateUserSession(r.Context(), token)
	if err != nil {
		return consolePrincipal{}, err
	}
	return consolePrincipal{Role: consoleRoleUser, Username: user.Username, User: &user}, nil
}

func (s *Server) currentIdentity(w http.ResponseWriter, r *http.Request) {
	principal, ok := consolePrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, errors.New("invalid console token"))
		return
	}
	payload := map[string]interface{}{"role": principal.Role, "username": principal.Username}
	if principal.User != nil {
		payload["display_name"] = principal.User.DisplayName
		payload["user"] = principal.User
	}
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) currentUserOverview(w http.ResponseWriter, r *http.Request) {
	principal, ok := consolePrincipalFromContext(r.Context())
	if !ok || principal.Role != consoleRoleUser || principal.User == nil {
		writeError(w, http.StatusForbidden, errors.New("user access is required"))
		return
	}
	writeJSON(w, http.StatusOK, principal.User)
}

func (s *Server) currentUserUsageLedger(w http.ResponseWriter, r *http.Request) {
	principal, ok := consolePrincipalFromContext(r.Context())
	if !ok || principal.Role != consoleRoleUser || principal.User == nil {
		writeError(w, http.StatusForbidden, errors.New("user access is required"))
		return
	}
	entries, err := s.store.ListUsageLedger(r.Context(), principal.User.ID, 100)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

func (s *Server) logoutConsole(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteConsoleSession(r.Context(), bearerToken(r)); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listConsoleUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.ListConsoleUsers(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, users)
}

func (s *Server) createConsoleUser(w http.ResponseWriter, r *http.Request) {
	var request ConsoleUserRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	user, err := s.store.CreateConsoleUser(r.Context(), request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	_ = s.store.AddSystemLog(r.Context(), "info", "user_management", fmt.Sprintf("创建控制台用户：%s", user.Username))
	writeJSON(w, http.StatusCreated, user)
}

func (s *Server) updateConsoleUser(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.ParseInt(chi.URLParam(r, "userID"), 10, 64)
	if err != nil || userID <= 0 {
		writeError(w, http.StatusBadRequest, errors.New("invalid user id"))
		return
	}
	var request ConsoleUserRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	user, err := s.store.UpdateConsoleUser(r.Context(), userID, request)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeStoreError(w, err)
		} else {
			writeError(w, http.StatusBadRequest, err)
		}
		return
	}
	_ = s.store.AddSystemLog(r.Context(), "info", "user_management", fmt.Sprintf("更新控制台用户：%s", user.Username))
	writeJSON(w, http.StatusOK, user)
}

func (s *Server) deleteConsoleUser(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.ParseInt(chi.URLParam(r, "userID"), 10, 64)
	if err != nil || userID <= 0 {
		writeError(w, http.StatusBadRequest, errors.New("invalid user id"))
		return
	}
	if err := s.store.DeleteConsoleUser(r.Context(), userID); err != nil {
		writeStoreError(w, err)
		return
	}
	_ = s.store.AddSystemLog(r.Context(), "info", "user_management", fmt.Sprintf("删除控制台用户 ID：%d", userID))
	w.WriteHeader(http.StatusNoContent)
}

func parseUserIDParam(r *http.Request) (int64, error) {
	userID, err := strconv.ParseInt(chi.URLParam(r, "userID"), 10, 64)
	if err != nil || userID <= 0 {
		return 0, errors.New("invalid user id")
	}
	return userID, nil
}

func (s *Server) userUsageLedger(w http.ResponseWriter, r *http.Request) {
	userID, err := parseUserIDParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	entries, err := s.store.ListUsageLedger(r.Context(), userID, 200)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

func (s *Server) adjustUserQuota(w http.ResponseWriter, r *http.Request) {
	userID, err := parseUserIDParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var request UsageAdjustmentRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	user, err := s.store.AdjustUserTrafficLimit(r.Context(), userID, request)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeStoreError(w, err)
		} else {
			writeError(w, http.StatusBadRequest, err)
		}
		return
	}
	_ = s.store.AddSystemLog(r.Context(), "info", "traffic_quota", fmt.Sprintf("调整用户 %s 额度：%+.3f GB", user.Username, request.DeltaGB))
	writeJSON(w, http.StatusOK, user)
}

func (s *Server) listEntryGroups(w http.ResponseWriter, r *http.Request) {
	userID := int64(0)
	if raw := r.URL.Query().Get("user_id"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, errors.New("invalid user id"))
			return
		}
		userID = parsed
	}
	groups, err := s.store.ListUserEntryGroups(r.Context(), userID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, groups)
}

func (s *Server) createEntryGroup(w http.ResponseWriter, r *http.Request) {
	var request CreateUserEntryGroupRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	group, err := s.store.CreateUserEntryGroup(r.Context(), request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	_ = s.store.AddSystemLog(r.Context(), "info", "entry_group", fmt.Sprintf("创建用户入口组：%s（%d 个端口）", group.Name, group.PortCount))
	writeJSON(w, http.StatusCreated, group)
}

func (s *Server) updateEntryGroup(w http.ResponseWriter, r *http.Request) {
	var request CreateUserEntryGroupRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	group, err := s.store.UpdateUserEntryGroup(r.Context(), chi.URLParam(r, "groupID"), request)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeStoreError(w, err)
		} else {
			writeError(w, http.StatusBadRequest, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, group)
}

func (s *Server) deleteEntryGroup(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteUserEntryGroup(r.Context(), chi.URLParam(r, "groupID")); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
	_ = s.store.RecordAdminSessionMetadata(r.Context(), token, clientAddress(r), r.UserAgent())
	writeJSON(w, http.StatusCreated, map[string]string{"token": token, "username": request.Username, "display_name": request.Username, "role": consoleRoleAdmin})
}

func (s *Server) loginAdmin(w http.ResponseWriter, r *http.Request) {
	address := clientAddress(r)
	if allowed, retryAfter := s.loginAllowed(address); !allowed {
		seconds := int(retryAfter.Seconds())
		if seconds < 1 {
			seconds = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
		writeError(w, http.StatusTooManyRequests, errors.New("too many failed login attempts; try again later"))
		return
	}
	var request struct {
		Username      string `json:"username"`
		Password      string `json:"password"`
		TwoFactorCode string `json:"two_factor_code"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if token, err := s.store.LoginAdminWithCode(r.Context(), request.Username, request.Password, request.TwoFactorCode); err == nil {
		s.recordLoginSuccess(address)
		_ = s.store.RecordAdminSessionMetadata(r.Context(), token, address, r.UserAgent())
		writeJSON(w, http.StatusOK, map[string]interface{}{"token": token, "username": request.Username, "display_name": request.Username, "role": consoleRoleAdmin})
		return
	} else if errors.Is(err, ErrAdminTwoFactorRequired) {
		s.recordLoginFailure(address)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "请输入有效的双因素认证验证码", "code": "two_factor_required"})
		return
	}
	token, user, err := s.store.LoginConsoleUser(r.Context(), request.Username, request.Password)
	if err != nil {
		s.recordLoginFailure(address)
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	s.recordLoginSuccess(address)
	writeJSON(w, http.StatusOK, map[string]interface{}{"token": token, "username": user.Username, "display_name": user.DisplayName, "role": consoleRoleUser})
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

func (s *Server) requestAgentUpgrade(w http.ResponseWriter, r *http.Request) {
	node, err := s.store.RequestAgentUpgrade(r.Context(), chi.URLParam(r, "agentID"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if err := s.signalAgentUpgrade(r.Context()); err != nil && agentCapabilitiesMissing(node) {
		_ = s.store.MarkAgentUpgradeFailed(r.Context(), node.ID, err.Error())
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	writeJSON(w, http.StatusAccepted, node)
}

func (s *Server) requestAllAgentUpgrades(w http.ResponseWriter, r *http.Request) {
	nodes, err := s.store.RequestLegacyAgentUpgrades(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if len(nodes) == 0 {
		writeJSON(w, http.StatusOK, map[string]interface{}{"requested": 0, "nodes": nodes, "message": "所有 Agent 已具备最新能力"})
		return
	}
	if err := s.signalAgentUpgrade(r.Context()); err != nil {
		for _, node := range nodes {
			_ = s.store.MarkAgentUpgradeFailed(r.Context(), node.ID, err.Error())
		}
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]interface{}{"requested": len(nodes), "nodes": nodes, "message": "已提交宿主机兼容升级任务"})
}

func agentCapabilitiesMissing(node RelayNode) bool {
	hasMeters, hasLeases := false, false
	for _, capability := range node.Capabilities {
		switch capability {
		case "shared_meters_v1":
			hasMeters = true
		case "quota_leases_v1":
			hasLeases = true
		}
	}
	return !hasMeters || !hasLeases
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

func (s *Server) agentUpdateState(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.store.SetAgentUpdateState(r.Context(), chi.URLParam(r, "agentID"), request.Status, request.Error); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) createEnrollmentToken(w http.ResponseWriter, r *http.Request) {
	var request struct {
		TTLMinutes int    `json:"ttl_minutes"`
		AccountID  *int64 `json:"account_id,omitempty"`
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
	var accountIDs []int64
	if request.AccountID != nil && *request.AccountID > 0 {
		accountIDs = []int64{*request.AccountID}
	}
	if err := s.store.CreateEnrollmentToken(r.Context(), raw, time.Duration(request.TTLMinutes)*time.Minute, accountIDs...); err != nil {
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
		if service.PoolID == "" && service.EntryGroupID == "" {
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

func (s *Server) resetRelayServiceTraffic(w http.ResponseWriter, r *http.Request) {
	service, err := s.store.ResetRelayServiceTraffic(r.Context(), chi.URLParam(r, "serviceID"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeStoreError(w, err)
		} else {
			writeError(w, http.StatusBadRequest, err)
		}
		return
	}
	_ = s.store.AddSystemLog(r.Context(), "info", "traffic_metering", fmt.Sprintf("重置转发服务流量：%s", service.Name))
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
	// A mode change can release or trigger a relay drain. Recompute managed
	// record desired state before returning so the next DNS provider sync sees
	// the new eligibility immediately.
	refreshCtx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	_ = s.store.RefreshRelayAgentDNSRecords(refreshCtx)
	_ = s.store.RefreshAllRelayPoolDNS(refreshCtx)
	cancel()
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
