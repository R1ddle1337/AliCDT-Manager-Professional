package dnsprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
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

func TestCloudflareEnsureRecordsRecreatesStaleManagedRecordID(t *testing.T) {
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/zones":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "result": []map[string]string{{"id": "zone-1", "name": "example.com"}}})
		case r.URL.Path == "/zones/zone-1/dns_records" && r.Method == http.MethodGet:
			// The desired record was removed out-of-band, so it is absent from
			// the provider listing and must be recreated.
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "result": []map[string]interface{}{}})
		case r.URL.Path == "/zones/zone-1/dns_records/stale" && r.Method == http.MethodPut:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":81044,"message":"Record does not exist."}]}`))
		case r.URL.Path == "/zones/zone-1/dns_records" && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "result": map[string]interface{}{"id": "record-recreated", "name": "relay.example.com", "type": "A", "content": "192.0.2.9", "ttl": 60}})
		default:
			http.Error(w, `{"success":false,"errors":[{"message":"unexpected request"}]}`, http.StatusNotFound)
		}
	}))
	defer server.Close()

	provider := NewCloudflare(Config{Zone: "example.com", Endpoint: server.URL, APIToken: "test-token", HTTPClient: server.Client()})
	result, err := provider.EnsureRecords(context.Background(), "example.com", []RecordScope{{Name: "relay", Type: "A"}}, []DesiredRecord{{Name: "relay", Type: "A", Value: "192.0.2.9", TTL: 60, ProviderRecordID: "stale"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Created != 1 || result.Updated != 0 || len(result.Records) != 1 || result.Records[0].ID != "record-recreated" {
		t.Fatalf("unexpected stale-record recovery result: %+v", result)
	}
	joined := strings.Join(methods, "\n")
	if !strings.Contains(joined, "PUT /zones/zone-1/dns_records/stale") || !strings.Contains(joined, "POST /zones/zone-1/dns_records") {
		t.Fatalf("stale record was not recovered: %s", joined)
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

func TestCloudflareListRecordsPaginatesCompleteZone(t *testing.T) {
	var pages []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages = append(pages, r.URL.Query().Get("page"))
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		count := 100
		if page == 2 {
			count = 1
		}
		records := make([]map[string]interface{}, 0, count)
		for index := 0; index < count; index++ {
			number := (page-1)*100 + index
			records = append(records, map[string]interface{}{"id": fmt.Sprint(number), "name": fmt.Sprintf("relay-%d.example.com", number), "type": "A", "content": "192.0.2.1", "ttl": 60})
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "result": records, "result_info": map[string]int{"total_pages": 2}})
	}))
	defer server.Close()

	provider := NewCloudflare(Config{Zone: "example.com", ZoneID: "zone-1", Endpoint: server.URL, APIToken: "test-token", HTTPClient: server.Client()})
	records, err := provider.ListRecords(context.Background(), "example.com", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 101 || strings.Join(pages, ",") != "1,2" {
		t.Fatalf("zone pagination was incomplete: records=%d pages=%v", len(records), pages)
	}
}

func TestCloudflareRejectsCrossOriginRedirect(t *testing.T) {
	targetReached := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetReached = true
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "result": []interface{}{}})
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusFound)
	}))
	defer source.Close()

	provider := NewCloudflare(Config{Zone: "example.com", ZoneID: "zone-1", Endpoint: source.URL, APIToken: "secret", HTTPClient: source.Client()})
	if _, err := provider.ListRecords(context.Background(), "example.com", "", ""); err == nil || !strings.Contains(err.Error(), "redirect changed origin") {
		t.Fatalf("expected redirect rejection, got %v", err)
	}
	if targetReached {
		t.Fatal("cross-origin DNS endpoint was contacted")
	}
}

func TestCloudflareRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", maxProviderResponseBytes+1)))
	}))
	defer server.Close()
	provider := NewCloudflare(Config{Zone: "example.com", ZoneID: "zone-1", Endpoint: server.URL, APIToken: "test-token", HTTPClient: server.Client()})
	if _, err := provider.ListRecords(context.Background(), "example.com", "", ""); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("expected oversized response rejection, got %v", err)
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
