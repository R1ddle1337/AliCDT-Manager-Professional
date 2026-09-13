package controller

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/protocol"
)

func TestReplaceVLESSNodeEndpointPreservesParameters(t *testing.T) {
	original := "vless://11111111-2222-3333-4444-555555555555@198.51.100.20:443?encryption=none&security=reality&sni=example.com&fp=chrome&pbk=PUBLICKEY&sid=abcd&type=tcp#production"
	parsed, err := parseNodeLink(original)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Address != "198.51.100.20" || parsed.Port != 443 || parsed.Protocol != "vless" {
		t.Fatalf("unexpected parsed node: %+v", parsed)
	}
	relayed, err := replaceNodeEndpoint(original, "203.0.113.10", 18443)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(relayed, "@203.0.113.10:18443") || !strings.Contains(relayed, "security=reality") || !strings.Contains(relayed, "pbk=PUBLICKEY") || !strings.HasSuffix(relayed, "#production") {
		t.Fatalf("parameters were not preserved: %s", relayed)
	}
}

func TestReplaceEncodedSSNodeEndpoint(t *testing.T) {
	payload := base64.RawStdEncoding.EncodeToString([]byte("2022-blake3-aes-128-gcm:secret-password@198.51.100.30:8443"))
	original := "ss://" + payload + "#ss2022"
	parsed, err := parseNodeLink(original)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Address != "198.51.100.30" || parsed.Port != 8443 || parsed.Network != "tcp+udp" {
		t.Fatalf("unexpected SS node: %+v", parsed)
	}
	relayed, err := replaceNodeEndpoint(original, "203.0.113.11", 18444)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(relayed, "#ss2022") {
		t.Fatalf("SS fragment was not preserved: %s", relayed)
	}
	decoded, err := decodeBase64(strings.TrimSuffix(strings.TrimPrefix(relayed, "ss://"), "#ss2022"))
	if err != nil || !strings.Contains(decoded, "@203.0.113.11:18444") || !strings.Contains(decoded, "2022-blake3-aes-128-gcm:secret-password") {
		t.Fatalf("encoded SS endpoint was not replaced safely: decoded=%s err=%v", decoded, err)
	}
}

func TestReplaceVMessNodeEndpoint(t *testing.T) {
	payload, _ := json.Marshal(map[string]interface{}{"v": "2", "ps": "vmess-node", "add": "198.51.100.40", "port": "443", "id": "uuid", "net": "ws", "path": "/edge", "tls": "tls"})
	original := "vmess://" + base64.RawStdEncoding.EncodeToString(payload)
	relayed, err := replaceNodeEndpoint(original, "203.0.113.12", 18445)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeBase64(strings.TrimPrefix(relayed, "vmess://"))
	if err != nil {
		t.Fatal(err)
	}
	var node map[string]interface{}
	if err := json.Unmarshal([]byte(decoded), &node); err != nil {
		t.Fatal(err)
	}
	if node["add"] != "203.0.113.12" || node["port"] != "18445" || node["path"] != "/edge" || node["id"] != "uuid" {
		t.Fatalf("VMess parameters were not preserved: %+v", node)
	}
}

func TestLandingRelayLinksReplaceOnlyEndpoint(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	landing, err := store.CreateLandingNode(context.Background(), CreateLandingNodeRequest{
		Name: "reality", ShareURI: "vless://11111111-2222-3333-4444-555555555555@198.51.100.20:443?encryption=none&security=reality&pbk=KEY#node",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateEnrollmentToken(context.Background(), "links-agent", time.Hour); err != nil {
		t.Fatal(err)
	}
	enrolled, err := store.EnrollAgent(context.Background(), protocol.AgentEnrollmentRequest{Token: "links-agent", NodeName: "relay", PublicIP: "203.0.113.20", Architecture: "amd64", OS: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRelayService(context.Background(), CreateRelayServiceRequest{
		RelayNodeID: enrolled.AgentID, Name: "entry", ListenHost: "0.0.0.0", ListenPort: 18443, Network: "tcp", Mode: "failover",
		Targets: []CreateServiceTarget{{LandingNodeID: landing.ID, Enabled: boolPtr(true)}},
	}); err != nil {
		t.Fatal(err)
	}
	links, err := store.LandingRelayLinks(context.Background(), landing.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 || !strings.Contains(links[0].URI, "@203.0.113.20:18443") || !strings.Contains(links[0].URI, "pbk=KEY") || !links[0].Available {
		t.Fatalf("unexpected generated relay link: %+v", links)
	}
}
