package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/protocol"
)

const firewallRulePrefix = "AliCDT relay"

type firewallPort struct {
	Port     int
	Protocol string
}

type firewallRule struct {
	Backend  string `json:"backend"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
	Tag      string `json:"tag"`
}

type firewallState struct {
	Rules []firewallRule `json:"rules"`
}

type firewallManager struct {
	mu       sync.Mutex
	state    string
	lookPath func(string) (string, error)
	run      func(context.Context, string, ...string) ([]byte, error)
}

func newFirewallManager(dataDir string) *firewallManager {
	return &firewallManager{
		state:    filepath.Join(dataDir, "firewall-state.json"),
		lookPath: exec.LookPath,
		run: func(ctx context.Context, path string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, path, args...).CombinedOutput()
		},
	}
}

// sync reconciles only rules previously created by this Agent. Existing
// operator rules are treated as sufficient and are never removed. A missing
// firewall binary, inactive firewall, or unsupported distribution is a safe
// no-op; cloud security groups remain outside the Agent's control.
func (m *firewallManager) sync(ctx context.Context, config protocol.AgentConfig) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	state, err := m.loadState()
	if err != nil {
		return err
	}
	desired := requiredFirewallPorts(config)
	backend, err := m.detect(ctx)
	if err != nil {
		return err
	}
	if backend.kind == "" {
		return nil
	}

	var next []firewallRule
	for _, rule := range state.Rules {
		if rule.Backend != backend.id {
			next = append(next, rule)
		}
	}
	managed := make(map[string]firewallRule)
	for _, rule := range state.Rules {
		if rule.Backend == backend.id {
			managed[firewallRuleKey(rule.Port, rule.Protocol)] = rule
		}
	}

	var current map[string]bool
	var reconcileErr error
	switch backend.kind {
	case "ufw":
		current, reconcileErr = m.syncUFW(ctx, backend.path, desired, managed)
	case "firewalld":
		current, reconcileErr = m.syncFirewalld(ctx, backend.path, desired, managed)
	case "nftables":
		current, reconcileErr = m.syncNftables(ctx, backend, desired, managed)
	case "iptables":
		current, reconcileErr = m.syncIPTables(ctx, backend, desired, managed)
	}
	if reconcileErr != nil {
		return reconcileErr
	}
	for key, rule := range managed {
		if _, exists := desired[key]; exists {
			next = append(next, rule)
			continue
		}
		if current[key] {
			// Removal failed or was not attempted; retain state for the next pass.
			next = append(next, rule)
		}
	}
	for key := range desired {
		if _, exists := managed[key]; !exists && current[key] {
			next = append(next, firewallRule{Backend: backend.id, Port: desired[key].Port, Protocol: desired[key].Protocol, Tag: backendRuleTag(backend, desired[key])})
		}
	}
	state.Rules = dedupeFirewallRules(next)
	return m.saveState(state)
}

func (m *firewallManager) loadState() (firewallState, error) {
	data, err := os.ReadFile(m.state)
	if errors.Is(err, os.ErrNotExist) {
		return firewallState{}, nil
	}
	if err != nil {
		return firewallState{}, fmt.Errorf("read firewall state: %w", err)
	}
	var state firewallState
	if err := json.Unmarshal(data, &state); err != nil {
		return firewallState{}, fmt.Errorf("parse firewall state: %w", err)
	}
	return state, nil
}

func (m *firewallManager) saveState(state firewallState) error {
	if len(state.Rules) == 0 {
		if err := os.Remove(m.state); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove empty firewall state: %w", err)
		}
		return nil
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(m.state), 0700); err != nil {
		return err
	}
	return writeFileAtomic(m.state, data, 0600)
}

type firewallBackend struct {
	kind    string
	id      string
	path    string
	path6   string
	family  string
	table   string
	chain   string
	family6 string
	table6  string
	chain6  string
}

func (m *firewallManager) detect(ctx context.Context) (firewallBackend, error) {
	if path, err := m.lookPath("ufw"); err == nil {
		output, runErr := m.run(ctx, path, "status")
		if runErr != nil {
			return firewallBackend{}, commandError("ufw status", output, runErr)
		}
		if strings.Contains(strings.ToLower(string(output)), "status: active") {
			return firewallBackend{kind: "ufw", id: "ufw", path: path}, nil
		}
	}
	if path, err := m.lookPath("firewall-cmd"); err == nil {
		output, runErr := m.run(ctx, path, "--state")
		if runErr == nil && strings.TrimSpace(strings.ToLower(string(output))) == "running" {
			return firewallBackend{kind: "firewalld", id: "firewalld", path: path}, nil
		}
	}
	if path, err := m.lookPath("nft"); err == nil {
		var candidates []firewallBackend
		for _, candidate := range []firewallBackend{{family: "inet", table: "filter", chain: "input"}, {family: "ip", table: "filter", chain: "input"}, {family: "ip6", table: "filter", chain: "input"}} {
			if _, runErr := m.run(ctx, path, "list", "chain", candidate.family, candidate.table, candidate.chain); runErr == nil {
				candidates = append(candidates, candidate)
			}
		}
		if output, runErr := m.run(ctx, path, "list", "ruleset"); runErr == nil {
			candidates = append(candidates, parseNftInputChains(output)...)
		}
		if backend, ok := selectNftBackend(path, candidates); ok {
			return backend, nil
		}
	}
	if path, err := m.lookPath("iptables"); err == nil {
		backend := firewallBackend{kind: "iptables", id: "iptables", path: path, family: "ipv4"}
		if path6, pathErr := m.lookPath("ip6tables"); pathErr == nil {
			backend.path6 = path6
		}
		return backend, nil
	}
	if path, err := m.lookPath("ip6tables"); err == nil {
		return firewallBackend{kind: "iptables", id: "iptables", path: path, family: "ipv6"}, nil
	}
	return firewallBackend{}, nil
}

func parseNftInputChains(output []byte) []firewallBackend {
	var currentFamily, currentTable, currentChain string
	var candidates []firewallBackend
	for _, raw := range strings.Split(string(output), "\n") {
		line := strings.TrimSpace(raw)
		fields := strings.Fields(line)
		if len(fields) >= 4 && fields[0] == "table" && fields[len(fields)-1] == "{" {
			currentFamily, currentTable, currentChain = fields[1], fields[2], ""
			continue
		}
		if len(fields) >= 3 && fields[0] == "chain" && fields[len(fields)-1] == "{" && currentTable != "" {
			currentChain = fields[1]
			continue
		}
		if currentChain != "" && strings.Contains(line, "hook input") {
			candidates = append(candidates, firewallBackend{family: currentFamily, table: currentTable, chain: currentChain})
			currentChain = ""
		}
	}
	return candidates
}

func selectNftBackend(path string, candidates []firewallBackend) (firewallBackend, bool) {
	seen := make(map[string]bool)
	unique := make([]firewallBackend, 0, len(candidates))
	for _, candidate := range candidates {
		key := candidate.family + ":" + candidate.table + ":" + candidate.chain
		if seen[key] {
			continue
		}
		seen[key] = true
		unique = append(unique, candidate)
	}
	for _, candidate := range unique {
		if candidate.family == "inet" && candidate.table == "filter" && candidate.chain == "input" {
			candidate.kind, candidate.path = "nftables", path
			candidate.id = "nftables:" + candidate.family + ":" + candidate.table + ":" + candidate.chain
			return candidate, true
		}
	}
	for _, candidate := range unique {
		if candidate.family == "inet" {
			candidate.kind, candidate.path = "nftables", path
			candidate.id = "nftables:" + candidate.family + ":" + candidate.table + ":" + candidate.chain
			return candidate, true
		}
	}
	var ipv4, ipv6 firewallBackend
	for _, candidate := range unique {
		switch candidate.family {
		case "ip":
			if ipv4.family == "" {
				ipv4 = candidate
			}
		case "ip6":
			if ipv6.family == "" {
				ipv6 = candidate
			}
		}
	}
	primary := ipv4
	if primary.family == "" {
		primary = ipv6
	}
	if primary.family == "" {
		return firewallBackend{}, false
	}
	primary.kind, primary.path = "nftables", path
	primary.id = "nftables:" + primary.family + ":" + primary.table + ":" + primary.chain
	if ipv4.family != "" && ipv6.family != "" {
		primary.family, primary.table, primary.chain = ipv4.family, ipv4.table, ipv4.chain
		primary.family6, primary.table6, primary.chain6 = ipv6.family, ipv6.table, ipv6.chain
		primary.id = "nftables:" + ipv4.family + ":" + ipv4.table + ":" + ipv4.chain + "+" + ipv6.family + ":" + ipv6.table + ":" + ipv6.chain
	}
	return primary, true
}

func (m *firewallManager) syncUFW(ctx context.Context, path string, desired map[string]firewallPort, managed map[string]firewallRule) (map[string]bool, error) {
	output, err := m.run(ctx, path, "status", "numbered")
	if err != nil {
		return nil, commandError("ufw status numbered", output, err)
	}
	rules := parseUFWRules(output)
	for key, port := range desired {
		if ufwHasTaggedRule(rules, port) {
			continue
		}
		if ufwHasAllowedRule(rules, port) {
			continue
		}
		tag := firewallRuleTag(port)
		result, allowErr := m.run(ctx, path, "allow", fmt.Sprintf("%d/%s", port.Port, port.Protocol), "comment", tag)
		if allowErr != nil {
			return nil, commandError("ufw allow "+key, result, allowErr)
		}
		output, err = m.run(ctx, path, "status", "numbered")
		if err != nil {
			return nil, commandError("ufw status numbered", output, err)
		}
		rules = parseUFWRules(output)
	}
	for key, rule := range managed {
		if _, exists := desired[key]; exists {
			continue
		}
		for {
			matched := ufwTaggedRules(rules, rule)
			if len(matched) == 0 {
				break
			}
			result, deleteErr := m.run(ctx, path, "--force", "delete", strconv.Itoa(matched[0].number))
			if deleteErr != nil {
				return nil, commandError("ufw delete "+key, result, deleteErr)
			}
			output, err = m.run(ctx, path, "status", "numbered")
			if err != nil {
				return nil, commandError("ufw status numbered", output, err)
			}
			rules = parseUFWRules(output)
		}
	}
	owned := make(map[string]bool)
	for key, port := range desired {
		if ufwHasTaggedRule(rules, port) {
			owned[key] = true
		}
	}
	return owned, nil
}

type ufwRule struct {
	number   int
	port     int
	protocol string
	allowed  bool
	comment  string
}

var ufwRulePattern = regexp.MustCompile(`^\[\s*([0-9]+)\]\s+(\S+)\s+(.*)$`)

func parseUFWRules(output []byte) []ufwRule {
	result := make([]ufwRule, 0)
	for _, line := range strings.Split(string(output), "\n") {
		matches := ufwRulePattern.FindStringSubmatch(strings.TrimSpace(line))
		if len(matches) != 4 {
			continue
		}
		number, _ := strconv.Atoi(matches[1])
		endpoint := strings.TrimSuffix(matches[2], "(v6)")
		endpoint = strings.TrimSpace(endpoint)
		protocol := ""
		if slash := strings.IndexByte(endpoint, '/'); slash >= 0 {
			protocol = strings.ToLower(endpoint[slash+1:])
			endpoint = endpoint[:slash]
		}
		port, err := strconv.Atoi(endpoint)
		if err != nil || port < 1 || port > 65535 {
			continue
		}
		comment := ""
		if marker := strings.Index(matches[3], "#"); marker >= 0 {
			comment = strings.TrimSpace(matches[3][marker+1:])
		}
		result = append(result, ufwRule{number: number, port: port, protocol: protocol, allowed: strings.Contains(strings.ToUpper(matches[3]), "ALLOW"), comment: comment})
	}
	return result
}

func ufwHasAllowedRule(rules []ufwRule, port firewallPort) bool {
	for _, rule := range rules {
		if rule.allowed && rule.port == port.Port && (rule.protocol == "" || rule.protocol == port.Protocol) {
			return true
		}
	}
	return false
}

func ufwHasTaggedRule(rules []ufwRule, port firewallPort) bool {
	for _, rule := range rules {
		if rule.allowed && rule.port == port.Port && rule.protocol == port.Protocol && strings.HasPrefix(rule.comment, firewallRulePrefix) {
			return true
		}
	}
	return false
}

func ufwTaggedRules(rules []ufwRule, owner firewallRule) []ufwRule {
	result := make([]ufwRule, 0)
	for _, rule := range rules {
		if rule.allowed && rule.port == owner.Port && rule.protocol == owner.Protocol && strings.HasPrefix(rule.comment, firewallRulePrefix) {
			result = append(result, rule)
		}
	}
	return result
}

func (m *firewallManager) syncFirewalld(ctx context.Context, path string, desired map[string]firewallPort, managed map[string]firewallRule) (map[string]bool, error) {
	owned := make(map[string]bool)
	for key, port := range desired {
		_, queryErr := m.run(ctx, path, "--query-port", fmt.Sprintf("%d/%s", port.Port, port.Protocol))
		if queryErr != nil {
			result, addErr := m.run(ctx, path, "--add-port", fmt.Sprintf("%d/%s", port.Port, port.Protocol))
			if addErr != nil {
				return nil, commandError("firewall-cmd --add-port "+key, result, addErr)
			}
			owned[key] = true
		} else if _, exists := managed[key]; exists {
			owned[key] = true
		}
	}
	for key, rule := range managed {
		if _, exists := desired[key]; exists {
			continue
		}
		result, removeErr := m.run(ctx, path, "--remove-port", fmt.Sprintf("%d/%s", rule.Port, rule.Protocol))
		if removeErr != nil {
			// firewalld returns failure when the port was already removed.
			if !strings.Contains(strings.ToLower(string(result)), "not enabled") {
				return nil, commandError("firewall-cmd --remove-port "+key, result, removeErr)
			}
		}
	}
	return owned, nil
}

func (m *firewallManager) syncNftables(ctx context.Context, backend firewallBackend, desired map[string]firewallPort, managed map[string]firewallRule) (map[string]bool, error) {
	owned, err := m.syncNftablesChain(ctx, backend, desired, managed)
	if err != nil {
		return nil, err
	}
	if backend.family6 != "" {
		backend6 := backend
		backend6.family, backend6.table, backend6.chain = backend.family6, backend.table6, backend.chain6
		owned6, chainErr := m.syncNftablesChain(ctx, backend6, desired, managed)
		if chainErr != nil {
			return nil, chainErr
		}
		for key := range owned6 {
			owned[key] = true
		}
	}
	return owned, nil
}

func (m *firewallManager) syncNftablesChain(ctx context.Context, backend firewallBackend, desired map[string]firewallPort, managed map[string]firewallRule) (map[string]bool, error) {
	args := []string{"-a", "list", "chain", backend.family, backend.table, backend.chain}
	output, err := m.run(ctx, backend.path, args...)
	if err != nil {
		return nil, commandError("nft list chain", output, err)
	}
	rules := parseNftRules(output)
	for key, port := range desired {
		if nftHasTaggedRule(rules, port) || nftHasAllowedRule(rules, port) {
			continue
		}
		tag := backendRuleTag(backend, port)
		result, addErr := m.run(ctx, backend.path, "add", "rule", backend.family, backend.table, backend.chain, port.Protocol, "dport", strconv.Itoa(port.Port), "counter", "accept", "comment", tag)
		if addErr != nil {
			return nil, commandError("nft add rule "+key, result, addErr)
		}
		output, err = m.run(ctx, backend.path, args...)
		if err != nil {
			return nil, commandError("nft list chain", output, err)
		}
		rules = parseNftRules(output)
	}
	for key, rule := range managed {
		if _, exists := desired[key]; exists {
			continue
		}
		for _, nftRule := range nftTaggedRules(rules, rule) {
			result, deleteErr := m.run(ctx, backend.path, "delete", "rule", backend.family, backend.table, backend.chain, "handle", strconv.Itoa(nftRule.handle))
			if deleteErr != nil {
				return nil, commandError("nft delete rule "+key, result, deleteErr)
			}
		}
		output, err = m.run(ctx, backend.path, args...)
		if err != nil {
			return nil, commandError("nft list chain", output, err)
		}
		rules = parseNftRules(output)
	}
	owned := make(map[string]bool)
	for key, port := range desired {
		if nftHasTaggedRule(rules, port) {
			owned[key] = true
		}
	}
	return owned, nil
}

type nftRule struct {
	handle   int
	port     int
	protocol string
	allowed  bool
	comment  string
}

var (
	nftPortPattern    = regexp.MustCompile(`(?m)^\s*(tcp|udp)\s+dport\s+([0-9]+)(.*?)#\s*handle\s+([0-9]+)\s*$`)
	nftCommentPattern = regexp.MustCompile(`comment\s+"([^"]+)"`)
)

