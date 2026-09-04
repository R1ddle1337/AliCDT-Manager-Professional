package controller

import (
	"context"
	"testing"
	"time"

	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/protocol"
)

func TestNormalizeDNSProviderTTL(t *testing.T) {
	tests := []struct {
		name         string
		ttl          int
		providerType string
		want         int
	}{
		{name: "cloudflare automatic", ttl: 1, providerType: "cloudflare", want: 1},
		{name: "aliyun translates automatic", ttl: 1, providerType: "aliyun", want: 60},
		{name: "unmanaged translates automatic", ttl: 1, want: 60},
		{name: "zero defaults", ttl: 0, providerType: "cloudflare", want: 60},
		{name: "cloudflare rounds up subminute", ttl: 30, providerType: "cloudflare", want: 60},
		{name: "fastest is preserved", ttl: 60, providerType: "cloudflare", want: 60},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := normalizeDNSProviderTTL(test.ttl, test.providerType)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("normalizeDNSProviderTTL(%d, %q) = %d, want %d", test.ttl, test.providerType, got, test.want)
			}
		})
	}
	if _, err := normalizeDNSProviderTTL(86401, "cloudflare"); err == nil {
		t.Fatal("expected an error for a TTL above the supported maximum")
	}
}

func TestCloudflarePoolPersistsAutomaticTTL(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	provider, err := store.CreateDNSProvider(context.Background(), CreateDNSProviderRequest{
		Name: "cf", Type: "cloudflare", Zone: "example.com", APIToken: "token",
	})
	if err != nil {
		t.Fatal(err)
	}
	landing, err := store.CreateLandingNode(context.Background(), CreateLandingNodeRequest{
		Name: "landing", Address: "127.0.0.1", Port: 443, Network: "tcp",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateEnrollmentToken(context.Background(), "auto-ttl-token", time.Hour); err != nil {
		t.Fatal(err)
	}
	agent, err := store.EnrollAgent(context.Background(), protocol.AgentEnrollmentRequest{
		Token: "auto-ttl-token", NodeName: "relay", PublicIP: "203.0.113.10",
	})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := store.CreateRelayPool(context.Background(), CreateRelayPoolRequest{
		Name: "auto-ttl", Hostname: "relay.example.com", ListenPort: 443,
		Network: "tcp", Mode: "failover", DNSProviderID: provider.ID,
		DNSRecordName: "relay", DNSTTL: 1,
		Members: []CreateRelayPoolMember{{RelayNodeID: agent.AgentID}},
		Targets: []CreateServiceTarget{{LandingNodeID: landing.ID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if pool.DNSTTL != 1 {
		t.Fatalf("Cloudflare automatic TTL was not retained on pool: %d", pool.DNSTTL)
	}
	records, err := store.ListDNSRecords(context.Background(), provider.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].TTL != 1 {
		t.Fatalf("Cloudflare automatic TTL was not retained on managed record: %+v", records)
	}
}
