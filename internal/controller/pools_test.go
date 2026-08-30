package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/protocol"
)

func TestRelayPoolReplicatesServiceAndGeneratesLogicalLink(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	landing, err := store.CreateLandingNode(context.Background(), CreateLandingNodeRequest{Name: "landing", ShareURI: "vless://11111111-2222-3333-4444-555555555555@198.51.100.10:443?security=reality&pbk=test#landing"})
	if err != nil {
		t.Fatal(err)
	}
	var members []CreateRelayPoolMember
	for index, name := range []string{"relay-1", "relay-2"} {
		token := "pool-token-" + name
		if err := store.CreateEnrollmentToken(context.Background(), token, time.Hour); err != nil {
			t.Fatal(err)
		}
		enrolled, err := store.EnrollAgent(context.Background(), protocol.AgentEnrollmentRequest{Token: token, NodeName: name, PublicIP: "203.0.113." + string(rune('1'+index)), Architecture: "amd64", OS: "linux"})
		if err != nil {
			t.Fatal(err)
		}
		members = append(members, CreateRelayPoolMember{RelayNodeID: enrolled.AgentID, Weight: 1})
	}
	pool, err := store.CreateRelayPool(context.Background(), CreateRelayPoolRequest{Name: "main-entry", Hostname: "relay.example.com", ListenPort: 8443, Network: "tcp", Mode: "failover", Members: members, Targets: []CreateServiceTarget{{LandingNodeID: landing.ID, Weight: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(pool.Members) != 2 || len(pool.Targets) != 1 {
		t.Fatalf("unexpected pool: %+v", pool)
	}
	services, err := store.ListRelayServices(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 2 {
		t.Fatalf("pool did not replicate services: %+v", services)
	}
	for _, member := range pool.Members {
		config, err := store.AgentConfig(context.Background(), member.RelayNodeID)
		if err != nil {
			t.Fatal(err)
		}
		if len(config.Services) != 1 || config.Services[0].Listen != "0.0.0.0:8443" {
			t.Fatalf("unexpected member config: %+v", config)
		}
	}
	links, err := store.LandingRelayLinks(context.Background(), landing.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 || links[0].PoolID != pool.ID || !strings.Contains(links[0].URI, "@relay.example.com:8443") {
		t.Fatalf("unexpected pool link: %+v", links)
	}
	pool.Name = "main-entry-updated"
	updated, err := store.UpdateRelayPool(context.Background(), pool.ID, CreateRelayPoolRequest{Name: pool.Name, Hostname: pool.Hostname, ListenPort: pool.ListenPort, Network: pool.Network, Mode: pool.Mode, Members: members, Targets: []CreateServiceTarget{{LandingNodeID: landing.ID}}})
	if err != nil || updated.Name != "main-entry-updated" {
		t.Fatalf("pool update failed: %+v err=%v", updated, err)
	}
}

func TestRelayPoolRefreshesManagedDNSRecordsFromRelayState(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	provider, err := store.CreateDNSProvider(context.Background(), CreateDNSProviderRequest{Name: "cf", Type: "cloudflare", Zone: "example.com", APIToken: "token"})
	if err != nil {
		t.Fatal(err)
	}
	landing, err := store.CreateLandingNode(context.Background(), CreateLandingNodeRequest{Name: "landing", ShareURI: "vless://11111111-2222-3333-4444-555555555555@198.51.100.10:443?security=reality&pbk=test"})
	if err != nil {
		t.Fatal(err)
	}
	members := make([]CreateRelayPoolMember, 0, 2)
	var firstAgent string
	for _, ip := range []string{"203.0.113.21", "203.0.113.22"} {
		token := "dns-pool-token-" + ip
		if err := store.CreateEnrollmentToken(context.Background(), token, time.Hour); err != nil {
			t.Fatal(err)
		}
		enrolled, err := store.EnrollAgent(context.Background(), protocol.AgentEnrollmentRequest{Token: token, NodeName: "relay-dns-" + ip, PublicIP: ip, Architecture: "amd64", OS: "linux"})
		if err != nil {
			t.Fatal(err)
		}
		if firstAgent == "" {
			firstAgent = enrolled.AgentID
		}
		members = append(members, CreateRelayPoolMember{RelayNodeID: enrolled.AgentID})
	}
	pool, err := store.CreateRelayPool(context.Background(), CreateRelayPoolRequest{Name: "dns-pool", Hostname: "relay.example.com", ListenPort: 443, Network: "tcp", Mode: "failover", DNSProviderID: provider.ID, Members: members, Targets: []CreateServiceTarget{{LandingNodeID: landing.ID}}})
	if err != nil {
		t.Fatal(err)
	}
	records, err := store.ListDNSRecords(context.Background(), provider.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[0].PoolID != pool.ID || !records[0].Enabled || !records[1].Enabled {
		t.Fatalf("unexpected pool DNS records: %+v", records)
	}
	if _, err := store.db.Exec(`UPDATE relay_nodes SET status='offline' WHERE id=?`, firstAgent); err != nil {
		t.Fatal(err)
	}
	if err := store.RefreshRelayPoolDNS(context.Background(), pool.ID); err != nil {
		t.Fatal(err)
	}
	records, err = store.ListDNSRecords(context.Background(), provider.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.RelayNodeID == firstAgent && record.Enabled {
			t.Fatalf("offline relay remained in DNS pool: %+v", record)
		}
	}
}
