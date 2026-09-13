package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/protocol"
)

func TestFirewallManagerUFWAddsAndRemovesOwnedPorts(t *testing.T) {
	state := t.TempDir()
	ufwRules := "Status: active\n[ 1] 443/tcp ALLOW IN Anywhere\n"
	var commands []string
	m := newFirewallManager(state)
	m.lookPath = func(name string) (string, error) {
		if name == "ufw" {
			return "/usr/bin/ufw", nil
		}
		return "", os.ErrNotExist
	}
	m.run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		commands = append(commands, strings.Join(args, " "))
		switch {
		case len(args) == 1 && args[0] == "status":
			return []byte(ufwRules), nil
		case len(args) == 2 && args[0] == "status" && args[1] == "numbered":
			return []byte(ufwRules), nil
		case len(args) >= 2 && args[0] == "allow":
			ufwRules += "[ 2] " + args[1] + " ALLOW IN Anywhere # " + strings.TrimSpace(strings.Join(args[3:], " ")) + "\n"
			return nil, nil
		case len(args) == 3 && args[0] == "--force" && args[1] == "delete":
			ufwRules = "Status: active\n[ 1] 443/tcp ALLOW IN Anywhere\n"
			return nil, nil
		default:
			return nil, errors.New("unexpected command")
		}
	}

	config := protocol.AgentConfig{Services: []protocol.ServiceConfig{{
		Listen: "0.0.0.0:51242", Network: "tcp", Enabled: true,
	}}}
	if err := m.sync(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(commands, "\n"), "allow 51242/tcp") {
		t.Fatalf("expected UFW allow command, got %v", commands)
	}
	if _, err := os.Stat(filepath.Join(state, "firewall-state.json")); err != nil {
		t.Fatalf("expected firewall state: %v", err)
	}

	commands = nil
	config.Services[0].Enabled = false
	if err := m.sync(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(commands, "\n"), "--force delete 2") {
		t.Fatalf("expected UFW delete command, got %v", commands)
	}
	if _, err := os.Stat(filepath.Join(state, "firewall-state.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected empty firewall state to be removed, got %v", err)
	}
}

func TestFirewallManagerDoesNotClaimExistingUFWRule(t *testing.T) {
	state := t.TempDir()
	ufwRules := "Status: active\n[ 1] 51242/tcp ALLOW IN Anywhere # operator rule\n"
	var commands []string
	m := newFirewallManager(state)
	m.lookPath = func(name string) (string, error) {
		if name == "ufw" {
			return "/usr/bin/ufw", nil
		}
		return "", os.ErrNotExist
	}
	m.run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		commands = append(commands, strings.Join(args, " "))
		if len(args) == 1 && args[0] == "status" {
			return []byte(ufwRules), nil
		}
		if len(args) == 2 && args[0] == "status" && args[1] == "numbered" {
			return []byte(ufwRules), nil
		}
		return nil, errors.New("unexpected command")
	}
	config := protocol.AgentConfig{Services: []protocol.ServiceConfig{{Listen: "*:51242", Network: "tcp", Enabled: true}}}
	if err := m.sync(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	config.Services[0].Enabled = false
	if err := m.sync(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(commands, "\n"), "delete") {
		t.Fatalf("existing operator rule was claimed or deleted: %v", commands)
	}
}

func TestRequiredFirewallPortsSkipsLoopbackAndSupportsTCPUDP(t *testing.T) {
	ports := requiredFirewallPorts(protocol.AgentConfig{Services: []protocol.ServiceConfig{
		{Listen: "127.0.0.1:10001", Network: "tcp+udp", Enabled: true},
		{Listen: "[::]:10002", Network: "tcp+udp", Enabled: true},
		{Listen: "0.0.0.0:10003", Network: "udp", Enabled: false},
	}})
	if len(ports) != 2 || ports["10002/tcp"].Port != 10002 || ports["10002/udp"].Port != 10002 {
		t.Fatalf("unexpected required ports: %+v", ports)
	}
}