func parseNftRules(output []byte) []nftRule {
	result := make([]nftRule, 0)
	for _, matches := range nftPortPattern.FindAllStringSubmatch(string(output), -1) {
		port, _ := strconv.Atoi(matches[2])
		handle, _ := strconv.Atoi(matches[4])
		comment := ""
		if commentMatches := nftCommentPattern.FindStringSubmatch(matches[3]); len(commentMatches) == 2 {
			comment = commentMatches[1]
		}
		result = append(result, nftRule{handle: handle, port: port, protocol: matches[1], allowed: strings.Contains(strings.ToLower(matches[3]), "accept"), comment: comment})
	}
	return result
}

func nftHasAllowedRule(rules []nftRule, port firewallPort) bool {
	for _, rule := range rules {
		if rule.allowed && rule.port == port.Port && rule.protocol == port.Protocol {
			return true
		}
	}
	return false
}

func nftHasTaggedRule(rules []nftRule, port firewallPort) bool {
	for _, rule := range rules {
		if rule.allowed && rule.port == port.Port && rule.protocol == port.Protocol && isManagedRuleTag(rule.comment) {
			return true
		}
	}
	return false
}

func nftTaggedRules(rules []nftRule, owner firewallRule) []nftRule {
	result := make([]nftRule, 0)
	for _, rule := range rules {
		if rule.allowed && rule.port == owner.Port && rule.protocol == owner.Protocol && isManagedRuleTag(rule.comment) {
			result = append(result, rule)
		}
	}
	return result
}

