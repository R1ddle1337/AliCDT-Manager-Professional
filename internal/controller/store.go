package controller

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/protocol"
	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

type RelayNode struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	PublicIP        string          `json:"public_ip"`
	Architecture    string          `json:"architecture"`
	OS              string          `json:"os"`
	AgentVersion    string          `json:"agent_version"`
	Status          string          `json:"status"`
	LastSeenAt      *time.Time      `json:"last_seen_at,omitempty"`
	CurrentRevision int64           `json:"current_revision"`
	DesiredRevision int64           `json:"desired_revision"`
	Services        json.RawMessage `json:"service_status,omitempty"`
}

type LandingNode struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Address   string    `json:"address"`
	Port      int       `json:"port"`
	Network   string    `json:"network"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
}

type RelayService struct {
	ID                    string          `json:"id"`
	RelayNodeID           string          `json:"relay_node_id"`
	Name                  string          `json:"name"`
	ListenHost            string          `json:"listen_host"`
	ListenPort            int             `json:"listen_port"`
	Network               string          `json:"network"`
	Mode                  string          `json:"mode"`
	Enabled               bool            `json:"enabled"`
	DialTimeoutMillis     int             `json:"dial_timeout_ms"`
	UDPIdleTimeoutSeconds int             `json:"udp_idle_timeout_seconds"`
	Health                HealthSettings  `json:"health"`
	Targets               []ServiceTarget `json:"targets"`
	CreatedAt             time.Time       `json:"created_at"`
	UpdatedAt             time.Time       `json:"updated_at"`
}

type RelayEvent struct {
	ID          string    `json:"id"`
	RelayNodeID string    `json:"relay_node_id,omitempty"`
	Level       string    `json:"level"`
	Category    string    `json:"category"`
	Message     string    `json:"message"`
	CreatedAt   time.Time `json:"created_at"`
}

type HealthSettings struct {
	Enabled              bool `json:"enabled"`
	IntervalSeconds      int  `json:"interval_seconds"`
	TimeoutMillis        int  `json:"timeout_ms"`
	FailureThreshold     int  `json:"failure_threshold"`
	SuccessThreshold     int  `json:"success_threshold"`
	RecoveryCooldownSecs int  `json:"recovery_cooldown_seconds"`
}

type ServiceTarget struct {
	ID            string `json:"id"`
	LandingNodeID string `json:"landing_node_id"`
	Name          string `json:"name"`
	Address       string `json:"address"`
	Port          int    `json:"port"`
	Weight        int    `json:"weight"`
	Priority      int    `json:"priority"`
	Enabled       bool   `json:"enabled"`
}

type CreateLandingNodeRequest struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	Port    int    `json:"port"`
	Network string `json:"network"`
	Enabled *bool  `json:"enabled,omitempty"`
}

type CreateRelayServiceRequest struct {
	RelayNodeID           string                `json:"relay_node_id"`
	Name                  string                `json:"name"`
	ListenHost            string                `json:"listen_host"`
	ListenPort            int                   `json:"listen_port"`
	Network               string                `json:"network"`
	Mode                  string                `json:"mode"`
	Enabled               *bool                 `json:"enabled,omitempty"`
	DialTimeoutMillis     int                   `json:"dial_timeout_ms"`
	UDPIdleTimeoutSeconds int                   `json:"udp_idle_timeout_seconds"`
	Health                HealthSettings        `json:"health"`
	Targets               []CreateServiceTarget `json:"targets"`
}

type CreateServiceTarget struct {
	LandingNodeID string `json:"landing_node_id"`
	Weight        int    `json:"weight"`
	Priority      int    `json:"priority"`
	Enabled       *bool  `json:"enabled,omitempty"`
}

func OpenStore(path string) (*Store, error) {
	if path == "" {
		path = "/app/data/guard.db"
	}
	dsn := path
	if path != ":memory:" && !strings.HasPrefix(path, "file:") {
		dsn = "file:" + path
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db}
	if err := store.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	statements := []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA busy_timeout=5000`,
		`PRAGMA foreign_keys=ON`,
		`CREATE TABLE IF NOT EXISTS relay_nodes (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			public_ip TEXT NOT NULL DEFAULT '',
			architecture TEXT NOT NULL DEFAULT '',
			os TEXT NOT NULL DEFAULT '',
			agent_version TEXT NOT NULL DEFAULT '',
			secret_hash TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'offline',
			last_seen_at TEXT,
			current_revision INTEGER NOT NULL DEFAULT 0,
			desired_revision INTEGER NOT NULL DEFAULT 0,
			service_status_json TEXT NOT NULL DEFAULT '[]',
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS admin_sessions (
			token_hash TEXT PRIMARY KEY,
			username TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS enrollment_tokens (
			token_hash TEXT PRIMARY KEY,
			expires_at TEXT NOT NULL,
			used_at TEXT,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS landing_nodes (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			address TEXT NOT NULL,
			port INTEGER NOT NULL,
			network TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS relay_services (
			id TEXT PRIMARY KEY,
			relay_node_id TEXT NOT NULL REFERENCES relay_nodes(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			listen_host TEXT NOT NULL DEFAULT '0.0.0.0',
			listen_port INTEGER NOT NULL,
			network TEXT NOT NULL,
			mode TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			dial_timeout_ms INTEGER NOT NULL DEFAULT 2500,
			udp_idle_timeout_seconds INTEGER NOT NULL DEFAULT 60,
			health_enabled INTEGER NOT NULL DEFAULT 1,
			health_interval_seconds INTEGER NOT NULL DEFAULT 4,
			health_timeout_ms INTEGER NOT NULL DEFAULT 2000,
			failure_threshold INTEGER NOT NULL DEFAULT 2,
			success_threshold INTEGER NOT NULL DEFAULT 3,
			recovery_cooldown_seconds INTEGER NOT NULL DEFAULT 60,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			UNIQUE(relay_node_id, listen_host, listen_port, network)
		)`,
		`CREATE TABLE IF NOT EXISTS service_targets (
			id TEXT PRIMARY KEY,
			service_id TEXT NOT NULL REFERENCES relay_services(id) ON DELETE CASCADE,
			landing_node_id TEXT NOT NULL REFERENCES landing_nodes(id) ON DELETE RESTRICT,
			weight INTEGER NOT NULL DEFAULT 1,
			priority INTEGER NOT NULL DEFAULT 0,
			enabled INTEGER NOT NULL DEFAULT 1,
			UNIQUE(service_id, landing_node_id)
		)`,
		`CREATE TABLE IF NOT EXISTS relay_events (
			id TEXT PRIMARY KEY,
			relay_node_id TEXT,
			level TEXT NOT NULL,
			category TEXT NOT NULL,
			message TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("migration failed: %w", err)
		}
	}
	return nil
}

func (s *Store) IsAdminInitialized(ctx context.Context) (bool, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key='admin_password_hash'`).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil && value != "", err
}

