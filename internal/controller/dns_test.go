package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDNSProviderAndManagedRecordLifecycle(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	provider, err := store.CreateDNSProvider(context.Background(), CreateDNSProviderRequest{Name: "cf", Type: "cloudflare", Zone: "example.com", APIToken: "token"})
	if err != nil {
		t.Fatal(err)
	}
	if provider.ID == "" || !provider.TokenConfigured {
		t.Fatalf("provider was not created: %+v", provider)
	}
	provider, err = store.UpdateDNSProvider(context.Background(), provider.ID, CreateDNSProviderRequest{Name: "cf-renamed", Type: "cloudflare", Zone: "example.com"})
	if err != nil || provider.Name != "cf-renamed" || !provider.TokenConfigured {
		t.Fatalf("provider secret was not retained: %+v err=%v", provider, err)
	}
	record, err := store.CreateDNSRecord(context.Background(), CreateDNSRecordRequest{ProviderID: provider.ID, Name: "relay", Type: "A", Value: "192.0.2.1", TTL: 60})
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != "pending" {
		t.Fatalf("unexpected initial record state: %+v", record)
	}
	if err := store.DeleteDNSProvider(context.Background(), provider.ID); err == nil {
		t.Fatal("provider with managed records was deleted")
	}
	updated, err := store.UpdateDNSRecord(context.Background(), record.ID, CreateDNSRecordRequest{ProviderID: provider.ID, Name: "relay", Type: "A", Value: "192.0.2.2", TTL: 120})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Value != "192.0.2.2" || updated.TTL != 120 {
		t.Fatalf("record was not updated: %+v", updated)
	}
	if err := store.DeleteDNSRecord(context.Background(), record.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteDNSProvider(context.Background(), provider.ID); err != nil {
		t.Fatal(err)
	}
}

func TestDNSProviderAPIValidatesCredentialsBeforePersisting(t *testing.T) {
	fakeDNS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer good-token" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"success":false,"errors":[{"message":"invalid token"}]}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "result": []interface{}{map[string]string{"id": "zone-1", "name": "example.com"}}})
	}))
	defer fakeDNS.Close()
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server, err := NewServer(store, ServerOptions{AdminToken: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	request := CreateDNSProviderRequest{Name: "cf", Type: "cloudflare", Zone: "example.com", Endpoint: fakeDNS.URL, APIToken: "bad-token"}
	requestJSON(t, httpServer.URL+"/api/v2/dns/providers", "admin", request, http.StatusBadRequest, nil)
	providers, err := store.ListDNSProviders(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(providers) != 0 {
		t.Fatalf("invalid provider was persisted: %+v", providers)
	}
	request.APIToken = "good-token"
	var created DNSProvider
	requestJSON(t, httpServer.URL+"/api/v2/dns/providers", "admin", request, http.StatusCreated, &created)
	if created.ID == "" || !created.TokenConfigured {
		t.Fatalf("valid provider was not persisted: %+v", created)
	}
}
