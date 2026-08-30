package controller

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/protocol"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type ServerOptions struct {
	AdminToken string
}

type Server struct {
	store      *Store
	adminToken string
	router     chi.Router
}

func NewServer(store *Store, opts ServerOptions) (*Server, error) {
	if store == nil {
		return nil, errors.New("store is required")
	}
	if strings.TrimSpace(opts.AdminToken) == "" {
		return nil, errors.New("admin token is required")
	}
	server := &Server{store: store, adminToken: opts.AdminToken}
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
		router.Post("/api/v2/landing-nodes", s.createLandingNode)
		router.Get("/api/v2/relay-services", s.listRelayServices)
		router.Post("/api/v2/relay-services", s.createRelayService)
		router.Delete("/api/v2/relay-services/{serviceID}", s.deleteRelayService)
	})
	return router
}

func (s *Server) adminAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if subtle.ConstantTimeCompare([]byte(token), []byte(s.adminToken)) != 1 {
			writeError(w, http.StatusUnauthorized, errors.New("invalid admin token"))
			return
		}
		next.ServeHTTP(w, r)
	})
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

func (s *Server) listLandingNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := s.store.ListLandingNodes(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, nodes)
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
	writeJSON(w, http.StatusOK, services)
}

func (s *Server) deleteRelayService(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteRelayService(r.Context(), chi.URLParam(r, "serviceID")); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
