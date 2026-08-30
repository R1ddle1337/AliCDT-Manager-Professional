package controller

import (
	"github.com/go-chi/chi/v5"
	"net/http"
)

func (s *Server) listDNSProviders(w http.ResponseWriter, r *http.Request) {
	providers, err := s.store.ListDNSProviders(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, providers)
}

func (s *Server) createDNSProvider(w http.ResponseWriter, r *http.Request) {
	var request CreateDNSProviderRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	provider, err := s.store.CreateDNSProvider(r.Context(), request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, provider)
}

func (s *Server) updateDNSProvider(w http.ResponseWriter, r *http.Request) {
	var request CreateDNSProviderRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	provider, err := s.store.UpdateDNSProvider(r.Context(), chi.URLParam(r, "providerID"), request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, provider)
}

func (s *Server) deleteDNSProvider(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteDNSProvider(r.Context(), chi.URLParam(r, "providerID")); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) testDNSProvider(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "providerID")
	err := s.store.TestDNSProvider(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "provider_id": id, "message": "DNS provider connection succeeded"})
}

func (s *Server) syncDNSProvider(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "providerID")
	result, err := s.store.SyncDNSProvider(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "provider_id": id, "result": result})
}

func (s *Server) listDNSRecords(w http.ResponseWriter, r *http.Request) {
	records, err := s.store.ListDNSRecords(r.Context(), r.URL.Query().Get("provider_id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, records)
}

func (s *Server) createDNSRecord(w http.ResponseWriter, r *http.Request) {
	var request CreateDNSRecordRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	record, err := s.store.CreateDNSRecord(r.Context(), request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, record)
}

func (s *Server) updateDNSRecord(w http.ResponseWriter, r *http.Request) {
	var request CreateDNSRecordRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	existing, err := s.store.GetDNSRecord(r.Context(), chi.URLParam(r, "recordID"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if existing.ProviderRecordID != "" && request.ProviderID != "" && request.ProviderID != existing.ProviderID {
		provider, providerErr := s.store.DNSProvider(r.Context(), existing.ProviderID)
		if providerErr != nil {
			writeStoreError(w, providerErr)
			return
		}
		if providerErr = provider.DeleteRecord(r.Context(), existing.ProviderRecordID, existing.Name); providerErr != nil {
			writeError(w, http.StatusBadGateway, providerErr)
			return
		}
	}
	record, err := s.store.UpdateDNSRecord(r.Context(), chi.URLParam(r, "recordID"), request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, record)
}

func (s *Server) deleteDNSRecord(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "recordID")
	record, err := s.store.GetDNSRecord(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if record.ProviderRecordID != "" {
		provider, providerErr := s.store.DNSProvider(r.Context(), record.ProviderID)
		if providerErr != nil {
			writeStoreError(w, providerErr)
			return
		}
		if providerErr = provider.DeleteRecord(r.Context(), record.ProviderRecordID, record.Name); providerErr != nil {
			writeError(w, http.StatusBadGateway, providerErr)
			return
		}
	}
	if err := s.store.DeleteDNSRecord(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) syncAllDNS(w http.ResponseWriter, r *http.Request) {
	result, err := s.store.SyncAllDNS(r.Context())
	if err != nil && len(result) == 0 {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	response := map[string]interface{}{"ok": err == nil, "results": result}
	if err != nil {
		response["error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, response)
}