func (m *firewallManager) syncIPTables(ctx context.Context, backend firewallBackend, desired map[string]firewallPort, managed map[string]firewallRule) (map[string]bool, error) {
	owned, err := m.syncIPTablesPath(ctx, backend, desired, managed)
	if err != nil {
		return nil, err
	}
	if backend.path6 != "" {
		backend6 := backend
		backend6.path = backend.path6
		owned6, pathErr := m.syncIPTablesPath(ctx, backend6, desired, managed)
		if pathErr != nil {
			return nil, pathErr
		}
		for key := range owned6 {
			owned[key] = true
		}
	}
	return owned, nil
}

func (m *firewallManager) syncIPTablesPath(ctx context.Context, backend firewallBackend, desired map[string]firewallPort, managed map[string]firewallRule) (map[string]bool, error) {
	path := backend.path
	output, err := m.run(ctx, path, "-S", "INPUT")
	if err != nil {
		return nil, commandError(path+" -S INPUT", output, err)
	}
	rules := string(output)
	for key, port := range desired {
		tag := backendRuleTag(backend, port)
		if iptablesHasTaggedRule(rules, port, tag) || iptablesHasAllowedRule(rules, port) {
			continue
		}
		result, addErr := m.run(ctx, backend.path, "-I", "INPUT", "1", "-p", port.Protocol, "--dport", strconv.Itoa(port.Port), "-m", "comment", "--comment", tag, "-j", "ACCEPT")
		if addErr != nil {
			return nil, commandError(backend.path+" add "+key, result, addErr)
		}
		output, err = m.run(ctx, backend.path, "-S", "INPUT")
		if err != nil {
			return nil, commandError(backend.path+" -S INPUT", output, err)
		}
		rules = string(output)
	}
	for key, rule := range managed {
		if _, exists := desired[key]; exists || !iptablesHasTaggedRule(rules, firewallPort{Port: rule.Port, Protocol: rule.Protocol}, rule.Tag) {
			continue
		}
		result, removeErr := m.run(ctx, backend.path, "-D", "INPUT", "-p", rule.Protocol, "--dport", strconv.Itoa(rule.Port), "-m", "comment", "--comment", rule.Tag, "-j", "ACCEPT")
		if removeErr != nil {
			return nil, commandError(backend.path+" delete "+key, result, removeErr)
		}
		output, err = m.run(ctx, backend.path, "-S", "INPUT")
		if err != nil {
			return nil, commandError(backend.path+" -S INPUT", output, err)
		}
		rules = string(output)
	}
	owned := make(map[string]bool)
	for key, port := range desired {
		if iptablesHasTaggedRule(rules, port, backendRuleTag(backend, port)) {
			owned[key] = true
		}
	}
	return owned, nil
}

