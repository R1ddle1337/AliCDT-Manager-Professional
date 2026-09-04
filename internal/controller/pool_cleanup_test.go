package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/protocol"
)

func TestDeleteRelayPoolRemovesPoolDNSAndProviderRecord(t *testing.T) {
	var mu sync.Mutex
	var deleted []string
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.Error(w, `{"success":false}`, http.StatusMethodNotAllowed)
			return
		}
		mu.Lock()
		deleted = append(deleted, r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
	}))
	defer providerServer.Close()

	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	provider, err := store.CreateDNSProvider(context.Background(), CreateDNSProviderRequest{
		Name: "test-cloudflare", Type: "cloudflare", Zone: "example.com", ZoneID: "zone-1", Endpoint: providerServer.URL, APIToken: "token",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateEnrollmentToken(context.Background(), "pool-dns-cleanup", time.Hour); err != nil {
		t.Fatal(err)
	}
	agent, err := store.EnrollAgent(context.Background(), protocol.AgentEnrollmentRequest{Token: "pool-dns-cleanup", NodeName: "relay", PublicIP: "203.0.113.60"})
	if err != nil {
		t.Fatal(err)
	}
	landing, err := store.CreateLandingNode(context.Background(), CreateLandingNodeRequest{Name: "landing", Address: "198.51.100.10", Port: 443, Network: "tcp"})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := store.CreateRelayPool(context.Background(), CreateRelayPoolRequest{
		Name: "dns-pool", Hostname: "relay.example.com", ListenPort: 443, Network: "tcp", Mode: "failover", DNSProviderID: provider.ID,
		Members: []CreateRelayPoolMember{{RelayNodeID: agent.AgentID}}, Targets: []CreateServiceTarget{{LandingNodeID: landing.ID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	records, err := store.ListDNSRecords(context.Background(), provider.ID)
	if err != nil || len(records) != 1 {
		t.Fatalf("expected one pool DNS record: records=%+v err=%v", records, err)
	}
	if _, err := store.db.Exec(`UPDATE dns_managed_records SET provider_record_id='provider-record-1',enabled=1,desired_enabled=1,status='synced' WHERE id=?`, records[0].ID); err != nil {
		t.Fatal(err)
	}

	if err := store.DeleteRelayPool(context.Background(), pool.ID); err != nil {
		t.Fatalf("delete relay pool: %v", err)
	}
	if _, err := store.GetRelayPool(context.Background(), pool.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("pool still exists after delete: err=%v", err)
	}
	remaining, err := store.ListDNSRecords(context.Background(), provider.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Fatalf("pool DNS record remained after delete: %+v", remaining)
	}
	mu.Lock()
	deletes := append([]string(nil), deleted...)
	mu.Unlock()
	if len(deletes) != 1 || deletes[0] != "/zones/zone-1/dns_records/provider-record-1" {
		t.Fatalf("provider record was not deleted exactly once: %v", deletes)
	}
}

func TestDeleteLandingNodeKeepsPoolWithAnotherTarget(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateEnrollmentToken(context.Background(), "partial-landing-delete", time.Hour); err != nil {
		t.Fatal(err)
	}
	agent, err := store.EnrollAgent(context.Background(), protocol.AgentEnrollmentRequest{Token: "partial-landing-delete", NodeName: "relay", PublicIP: "203.0.113.61"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.CreateLandingNode(context.Background(), CreateLandingNodeRequest{Name: "first", Address: "198.51.100.11", Port: 443, Network: "tcp"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateLandingNode(context.Background(), CreateLandingNodeRequest{Name: "second", Address: "198.51.100.12", Port: 443, Network: "tcp"})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := store.CreateRelayPool(context.Background(), CreateRelayPoolRequest{
		Name: "multi-target", Hostname: "multi.example.com", ListenPort: 18461, Network: "tcp", Mode: "failover",
		Members: []CreateRelayPoolMember{{RelayNodeID: agent.AgentID}}, Targets: []CreateServiceTarget{{LandingNodeID: first.ID}, {LandingNodeID: second.ID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteLandingNode(context.Background(), first.ID); err != nil {
		t.Fatal(err)
	}
	remaining, err := store.GetRelayPool(context.Background(), pool.ID)
	if err != nil {
		t.Fatalf("pool with a remaining target was deleted: %v", err)
	}
	if !remaining.Enabled || len(remaining.Targets) != 1 || remaining.Targets[0].LandingNodeID != second.ID {
		t.Fatalf("remaining pool target state is incorrect: %+v", remaining)
	}
}

func TestDeleteDisabledRelayPoolStillCleansOwnedDNS(t *testing.T) {
	var mu sync.Mutex
	var deleted int
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			mu.Lock()
			deleted++
			mu.Unlock()
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
	}))
	defer providerServer.Close()

	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	provider, err := store.CreateDNSProvider(context.Background(), CreateDNSProviderRequest{Name: "disabled-pool-provider", Type: "cloudflare", Zone: "example.com", ZoneID: "zone-1", Endpoint: providerServer.URL, APIToken: "token"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateEnrollmentToken(context.Background(), "disabled-pool-cleanup", time.Hour); err != nil {
		t.Fatal(err)
	}
	agent, err := store.EnrollAgent(context.Background(), protocol.AgentEnrollmentRequest{Token: "disabled-pool-cleanup", NodeName: "relay", PublicIP: "203.0.113.62"})
	if err != nil {
		t.Fatal(err)
	}
	landing, err := store.CreateLandingNode(context.Background(), CreateLandingNodeRequest{Name: "landing", Address: "198.51.100.13", Port: 443, Network: "tcp"})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := store.CreateRelayPool(context.Background(), CreateRelayPoolRequest{
		Name: "disabled-pool", Hostname: "disabled.example.com", ListenPort: 18462, Network: "tcp", Mode: "failover", DNSProviderID: provider.ID,
		Members: []CreateRelayPoolMember{{RelayNodeID: agent.AgentID}}, Targets: []CreateServiceTarget{{LandingNodeID: landing.ID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	records, err := store.ListDNSRecords(context.Background(), provider.ID)
	if err != nil || len(records) != 1 {
		t.Fatalf("expected disabled-pool DNS row: records=%+v err=%v", records, err)
	}
	if _, err := store.db.Exec(`UPDATE dns_managed_records SET provider_record_id='disabled-record',enabled=1,desired_enabled=1,status='synced' WHERE id=?`, records[0].ID); err != nil {
		t.Fatal(err)
	}
	disabled := false
	if _, err := store.UpdateRelayPool(context.Background(), pool.ID, CreateRelayPoolRequest{
		Name: pool.Name, Hostname: pool.Hostname, ListenPort: pool.ListenPort, Network: pool.Network, Mode: pool.Mode, Enabled: &disabled,
		DNSProviderID: provider.ID, Members: []CreateRelayPoolMember{{RelayNodeID: agent.AgentID}}, Targets: []CreateServiceTarget{{LandingNodeID: landing.ID}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteRelayPool(context.Background(), pool.ID); err != nil {
		t.Fatal(err)
	}
	remaining, err := store.ListDNSRecords(context.Background(), provider.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Fatalf("disabled pool DNS row remained after pool deletion: %+v", remaining)
	}
	mu.Lock()
	deleteCount := deleted
	mu.Unlock()
	if deleteCount != 1 {
		t.Fatalf("expected one provider-side delete for disabled pool, got %d", deleteCount)
	}
}
