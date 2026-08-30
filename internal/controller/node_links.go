package controller

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

type parsedNodeLink struct {
	Protocol string
	Address  string
	Port     int
	Network  string
	Name     string
}

type relayLink struct {
	ServiceID     string `json:"service_id"`
	ServiceName   string `json:"service_name"`
	RelayNodeID   string `json:"relay_node_id"`
	RelayNodeName string `json:"relay_node_name"`
	Host          string `json:"host,omitempty"`
	Port          int    `json:"port"`
	URI           string `json:"uri,omitempty"`
	Available     bool   `json:"available"`
	Message       string `json:"message,omitempty"`
}

func (s *Store) LandingRelayLinks(ctx context.Context, landingID string) ([]relayLink, error) {
	var shareURI string
	if err := s.db.QueryRowContext(ctx, `SELECT share_uri FROM landing_nodes WHERE id=?`, landingID).Scan(&shareURI); err != nil {
		return nil, err
	}
	if strings.TrimSpace(shareURI) == "" {
		return []relayLink{}, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT rs.id,rs.name,rs.listen_host,rs.listen_port,rn.id,rn.name,COALESCE(rn.public_ip,''),rn.status
		FROM relay_services rs JOIN service_targets st ON st.service_id=rs.id
		JOIN relay_nodes rn ON rn.id=rs.relay_node_id
		JOIN landing_nodes ln ON ln.id=st.landing_node_id
		WHERE st.landing_node_id=? AND st.enabled=1 AND ln.enabled=1 AND rs.enabled=1
		ORDER BY rs.listen_port,rs.created_at`, landingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	links := make([]relayLink, 0)
	for rows.Next() {
		var link relayLink
		var listenHost, relayIP, relayStatus string
		if err := rows.Scan(&link.ServiceID, &link.ServiceName, &listenHost, &link.Port, &link.RelayNodeID, &link.RelayNodeName, &relayIP, &relayStatus); err != nil {
			return nil, err
		}
		link.Host = strings.TrimSpace(relayIP)
		if link.Host == "" || link.Host == "0.0.0.0" || link.Host == "::" {
			link.Host = strings.TrimSpace(listenHost)
		}
		if link.Host == "" || link.Host == "0.0.0.0" || link.Host == "::" {
			link.Message = "中转节点尚未上报公网 IP"
			links = append(links, link)
			continue
		}
		generated, err := replaceNodeEndpoint(shareURI, link.Host, link.Port)
		if err != nil {
			link.Message = "节点链接无法生成中转版本: " + err.Error()
			links = append(links, link)
			continue
		}
		link.URI = generated
		link.Available = strings.EqualFold(relayStatus, "online")
		if !link.Available {
			link.Message = "中转 Agent 当前离线，链接将在 Agent 上线后生效"
		}
		links = append(links, link)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return links, nil
}

func parseNodeLink(raw string) (parsedNodeLink, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return parsedNodeLink{}, errors.New("完整节点链接不能为空")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return parsedNodeLink{}, fmt.Errorf("节点链接格式无效: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme == "vmess" {
		return parseVMessLink(raw)
	}
	if scheme == "ss" {
		return parseSSLink(raw)
	}
	if !oneOf(scheme, "vless", "trojan", "hysteria2", "hy2", "tuic") {
		return parsedNodeLink{}, fmt.Errorf("暂不支持 %q 节点格式", scheme)
	}
	address, port, err := urlEndpoint(u)
	if err != nil {
		return parsedNodeLink{}, err
	}
	network := "tcp"
	if scheme == "hysteria2" || scheme == "hy2" || scheme == "tuic" {
		network = "udp"
	}
	return parsedNodeLink{Protocol: scheme, Address: address, Port: port, Network: network, Name: fragmentName(u.Fragment)}, nil
}

func defaultNodeName(node parsedNodeLink) string {
	if node.Protocol == "" || node.Address == "" || node.Port <= 0 {
		return "落地节点"
	}
	return fmt.Sprintf("%s · %s:%d", strings.ToUpper(node.Protocol), node.Address, node.Port)
}

func parseSSLink(raw string) (parsedNodeLink, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return parsedNodeLink{}, fmt.Errorf("SS 节点链接格式无效: %w", err)
	}
	if u.Hostname() != "" && u.Port() != "" {
		address, port, err := urlEndpoint(u)
		if err != nil {
			return parsedNodeLink{}, err
		}
		return parsedNodeLink{Protocol: "ss", Address: address, Port: port, Network: "tcp+udp", Name: fragmentName(u.Fragment)}, nil
	}
	payload, fragment := splitFragment(strings.TrimPrefix(raw, "ss://"))
	decoded, err := decodeBase64(payload)
	if err != nil {
		return parsedNodeLink{}, errors.New("SS 节点链接缺少可解析的地址")
	}
	decodedURL, err := url.Parse("ss://" + decoded)
	if err != nil {
		return parsedNodeLink{}, fmt.Errorf("SS 节点内容无效: %w", err)
	}
	address, port, err := urlEndpoint(decodedURL)
	if err != nil {
		return parsedNodeLink{}, err
	}
	if fragment == "" {
		fragment = decodedURL.Fragment
	}
	return parsedNodeLink{Protocol: "ss", Address: address, Port: port, Network: "tcp+udp", Name: fragmentName(fragment)}, nil
}

func parseVMessLink(raw string) (parsedNodeLink, error) {
	payload := strings.TrimPrefix(strings.TrimSpace(raw), "vmess://")
	decoded, err := decodeBase64(payload)
	if err != nil {
		return parsedNodeLink{}, errors.New("VMess 节点内容不是有效 Base64")
	}
	var node map[string]interface{}
	if err := json.Unmarshal([]byte(decoded), &node); err != nil {
		return parsedNodeLink{}, fmt.Errorf("VMess 节点 JSON 无效: %w", err)
	}
	address, _ := nodeString(node, "add")
	if address == "" {
		address, _ = nodeString(node, "address")
	}
	portValue, ok := node["port"]
	if !ok {
		return parsedNodeLink{}, errors.New("VMess 节点缺少端口")
	}
	port, err := interfacePort(portValue)
	if err != nil {
		return parsedNodeLink{}, err
	}
	if net.ParseIP(address) == nil && strings.TrimSpace(address) == "" {
		return parsedNodeLink{}, errors.New("VMess 节点缺少地址")
	}
	name, _ := nodeString(node, "ps")
	return parsedNodeLink{Protocol: "vmess", Address: address, Port: port, Network: "tcp", Name: name}, nil
}

func replaceNodeEndpoint(raw, host string, port int) (string, error) {
	parsed, err := parseNodeLink(raw)
	if err != nil {
		return "", err
	}
	host = strings.TrimSpace(host)
	if host == "" || port < 1 || port > 65535 {
		return "", errors.New("中转入口地址或端口无效")
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	if parsed.Protocol == "vmess" {
		return replaceVMessEndpoint(raw, host, port)
	}
	if parsed.Protocol == "ss" && u.Port() == "" {
		payload, fragment := splitFragment(strings.TrimPrefix(strings.TrimSpace(raw), "ss://"))
		decoded, err := decodeBase64(payload)
		if err != nil {
			return "", err
		}
		decodedURL, err := url.Parse("ss://" + decoded)
		if err != nil {
			return "", err
		}
		decodedURL.Host = net.JoinHostPort(host, strconv.Itoa(port))
		encoded := base64.RawStdEncoding.EncodeToString([]byte(strings.TrimPrefix(decodedURL.String(), "ss://")))
		return "ss://" + encoded + fragment, nil
	}
	u.Host = net.JoinHostPort(host, strconv.Itoa(port))
	return u.String(), nil
}

func replaceVMessEndpoint(raw, host string, port int) (string, error) {
	payload := strings.TrimPrefix(strings.TrimSpace(raw), "vmess://")
	decoded, err := decodeBase64(payload)
	if err != nil {
		return "", err
	}
	var node map[string]interface{}
	if err := json.Unmarshal([]byte(decoded), &node); err != nil {
		return "", err
	}
	if _, exists := node["add"]; exists {
		node["add"] = host
	} else {
		node["address"] = host
	}
	if _, ok := node["port"].(string); ok {
		node["port"] = strconv.Itoa(port)
	} else {
		node["port"] = port
	}
	encodedJSON, err := json.Marshal(node)
	if err != nil {
		return "", err
	}
	return "vmess://" + base64.RawStdEncoding.EncodeToString(encodedJSON), nil
}

func urlEndpoint(u *url.URL) (string, int, error) {
	address := strings.TrimSpace(u.Hostname())
	if address == "" {
		return "", 0, errors.New("节点链接缺少服务器地址")
	}
	portText := strings.TrimSpace(u.Port())
	if portText == "" {
		return "", 0, errors.New("节点链接缺少服务器端口")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, errors.New("节点服务器端口无效")
	}
	return address, port, nil
}

func fragmentName(value string) string {
	if decoded, err := url.QueryUnescape(value); err == nil {
		value = decoded
	}
	return strings.TrimSpace(value)
}

func splitFragment(value string) (string, string) {
	index := strings.IndexByte(value, '#')
	if index < 0 {
		return value, ""
	}
	return value[:index], value[index:]
}

func decodeBase64(value string) (string, error) {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "-", "+")
	value = strings.ReplaceAll(value, "_", "/")
	for len(value)%4 != 0 {
		value += "="
	}
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding} {
		decoded, err := encoding.DecodeString(value)
		if err == nil && bytes.IndexByte(decoded, 0) < 0 {
			return string(decoded), nil
		}
	}
	return "", errors.New("base64 decode failed")
}

func nodeString(node map[string]interface{}, key string) (string, bool) {
	value, ok := node[key]
	if !ok {
		return "", false
	}
	text, ok := value.(string)
	return strings.TrimSpace(text), ok
}

func interfacePort(value interface{}) (int, error) {
	switch typed := value.(type) {
	case float64:
		port := int(typed)
		if typed != float64(port) {
			return 0, errors.New("节点服务器端口无效")
		}
		if port < 1 || port > 65535 {
			return 0, errors.New("节点服务器端口无效")
		}
		return port, nil
	case string:
		port, err := strconv.Atoi(strings.TrimSpace(typed))
		if err != nil || port < 1 || port > 65535 {
			return 0, errors.New("节点服务器端口无效")
		}
		return port, nil
	default:
		return 0, errors.New("节点服务器端口无效")
	}
}