func iptablesHasTaggedRule(output string, port firewallPort, tag string) bool {
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "-p "+port.Protocol) && strings.Contains(line, "--dport "+strconv.Itoa(port.Port)) && strings.Contains(line, "--comment "+tag) && strings.Contains(line, "-j ACCEPT") {
			return true
		}
	}
	return false
}

func iptablesHasAllowedRule(output string, port firewallPort) bool {
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "-p "+port.Protocol) && strings.Contains(line, "--dport "+strconv.Itoa(port.Port)) && strings.Contains(line, "-j ACCEPT") {
			return true
		}
	}
	return false
}

func requiredFirewallPorts(config protocol.AgentConfig) map[string]firewallPort {
	result := make(map[string]firewallPort)
	for _, service := range config.Services {
		if !service.Enabled {
			continue
		}
		host, portText, err := net.SplitHostPort(strings.TrimSpace(service.Listen))
		if err != nil || !externallyBound(host) {
			continue
		}
		port, err := strconv.Atoi(portText)
		if err != nil || port < 1 || port > 65535 {
			continue
		}
		for _, protocol := range listenerProtocols(service.Network) {
			item := firewallPort{Port: port, Protocol: protocol}
			result[firewallRuleKey(port, protocol)] = item
		}
	}
	return result
}