func TestFirewallManagerIPTablesAddsAndRemovesIPv4AndIPv6(t *testing.T) {
	state := t.TempDir()
	var v4, v6 string
	var commands []string
	m := newFirewallManager(state)
	m.lookPath = func(name string) (string, error) {
		switch name {
		case "iptables":
			return "/sbin/iptables", nil
		case "ip6tables":
			return "/sbin/ip6tables", nil
		default:
			return "", os.ErrNotExist
		}
	}
	m.run = func(_ context.Context, path string, args ...string) ([]byte, error) {
		commands = append(commands, path+" "+strings.Join(args, " "))
		if len(args) >= 2 && args[0] == "-S" && args[1] == "INPUT" {
			if path == "/sbin/ip6tables" {
				return []byte(v6), nil
			}
			return []byte(v4), nil
		}
		if len(args) >= 2 && args[0] == "-I" && args[1] == "INPUT" {
			line := "-A INPUT -p tcp -m tcp --dport 51242 -m comment --comment cdt-relay-51242-tcp -j ACCEPT\n"
			if path == "/sbin/ip6tables" {
				v6 += line
			} else {
				v4 += line
			}
			return nil, nil
		}
		if len(args) >= 2 && args[0] == "-D" && args[1] == "INPUT" {
			if path == "/sbin/ip6tables" {
				v6 = ""
			} else {
				v4 = ""
			}
			return nil, nil
		}
		return nil, errors.New("unexpected command")
	}
	config := protocol.AgentConfig{Services: []protocol.ServiceConfig{{Listen: "0.0.0.0:51242", Network: "tcp", Enabled: true}}}
	if err := m.sync(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(commands, "\n")
	if !strings.Contains(joined, "/sbin/iptables -I INPUT") || !strings.Contains(joined, "/sbin/ip6tables -I INPUT") {
		t.Fatalf("expected both iptables families to be updated: %s", joined)
	}
	commands = nil
	config.Services[0].Enabled = false
	if err := m.sync(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	joined = strings.Join(commands, "\n")
	if !strings.Contains(joined, "/sbin/iptables -D INPUT") || !strings.Contains(joined, "/sbin/ip6tables -D INPUT") {
		t.Fatalf("expected both iptables families to be reconciled: %s", joined)
	}
}

func TestParseNftRulesFindsManagedAndOperatorRules(t *testing.T) {
	output := []byte(`	tcp dport 443 counter packets 0 bytes 0 accept comment "operator" # handle 8
	udp dport 51242 counter packets 0 bytes 0 accept comment "cdt-relay-51242-udp" # handle 9
`)
	rules := parseNftRules(output)
	if len(rules) != 2 || !nftHasAllowedRule(rules, firewallPort{Port: 443, Protocol: "tcp"}) || !nftHasTaggedRule(rules, firewallPort{Port: 51242, Protocol: "udp"}) {
		t.Fatalf("unexpected nft rules: %+v", rules)
	}
}

func TestParseNftInputChainPrefersInet(t *testing.T) {
	output := []byte(`table ip legacy {
	chain ingress {
		type filter hook input priority filter; policy drop;
	}
}
table inet custom_firewall {
	chain public_input {
		type filter hook input priority filter; policy drop;
	}
}`)
	backend, ok := selectNftBackend("/usr/sbin/nft", parseNftInputChains(output))
	if !ok || backend.family != "inet" || backend.table != "custom_firewall" || backend.chain != "public_input" {
		t.Fatalf("unexpected nft input chain: %+v %t", backend, ok)
	}
}

func TestSelectNftBackendCombinesSeparateIPv4AndIPv6Chains(t *testing.T) {
	backend, ok := selectNftBackend("/usr/sbin/nft", []firewallBackend{
		{family: "ip", table: "filter", chain: "input"},
		{family: "ip6", table: "firewall", chain: "incoming"},
	})
	if !ok || backend.family != "ip" || backend.family6 != "ip6" || backend.table6 != "firewall" || backend.chain6 != "incoming" {
		t.Fatalf("unexpected dual-stack nft backend: %+v %t", backend, ok)
	}
}

func TestFirewallManagerFirewalldAddsAndRemovesOwnedPorts(t *testing.T) {
	state := t.TempDir()
	enabled := false
	var commands []string
	m := newFirewallManager(state)
	m.lookPath = func(name string) (string, error) {
		if name == "firewall-cmd" {
			return "/usr/bin/firewall-cmd", nil
		}
		return "", os.ErrNotExist
	}
	m.run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		commands = append(commands, strings.Join(args, " "))
		switch args[0] {
		case "--state":
			return []byte("running\n"), nil
		case "--query-port":
			if enabled {
				return []byte("yes\n"), nil
			}
			return []byte("no\n"), errors.New("exit status 1")
		case "--add-port":
			enabled = true
			return []byte("success\n"), nil
		case "--remove-port":
			enabled = false
			return []byte("success\n"), nil
		default:
			return nil, errors.New("unexpected command")
		}
	}
	config := protocol.AgentConfig{Services: []protocol.ServiceConfig{{Listen: "0.0.0.0:51242", Network: "tcp", Enabled: true}}}
	if err := m.sync(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	config.Services[0].Enabled = false
	if err := m.sync(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(commands, "\n")
	if !strings.Contains(joined, "--add-port 51242/tcp") || !strings.Contains(joined, "--remove-port 51242/tcp") {
		t.Fatalf("firewalld was not reconciled: %s", joined)
	}
}

func TestFirewallManagerNftablesAddsAndDeletesByHandle(t *testing.T) {
	var rules string
	var commands []string
	m := newFirewallManager(t.TempDir())
	m.run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		commands = append(commands, strings.Join(args, " "))
		if len(args) >= 3 && args[0] == "-a" && args[1] == "list" && args[2] == "chain" {
			return []byte(rules), nil
		}
		if len(args) >= 2 && args[0] == "add" && args[1] == "rule" {
			rules = "tcp dport 51242 counter packets 0 bytes 0 accept comment \"cdt-relay-51242-tcp\" # handle 9\n"
			return nil, nil
		}
		if len(args) >= 2 && args[0] == "delete" && args[1] == "rule" {
			rules = ""
			return nil, nil
		}
		return nil, errors.New("unexpected command")
	}
	backend := firewallBackend{kind: "nftables", id: "nftables:inet:filter:input", path: "/usr/sbin/nft", family: "inet", table: "filter", chain: "input"}
	port := firewallPort{Port: 51242, Protocol: "tcp"}
	owned, err := m.syncNftables(context.Background(), backend, map[string]firewallPort{"51242/tcp": port}, nil)
	if err != nil || !owned["51242/tcp"] {
		t.Fatalf("nft add failed: owned=%v err=%v", owned, err)
	}
	managed := map[string]firewallRule{"51242/tcp": {Backend: backend.id, Port: port.Port, Protocol: port.Protocol, Tag: nftRuleTag(port)}}
	if _, err := m.syncNftables(context.Background(), backend, nil, managed); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(commands, "\n")
	if !strings.Contains(joined, "add rule inet filter input") || !strings.Contains(joined, "delete rule inet filter input handle 9") {
		t.Fatalf("nft rules were not reconciled by handle: %s", joined)
	}
}