func (s *Store) InitAdmin(ctx context.Context, username, password string) (string, error) {
	initialized, err := s.IsAdminInitialized(ctx)
	if err != nil {
		return "", err
	}
	if initialized {
		return "", errors.New("administrator is already initialized")
	}
	username = strings.TrimSpace(username)
	if username == "" || len(password) < 8 {
		return "", errors.New("username and password of at least 8 characters are required")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES('admin_username',?),('admin_password_hash',?)`, username, string(hash)); err != nil {
		return "", err
	}
	token, err := createAdminSession(ctx, tx, username)
	if err != nil {
		return "", err
	}
	return token, tx.Commit()
}

func (s *Store) LoginAdmin(ctx context.Context, username, password string) (string, error) {
	var storedUsername, passwordHash string
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key='admin_username'`).Scan(&storedUsername); err != nil {
		return "", errors.New("administrator is not initialized")
	}
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key='admin_password_hash'`).Scan(&passwordHash); err != nil {
		return "", errors.New("administrator is not initialized")
	}
	if subtleStringCompare(username, storedUsername) == false || bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)) != nil {
		return "", errors.New("invalid username or password")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	token, err := createAdminSession(ctx, tx, storedUsername)
	if err != nil {
		return "", err
	}
	return token, tx.Commit()
}

func (s *Store) AuthenticateAdminSession(ctx context.Context, token string) error {
	if token == "" {
		return errors.New("missing session")
	}
	var expires string
	if err := s.db.QueryRowContext(ctx, `SELECT expires_at FROM admin_sessions WHERE token_hash=?`, hashSecret(token)).Scan(&expires); err != nil {
		return errors.New("invalid session")
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, expires)
	if err != nil || time.Now().UTC().After(expiresAt) {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM admin_sessions WHERE token_hash=?`, hashSecret(token))
		return errors.New("session expired")
	}
	return nil
}

