package controller

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/protocol"
)

func TestRelayPoolMemberJSONRetainsKnownZeroTraffic(t *testing.T) {
	member := RelayPoolMember{TrafficKnown: true}
	encoded, err := json.Marshal(member)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"traffic_used_gb", "traffic_limit_gb", "traffic_percent", "traffic_remaining_gb", "traffic_rate_gb_per_minute", "traffic_threshold_percent"} {
		if _, ok := payload[field]; !ok {
			t.Fatalf("known zero traffic field %q was omitted: %s", field, encoded)
		}
	}
}

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

func TestDispatcherFrontDoorNeverPublishesRelayDNS(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	provider, err := store.CreateDNSProvider(context.Background(), CreateDNSProviderRequest{Name: "cf", Type: "cloudflare", Zone: "example.com", APIToken: "token"})
	if err != nil {
		t.Fatal(err)
	}
	landing, err := store.CreateLandingNode(context.Background(), CreateLandingNodeRequest{Name: "landing", Address: "127.0.0.1", Port: 443, Network: "tcp"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateEnrollmentToken(context.Background(), "dispatcher-front-door", time.Hour); err != nil {
		t.Fatal(err)
	}
	agent, err := store.EnrollAgent(context.Background(), protocol.AgentEnrollmentRequest{Token: "dispatcher-front-door", NodeName: "relay", PublicIP: "203.0.113.90"})
	if err != nil {
		t.Fatal(err)
	}
	// Binding a DNS provider to a fixed-front-door pool is rejected before any
	// service or DNS row is created.
	if _, err := store.CreateRelayPool(context.Background(), CreateRelayPoolRequest{Name: "fixed", Hostname: "entry.example.com", FrontDoorMode: FrontDoorDispatcher, ListenPort: 443, Network: "tcp", Mode: "failover", DNSProviderID: provider.ID, Members: []CreateRelayPoolMember{{RelayNodeID: agent.AgentID}}, Targets: []CreateServiceTarget{{LandingNodeID: landing.ID}}}); err == nil {
		t.Fatal("expected dispatcher pool with Relay DNS provider to be rejected")
	}
	pool, err := store.CreateRelayPool(context.Background(), CreateRelayPoolRequest{Name: "fixed", Hostname: "entry.example.com", FrontDoorMode: FrontDoorDispatcher, ListenPort: 443, Network: "tcp", Mode: "failover", Members: []CreateRelayPoolMember{{RelayNodeID: agent.AgentID}}, Targets: []CreateServiceTarget{{LandingNodeID: landing.ID}}})
	if err != nil {
		t.Fatal(err)
	}
	if pool.FrontDoorMode != FrontDoorDispatcher {
		t.Fatalf("front door mode was not persisted: %+v", pool)
	}
	if err := store.RefreshRelayPoolDNS(context.Background(), pool.ID); err != nil {
		t.Fatal(err)
	}
	records, err := store.ListDNSRecords(context.Background(), provider.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("dispatcher pool unexpectedly created Relay DNS records: %+v", records)
	}
	// A fixed pool can later be converted to Relay-DNS mode after the cleanup;
	// stale ownership metadata must not make that migration impossible.
	updated, err := store.UpdateRelayPool(context.Background(), pool.ID, CreateRelayPoolRequest{Name: pool.Name, Hostname: pool.Hostname, FrontDoorMode: FrontDoorRelayDNS, ListenPort: pool.ListenPort, Network: pool.Network, Mode: pool.Mode, DNSProviderID: provider.ID, Members: []CreateRelayPoolMember{{RelayNodeID: agent.AgentID}}, Targets: []CreateServiceTarget{{LandingNodeID: landing.ID}}})
	if err != nil || updated.FrontDoorMode != FrontDoorRelayDNS {
		t.Fatalf("dispatcher-to-DNS mode transition failed: pool=%+v err=%v", updated, err)
	}
}

func TestRelayDNSToDispatcherTransitionCleansManagedRows(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	provider, err := store.CreateDNSProvider(context.Background(), CreateDNSProviderRequest{Name: "cf", Type: "cloudflare", Zone: "example.com", APIToken: "token"})
	if err != nil {
		t.Fatal(err)
	}
	landing, err := store.CreateLandingNode(context.Background(), CreateLandingNodeRequest{Name: "landing", Address: "127.0.0.1", Port: 443, Network: "tcp"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateEnrollmentToken(context.Background(), "switch-front-door", time.Hour); err != nil {
		t.Fatal(err)
	}
	agent, err := store.EnrollAgent(context.Background(), protocol.AgentEnrollmentRequest{Token: "switch-front-door", NodeName: "relay", PublicIP: "203.0.113.91"})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := store.CreateRelayPool(context.Background(), CreateRelayPoolRequest{Name: "switch", Hostname: "switch.example.com", FrontDoorMode: FrontDoorRelayDNS, ListenPort: 443, Network: "tcp", Mode: "failover", DNSProviderID: provider.ID, Members: []CreateRelayPoolMember{{RelayNodeID: agent.AgentID}}, Targets: []CreateServiceTarget{{LandingNodeID: landing.ID}}})
	if err != nil {
		t.Fatal(err)
	}
	config, err := store.AgentConfig(context.Background(), agent.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateHeartbeat(context.Background(), agent.AgentID, protocol.AgentHeartbeat{CurrentRevision: config.Revision, Services: []protocol.ServiceStatus{{ID: config.Services[0].ID, Listening: true}}}); err != nil {
		t.Fatal(err)
	}
	if err := store.RefreshRelayPoolDNS(context.Background(), pool.ID); err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListDNSRecords(context.Background(), provider.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("expected one Relay DNS row before transition: rows=%+v err=%v", rows, err)
	}
	updated, err := store.UpdateRelayPool(context.Background(), pool.ID, CreateRelayPoolRequest{Name: pool.Name, Hostname: pool.Hostname, FrontDoorMode: FrontDoorDispatcher, ListenPort: pool.ListenPort, Network: pool.Network, Mode: pool.Mode, DNSProviderID: "", Members: []CreateRelayPoolMember{{RelayNodeID: agent.AgentID}}, Targets: []CreateServiceTarget{{LandingNodeID: landing.ID}}})
	if err != nil || updated.FrontDoorMode != FrontDoorDispatcher {
		t.Fatalf("Relay-DNS to Dispatcher transition failed: pool=%+v err=%v", updated, err)
	}
	rows, err = store.ListDNSRecords(context.Background(), provider.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Enabled || rows[0].Status != "disabled" {
		t.Fatalf("old Relay DNS row was not disabled: %+v", rows)
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
	// A relay is not eligible for a managed DNS record until it has reported
	// the current configuration revision and the pool service is listening.
	for _, member := range members {
		config, configErr := store.AgentConfig(context.Background(), member.RelayNodeID)
		if configErr != nil {
			t.Fatal(configErr)
		}
		if len(config.Services) != 1 {
			t.Fatalf("unexpected pool service config: %+v", config)
		}
		if heartbeatErr := store.UpdateHeartbeat(context.Background(), member.RelayNodeID, protocol.AgentHeartbeat{
			CurrentRevision: config.Revision,
			Services:        []protocol.ServiceStatus{{ID: config.Services[0].ID, Listening: true}},
		}); heartbeatErr != nil {
			t.Fatal(heartbeatErr)
		}
	}
	if err := store.RefreshRelayPoolDNS(context.Background(), pool.ID); err != nil {
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

func TestRelayPoolDNSRequiresFreshAppliedListeningHeartbeat(t *testing.T) {
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
	if err := store.CreateEnrollmentToken(context.Background(), "eligibility-token", time.Hour); err != nil {
		t.Fatal(err)
	}
	agent, err := store.EnrollAgent(context.Background(), protocol.AgentEnrollmentRequest{Token: "eligibility-token", NodeName: "relay", PublicIP: "203.0.113.30", Architecture: "amd64", OS: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := store.CreateRelayPool(context.Background(), CreateRelayPoolRequest{
		Name: "pool", Hostname: "relay.example.com", ListenPort: 443, Network: "tcp", Mode: "failover", DNSProviderID: provider.ID,
		Members: []CreateRelayPoolMember{{RelayNodeID: agent.AgentID}}, Targets: []CreateServiceTarget{{LandingNodeID: landing.ID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	getRecord := func() DNSManagedRecord {
		t.Helper()
		records, listErr := store.ListDNSRecords(context.Background(), provider.ID)
		if listErr != nil || len(records) != 1 {
			t.Fatalf("unexpected managed records: %+v err=%v", records, listErr)
		}
		return records[0]
	}
	if record := getRecord(); record.Enabled || record.Status != "pending" {
		t.Fatalf("unapplied relay was published to DNS: %+v", record)
	}

	config, err := store.AgentConfig(context.Background(), agent.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	heartbeat := func(listening bool, revision int64) {
		t.Helper()
		if heartbeatErr := store.UpdateHeartbeat(context.Background(), agent.AgentID, protocol.AgentHeartbeat{
			CurrentRevision: revision,
			Services:        []protocol.ServiceStatus{{ID: config.Services[0].ID, Listening: listening}},
		}); heartbeatErr != nil {
			t.Fatal(heartbeatErr)
		}
		if refreshErr := store.RefreshRelayPoolDNS(context.Background(), pool.ID); refreshErr != nil {
			t.Fatal(refreshErr)
		}
	}
	heartbeat(true, config.Revision)
	if record := getRecord(); !record.Enabled || record.Status != "pending" {
		t.Fatalf("fresh applied listening relay was not enabled: %+v", record)
	}

	// A newer desired revision must withdraw the old listener until the Agent
	// confirms that it has applied the revision.
	if _, err := store.db.Exec(`UPDATE relay_nodes SET desired_revision=desired_revision+1 WHERE id=?`, agent.AgentID); err != nil {
		t.Fatal(err)
	}
	if err := store.RefreshRelayPoolDNS(context.Background(), pool.ID); err != nil {
		t.Fatal(err)
	}
	if record := getRecord(); record.Enabled || record.Status != "pending" {
		t.Fatalf("relay with unapplied revision remained enabled: %+v", record)
	}
	heartbeat(false, config.Revision+1)
	if record := getRecord(); record.Enabled || record.Status != "pending" {
		t.Fatalf("relay with non-listening service remained enabled: %+v", record)
	}
	heartbeat(true, config.Revision+1)
	if record := getRecord(); !record.Enabled {
		t.Fatalf("relay did not recover after listening heartbeat: %+v", record)
	}

	// Heartbeats older than the freshness window are withdrawn even when the
	// persisted node status has not yet been marked offline by automation.
	old := time.Now().UTC().Add(-(relayPoolHeartbeatFreshness + time.Second)).Format(time.RFC3339Nano)
	if _, err := store.db.Exec(`UPDATE relay_nodes SET last_seen_at=? WHERE id=?`, old, agent.AgentID); err != nil {
		t.Fatal(err)
	}
	if err := store.RefreshRelayPoolDNS(context.Background(), pool.ID); err != nil {
		t.Fatal(err)
	}
	if record := getRecord(); record.Enabled || record.Status != "pending" {
		t.Fatalf("stale heartbeat remained enabled: %+v", record)
	}
}

func TestPoolAutoDrainProtectsAlertOnlyAccount(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	account, err := store.CreateCloudAccount(context.Background(), CloudAccountRequest{
		Name: "pool-account", AccessKeyID: "key", AccessKeySecret: "secret", RegionID: "cn-hongkong", SiteType: "china",
		TrafficLimitGB: 200, ThresholdPercent: 90, ProtectionMode: ProtectionAlertOnly,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateEnrollmentToken(context.Background(), "pool-auto-drain", time.Hour, account.ID); err != nil {
		t.Fatal(err)
	}
	agent, err := store.EnrollAgent(context.Background(), protocol.AgentEnrollmentRequest{Token: "pool-auto-drain", NodeName: "relay", PublicIP: "203.0.113.40"})
	if err != nil {
		t.Fatal(err)
	}
	landing, err := store.CreateLandingNode(context.Background(), CreateLandingNodeRequest{Name: "landing", Address: "127.0.0.1", Port: 443, Network: "tcp"})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := store.CreateRelayPool(context.Background(), CreateRelayPoolRequest{
		Name: "auto", Hostname: "relay.example.com", ListenPort: 18443, Network: "tcp", Mode: "failover",
		Members: []CreateRelayPoolMember{{RelayNodeID: agent.AgentID}}, Targets: []CreateServiceTarget{{LandingNodeID: landing.ID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !pool.AutoDrain {
		t.Fatalf("new pool should default to automatic draining: %+v", pool)
	}
	before, err := store.AgentConfig(context.Background(), agent.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := store.ApplyTrafficProtection(context.Background(), account.ID, 190)
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Changed || !decision.Triggered || decision.Mode != ProtectionAlertOnly {
		t.Fatalf("unexpected account protection decision: %+v", decision)
	}
	after, err := store.AgentConfig(context.Background(), agent.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != before.Revision+1 || len(after.Services) != 1 || after.Services[0].Enabled {
		t.Fatalf("auto-drain pool did not suspend the relay: before=%+v after=%+v", before, after)
	}
	if err := store.RefreshRelayPoolDNS(context.Background(), pool.ID); err != nil {
		t.Fatal(err)
	}
	members, err := store.GetRelayPool(context.Background(), pool.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(members.Members) != 1 || members.Members[0].Status != "draining" {
		t.Fatalf("auto-drain member was not marked draining: %+v", members.Members)
	}
}

func TestPoolAutoDrainUsesPredictiveTrafficGuard(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	account, err := store.CreateCloudAccount(context.Background(), CloudAccountRequest{
		Name: "predictive-pool-account", AccessKeyID: "key", AccessKeySecret: "secret", RegionID: "cn-hongkong", SiteType: "china",
		TrafficLimitGB: 200, ThresholdPercent: 90, ProtectionMode: ProtectionAlertOnly,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateEnrollmentToken(context.Background(), "predictive-pool-token", time.Hour, account.ID); err != nil {
		t.Fatal(err)
	}
	agent, err := store.EnrollAgent(context.Background(), protocol.AgentEnrollmentRequest{Token: "predictive-pool-token", NodeName: "relay", PublicIP: "203.0.113.42"})
	if err != nil {
		t.Fatal(err)
	}
	landing, err := store.CreateLandingNode(context.Background(), CreateLandingNodeRequest{Name: "landing", Address: "127.0.0.1", Port: 9443, Network: "tcp"})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := store.CreateRelayPool(context.Background(), CreateRelayPoolRequest{
		Name: "predictive", Hostname: "relay.example.com", ListenPort: 18449, Network: "tcp", Mode: "failover",
		Members: []CreateRelayPoolMember{{RelayNodeID: agent.AgentID}}, Targets: []CreateServiceTarget{{LandingNodeID: landing.ID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveCloudSync(context.Background(), account, nil, false, "", 10, true, ""); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := store.db.Exec(`UPDATE account_traffic_snapshots SET used_gb=?,synced_at=?,previous_used_gb=?,previous_synced_at=? WHERE account_id=?`,
		160, now.Add(-2*time.Minute).Format(time.RFC3339Nano), 140, now.Add(-4*time.Minute).Format(time.RFC3339Nano), account.ID); err != nil {
		t.Fatal(err)
	}
	decision, err := store.ApplyTrafficProtectionWithWindow(context.Background(), account.ID, 170, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Triggered || !decision.Predictive || decision.Percent >= 90 {
		t.Fatalf("pool did not enter predictive protection: %+v", decision)
	}
	memberPool, err := store.GetRelayPool(context.Background(), pool.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(memberPool.Members) != 1 || memberPool.Members[0].Status != "draining" || !memberPool.Members[0].ProtectionPredictive {
		t.Fatalf("predictive pool member was not withdrawn: %+v", memberPool.Members)
	}
}

func TestPoolAutoDrainCanBeDisabled(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	account, err := store.CreateCloudAccount(context.Background(), CloudAccountRequest{
		Name: "manual-account", AccessKeyID: "key", AccessKeySecret: "secret", RegionID: "cn-hongkong", SiteType: "china",
		TrafficLimitGB: 200, ThresholdPercent: 90, ProtectionMode: ProtectionAlertOnly,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateEnrollmentToken(context.Background(), "pool-manual", time.Hour, account.ID); err != nil {
		t.Fatal(err)
	}
	agent, err := store.EnrollAgent(context.Background(), protocol.AgentEnrollmentRequest{Token: "pool-manual", NodeName: "relay", PublicIP: "203.0.113.41"})
	if err != nil {
		t.Fatal(err)
	}
	landing, err := store.CreateLandingNode(context.Background(), CreateLandingNodeRequest{Name: "landing", Address: "127.0.0.1", Port: 443, Network: "tcp"})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := store.CreateRelayPool(context.Background(), CreateRelayPoolRequest{
		Name: "manual", Hostname: "relay.example.com", ListenPort: 18444, Network: "tcp", Mode: "failover", AutoDrain: boolPtr(false),
		Members: []CreateRelayPoolMember{{RelayNodeID: agent.AgentID}}, Targets: []CreateServiceTarget{{LandingNodeID: landing.ID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.AgentConfig(context.Background(), agent.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyTrafficProtection(context.Background(), account.ID, 190); err != nil {
		t.Fatal(err)
	}
	after, err := store.AgentConfig(context.Background(), agent.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != before.Revision || len(after.Services) != 1 || !after.Services[0].Enabled {
		t.Fatalf("disabled auto-drain unexpectedly suspended relay: before=%+v after=%+v pool=%+v", before, after, pool)
	}
}

func TestPortSpecificPoolsShareOneDNSRRset(t *testing.T) {
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
	if err := store.CreateEnrollmentToken(context.Background(), "shared-dns", time.Hour); err != nil {
		t.Fatal(err)
	}
	agent, err := store.EnrollAgent(context.Background(), protocol.AgentEnrollmentRequest{Token: "shared-dns", NodeName: "relay", PublicIP: "203.0.113.50"})
	if err != nil {
		t.Fatal(err)
	}
	makePool := func(port int) RelayPool {
		pool, poolErr := store.CreateRelayPool(context.Background(), CreateRelayPoolRequest{
			Name: "route", Hostname: "relay.example.com", ListenPort: port, Network: "tcp", Mode: "failover", DNSProviderID: provider.ID,
			Members: []CreateRelayPoolMember{{RelayNodeID: agent.AgentID}}, Targets: []CreateServiceTarget{{LandingNodeID: landing.ID}},
		})
		if poolErr != nil {
			t.Fatal(poolErr)
		}
		return pool
	}
	first := makePool(18445)
	second := makePool(18446)
	records, err := store.ListDNSRecords(context.Background(), provider.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("same hostname/member should have one DNS row, got %d: %+v", len(records), records)
	}
	config, err := store.AgentConfig(context.Background(), agent.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	statuses := make([]protocol.ServiceStatus, 0, len(config.Services))
	for _, service := range config.Services {
		statuses = append(statuses, protocol.ServiceStatus{ID: service.ID, Listening: true})
	}
	if err := store.UpdateHeartbeat(context.Background(), agent.AgentID, protocol.AgentHeartbeat{CurrentRevision: config.Revision, Services: statuses}); err != nil {
		t.Fatal(err)
	}
	if err := store.RefreshAllRelayPoolDNS(context.Background()); err != nil {
		t.Fatal(err)
	}
	records, err = store.ListDNSRecords(context.Background(), provider.ID)
	if err != nil || len(records) != 1 || !records[0].Enabled {
		t.Fatalf("shared DNS row did not become active: records=%+v err=%v", records, err)
	}
	if err := store.DeleteRelayPool(context.Background(), first.ID); err != nil {
		t.Fatal(err)
	}
	// Removing a route publishes a new node revision. Wait for the remaining
	// listener to acknowledge that revision before expecting DNS recovery.
	config, err = store.AgentConfig(context.Background(), agent.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	statuses = statuses[:0]
	for _, service := range config.Services {
		statuses = append(statuses, protocol.ServiceStatus{ID: service.ID, Listening: true})
	}
	if err := store.UpdateHeartbeat(context.Background(), agent.AgentID, protocol.AgentHeartbeat{CurrentRevision: config.Revision, Services: statuses}); err != nil {
		t.Fatal(err)
	}
	if err := store.RefreshAllRelayPoolDNS(context.Background()); err != nil {
		t.Fatal(err)
	}
	records, err = store.ListDNSRecords(context.Background(), provider.ID)
	if err != nil || len(records) != 1 || !records[0].Enabled {
		t.Fatalf("deleting one route disabled the shared DNS row: records=%+v err=%v", records, err)
	}
	if _, err := store.GetRelayPool(context.Background(), second.ID); err != nil {
		t.Fatal(err)
	}
}