func listenerProtocols(network string) []string {
	switch strings.ToLower(strings.TrimSpace(network)) {
	case "tcp":
		return []string{"tcp"}
	case "udp":
		return []string{"udp"}
	case "tcp+udp":
		return []string{"tcp", "udp"}
	default:
		return nil
	}
}

func externallyBound(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		return true
	}
	ip := net.ParseIP(host)
	return ip == nil || !ip.IsLoopback()
}

func firewallRuleKey(port int, protocol string) string {
	return strconv.Itoa(port) + "/" + strings.ToLower(protocol)
}

func firewallRuleTag(port firewallPort) string {
	return fmt.Sprintf("%s %d %s", firewallRulePrefix, port.Port, strings.ToUpper(port.Protocol))
}

func nftRuleTag(port firewallPort) string {
	return fmt.Sprintf("cdt-relay-%d-%s", port.Port, strings.ToLower(port.Protocol))
}

func iptablesRuleTag(port firewallPort) string {
	return fmt.Sprintf("cdt-relay-%d-%s", port.Port, strings.ToLower(port.Protocol))
}

func backendRuleTag(backend firewallBackend, port firewallPort) string {
	switch backend.kind {
	case "nftables":
		return nftRuleTag(port)
	case "iptables":
		return iptablesRuleTag(port)
	default:
		return firewallRuleTag(port)
	}
}

func isManagedRuleTag(tag string) bool {
	return strings.HasPrefix(tag, firewallRulePrefix) || strings.HasPrefix(tag, "cdt-relay-")
}

func dedupeFirewallRules(rules []firewallRule) []firewallRule {
	seen := make(map[string]struct{})
	result := make([]firewallRule, 0, len(rules))
	for _, rule := range rules {
		key := rule.Backend + ":" + firewallRuleKey(rule.Port, rule.Protocol)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, rule)
	}
	return result
}

func commandError(name string, output []byte, err error) error {
	message := strings.TrimSpace(string(output))
	if message == "" {
		return fmt.Errorf("%s: %w", name, err)
	}
	return fmt.Errorf("%s: %w: %s", name, err, message)
}