func createAdminSession(ctx context.Context, tx *sql.Tx, username string) (string, error) {
	raw := randomSecret(32)
	now := time.Now().UTC()
	_, err := tx.ExecContext(ctx, `INSERT INTO admin_sessions(token_hash,username,expires_at,created_at) VALUES(?,?,?,?)`, hashSecret(raw), username, now.Add(7*24*time.Hour).Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	return raw, err
}

func subtleStringCompare(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	var result byte
	for index := range left {
		result |= left[index] ^ right[index]
	}
	return result == 0
}

func (s *Store) CreateEnrollmentToken(ctx context.Context, raw string, ttl time.Duration) error {
	if raw == "" {
		return errors.New("token is required")
	}
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO enrollment_tokens(token_hash, expires_at, used_at, created_at) VALUES(?, ?, NULL, ?)`,
		hashSecret(raw), now.Add(ttl).Format(time.RFC3339Nano), now.Format(time.RFC3339Nano),
	)
	return err
}

func (s *Store) EnrollAgent(ctx context.Context, request protocol.AgentEnrollmentRequest) (protocol.AgentEnrollmentResponse, error) {
	if request.Token == "" || strings.TrimSpace(request.NodeName) == "" {
		return protocol.AgentEnrollmentResponse{}, errors.New("token and node name are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return protocol.AgentEnrollmentResponse{}, err
	}
	defer tx.Rollback()
	var expires string
	var used sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT expires_at, used_at FROM enrollment_tokens WHERE token_hash=?`, hashSecret(request.Token)).Scan(&expires, &used); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return protocol.AgentEnrollmentResponse{}, errors.New("invalid enrollment token")
		}
		return protocol.AgentEnrollmentResponse{}, err
	}
	expiresAt, _ := time.Parse(time.RFC3339Nano, expires)
	if used.Valid || time.Now().UTC().After(expiresAt) {
		return protocol.AgentEnrollmentResponse{}, errors.New("enrollment token is expired or already used")
	}
	agentID := randomID("relay")
	secret := randomSecret(32)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `INSERT INTO relay_nodes(id,name,public_ip,architecture,os,agent_version,secret_hash,status,last_seen_at,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		agentID, strings.TrimSpace(request.NodeName), request.PublicIP, request.Architecture, request.OS, request.AgentVersion, hashSecret(secret), "online", now, now); err != nil {
		return protocol.AgentEnrollmentResponse{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE enrollment_tokens SET used_at=? WHERE token_hash=?`, now, hashSecret(request.Token)); err != nil {
		return protocol.AgentEnrollmentResponse{}, err
	}
	if err := tx.Commit(); err != nil {
		return protocol.AgentEnrollmentResponse{}, err
	}
	return protocol.AgentEnrollmentResponse{AgentID: agentID, Secret: secret}, nil
}

func (s *Store) AuthenticateAgent(ctx context.Context, id, secret string) error {
	var expected string
	if err := s.db.QueryRowContext(ctx, `SELECT secret_hash FROM relay_nodes WHERE id=?`, id).Scan(&expected); err != nil {
		return err
	}
	if expected != hashSecret(secret) {
		return errors.New("invalid agent credentials")
	}
	return nil
}

