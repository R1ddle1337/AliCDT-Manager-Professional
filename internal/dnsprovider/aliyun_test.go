package dnsprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAliyunListRecordsPaginatesCompleteZone(t *testing.T) {
	var pages []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if query.Get("Action") != "DescribeDomainRecords" || query.Get("AccessKeyId") != "test-key" || query.Get("Signature") == "" {
			t.Errorf("request was not signed correctly: %s", r.URL.RawQuery)
		}
		page := query.Get("PageNumber")
		pages = append(pages, page)
		count := 500
		if page == "2" {
			count = 1
		}
		records := make([]map[string]interface{}, 0, count)
		for index := 0; index < count; index++ {
			number := index
			if page == "2" {
				number += 500
			}
			records = append(records, map[string]interface{}{"RecordId": fmt.Sprint(number), "RR": fmt.Sprintf("relay-%d", number), "DomainName": "example.com", "Type": "A", "Value": "192.0.2.1", "TTL": 60})
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"TotalCount": "501", "DomainRecords": map[string]interface{}{"Record": records}})
	}))
	defer server.Close()

	provider := NewAliyun(Config{Zone: "example.com", Endpoint: server.URL, AccessKeyID: "test-key", AccessKeySecret: "test-secret", HTTPClient: server.Client()})
	records, err := provider.ListRecords(context.Background(), "example.com", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 501 || strings.Join(pages, ",") != "1,2" {
		t.Fatalf("zone pagination was incomplete: records=%d pages=%v", len(records), pages)
	}
	if records[500].Name != "relay-500.example.com" {
		t.Fatalf("unexpected final record: %+v", records[500])
	}
}

func TestAliyunRejectsOversizedResponseAndHidesHTTPBody(t *testing.T) {
	mode := "status"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if mode == "status" {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("upstream-secret"))
			return
		}
		_, _ = w.Write([]byte(strings.Repeat("x", maxProviderResponseBytes+1)))
	}))
	defer server.Close()
	provider := NewAliyun(Config{Zone: "example.com", Endpoint: server.URL, AccessKeyID: "test-key", AccessKeySecret: "test-secret", HTTPClient: server.Client()})
	if _, err := provider.ListRecords(context.Background(), "example.com", "", ""); err == nil || strings.Contains(err.Error(), "upstream-secret") {
		t.Fatalf("HTTP error body leaked or request passed: %v", err)
	}
	mode = "oversized"
	if _, err := provider.ListRecords(context.Background(), "example.com", "", ""); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("expected oversized response rejection, got %v", err)
	}
}
