package dnsprovider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCloudflareEnsureRecordsUsesManagedRecordID(t *testing.T) {
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/zones":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "result": []map[string]string{{"id": "zone-1", "name": "example.com"}}})
		case r.URL.Path == "/zones/zone-1/dns_records" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "result": []map[string]interface{}{{"id": "record-1", "name": "relay.example.com", "type": "A", "content": "192.0.2.1", "ttl": 60}}})
		case r.URL.Path == "/zones/zone-1/dns_records/record-1" && r.Method == http.MethodPut:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "result": map[string]interface{}{"id": "record-1", "name": "relay.example.com", "type": "A", "content": "192.0.2.2", "ttl": 120}})
		default:
			http.Error(w, `{"success":false,"errors":[{"message":"unexpected request"}]}`, http.StatusNotFound)
		}
	}))
	defer server.Close()

	provider := NewCloudflare(Config{Zone: "example.com", Endpoint: server.URL, APIToken: "test-token", HTTPClient: server.Client()})
	result, err := provider.EnsureRecords(context.Background(), "example.com", []RecordScope{{Name: "relay", Type: "A"}}, []DesiredRecord{{Name: "relay", Type: "A", Value: "192.0.2.2", TTL: 120, ProviderRecordID: "record-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Updated != 1 || len(result.Records) != 1 || result.Records[0].ID != "record-1" {
		t.Fatalf("unexpected sync result: %+v", result)
	}
	joined := strings.Join(methods, "\n")
	if !strings.Contains(joined, "PUT /zones/zone-1/dns_records/record-1") {
		t.Fatalf("managed record ID was not updated: %s", joined)
	}
}

func TestCloudflareEnsureRecordsSupportsAutomaticTTL(t *testing.T) {
	var payload map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/zones":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "result": []map[string]string{{"id": "zone-1", "name": "example.com"}}})
		case r.URL.Path == "/zones/zone-1/dns_records" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "result": []map[string]interface{}{}})
		case r.URL.Path == "/zones/zone-1/dns_records" && r.Method == http.MethodPost:
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode create payload: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "result": map[string]interface{}{"id": "record-auto", "name": "relay.example.com", "type": "A", "content": "192.0.2.1", "ttl": 1}})
		default:
			http.Error(w, `{"success":false,"errors":[{"message":"unexpected request"}]}`, http.StatusNotFound)
		}
	}))
	defer server.Close()

	provider := NewCloudflare(Config{Zone: "example.com", Endpoint: server.URL, APIToken: "test-token", HTTPClient: server.Client()})
	result, err := provider.EnsureRecords(context.Background(), "example.com", []RecordScope{{Name: "relay", Type: "A"}}, []DesiredRecord{{Name: "relay", Type: "A", Value: "192.0.2.1", TTL: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Created != 1 || len(result.Records) != 1 || result.Records[0].TTL != 1 {
		t.Fatalf("unexpected automatic-TTL sync result: %+v", result)
	}
	if got, ok := payload["ttl"].(float64); !ok || got != 1 {
		t.Fatalf("Cloudflare automatic TTL was not sent as ttl=1: %#v", payload["ttl"])
	}
}

func TestCloudflareDeleteRecordTreatsMissingRecordAsSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/zones":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "result": []map[string]string{{"id": "zone-1", "name": "example.com"}}})
		case r.URL.Path == "/zones/zone-1/dns_records/missing" && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":81044,"message":"Record does not exist."}]}`))
		default:
			http.Error(w, `{"success":false,"errors":[{"message":"unexpected request"}]}`, http.StatusNotFound)
		}
	}))
	defer server.Close()

	provider := NewCloudflare(Config{Zone: "example.com", Endpoint: server.URL, APIToken: "test-token", HTTPClient: server.Client()})
	if err := provider.DeleteRecord(context.Background(), "missing", "relay"); err != nil {
		t.Fatalf("missing record should be treated as already deleted: %v", err)
	}
}

func TestFQDNAndRelativeName(t *testing.T) {
	if got := fqdn("relay", "example.com"); got != "relay.example.com" {
		t.Fatalf("unexpected fqdn: %s", got)
	}
	if got := fqdn("@", "example.com"); got != "example.com" {
		t.Fatalf("unexpected root fqdn: %s", got)
	}
	if got := relativeName("relay.example.com", "example.com"); got != "relay" {
		t.Fatalf("unexpected relative name: %s", got)
	}
}