func (s *Store) UpdateHeartbeat(ctx context.Context, id string, heartbeat protocol.AgentHeartbeat) error {
	encoded, err := json.Marshal(heartbeat.Services)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var oldRevision int64
	var oldJSON string
	if err := tx.QueryRowContext(ctx, `SELECT current_revision,service_status_json FROM relay_nodes WHERE id=?`, id).Scan(&oldRevision, &oldJSON); err != nil {
		return err
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `UPDATE relay_nodes SET status='online', last_seen_at=?, agent_version=?, current_revision=?, service_status_json=? WHERE id=?`,
		now.Format(time.RFC3339Nano), heartbeat.AgentVersion, heartbeat.CurrentRevision, string(encoded), id); err != nil {
		return err
	}
	if oldRevision != heartbeat.CurrentRevision {
		if err := insertEvent(ctx, tx, id, "info", "deployment", fmt.Sprintf("Agent applied configuration revision %d", heartbeat.CurrentRevision), now); err != nil {
			return err
		}
	}
	oldHealth := flattenTargetHealth(oldJSON)
	for _, service := range heartbeat.Services {
		for _, target := range service.Targets {
			key := service.ID + "/" + target.ID
			previous, existed := oldHealth[key]
			if existed && previous != target.Healthy {
				level, state := "warning", "unhealthy"
				if target.Healthy {
					level, state = "info", "recovered"
				}
				if err := insertEvent(ctx, tx, id, level, "health", fmt.Sprintf("Target %s for service %s is %s", target.ID, service.Name, state), now); err != nil {
					return err
				}
			}
		}
	}
	return tx.Commit()
}

func (s *Store) ListEvents(ctx context.Context, limit int) ([]RelayEvent, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,COALESCE(relay_node_id,''),level,category,message,created_at FROM relay_events ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []RelayEvent
	for rows.Next() {
		var event RelayEvent
		var created string
		if err := rows.Scan(&event.ID, &event.RelayNodeID, &event.Level, &event.Category, &event.Message, &created); err != nil {
			return nil, err
		}
		event.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		events = append(events, event)
	}
	return events, rows.Err()
}

