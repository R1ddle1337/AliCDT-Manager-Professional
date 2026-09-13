package controller

import (
	"crypto/subtle"
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

func (s *Server) dispatchPoolSnapshot(w http.ResponseWriter, r *http.Request) {
	// A dispatcher receives a dedicated read-only token. Never fall back to
	// the admin token: gateway hosts should not gain control-plane write access.
	if !s.dispatchAuthorized(r) {
		// Hide the route when no dispatch token has been configured, and avoid
		// revealing pool existence to unauthenticated callers.
		http.NotFound(w, r)
		return
	}
	snapshot, err := s.store.DispatcherPoolSnapshot(r.Context(), chi.URLParam(r, "poolID"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		writeStoreError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *Server) dispatchAuthorized(r *http.Request) bool {
	expected := strings.TrimSpace(s.dispatchToken)
	provided := strings.TrimSpace(bearerToken(r))
	if expected == "" || provided == "" || len(expected) != len(provided) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) == 1
}
