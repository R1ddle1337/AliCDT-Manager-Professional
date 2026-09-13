package controller

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (s *Server) listRelayPools(w http.ResponseWriter, r *http.Request) {
	pools, err := s.store.ListRelayPools(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pools)
}

func (s *Server) createRelayPool(w http.ResponseWriter, r *http.Request) {
	var request CreateRelayPoolRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	pool, err := s.store.CreateRelayPool(r.Context(), request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, pool)
}

func (s *Server) updateRelayPool(w http.ResponseWriter, r *http.Request) {
	var request CreateRelayPoolRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	pool, err := s.store.UpdateRelayPool(r.Context(), chi.URLParam(r, "poolID"), request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, pool)
}

func (s *Server) deleteRelayPool(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteRelayPool(r.Context(), chi.URLParam(r, "poolID")); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) relayPoolLinks(w http.ResponseWriter, r *http.Request) {
	links, err := s.store.RelayPoolLinks(r.Context(), chi.URLParam(r, "poolID"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, links)
}