func insertEvent(ctx context.Context, tx *sql.Tx, relayNodeID, level, category, message string, createdAt time.Time) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO relay_events(id,relay_node_id,level,category,message,created_at) VALUES(?,?,?,?,?,?)`, randomID("event"), relayNodeID, level, category, message, createdAt.Format(time.RFC3339Nano))
	return err
}

func flattenTargetHealth(raw string) map[string]bool {
	var services []protocol.ServiceStatus
	_ = json.Unmarshal([]byte(raw), &services)
	result := make(map[string]bool)
	for _, service := range services {
		for _, target := range service.Targets {
			result[service.ID+"/"+target.ID] = target.Healthy
		}
	}
	return result
}

func (s *Store) AgentConfig(ctx context.Context, id string) (protocol.AgentConfig, error) {
	var revision int64
	if err := s.db.QueryRowContext(ctx, `SELECT desired_revision FROM relay_nodes WHERE id=?`, id).Scan(&revision); err != nil {
		return protocol.AgentConfig{}, err
	}
	services, err := s.ListRelayServices(ctx, id)
	if err != nil {
		return protocol.AgentConfig{}, err
	}
	config := protocol.AgentConfig{Revision: revision, Services: make([]protocol.ServiceConfig, 0, len(services))}
	for _, service := range services {
		item := protocol.ServiceConfig{
			ID:                    service.ID,
			Name:                  service.Name,
			Listen:                net.JoinHostPort(service.ListenHost, fmt.Sprint(service.ListenPort)),
			Network:               service.Network,
			Mode:                  service.Mode,
			Enabled:               service.Enabled,
			DialTimeoutMillis:     service.DialTimeoutMillis,
			UDPIdleTimeoutSeconds: service.UDPIdleTimeoutSeconds,
			Health: protocol.HealthConfig{
				Enabled:              service.Health.Enabled,
				IntervalSeconds:      service.Health.IntervalSeconds,
				TimeoutMillis:        service.Health.TimeoutMillis,
				FailureThreshold:     service.Health.FailureThreshold,
				SuccessThreshold:     service.Health.SuccessThreshold,
				RecoveryCooldownSecs: service.Health.RecoveryCooldownSecs,
			},
		}
		for _, target := range service.Targets {
			item.Targets = append(item.Targets, protocol.TargetConfig{
				ID: target.ID, Name: target.Name,
				Address: net.JoinHostPort(target.Address, fmt.Sprint(target.Port)),
				Weight:  target.Weight, Priority: target.Priority, Enabled: target.Enabled,
			})
		}
		config.Services = append(config.Services, item)
	}
	return config, nil
}

func (s *Store) ListRelayNodes(ctx context.Context) ([]RelayNode, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,public_ip,architecture,os,agent_version,status,last_seen_at,current_revision,desired_revision,service_status_json FROM relay_nodes ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var nodes []RelayNode
	for rows.Next() {
		var node RelayNode
		var lastSeen sql.NullString
		var statusJSON string
		if err := rows.Scan(&node.ID, &node.Name, &node.PublicIP, &node.Architecture, &node.OS, &node.AgentVersion, &node.Status, &lastSeen, &node.CurrentRevision, &node.DesiredRevision, &statusJSON); err != nil {
			return nil, err
		}
		if lastSeen.Valid {
			parsed, _ := time.Parse(time.RFC3339Nano, lastSeen.String)
			node.LastSeenAt = &parsed
		}
		node.Services = json.RawMessage(statusJSON)
		nodes = append(nodes, node)
	}
	return nodes, rows.Err()
}

func (s *Store) CreateLandingNode(ctx context.Context, request CreateLandingNodeRequest) (LandingNode, error) {
	request.Name = strings.TrimSpace(request.Name)
	request.Address = strings.TrimSpace(request.Address)
	request.Network = strings.ToLower(strings.TrimSpace(request.Network))
	if request.Name == "" || request.Address == "" || request.Port < 1 || request.Port > 65535 {
		return LandingNode{}, errors.New("valid name, address and port are required")
	}
	switch request.Network {
	case "tcp", "udp", "tcp+udp":
	default:
		return LandingNode{}, errors.New("network must be tcp, udp or tcp+udp")
	}
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	node := LandingNode{ID: randomID("landing"), Name: request.Name, Address: request.Address, Port: request.Port, Network: request.Network, Enabled: enabled, CreatedAt: time.Now().UTC()}
	_, err := s.db.ExecContext(ctx, `INSERT INTO landing_nodes(id,name,address,port,network,enabled,created_at) VALUES(?,?,?,?,?,?,?)`,
		node.ID, node.Name, node.Address, node.Port, node.Network, boolInt(node.Enabled), node.CreatedAt.Format(time.RFC3339Nano))
	return node, err
}

func (s *Store) ListLandingNodes(ctx context.Context) ([]LandingNode, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,address,port,network,enabled,created_at FROM landing_nodes ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var nodes []LandingNode
	for rows.Next() {
		var node LandingNode
		var enabled int
		var created string
		if err := rows.Scan(&node.ID, &node.Name, &node.Address, &node.Port, &node.Network, &enabled, &created); err != nil {
			return nil, err
		}
		node.Enabled = enabled != 0
		node.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		nodes = append(nodes, node)
	}
	return nodes, rows.Err()
}

func (s *Store) UpdateLandingNode(ctx context.Context, id string, request CreateLandingNodeRequest) (LandingNode, error) {
	request.Name = strings.TrimSpace(request.Name)
	request.Address = strings.TrimSpace(request.Address)
	request.Network = strings.ToLower(strings.TrimSpace(request.Network))
	if request.Name == "" || request.Address == "" || request.Port < 1 || request.Port > 65535 {
		return LandingNode{}, errors.New("valid name, address and port are required")
	}
	if !oneOf(request.Network, "tcp", "udp", "tcp+udp") {
		return LandingNode{}, errors.New("network must be tcp, udp or tcp+udp")
	}
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LandingNode{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE landing_nodes SET name=?,address=?,port=?,network=?,enabled=? WHERE id=?`, request.Name, request.Address, request.Port, request.Network, boolInt(enabled), id)
	if err != nil {
		return LandingNode{}, err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return LandingNode{}, sql.ErrNoRows
	}
	if _, err := tx.ExecContext(ctx, `UPDATE relay_nodes SET desired_revision=desired_revision+1 WHERE id IN (
		SELECT DISTINCT rs.relay_node_id FROM relay_services rs JOIN service_targets st ON st.service_id=rs.id WHERE st.landing_node_id=?
	)`, id); err != nil {
		return LandingNode{}, err
	}
	if err := tx.Commit(); err != nil {
		return LandingNode{}, err
	}
	nodes, err := s.ListLandingNodes(ctx)
	if err != nil {
		return LandingNode{}, err
	}
	for _, node := range nodes {
		if node.ID == id {
			return node, nil
		}
	}
	return LandingNode{}, sql.ErrNoRows
}

func (s *Store) DeleteLandingNode(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM landing_nodes WHERE id=?`, id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) CreateRelayService(ctx context.Context, request CreateRelayServiceRequest) (RelayService, error) {
	if request.RelayNodeID == "" || strings.TrimSpace(request.Name) == "" || request.ListenPort < 1 || request.ListenPort > 65535 {
		return RelayService{}, errors.New("relay node, name and valid listen port are required")
	}
	if request.ListenHost == "" {
		request.ListenHost = "0.0.0.0"
	}
	request.Network = strings.ToLower(request.Network)
	request.Mode = strings.ToLower(request.Mode)
	if !oneOf(request.Network, "tcp", "udp", "tcp+udp") || !oneOf(request.Mode, "failover", "round_robin", "ip_hash", "weighted") {
		return RelayService{}, errors.New("invalid network or mode")
	}
	if len(request.Targets) == 0 {
		return RelayService{}, errors.New("at least one target is required")
	}
	if request.DialTimeoutMillis <= 0 {
		request.DialTimeoutMillis = 2500
	}
	if request.UDPIdleTimeoutSeconds <= 0 {
		request.UDPIdleTimeoutSeconds = 60
	}
	request.Health = normalizeHealth(request.Health)
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	now := time.Now().UTC()
	serviceID := randomID("service")
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RelayService{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO relay_services(id,relay_node_id,name,listen_host,listen_port,network,mode,enabled,dial_timeout_ms,udp_idle_timeout_seconds,health_enabled,health_interval_seconds,health_timeout_ms,failure_threshold,success_threshold,recovery_cooldown_seconds,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		serviceID, request.RelayNodeID, strings.TrimSpace(request.Name), request.ListenHost, request.ListenPort, request.Network, request.Mode, boolInt(enabled), request.DialTimeoutMillis, request.UDPIdleTimeoutSeconds,
		boolInt(request.Health.Enabled), request.Health.IntervalSeconds, request.Health.TimeoutMillis, request.Health.FailureThreshold, request.Health.SuccessThreshold, request.Health.RecoveryCooldownSecs, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		return RelayService{}, err
	}
	for _, target := range request.Targets {
		targetEnabled := true
		if target.Enabled != nil {
			targetEnabled = *target.Enabled
		}
		if target.Weight <= 0 {
			target.Weight = 1
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO service_targets(id,service_id,landing_node_id,weight,priority,enabled) VALUES(?,?,?,?,?,?)`,
			randomID("target"), serviceID, target.LandingNodeID, target.Weight, target.Priority, boolInt(targetEnabled)); err != nil {
			return RelayService{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE relay_nodes SET desired_revision=desired_revision+1 WHERE id=?`, request.RelayNodeID); err != nil {
		return RelayService{}, err
	}
	if err := tx.Commit(); err != nil {
		return RelayService{}, err
	}
	services, err := s.ListRelayServices(ctx, request.RelayNodeID)
	if err != nil {
		return RelayService{}, err
	}
	for _, service := range services {
		if service.ID == serviceID {
			return service, nil
		}
	}
	return RelayService{}, errors.New("service was created but could not be loaded")
}

func (s *Store) UpdateRelayService(ctx context.Context, id string, request CreateRelayServiceRequest) (RelayService, error) {
	var existingNodeID string
	if err := s.db.QueryRowContext(ctx, `SELECT relay_node_id FROM relay_services WHERE id=?`, id).Scan(&existingNodeID); err != nil {
		return RelayService{}, err
	}
	if request.RelayNodeID == "" {
		request.RelayNodeID = existingNodeID
	}
	if request.RelayNodeID != existingNodeID {
		return RelayService{}, errors.New("moving a service to another relay node is not supported")
	}
	if strings.TrimSpace(request.Name) == "" || request.ListenPort < 1 || request.ListenPort > 65535 {
		return RelayService{}, errors.New("name and valid listen port are required")
	}
	if request.ListenHost == "" {
		request.ListenHost = "0.0.0.0"
	}
	request.Network = strings.ToLower(request.Network)
	request.Mode = strings.ToLower(request.Mode)
	if !oneOf(request.Network, "tcp", "udp", "tcp+udp") || !oneOf(request.Mode, "failover", "round_robin", "ip_hash", "weighted") {
		return RelayService{}, errors.New("invalid network or mode")
	}
	if len(request.Targets) == 0 {
		return RelayService{}, errors.New("at least one target is required")
	}
	if request.DialTimeoutMillis <= 0 {
		request.DialTimeoutMillis = 2500
	}
	if request.UDPIdleTimeoutSeconds <= 0 {
		request.UDPIdleTimeoutSeconds = 60
	}
	request.Health = normalizeHealth(request.Health)
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RelayService{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE relay_services SET name=?,listen_host=?,listen_port=?,network=?,mode=?,enabled=?,dial_timeout_ms=?,udp_idle_timeout_seconds=?,health_enabled=?,health_interval_seconds=?,health_timeout_ms=?,failure_threshold=?,success_threshold=?,recovery_cooldown_seconds=?,updated_at=? WHERE id=?`,
		strings.TrimSpace(request.Name), request.ListenHost, request.ListenPort, request.Network, request.Mode, boolInt(enabled), request.DialTimeoutMillis, request.UDPIdleTimeoutSeconds,
		boolInt(request.Health.Enabled), request.Health.IntervalSeconds, request.Health.TimeoutMillis, request.Health.FailureThreshold, request.Health.SuccessThreshold, request.Health.RecoveryCooldownSecs, now, id); err != nil {
		return RelayService{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM service_targets WHERE service_id=?`, id); err != nil {
		return RelayService{}, err
	}
	for _, target := range request.Targets {
		targetEnabled := true
		if target.Enabled != nil {
			targetEnabled = *target.Enabled
		}
		if target.Weight <= 0 {
			target.Weight = 1
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO service_targets(id,service_id,landing_node_id,weight,priority,enabled) VALUES(?,?,?,?,?,?)`, randomID("target"), id, target.LandingNodeID, target.Weight, target.Priority, boolInt(targetEnabled)); err != nil {
			return RelayService{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE relay_nodes SET desired_revision=desired_revision+1 WHERE id=?`, existingNodeID); err != nil {
		return RelayService{}, err
	}
	if err := tx.Commit(); err != nil {
		return RelayService{}, err
	}
	services, err := s.ListRelayServices(ctx, existingNodeID)
	if err != nil {
		return RelayService{}, err
	}
	for _, service := range services {
		if service.ID == id {
			return service, nil
		}
	}
	return RelayService{}, sql.ErrNoRows
}

func (s *Store) ListRelayServices(ctx context.Context, relayNodeID string) ([]RelayService, error) {
	query := `SELECT id,relay_node_id,name,listen_host,listen_port,network,mode,enabled,dial_timeout_ms,udp_idle_timeout_seconds,health_enabled,health_interval_seconds,health_timeout_ms,failure_threshold,success_threshold,recovery_cooldown_seconds,created_at,updated_at FROM relay_services`
	var args []interface{}
	if relayNodeID != "" {
		query += ` WHERE relay_node_id=?`
		args = append(args, relayNodeID)
	}
	query += ` ORDER BY listen_port, created_at`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var services []RelayService
	for rows.Next() {
		var service RelayService
		var enabled, healthEnabled int
		var created, updated string
		if err := rows.Scan(&service.ID, &service.RelayNodeID, &service.Name, &service.ListenHost, &service.ListenPort, &service.Network, &service.Mode, &enabled,
			&service.DialTimeoutMillis, &service.UDPIdleTimeoutSeconds, &healthEnabled, &service.Health.IntervalSeconds, &service.Health.TimeoutMillis,
			&service.Health.FailureThreshold, &service.Health.SuccessThreshold, &service.Health.RecoveryCooldownSecs, &created, &updated); err != nil {
			return nil, err
		}
		service.Enabled = enabled != 0
		service.Health.Enabled = healthEnabled != 0
		service.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		service.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
		services = append(services, service)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for index := range services {
		services[index].Targets, err = s.listServiceTargets(ctx, services[index].ID)
		if err != nil {
			return nil, err
		}
	}
	return services, nil
}

func (s *Store) listServiceTargets(ctx context.Context, serviceID string) ([]ServiceTarget, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT st.id,st.landing_node_id,ln.name,ln.address,ln.port,st.weight,st.priority,st.enabled FROM service_targets st JOIN landing_nodes ln ON ln.id=st.landing_node_id WHERE st.service_id=? ORDER BY st.priority,st.id`, serviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var targets []ServiceTarget
	for rows.Next() {
		var target ServiceTarget
		var enabled int
		if err := rows.Scan(&target.ID, &target.LandingNodeID, &target.Name, &target.Address, &target.Port, &target.Weight, &target.Priority, &enabled); err != nil {
			return nil, err
		}
		target.Enabled = enabled != 0
		targets = append(targets, target)
	}
	return targets, rows.Err()
}

func (s *Store) DeleteRelayService(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var nodeID string
	if err := tx.QueryRowContext(ctx, `SELECT relay_node_id FROM relay_services WHERE id=?`, id).Scan(&nodeID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM relay_services WHERE id=?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE relay_nodes SET desired_revision=desired_revision+1 WHERE id=?`, nodeID); err != nil {
		return err
	}
	return tx.Commit()
}

func normalizeHealth(value HealthSettings) HealthSettings {
	if value.IntervalSeconds <= 0 {
		value.IntervalSeconds = 4
	}
	if value.TimeoutMillis <= 0 {
		value.TimeoutMillis = 2000
	}
	if value.FailureThreshold <= 0 {
		value.FailureThreshold = 2
	}
	if value.SuccessThreshold <= 0 {
		value.SuccessThreshold = 3
	}
	if value.RecoveryCooldownSecs <= 0 {
		value.RecoveryCooldownSecs = 60
	}
	return value
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func oneOf(value string, allowed ...string) bool {
	for _, item := range allowed {
		if value == item {
			return true
		}
	}
	return false
}

func hashSecret(value string) string {
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}

func randomID(prefix string) string {
	return prefix + "_" + randomSecret(10)
}

func randomSecret(bytes int) string {
	data := make([]byte, bytes)
	if _, err := rand.Read(data); err != nil {
		panic(err)
	}
	return hex.EncodeToString(data)
}
