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

const (
	ProtectionAlertOnly  = "alert_only"
	ProtectionDrainRelay = "drain_relay"
	ProtectionStopECS    = "stop_ecs"
	FrontDoorRelayDNS    = "relay_dns"
	FrontDoorDispatcher  = "dispatcher"
)

type RelayNode struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	PublicIP        string          `json:"public_ip"`
	ECSInstanceID   string          `json:"ecs_instance_id,omitempty"`
	RegionID        string          `json:"region_id,omitempty"`
	CloudAccountID  *int64          `json:"cloud_account_id,omitempty"`
	Architecture    string          `json:"architecture"`
	OS              string          `json:"os"`
	AgentVersion    string          `json:"agent_version"`
	BinarySHA256    string          `json:"binary_sha256,omitempty"`
	UpdateStatus    string          `json:"update_status,omitempty"`
	UpdateError     string          `json:"update_error,omitempty"`
	UpdateAt        *time.Time      `json:"update_at,omitempty"`
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
	Protocol  string    `json:"protocol,omitempty"`
	ShareURI  string    `json:"share_uri,omitempty"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
}

type RelayService struct {
	ID                    string          `json:"id"`
	RelayNodeID           string          `json:"relay_node_id"`
	PoolID                string          `json:"pool_id,omitempty"`
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

type CloudAccount struct {
	ID                   int64   `json:"id"`
	Name                 string  `json:"name"`
	AccessKeyID          string  `json:"access_key_id"`
	AccessKeySecret      string  `json:"-"`
	RegionID             string  `json:"region_id"`
	SiteType             string  `json:"site_type"`
	ProtectedInstanceID  string  `json:"instance_id,omitempty"`
	TrafficLimitGB       float64 `json:"traffic_limit_gb"`
	ThresholdPercent     float64 `json:"threshold_percent"`
	OutstandingThreshold float64 `json:"outstanding_threshold"`
	ShutdownMode         string  `json:"shutdown_mode"`
	KeepAlive            bool    `json:"keep_alive"`
	AutoStartTime        string  `json:"auto_start_time,omitempty"`
	AutoStopTime         string  `json:"auto_stop_time,omitempty"`
	ManualStopped        bool    `json:"manual_stopped"`
	NoStockNotified      bool    `json:"nostock_notified"`
	ProtectionMode       string  `json:"protection_mode"`
	ProtectionTriggered  bool    `json:"protection_triggered"`
	// ProtectionPredictive is true when the account was drained ahead of the
	// hard threshold because its observed rate would cross it during the
	// controller/DNS reaction window.
	ProtectionPredictive      bool       `json:"protection_predictive"`
	ProtectionTriggeredAt     *time.Time `json:"protection_triggered_at,omitempty"`
	ProtectionDrainPublished  bool       `json:"-"`
	ProtectionActionCompleted bool       `json:"protection_action_completed"`
	ProtectionLastError       string     `json:"protection_last_error,omitempty"`
	Enabled                   bool       `json:"enabled"`
	AgentInstalled            bool       `json:"agent_installed"`
	AgentCount                int        `json:"agent_count"`
	OnlineAgentCount          int        `json:"online_agent_count"`
	CreatedAt                 *time.Time `json:"created_at,omitempty"`
}

type CloudAccountRequest struct {
	Name                 string  `json:"name"`
	AccessKeyID          string  `json:"access_key_id"`
	AccessKeySecret      string  `json:"access_key_secret"`
	RegionID             string  `json:"region_id"`
	SiteType             string  `json:"site_type"`
	ProtectedInstanceID  string  `json:"instance_id"`
	TrafficLimitGB       float64 `json:"traffic_limit_gb"`
	ThresholdPercent     float64 `json:"threshold_percent"`
	OutstandingThreshold float64 `json:"outstanding_threshold"`
	ShutdownMode         string  `json:"shutdown_mode"`
	KeepAlive            bool    `json:"keep_alive"`
	AutoStartTime        string  `json:"auto_start_time"`
	AutoStopTime         string  `json:"auto_stop_time"`
	ProtectionMode       string  `json:"protection_mode"`
	Enabled              *bool   `json:"enabled,omitempty"`
}

type TrafficProtectionDecision struct {
	AccountID       int64
	AccountName     string
	Mode            string
	InstanceID      string
	Percent         float64
	RateGBPerMinute float64
	ProjectedGB     float64
	Predictive      bool
	Triggered       bool
	Changed         bool
	NeedsStop       bool
}

// CloudInstance contains ECS inventory and control state only. CDT traffic is
// reported by Aliyun at account scope and therefore belongs in AccountTraffic,
// not on an individual instance.
type CloudInstance struct {
	ID            int64      `json:"id"`
	AccountID     int64      `json:"account_id"`
	InstanceID    string     `json:"instance_id"`
	InstanceName  string     `json:"instance_name"`
	RegionID      string     `json:"region_id"`
	Status        string     `json:"status"`
	PublicIP      string     `json:"public_ip"`
	InstanceType  string     `json:"instance_type"`
	BandwidthMbps int        `json:"bandwidth_mbps"`
	IsSpot        bool       `json:"is_spot"`
	LastSynced    *time.Time `json:"last_synced,omitempty"`
	UpdatedAt     *time.Time `json:"updated_at,omitempty"`
}

// AccountTraffic is the CDT Internet traffic snapshot for one cloud account.
// Aliyun's ListCdtInternetTraffic API aggregates usage by account/region/ISP
// and does not return an ECS instance ID. Keep this collection separate from
// CloudOverview.Instances so an account total is never presented as per-ECS
// usage.
type AccountTraffic struct {
	AccountID          int64      `json:"account_id"`
	Scope              string     `json:"scope"`
	UsedGB             float64    `json:"used_gb"`
	RateGBPerMinute    float64    `json:"rate_gb_per_minute,omitempty"`
	MinutesToThreshold *float64   `json:"minutes_to_threshold,omitempty"`
	SyncedAt           *time.Time `json:"synced_at,omitempty"`
	LastError          string     `json:"last_error,omitempty"`
}

const TrafficScopeAccount = "account"

type CloudOverview struct {
	Accounts  []CloudAccount  `json:"accounts"`
	Instances []CloudInstance `json:"instances"`
	// Traffic contains one account-level CDT snapshot per account. It is not
	// an instance usage list; use AccountID to associate it with an account.
	Traffic []AccountTraffic `json:"traffic"`
}

type CloudInstanceUpdate struct {
	InstanceID    string
	InstanceName  string
	RegionID      string
	Status        string
	PublicIP      string
	InstanceType  string
	BandwidthMbps int
	IsSpot        bool
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
	Name     string `json:"name"`
	Address  string `json:"address"`
	Port     int    `json:"port"`
	Network  string `json:"network"`
	Protocol string `json:"protocol"`
	ShareURI string `json:"share_uri"`
	Enabled  *bool  `json:"enabled,omitempty"`
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

type DNSProvider struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	Type             string     `json:"type"`
	Zone             string     `json:"zone"`
	ZoneID           string     `json:"zone_id,omitempty"`
	Endpoint         string     `json:"endpoint,omitempty"`
	AccessKeyID      string     `json:"access_key_id,omitempty"`
	SecretConfigured bool       `json:"secret_configured"`
	TokenConfigured  bool       `json:"token_configured"`
	Enabled          bool       `json:"enabled"`
	LastTestAt       *time.Time `json:"last_test_at,omitempty"`
	LastError        string     `json:"last_error,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type CreateDNSProviderRequest struct {
	Name            string `json:"name"`
	Type            string `json:"type"`
	Zone            string `json:"zone"`
	ZoneID          string `json:"zone_id"`
	Endpoint        string `json:"endpoint"`
	AccessKeyID     string `json:"access_key_id"`
	AccessKeySecret string `json:"access_key_secret"`
	APIToken        string `json:"api_token"`
	APIEmail        string `json:"api_email"`
	Enabled         *bool  `json:"enabled,omitempty"`
}

type DNSManagedRecord struct {
	ID               string     `json:"id"`
	ProviderID       string     `json:"provider_id"`
	PoolID           string     `json:"pool_id,omitempty"`
	RelayNodeID      string     `json:"relay_node_id,omitempty"`
	Name             string     `json:"name"`
	Type             string     `json:"type"`
	Value            string     `json:"value"`
	TTL              int        `json:"ttl"`
	Enabled          bool       `json:"enabled"`
	DesiredEnabled   bool       `json:"desired_enabled,omitempty"`
	ProviderRecordID string     `json:"provider_record_id,omitempty"`
	Status           string     `json:"status"`
	LastError        string     `json:"last_error,omitempty"`
	LastSyncedAt     *time.Time `json:"last_synced_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type CreateDNSRecordRequest struct {
	ProviderID   string   `json:"provider_id"`
	RelayNodeID  string   `json:"relay_node_id"`
	RelayNodeIDs []string `json:"relay_node_ids,omitempty"`
	Name         string   `json:"name"`
	Type         string   `json:"type"`
	Value        string   `json:"value"`
	TTL          int      `json:"ttl"`
	Enabled      *bool    `json:"enabled,omitempty"`
}

type RelayPool struct {
	ID                    string            `json:"id"`
	Name                  string            `json:"name"`
	Hostname              string            `json:"hostname"`
	FrontDoorMode         string            `json:"front_door_mode"`
	ListenPort            int               `json:"listen_port"`
	Network               string            `json:"network"`
	Mode                  string            `json:"mode"`
	Enabled               bool              `json:"enabled"`
	AutoDrain             bool              `json:"auto_drain"`
	DNSProviderID         string            `json:"dns_provider_id,omitempty"`
	DNSRecordName         string            `json:"dns_record_name,omitempty"`
	DNSTTL                int               `json:"dns_ttl"`
	DialTimeoutMillis     int               `json:"dial_timeout_ms"`
	UDPIdleTimeoutSeconds int               `json:"udp_idle_timeout_seconds"`
	Health                HealthSettings    `json:"health"`
	Members               []RelayPoolMember `json:"members"`
	Targets               []ServiceTarget   `json:"targets"`
	CreatedAt             time.Time         `json:"created_at"`
	UpdatedAt             time.Time         `json:"updated_at"`
}

type RelayPoolMember struct {
	ID               string `json:"id"`
	PoolID           string `json:"pool_id"`
	RelayNodeID      string `json:"relay_node_id"`
	RelayNodeName    string `json:"relay_node_name"`
	PublicIP         string `json:"public_ip,omitempty"`
	Status           string `json:"status"`
	Weight           int    `json:"weight"`
	Enabled          bool   `json:"enabled"`
	ServiceID        string `json:"service_id,omitempty"`
	CloudAccountID   *int64 `json:"cloud_account_id,omitempty"`
	CloudAccountName string `json:"cloud_account_name,omitempty"`
	TrafficKnown     bool   `json:"traffic_known"`
	// These values are part of the relay-pool traffic contract. Keep zero
	// values in the JSON response: zero is a valid, known usage measurement
	// and omitting it makes clients mistake a known account for an incomplete
	// response.
	TrafficUsedGB             float64    `json:"traffic_used_gb"`
	TrafficLimitGB            float64    `json:"traffic_limit_gb"`
	TrafficPercent            float64    `json:"traffic_percent"`
	TrafficRemainingGB        float64    `json:"traffic_remaining_gb"`
	TrafficRateGBPerMinute    float64    `json:"traffic_rate_gb_per_minute"`
	TrafficMinutesToThreshold *float64   `json:"traffic_minutes_to_threshold,omitempty"`
	TrafficThresholdPercent   float64    `json:"traffic_threshold_percent"`
	ProtectionMode            string     `json:"protection_mode,omitempty"`
	ProtectionTriggered       bool       `json:"protection_triggered"`
	ProtectionPredictive      bool       `json:"protection_predictive"`
	ProtectionActionCompleted bool       `json:"protection_action_completed"`
	ProtectionTriggeredAt     *time.Time `json:"protection_triggered_at,omitempty"`
}

// DispatcherPoolSnapshot is the deliberately small, credential-free view a
// front-door L4 dispatcher needs. It contains only ready Relay backends and
// the minimal quota signals needed for selection; landing-node share links and
// cloud credentials are never exposed through this view.
type DispatcherPoolSnapshot struct {
	PoolID                string              `json:"pool_id"`
	PoolName              string              `json:"pool_name"`
	FrontDoorMode         string              `json:"front_door_mode"`
	Revision              string              `json:"revision"`
	ListenPort            int                 `json:"listen_port"`
	Network               string              `json:"network"`
	SelectionMode         string              `json:"selection_mode"`
	DialTimeoutMillis     int                 `json:"dial_timeout_ms"`
	UDPIdleTimeoutSeconds int                 `json:"udp_idle_timeout_seconds"`
	FailureThreshold      int                 `json:"failure_threshold"`
	FailureCooldownSecs   int                 `json:"failure_cooldown_seconds"`
	MaxUDPSessions        int                 `json:"max_udp_sessions"`
	Backends              []DispatcherBackend `json:"backends"`
	GeneratedAt           time.Time           `json:"generated_at"`
}

type DispatcherBackend struct {
	ID                     string  `json:"id"`
	Name                   string  `json:"name"`
	Address                string  `json:"address"`
	Port                   int     `json:"port"`
	Weight                 int     `json:"weight"`
	TrafficKnown           bool    `json:"traffic_known"`
	TrafficRemainingGB     float64 `json:"traffic_remaining_gb,omitempty"`
	TrafficRateGBPerMinute float64 `json:"traffic_rate_gb_per_minute,omitempty"`
}

type CreateRelayPoolRequest struct {
	Name                  string                  `json:"name"`
	Hostname              string                  `json:"hostname"`
	FrontDoorMode         string                  `json:"front_door_mode"`
	ListenPort            int                     `json:"listen_port"`
	Network               string                  `json:"network"`
	Mode                  string                  `json:"mode"`
	Enabled               *bool                   `json:"enabled,omitempty"`
	AutoDrain             *bool                   `json:"auto_drain,omitempty"`
	DNSProviderID         string                  `json:"dns_provider_id"`
	DNSRecordName         string                  `json:"dns_record_name"`
	DNSTTL                int                     `json:"dns_ttl"`
	DialTimeoutMillis     int                     `json:"dial_timeout_ms"`
	UDPIdleTimeoutSeconds int                     `json:"udp_idle_timeout_seconds"`
	Health                HealthSettings          `json:"health"`
	Members               []CreateRelayPoolMember `json:"members"`
	Targets               []CreateServiceTarget   `json:"targets"`
}

type CreateRelayPoolMember struct {
	RelayNodeID string `json:"relay_node_id"`
	Weight      int    `json:"weight"`
	Enabled     *bool  `json:"enabled,omitempty"`
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
		// Cloud synchronization and the DNS/Agent schedulers may briefly overlap;
		// wait for the active writer instead of failing a request immediately.
		`PRAGMA busy_timeout=30000`,
		`PRAGMA foreign_keys=ON`,
		`CREATE TABLE IF NOT EXISTS relay_nodes (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			public_ip TEXT NOT NULL DEFAULT '',
			architecture TEXT NOT NULL DEFAULT '',
			os TEXT NOT NULL DEFAULT '',
			agent_version TEXT NOT NULL DEFAULT '',
			binary_sha256 TEXT NOT NULL DEFAULT '',
			update_status TEXT NOT NULL DEFAULT 'idle',
			update_error TEXT NOT NULL DEFAULT '',
			update_at TEXT,
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
		`CREATE TABLE IF NOT EXISTS logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			level TEXT NOT NULL DEFAULT 'info',
			category TEXT NOT NULL DEFAULT 'system',
			message TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS accounts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			access_key_id TEXT NOT NULL,
			access_key_secret TEXT NOT NULL,
			region_id TEXT NOT NULL,
			site_type TEXT DEFAULT 'international',
			instance_id TEXT,
			traffic_limit_gb REAL DEFAULT 200,
			threshold_percent REAL DEFAULT 95,
			outstanding_threshold REAL DEFAULT 0,
			shutdown_mode TEXT DEFAULT 'StopCharging',
			keep_alive INTEGER DEFAULT 0,
			auto_start_time TEXT,
			auto_stop_time TEXT,
			manual_stopped INTEGER DEFAULT 0,
			nostock_notified INTEGER DEFAULT 0,
			protection_mode TEXT NOT NULL DEFAULT 'alert_only',
			protection_triggered INTEGER NOT NULL DEFAULT 0,
			protection_triggered_at TEXT,
			protection_drain_published INTEGER NOT NULL DEFAULT 0,
			protection_action_completed INTEGER NOT NULL DEFAULT 0,
			protection_last_error TEXT NOT NULL DEFAULT '',
			enabled INTEGER DEFAULT 1,
			created_at TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS instances (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			account_id INTEGER NOT NULL,
			instance_id TEXT NOT NULL UNIQUE,
			instance_name TEXT,
			region_id TEXT,
			status TEXT DEFAULT 'Unknown',
			public_ip TEXT,
			instance_type TEXT,
			bandwidth_mbps INTEGER DEFAULT 0,
			traffic_used_gb REAL DEFAULT 0,
			traffic_percent REAL DEFAULT 0,
			is_spot INTEGER DEFAULT 0,
			last_synced TEXT,
			updated_at TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS account_traffic_snapshots (
			account_id INTEGER PRIMARY KEY,
			used_gb REAL NOT NULL DEFAULT 0,
			synced_at TEXT,
			previous_used_gb REAL,
			previous_synced_at TEXT,
			last_error TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS admin_sessions (
			token_hash TEXT PRIMARY KEY,
			username TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS enrollment_tokens (
			token_hash TEXT PRIMARY KEY,
			account_id INTEGER,
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
			protocol TEXT NOT NULL DEFAULT '',
			share_uri TEXT NOT NULL DEFAULT '',
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
			pool_id TEXT REFERENCES relay_pools(id) ON DELETE SET NULL,
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
		`CREATE TABLE IF NOT EXISTS dns_providers (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			type TEXT NOT NULL,
			zone TEXT NOT NULL,
			zone_id TEXT NOT NULL DEFAULT '',
			endpoint TEXT NOT NULL DEFAULT '',
			access_key_id TEXT NOT NULL DEFAULT '',
			access_key_secret TEXT NOT NULL DEFAULT '',
			api_token TEXT NOT NULL DEFAULT '',
			api_email TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL DEFAULT 1,
			last_test_at TEXT,
			last_error TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS dns_managed_records (
			id TEXT PRIMARY KEY,
			provider_id TEXT NOT NULL REFERENCES dns_providers(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			type TEXT NOT NULL DEFAULT 'A',
			value TEXT NOT NULL,
			ttl INTEGER NOT NULL DEFAULT 60,
			enabled INTEGER NOT NULL DEFAULT 1,
			desired_enabled INTEGER NOT NULL DEFAULT 1,
			provider_record_id TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'pending',
			last_error TEXT NOT NULL DEFAULT '',
			last_synced_at TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			UNIQUE(provider_id,name,type,value)
		)`,
		`CREATE TABLE IF NOT EXISTS relay_pools (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			hostname TEXT NOT NULL,
			front_door_mode TEXT NOT NULL DEFAULT 'relay_dns',
			listen_port INTEGER NOT NULL,
			network TEXT NOT NULL,
			mode TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			auto_drain INTEGER NOT NULL DEFAULT 1,
			dns_provider_id TEXT REFERENCES dns_providers(id) ON DELETE SET NULL,
			dns_record_name TEXT NOT NULL DEFAULT '',
			dns_ttl INTEGER NOT NULL DEFAULT 60,
			dial_timeout_ms INTEGER NOT NULL DEFAULT 2500,
			udp_idle_timeout_seconds INTEGER NOT NULL DEFAULT 60,
			health_enabled INTEGER NOT NULL DEFAULT 1,
			health_interval_seconds INTEGER NOT NULL DEFAULT 4,
			health_timeout_ms INTEGER NOT NULL DEFAULT 2000,
			failure_threshold INTEGER NOT NULL DEFAULT 2,
			success_threshold INTEGER NOT NULL DEFAULT 3,
			recovery_cooldown_seconds INTEGER NOT NULL DEFAULT 60,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS relay_pool_members (
			id TEXT PRIMARY KEY,
			pool_id TEXT NOT NULL REFERENCES relay_pools(id) ON DELETE CASCADE,
			relay_node_id TEXT NOT NULL REFERENCES relay_nodes(id) ON DELETE CASCADE,
			service_id TEXT REFERENCES relay_services(id) ON DELETE SET NULL,
			weight INTEGER NOT NULL DEFAULT 1,
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL,
			UNIQUE(pool_id,relay_node_id)
		)`,
		`CREATE TABLE IF NOT EXISTS relay_pool_targets (
			id TEXT PRIMARY KEY,
			pool_id TEXT NOT NULL REFERENCES relay_pools(id) ON DELETE CASCADE,
			landing_node_id TEXT NOT NULL REFERENCES landing_nodes(id) ON DELETE RESTRICT,
			weight INTEGER NOT NULL DEFAULT 1,
			priority INTEGER NOT NULL DEFAULT 0,
			enabled INTEGER NOT NULL DEFAULT 1,
			UNIQUE(pool_id,landing_node_id)
		)`,
		`CREATE TABLE IF NOT EXISTS dns_managed_record_pools (
			record_id TEXT NOT NULL REFERENCES dns_managed_records(id) ON DELETE CASCADE,
			pool_id TEXT NOT NULL REFERENCES relay_pools(id) ON DELETE CASCADE,
			PRIMARY KEY(record_id,pool_id)
		)`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("migration failed: %w", err)
		}
	}
	for _, column := range []struct {
		name       string
		definition string
	}{
		{name: "ecs_instance_id", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "region_id", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "cloud_account_id", definition: "INTEGER"},
		{name: "binary_sha256", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "update_status", definition: "TEXT NOT NULL DEFAULT 'idle'"},
		{name: "update_error", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "update_at", definition: "TEXT"},
	} {
		if err := s.ensureColumn(ctx, "relay_nodes", column.name, column.definition); err != nil {
			return err
		}
	}
	if err := s.ensureColumn(ctx, "enrollment_tokens", "account_id", "INTEGER"); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "relay_services", "pool_id", "TEXT"); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "relay_pools", "auto_drain", "INTEGER NOT NULL DEFAULT 1"); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "relay_pools", "front_door_mode", "TEXT NOT NULL DEFAULT 'relay_dns'"); err != nil {
		return err
	}
	for _, column := range []struct {
		name       string
		definition string
	}{
		{name: "protocol", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "share_uri", definition: "TEXT NOT NULL DEFAULT ''"},
	} {
		if err := s.ensureColumn(ctx, "landing_nodes", column.name, column.definition); err != nil {
			return err
		}
	}
	for _, column := range []struct {
		name       string
		definition string
	}{
		{name: "protection_mode", definition: "TEXT NOT NULL DEFAULT 'alert_only'"},
		{name: "protection_triggered", definition: "INTEGER NOT NULL DEFAULT 0"},
		{name: "protection_triggered_at", definition: "TEXT"},
		{name: "protection_action_completed", definition: "INTEGER NOT NULL DEFAULT 0"},
		{name: "protection_last_error", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "protection_drain_published", definition: "INTEGER NOT NULL DEFAULT 0"},
		{name: "protection_predictive", definition: "INTEGER NOT NULL DEFAULT 0"},
	} {
		if err := s.ensureColumn(ctx, "accounts", column.name, column.definition); err != nil {
			return err
		}
	}
	if err := s.ensureColumn(ctx, "dns_providers", "zone_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	for _, column := range []struct{ name, definition string }{
		{name: "pool_id", definition: "TEXT"},
		{name: "relay_node_id", definition: "TEXT"},
		{name: "desired_enabled", definition: "INTEGER NOT NULL DEFAULT 1"},
	} {
		if err := s.ensureColumn(ctx, "dns_managed_records", column.name, column.definition); err != nil {
			return err
		}
	}
	for _, column := range []struct{ name, definition string }{
		{name: "previous_used_gb", definition: "REAL"},
		{name: "previous_synced_at", definition: "TEXT"},
	} {
		if err := s.ensureColumn(ctx, "account_traffic_snapshots", column.name, column.definition); err != nil {
			return err
		}
	}
	// A single DNS A/AAAA row may be shared by several port-specific pools
	// using the same hostname. Backfill the ownership relation introduced after
	// the original pool_id column so existing installations remain manageable.
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO dns_managed_record_pools(record_id,pool_id)
		SELECT id,pool_id FROM dns_managed_records WHERE COALESCE(pool_id,'')<>''`); err != nil {
		return err
	}
	// Older Agents were associated through ECS metadata only. Backfill the
	// account relationship from the synchronized instance inventory so those
	// Agents are visible under the correct cloud account after an upgrade.
	if _, err := s.db.ExecContext(ctx, `UPDATE relay_nodes SET cloud_account_id=(SELECT account_id FROM instances WHERE instances.instance_id=relay_nodes.ecs_instance_id) WHERE (cloud_account_id IS NULL OR cloud_account_id=0) AND COALESCE(ecs_instance_id,'')<>''`); err != nil {
		return err
	}
	// The historical schema stored account-level CDT usage on every instance
	// row. Seed only clearly valid, positive values. An absent snapshot is
	// preferable to presenting an unknown or previously failed request as 0 GB.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO account_traffic_snapshots(account_id,used_gb,synced_at,last_error,updated_at)
		SELECT account_id,MAX(COALESCE(traffic_used_gb,0)),MAX(last_synced),'',?
		FROM instances
		GROUP BY account_id
		HAVING MAX(COALESCE(traffic_used_gb,0)) > 0
		ON CONFLICT(account_id) DO NOTHING`, now); err != nil {
		return fmt.Errorf("seed historical traffic snapshots: %w", err)
	}
	return nil
}

func (s *Store) ensureColumn(ctx context.Context, table, column, definition string) error {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue interface{}
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return err
		}
		if name == column {
			found = true
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if found {
		return nil
	}
	_, err = s.db.ExecContext(ctx, `ALTER TABLE `+table+` ADD COLUMN `+column+` `+definition)
	return err
}

// trafficRate calculates a conservative short-term consumption rate from the
// two most recent successful account-level CDT snapshots. A counter decrease
// is treated as a billing-period reset rather than negative usage.
func trafficRate(currentGB float64, currentAt time.Time, previousGB sql.NullFloat64, previousAt sql.NullString) (rateGBPerMinute float64, reset bool) {
	if !previousGB.Valid || !previousAt.Valid || previousAt.String == "" {
		return 0, false
	}
	previousTime := parseDatabaseTime(previousAt.String)
	if previousTime.IsZero() || currentAt.IsZero() {
		return 0, false
	}
	if currentGB+0.000001 < previousGB.Float64 {
		return 0, true
	}
	minutes := currentAt.Sub(previousTime).Minutes()
	// Ignore duplicate/clock-skewed samples. The cloud scheduler normally
	// samples every couple of minutes, so a shorter interval is not useful for
	// a stable forecast and can amplify manual-sync noise.
	if minutes < 0.5 {
		return 0, false
	}
	delta := currentGB - previousGB.Float64
	if delta <= 0 {
		return 0, false
	}
	return delta / minutes, false
}

func (s *Store) trafficRateTx(ctx context.Context, tx *sql.Tx, accountID int64, currentGB float64, now time.Time) (float64, bool, error) {
	var previousGB sql.NullFloat64
	var previousAt sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT previous_used_gb,previous_synced_at FROM account_traffic_snapshots WHERE account_id=?`, accountID).Scan(&previousGB, &previousAt)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	rate, reset := trafficRate(currentGB, now, previousGB, previousAt)
	return rate, reset, nil
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

// CreateEnrollmentToken creates a one-time token. The optional account ID is
// embedded in the token so an Agent installed from an account card is shown
// on that account immediately, even if ECS metadata is unavailable.
func (s *Store) CreateEnrollmentToken(ctx context.Context, raw string, ttl time.Duration, accountIDs ...int64) error {
	if raw == "" {
		return errors.New("token is required")
	}
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	now := time.Now().UTC()
	var accountID interface{}
	if len(accountIDs) > 0 && accountIDs[0] > 0 {
		var validatedAccountID int64
		if err := s.db.QueryRowContext(ctx, `SELECT id FROM accounts WHERE id=?`, accountIDs[0]).Scan(&validatedAccountID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errors.New("cloud account not found")
			}
			return err
		}
		accountID = validatedAccountID
	} else {
		accountID = nil
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO enrollment_tokens(token_hash, account_id, expires_at, used_at, created_at) VALUES(?, ?, ?, NULL, ?)`,
		hashSecret(raw), accountID, now.Add(ttl).Format(time.RFC3339Nano), now.Format(time.RFC3339Nano),
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
	var tokenAccountID sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT expires_at, used_at, account_id FROM enrollment_tokens WHERE token_hash=?`, hashSecret(request.Token)).Scan(&expires, &used, &tokenAccountID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return protocol.AgentEnrollmentResponse{}, errors.New("invalid enrollment token")
		}
		return protocol.AgentEnrollmentResponse{}, err
	}
	expiresAt, _ := time.Parse(time.RFC3339Nano, expires)
	if used.Valid || time.Now().UTC().After(expiresAt) {
		return protocol.AgentEnrollmentResponse{}, errors.New("enrollment token is expired or already used")
	}
	secret := randomSecret(32)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	nodeName := strings.TrimSpace(request.NodeName)
	accountExpression := `(SELECT account_id FROM instances WHERE instance_id=?)`
	var accountValue interface{} = request.ECSInstanceID
	if tokenAccountID.Valid {
		accountExpression = `?`
		accountValue = tokenAccountID.Int64
	}

	// A host can lose the Agent credential file when its local/ephemeral disk is
	// recreated or when the Agent is reinstalled. Re-enrolling that same ECS
	// instance must not create a second Relay node: entry pools and services are
	// keyed by relay_nodes.id and would otherwise remain attached to the stale
	// node forever. An account-bound token may only reclaim a node belonging to
	// the same account; an unbound token can reclaim a node whose ECS metadata
	// identifies the instance.
	if instanceID := strings.TrimSpace(request.ECSInstanceID); instanceID != "" {
		var existingID string
		var existingAccountID sql.NullInt64
		existingErr := tx.QueryRowContext(ctx, `SELECT id,cloud_account_id FROM relay_nodes rn
			WHERE ecs_instance_id=?
			ORDER BY
				(SELECT COUNT(*) FROM relay_pool_members rpm WHERE rpm.relay_node_id=rn.id) DESC,
				(SELECT COUNT(*) FROM relay_services rs WHERE rs.relay_node_id=rn.id) DESC,
				CASE WHEN status='online' THEN 0 ELSE 1 END,created_at DESC LIMIT 1`, instanceID).
			Scan(&existingID, &existingAccountID)
		if existingErr == nil {
			if tokenAccountID.Valid && existingAccountID.Valid && existingAccountID.Int64 != tokenAccountID.Int64 {
				return protocol.AgentEnrollmentResponse{}, errors.New("ECS instance is already associated with another cloud account")
			}
			if tokenAccountID.Valid && !existingAccountID.Valid {
				// Legacy nodes may not have cloud_account_id populated yet. Check
				// the synchronized inventory before allowing an account-bound token
				// to claim such a node.
				var inventoryAccountID sql.NullInt64
				if inventoryErr := tx.QueryRowContext(ctx, `SELECT account_id FROM instances WHERE instance_id=?`, instanceID).Scan(&inventoryAccountID); inventoryErr == nil && inventoryAccountID.Valid && inventoryAccountID.Int64 != tokenAccountID.Int64 {
					return protocol.AgentEnrollmentResponse{}, errors.New("ECS instance is already associated with another cloud account")
				} else if inventoryErr != nil && !errors.Is(inventoryErr, sql.ErrNoRows) {
					return protocol.AgentEnrollmentResponse{}, inventoryErr
				}
			}
			cloudAccountValue := interface{}(nil)
			if tokenAccountID.Valid {
				cloudAccountValue = tokenAccountID.Int64
			} else if existingAccountID.Valid {
				cloudAccountValue = existingAccountID.Int64
			} else {
				// Preserve the automatic ECS-to-account association for legacy
				// nodes even when the node itself did not yet carry the backfilled
				// cloud_account_id column.
				var inventoryAccountID sql.NullInt64
				if inventoryErr := tx.QueryRowContext(ctx, `SELECT account_id FROM instances WHERE instance_id=?`, instanceID).Scan(&inventoryAccountID); inventoryErr == nil && inventoryAccountID.Valid {
					cloudAccountValue = inventoryAccountID.Int64
				}
			}
			if _, err := tx.ExecContext(ctx, `UPDATE relay_nodes SET name=?,
				public_ip=CASE WHEN ?<>'' THEN ? ELSE public_ip END,
				ecs_instance_id=?,region_id=CASE WHEN ?<>'' THEN ? ELSE region_id END,cloud_account_id=?,
				architecture=CASE WHEN ?<>'' THEN ? ELSE architecture END,
				os=CASE WHEN ?<>'' THEN ? ELSE os END,
				agent_version=CASE WHEN ?<>'' THEN ? ELSE agent_version END,
				secret_hash=?,status='online',last_seen_at=? WHERE id=?`,
				nodeName, request.PublicIP, request.PublicIP, instanceID, request.RegionID, request.RegionID, cloudAccountValue,
				request.Architecture, request.Architecture, request.OS, request.OS, request.AgentVersion, request.AgentVersion,
				hashSecret(secret), now, existingID); err != nil {
				return protocol.AgentEnrollmentResponse{}, err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE enrollment_tokens SET used_at=? WHERE token_hash=?`, now, hashSecret(request.Token)); err != nil {
				return protocol.AgentEnrollmentResponse{}, err
			}
			// Reinstalling an Agent before this controller fix could have created
			// an unreferenced duplicate node. Remove only duplicates that own no
			// services, pools or standalone DNS records; referenced nodes remain
			// intact for an operator to reconcile explicitly.
			if _, err := tx.ExecContext(ctx, `DELETE FROM relay_nodes WHERE ecs_instance_id=? AND id<>?
				AND NOT EXISTS(SELECT 1 FROM relay_services WHERE relay_node_id=relay_nodes.id)
				AND NOT EXISTS(SELECT 1 FROM relay_pool_members WHERE relay_node_id=relay_nodes.id)
				AND NOT EXISTS(SELECT 1 FROM dns_managed_records WHERE relay_node_id=relay_nodes.id)`, instanceID, existingID); err != nil {
				return protocol.AgentEnrollmentResponse{}, err
			}
			if err := tx.Commit(); err != nil {
				return protocol.AgentEnrollmentResponse{}, err
			}
			return protocol.AgentEnrollmentResponse{AgentID: existingID, Secret: secret}, nil
		}
		if !errors.Is(existingErr, sql.ErrNoRows) {
			return protocol.AgentEnrollmentResponse{}, existingErr
		}
	}

	agentID := randomID("relay")
	if _, err := tx.ExecContext(ctx, `INSERT INTO relay_nodes(id,name,public_ip,ecs_instance_id,region_id,cloud_account_id,architecture,os,agent_version,secret_hash,status,last_seen_at,created_at)
		VALUES(?,?,?,?,?,`+accountExpression+`,?,?,?,?,?,?,?)`,
		agentID, nodeName, request.PublicIP, request.ECSInstanceID, request.RegionID, accountValue, request.Architecture, request.OS, request.AgentVersion, hashSecret(secret), "online", now, now); err != nil {
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
	updateStatus := strings.TrimSpace(heartbeat.UpdateStatus)
	if updateStatus == "" {
		updateStatus = "idle"
	}
	if _, err := tx.ExecContext(ctx, `UPDATE relay_nodes SET status='online', last_seen_at=?, agent_version=?, binary_sha256=?, update_status=?, update_error=?, update_at=?, current_revision=?, service_status_json=? WHERE id=?`,
		now.Format(time.RFC3339Nano), heartbeat.AgentVersion, strings.TrimSpace(heartbeat.BinarySHA256), updateStatus, strings.TrimSpace(heartbeat.UpdateError), now.Format(time.RFC3339Nano), heartbeat.CurrentRevision, string(encoded), id); err != nil {
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
	var protectionSuspended int
	if err := s.db.QueryRowContext(ctx, `SELECT rn.desired_revision,
		CASE WHEN (COALESCE(a.protection_triggered,0)=1 OR COALESCE(a.protection_predictive,0)=1) AND (
			COALESCE(a.protection_mode,'alert_only')='drain_relay' OR EXISTS(
				SELECT 1 FROM relay_services ars JOIN relay_pools arp ON arp.id=ars.pool_id
				WHERE ars.relay_node_id=rn.id AND ars.enabled=1 AND COALESCE(arp.enabled,1)=1 AND COALESCE(arp.auto_drain,1)=1
			)
		) THEN 1 ELSE 0 END
		FROM relay_nodes rn LEFT JOIN accounts a ON a.id=rn.cloud_account_id OR (rn.cloud_account_id IS NULL AND rn.ecs_instance_id IN (SELECT instance_id FROM instances WHERE account_id=a.id)) WHERE rn.id=?`, id).Scan(&revision, &protectionSuspended); err != nil {
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
			Enabled:               service.Enabled && protectionSuspended == 0,
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
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,public_ip,COALESCE(ecs_instance_id,''),COALESCE(region_id,''),cloud_account_id,architecture,os,agent_version,COALESCE(binary_sha256,''),COALESCE(update_status,'idle'),COALESCE(update_error,''),update_at,status,last_seen_at,current_revision,desired_revision,service_status_json FROM relay_nodes ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var nodes []RelayNode
	for rows.Next() {
		var node RelayNode
		var lastSeen, updateAt sql.NullString
		var cloudAccountID sql.NullInt64
		var statusJSON string
		if err := rows.Scan(&node.ID, &node.Name, &node.PublicIP, &node.ECSInstanceID, &node.RegionID, &cloudAccountID, &node.Architecture, &node.OS, &node.AgentVersion, &node.BinarySHA256, &node.UpdateStatus, &node.UpdateError, &updateAt, &node.Status, &lastSeen, &node.CurrentRevision, &node.DesiredRevision, &statusJSON); err != nil {
			return nil, err
		}
		if cloudAccountID.Valid {
			node.CloudAccountID = &cloudAccountID.Int64
		}
		if lastSeen.Valid {
			parsed, _ := time.Parse(time.RFC3339Nano, lastSeen.String)
			node.LastSeenAt = &parsed
		}
		if updateAt.Valid {
			parsed := parseDatabaseTime(updateAt.String)
			node.UpdateAt = &parsed
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
	request.ShareURI = strings.TrimSpace(request.ShareURI)
	request.Protocol = strings.ToLower(strings.TrimSpace(request.Protocol))
	if request.ShareURI != "" {
		parsed, err := parseNodeLink(request.ShareURI)
		if err != nil {
			return LandingNode{}, err
		}
		if request.Name == "" {
			request.Name = parsed.Name
			if request.Name == "" {
				request.Name = defaultNodeName(parsed)
			}
		}
		request.Address, request.Port, request.Network, request.Protocol = parsed.Address, parsed.Port, parsed.Network, parsed.Protocol
	}
	if request.Name == "" || request.Address == "" || request.Port < 1 || request.Port > 65535 {
		return LandingNode{}, errors.New("请填写完整节点链接，或提供有效的地址和端口")
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
	node := LandingNode{ID: randomID("landing"), Name: request.Name, Address: request.Address, Port: request.Port, Network: request.Network, Protocol: request.Protocol, ShareURI: request.ShareURI, Enabled: enabled, CreatedAt: time.Now().UTC()}
	_, err := s.db.ExecContext(ctx, `INSERT INTO landing_nodes(id,name,address,port,network,protocol,share_uri,enabled,created_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		node.ID, node.Name, node.Address, node.Port, node.Network, node.Protocol, node.ShareURI, boolInt(node.Enabled), node.CreatedAt.Format(time.RFC3339Nano))
	return node, err
}

func (s *Store) ListLandingNodes(ctx context.Context) ([]LandingNode, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,address,port,network,COALESCE(protocol,''),COALESCE(share_uri,''),enabled,created_at FROM landing_nodes ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	nodes := make([]LandingNode, 0)
	for rows.Next() {
		var node LandingNode
		var enabled int
		var created string
		if err := rows.Scan(&node.ID, &node.Name, &node.Address, &node.Port, &node.Network, &node.Protocol, &node.ShareURI, &enabled, &created); err != nil {
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
	request.ShareURI = strings.TrimSpace(request.ShareURI)
	request.Protocol = strings.ToLower(strings.TrimSpace(request.Protocol))
	if request.ShareURI != "" {
		parsed, err := parseNodeLink(request.ShareURI)
		if err != nil {
			return LandingNode{}, err
		}
		if request.Name == "" {
			request.Name = parsed.Name
			if request.Name == "" {
				request.Name = defaultNodeName(parsed)
			}
		}
		request.Address, request.Port, request.Network, request.Protocol = parsed.Address, parsed.Port, parsed.Network, parsed.Protocol
	}
	if request.Name == "" || request.Address == "" || request.Port < 1 || request.Port > 65535 {
		return LandingNode{}, errors.New("请填写完整节点链接，或提供有效的地址和端口")
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
	result, err := tx.ExecContext(ctx, `UPDATE landing_nodes SET name=?,address=?,port=?,network=?,protocol=?,share_uri=?,enabled=? WHERE id=?`, request.Name, request.Address, request.Port, request.Network, request.Protocol, request.ShareURI, boolInt(enabled), id)
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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete landing node: %w", err)
	}
	defer tx.Rollback()

	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM landing_nodes WHERE id=?)`, id).Scan(&exists); err != nil {
		return fmt.Errorf("check landing node: %w", err)
	}
	if exists == 0 {
		return sql.ErrNoRows
	}

	// Landing nodes are referenced by both standalone services and the
	// replicated services behind relay pools. The schema deliberately uses
	// RESTRICT for these foreign keys; detach every reference in one
	// transaction, then remove any pool that no longer has a target.
	type serviceRef struct {
		id     string
		nodeID string
		poolID string
	}
	serviceRefs := make(map[string]serviceRef)
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT st.service_id,rs.relay_node_id,COALESCE(rs.pool_id,'')
		FROM service_targets st JOIN relay_services rs ON rs.id=st.service_id WHERE st.landing_node_id=?`, id)
	if err != nil {
		return fmt.Errorf("list landing service references: %w", err)
	}
	for rows.Next() {
		var ref serviceRef
		if err := rows.Scan(&ref.id, &ref.nodeID, &ref.poolID); err != nil {
			rows.Close()
			return fmt.Errorf("scan landing service reference: %w", err)
		}
		serviceRefs[ref.id] = ref
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close landing service references: %w", err)
	}

	poolIDs := make(map[string]bool)
	rows, err = tx.QueryContext(ctx, `SELECT DISTINCT pool_id FROM relay_pool_targets WHERE landing_node_id=?`, id)
	if err != nil {
		return fmt.Errorf("list landing pool references: %w", err)
	}
	for rows.Next() {
		var poolID string
		if err := rows.Scan(&poolID); err != nil {
			rows.Close()
			return fmt.Errorf("scan landing pool reference: %w", err)
		}
		if poolID != "" {
			poolIDs[poolID] = true
		}
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close landing pool references: %w", err)
	}
	for _, ref := range serviceRefs {
		if ref.poolID != "" {
			poolIDs[ref.poolID] = true
		}
	}

	// Include every Relay member of an affected pool so each Agent receives a
	// fresh desired revision, even when an older database has incomplete target
	// rows for that pool.
	changedNodes := make(map[string]bool)
	for _, ref := range serviceRefs {
		if ref.nodeID != "" {
			changedNodes[ref.nodeID] = true
		}
	}
	for poolID := range poolIDs {
		memberRows, memberErr := tx.QueryContext(ctx, `SELECT relay_node_id FROM relay_pool_members WHERE pool_id=?`, poolID)
		if memberErr != nil {
			return fmt.Errorf("list pool members for landing node: %w", memberErr)
		}
		for memberRows.Next() {
			var nodeID string
			if scanErr := memberRows.Scan(&nodeID); scanErr != nil {
				memberRows.Close()
				return fmt.Errorf("scan pool member for landing node: %w", scanErr)
			}
			changedNodes[nodeID] = true
		}
		if closeErr := memberRows.Close(); closeErr != nil {
			return fmt.Errorf("close pool members for landing node: %w", closeErr)
		}
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM service_targets WHERE landing_node_id=?`, id); err != nil {
		return fmt.Errorf("detach landing service targets: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM relay_pool_targets WHERE landing_node_id=?`, id); err != nil {
		return fmt.Errorf("detach landing pool targets: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, ref := range serviceRefs {
		var remaining int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM service_targets WHERE service_id=?`, ref.id).Scan(&remaining); err != nil {
			return fmt.Errorf("check remaining service targets: %w", err)
		}
		if remaining != 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE relay_services SET enabled=0,updated_at=? WHERE id=?`, now, ref.id); err != nil {
			return fmt.Errorf("disable targetless relay service: %w", err)
		}
		if ref.poolID != "" {
			if _, err := tx.ExecContext(ctx, `UPDATE relay_pool_members SET enabled=0 WHERE pool_id=? AND service_id=?`, ref.poolID, ref.id); err != nil {
				return fmt.Errorf("disable targetless pool member: %w", err)
			}
		}
	}
	// A pool with no remaining landing target has no useful behavior. Delete it
	// in the same transaction (rather than merely disabling it), including its
	// generated services and pool-owned DNS records. Pools with at least one
	// other target remain intact.
	poolDNSCleanup := make([]poolDNSCleanupRecord, 0)
	for poolID := range poolIDs {
		var remaining int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM relay_pool_targets WHERE pool_id=?`, poolID).Scan(&remaining); err != nil {
			return fmt.Errorf("check remaining pool targets: %w", err)
		}
		if remaining != 0 {
			continue
		}
		deletedPool, deleteErr := s.deleteRelayPoolTx(ctx, tx, poolID)
		if deleteErr != nil {
			return fmt.Errorf("delete targetless relay pool: %w", deleteErr)
		}
		for nodeID := range deletedPool.ChangedNodes {
			changedNodes[nodeID] = true
		}
		poolDNSCleanup = append(poolDNSCleanup, deletedPool.DNSRecords...)
	}

	result, err := tx.ExecContext(ctx, `DELETE FROM landing_nodes WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("delete landing node: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return sql.ErrNoRows
	}
	for nodeID := range changedNodes {
		if _, err := tx.ExecContext(ctx, `UPDATE relay_nodes SET desired_revision=desired_revision+1 WHERE id=?`, nodeID); err != nil {
			return fmt.Errorf("bump relay revision after landing delete: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit landing node delete: %w", err)
	}

	// Reconcile local DNS desired state immediately. Provider API failures are
	// intentionally left to the scheduler so a landing-node delete is not
	// rolled back by a transient external DNS outage.
	_ = s.RefreshAllRelayPoolDNS(ctx)
	_ = s.RefreshRelayAgentDNSRecords(ctx)
	s.cleanupDeletedPoolDNS(ctx, poolDNSCleanup)
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
	if err := validateListenConflictTx(ctx, tx, request.RelayNodeID, request.ListenHost, request.ListenPort, request.Network, ""); err != nil {
		return RelayService{}, err
	}
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

// validateListenConflictTx catches transport overlaps before an Agent receives
// an impossible revision. SQLite's uniqueness constraint only compares the
// literal network string, while a tcp+udp listener occupies both sockets.
func validateListenConflictTx(ctx context.Context, tx *sql.Tx, relayNodeID, listenHost string, listenPort int, network, excludeID string) error {
	rows, err := tx.QueryContext(ctx, `SELECT id,listen_host,listen_port,network FROM relay_services WHERE relay_node_id=?`, relayNodeID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, existingHost, existingNetwork string
		var existingPort int
		if err := rows.Scan(&id, &existingHost, &existingPort, &existingNetwork); err != nil {
			return err
		}
		if id == excludeID || existingPort != listenPort || !listenHostsOverlap(existingHost, listenHost) || !networksOverlap(existingNetwork, network) {
			continue
		}
		return fmt.Errorf("relay listener conflicts with existing service %s on %s:%d (%s)", id, listenHost, listenPort, existingNetwork)
	}
	return rows.Err()
}

func listenHostsOverlap(left, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" || right == "" || left == right {
		return true
	}
	return left == "0.0.0.0" || left == "::" || right == "0.0.0.0" || right == "::"
}

func networksOverlap(left, right string) bool {
	left = strings.ToLower(strings.TrimSpace(left))
	right = strings.ToLower(strings.TrimSpace(right))
	if left == right {
		return true
	}
	return left == "tcp+udp" || right == "tcp+udp"
}

func (s *Store) UpdateRelayService(ctx context.Context, id string, request CreateRelayServiceRequest) (RelayService, error) {
	var existingNodeID, poolID string
	if err := s.db.QueryRowContext(ctx, `SELECT relay_node_id,COALESCE(pool_id,'') FROM relay_services WHERE id=?`, id).Scan(&existingNodeID, &poolID); err != nil {
		return RelayService{}, err
	}
	if poolID != "" {
		return RelayService{}, errors.New("pool-managed services must be changed from the relay pool")
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
	if err := validateListenConflictTx(ctx, tx, existingNodeID, request.ListenHost, request.ListenPort, request.Network, id); err != nil {
		return RelayService{}, err
	}
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
	query := `SELECT id,relay_node_id,COALESCE(pool_id,''),name,listen_host,listen_port,network,mode,enabled,dial_timeout_ms,udp_idle_timeout_seconds,health_enabled,health_interval_seconds,health_timeout_ms,failure_threshold,success_threshold,recovery_cooldown_seconds,created_at,updated_at FROM relay_services`
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
		if err := rows.Scan(&service.ID, &service.RelayNodeID, &service.PoolID, &service.Name, &service.ListenHost, &service.ListenPort, &service.Network, &service.Mode, &enabled,
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
	rows, err := s.db.QueryContext(ctx, `SELECT st.id,st.landing_node_id,ln.name,ln.address,ln.port,st.weight,st.priority,st.enabled,ln.enabled FROM service_targets st JOIN landing_nodes ln ON ln.id=st.landing_node_id WHERE st.service_id=? ORDER BY st.priority,st.id`, serviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var targets []ServiceTarget
	for rows.Next() {
		var target ServiceTarget
		var enabled, landingEnabled int
		if err := rows.Scan(&target.ID, &target.LandingNodeID, &target.Name, &target.Address, &target.Port, &target.Weight, &target.Priority, &enabled, &landingEnabled); err != nil {
			return nil, err
		}
		target.Enabled = enabled != 0 && landingEnabled != 0
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
	var nodeID, poolID string
	if err := tx.QueryRowContext(ctx, `SELECT relay_node_id,COALESCE(pool_id,'') FROM relay_services WHERE id=?`, id).Scan(&nodeID, &poolID); err != nil {
		return err
	}
	if poolID != "" {
		return errors.New("pool-managed services must be changed from the relay pool")
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM relay_services WHERE id=?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE relay_nodes SET desired_revision=desired_revision+1 WHERE id=?`, nodeID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ListCloudAccounts(ctx context.Context, enabledOnly bool) ([]CloudAccount, error) {
	query := `SELECT a.id,a.name,a.access_key_id,a.access_key_secret,a.region_id,COALESCE(a.site_type,'international'),COALESCE(a.instance_id,''),
		COALESCE(traffic_limit_gb,200),COALESCE(threshold_percent,95),COALESCE(outstanding_threshold,0),COALESCE(shutdown_mode,'StopCharging'),
		COALESCE(keep_alive,0),COALESCE(auto_start_time,''),COALESCE(auto_stop_time,''),COALESCE(manual_stopped,0),COALESCE(nostock_notified,0),
		COALESCE(protection_mode,'alert_only'),COALESCE(protection_triggered,0),COALESCE(protection_predictive,0),protection_triggered_at,
		COALESCE(protection_action_completed,0),COALESCE(a.protection_last_error,''),COALESCE(a.protection_drain_published,0),COALESCE(a.enabled,1),a.created_at,
		(SELECT COUNT(*) FROM relay_nodes rn LEFT JOIN instances ri ON ri.instance_id=rn.ecs_instance_id
		 WHERE rn.cloud_account_id=a.id OR (rn.cloud_account_id IS NULL AND ri.account_id=a.id)),
		(SELECT COUNT(*) FROM relay_nodes rn LEFT JOIN instances ri ON ri.instance_id=rn.ecs_instance_id
		 WHERE (rn.cloud_account_id=a.id OR (rn.cloud_account_id IS NULL AND ri.account_id=a.id)) AND rn.status='online'
		 AND rn.last_seen_at IS NOT NULL AND julianday(rn.last_seen_at) >= julianday('now','-35 seconds')
		) FROM accounts a`
	if enabledOnly {
		query += ` WHERE a.enabled=1`
	}
	query += ` ORDER BY id`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	accounts := make([]CloudAccount, 0)
	for rows.Next() {
		var account CloudAccount
		var enabled, keepAlive, manualStopped, noStockNotified, triggered, predictive, actionCompleted, drainPublished int
		var agentCount, onlineAgentCount int
		var triggeredAt, createdAt sql.NullString
		if err := rows.Scan(&account.ID, &account.Name, &account.AccessKeyID, &account.AccessKeySecret, &account.RegionID, &account.SiteType,
			&account.ProtectedInstanceID, &account.TrafficLimitGB, &account.ThresholdPercent, &account.OutstandingThreshold, &account.ShutdownMode,
			&keepAlive, &account.AutoStartTime, &account.AutoStopTime, &manualStopped, &noStockNotified,
			&account.ProtectionMode, &triggered, &predictive, &triggeredAt, &actionCompleted, &account.ProtectionLastError, &drainPublished, &enabled, &createdAt, &agentCount, &onlineAgentCount); err != nil {
			return nil, err
		}
		account.Enabled = enabled != 0
		account.KeepAlive = keepAlive != 0
		account.ManualStopped = manualStopped != 0
		account.NoStockNotified = noStockNotified != 0
		account.ProtectionTriggered = triggered != 0
		account.ProtectionPredictive = predictive != 0
		account.ProtectionActionCompleted = actionCompleted != 0
		account.ProtectionDrainPublished = drainPublished != 0
		account.AgentCount = agentCount
		account.OnlineAgentCount = onlineAgentCount
		account.AgentInstalled = agentCount > 0
		if triggeredAt.Valid {
			parsed := parseDatabaseTime(triggeredAt.String)
			account.ProtectionTriggeredAt = &parsed
		}
		if createdAt.Valid {
			parsed := parseDatabaseTime(createdAt.String)
			account.CreatedAt = &parsed
		}
		accounts = append(accounts, account)
	}
	return accounts, rows.Err()
}

func (s *Store) CreateCloudAccount(ctx context.Context, request CloudAccountRequest) (CloudAccount, error) {
	request, enabled, err := normalizeCloudAccountRequest(request, false)
	if err != nil {
		return CloudAccount{}, err
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO accounts(name,access_key_id,access_key_secret,region_id,site_type,instance_id,traffic_limit_gb,threshold_percent,outstanding_threshold,shutdown_mode,keep_alive,auto_start_time,auto_stop_time,protection_mode,enabled,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		request.Name, request.AccessKeyID, request.AccessKeySecret, request.RegionID, request.SiteType, request.ProtectedInstanceID,
		request.TrafficLimitGB, request.ThresholdPercent, request.OutstandingThreshold, request.ShutdownMode, boolInt(request.KeepAlive), nullIfEmpty(request.AutoStartTime), nullIfEmpty(request.AutoStopTime), request.ProtectionMode,
		boolInt(enabled), time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return CloudAccount{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return CloudAccount{}, err
	}
	return CloudAccount{
		ID: id, Name: request.Name, AccessKeyID: request.AccessKeyID, RegionID: request.RegionID, SiteType: request.SiteType,
		ProtectedInstanceID: request.ProtectedInstanceID, TrafficLimitGB: request.TrafficLimitGB, ThresholdPercent: request.ThresholdPercent,
		OutstandingThreshold: request.OutstandingThreshold, ShutdownMode: request.ShutdownMode, KeepAlive: request.KeepAlive,
		AutoStartTime: request.AutoStartTime, AutoStopTime: request.AutoStopTime, ProtectionMode: request.ProtectionMode, Enabled: enabled,
	}, nil
}

func (s *Store) UpdateCloudAccount(ctx context.Context, id int64, request CloudAccountRequest) (CloudAccount, error) {
	request, enabled, err := normalizeCloudAccountRequest(request, true)
	if err != nil {
		return CloudAccount{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CloudAccount{}, err
	}
	defer tx.Rollback()
	var oldMode, oldInstanceID string
	var triggered, predictive int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(protection_mode,'alert_only'),COALESCE(instance_id,''),COALESCE(protection_triggered,0),COALESCE(protection_predictive,0) FROM accounts WHERE id=?`, id).Scan(&oldMode, &oldInstanceID, &triggered, &predictive); err != nil {
		return CloudAccount{}, err
	}
	if request.AccessKeySecret == "" {
		_, err = tx.ExecContext(ctx, `UPDATE accounts SET name=?,access_key_id=?,region_id=?,site_type=?,instance_id=?,traffic_limit_gb=?,threshold_percent=?,outstanding_threshold=?,shutdown_mode=?,keep_alive=?,auto_start_time=?,auto_stop_time=?,protection_mode=?,enabled=? WHERE id=?`,
			request.Name, request.AccessKeyID, request.RegionID, request.SiteType, request.ProtectedInstanceID, request.TrafficLimitGB,
			request.ThresholdPercent, request.OutstandingThreshold, request.ShutdownMode, boolInt(request.KeepAlive), nullIfEmpty(request.AutoStartTime), nullIfEmpty(request.AutoStopTime), request.ProtectionMode, boolInt(enabled), id)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE accounts SET name=?,access_key_id=?,access_key_secret=?,region_id=?,site_type=?,instance_id=?,traffic_limit_gb=?,threshold_percent=?,outstanding_threshold=?,shutdown_mode=?,keep_alive=?,auto_start_time=?,auto_stop_time=?,protection_mode=?,enabled=? WHERE id=?`,
			request.Name, request.AccessKeyID, request.AccessKeySecret, request.RegionID, request.SiteType, request.ProtectedInstanceID,
			request.TrafficLimitGB, request.ThresholdPercent, request.OutstandingThreshold, request.ShutdownMode, boolInt(request.KeepAlive), nullIfEmpty(request.AutoStartTime), nullIfEmpty(request.AutoStopTime), request.ProtectionMode, boolInt(enabled), id)
	}
	if err != nil {
		return CloudAccount{}, err
	}
	autoDrain, err := accountHasAutoDrainPoolTx(ctx, tx, id)
	if err != nil {
		return CloudAccount{}, err
	}
	if (triggered != 0 || predictive != 0) && oldMode != request.ProtectionMode && (oldMode == ProtectionDrainRelay || request.ProtectionMode == ProtectionDrainRelay) {
		if err := bumpAccountRelayRevisions(ctx, tx, id); err != nil {
			return CloudAccount{}, err
		}
		published := request.ProtectionMode == ProtectionDrainRelay || autoDrain
		if _, err := tx.ExecContext(ctx, `UPDATE accounts SET protection_drain_published=? WHERE id=?`, boolInt(published), id); err != nil {
			return CloudAccount{}, err
		}
	}
	if (triggered != 0 || predictive != 0) && !enabled && (oldMode == ProtectionDrainRelay || autoDrain) {
		if err := bumpAccountRelayRevisions(ctx, tx, id); err != nil {
			return CloudAccount{}, err
		}
	}
	if triggered != 0 && request.ProtectionMode == ProtectionStopECS && (oldMode != ProtectionStopECS || oldInstanceID != request.ProtectedInstanceID) {
		if _, err := tx.ExecContext(ctx, `UPDATE accounts SET protection_action_completed=0,protection_last_error='' WHERE id=?`, id); err != nil {
			return CloudAccount{}, err
		}
	}
	if triggered != 0 && request.ProtectionMode != ProtectionStopECS {
		if _, err := tx.ExecContext(ctx, `UPDATE accounts SET protection_action_completed=0,protection_last_error='' WHERE id=?`, id); err != nil {
			return CloudAccount{}, err
		}
	}
	if (triggered != 0 || predictive != 0) && !enabled {
		if _, err := tx.ExecContext(ctx, `UPDATE accounts SET protection_triggered=0,protection_predictive=0,protection_triggered_at=NULL,protection_action_completed=0,protection_drain_published=0,protection_last_error='' WHERE id=?`, id); err != nil {
			return CloudAccount{}, err
		}
	} else if predictive != 0 && request.ProtectionMode != ProtectionDrainRelay && !autoDrain {
		// A forecast is only an admission guard for a drain-capable route. If
		// the operator switches to alert-only and no pool requests automatic
		// draining, keep the measured protection state but drop the predictive
		// label so the relay can resume normally.
		if _, err := tx.ExecContext(ctx, `UPDATE accounts SET protection_predictive=0 WHERE id=?`, id); err != nil {
			return CloudAccount{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return CloudAccount{}, err
	}
	accounts, err := s.ListCloudAccounts(ctx, false)
	if err != nil {
		return CloudAccount{}, err
	}
	for _, account := range accounts {
		if account.ID == id {
			account.AccessKeySecret = ""
			return account, nil
		}
	}
	return CloudAccount{}, sql.ErrNoRows
}

func (s *Store) DeleteCloudAccount(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM instances WHERE account_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM account_traffic_snapshots WHERE account_id=?`, id); err != nil {
		return err
	}
	if err := bumpAccountRelayRevisions(ctx, tx, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE relay_nodes SET cloud_account_id=NULL WHERE cloud_account_id=?`, id); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM accounts WHERE id=?`, id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}

func (s *Store) CloudOverview(ctx context.Context) (CloudOverview, error) {
	accounts, err := s.ListCloudAccounts(ctx, false)
	if err != nil {
		return CloudOverview{}, err
	}
	for index := range accounts {
		accounts[index].AccessKeySecret = ""
	}
	// CDT usage is deliberately not joined onto instance rows here. The
	// ListCdtInternetTraffic API returns an account-level aggregate, so joining
	// account_traffic_snapshots would duplicate the same total for every ECS
	// instance and make it look like a per-instance measurement.
	rows, err := s.db.QueryContext(ctx, `SELECT i.id,i.account_id,i.instance_id,COALESCE(i.instance_name,''),COALESCE(i.region_id,''),COALESCE(i.status,'Unknown'),COALESCE(i.public_ip,''),COALESCE(i.instance_type,''),COALESCE(i.bandwidth_mbps,0),COALESCE(i.is_spot,0),
		i.last_synced,i.updated_at
		FROM instances i JOIN accounts a ON a.id=i.account_id ORDER BY i.account_id,i.id`)
	if err != nil {
		return CloudOverview{}, err
	}
	instances := make([]CloudInstance, 0)
	for rows.Next() {
		var instance CloudInstance
		var isSpot int
		var lastSynced, updatedAt sql.NullString
		if err := rows.Scan(&instance.ID, &instance.AccountID, &instance.InstanceID, &instance.InstanceName, &instance.RegionID, &instance.Status, &instance.PublicIP, &instance.InstanceType, &instance.BandwidthMbps, &isSpot, &lastSynced, &updatedAt); err != nil {
			rows.Close()
			return CloudOverview{}, err
		}
		instance.IsSpot = isSpot != 0
		if lastSynced.Valid {
			parsed := parseDatabaseTime(lastSynced.String)
			instance.LastSynced = &parsed
		}
		if updatedAt.Valid {
			parsed := parseDatabaseTime(updatedAt.String)
			instance.UpdatedAt = &parsed
		}
		instances = append(instances, instance)
	}
	if err := rows.Close(); err != nil {
		return CloudOverview{}, err
	}
	trafficRows, err := s.db.QueryContext(ctx, `SELECT account_id,used_gb,synced_at,previous_used_gb,previous_synced_at,last_error FROM account_traffic_snapshots ORDER BY account_id`)
	if err != nil {
		return CloudOverview{}, err
	}
	defer trafficRows.Close()
	traffic := make([]AccountTraffic, 0)
	for trafficRows.Next() {
		var snapshot AccountTraffic
		var synced, previousSynced sql.NullString
		var previousUsed sql.NullFloat64
		if err := trafficRows.Scan(&snapshot.AccountID, &snapshot.UsedGB, &synced, &previousUsed, &previousSynced, &snapshot.LastError); err != nil {
			return CloudOverview{}, err
		}
		snapshot.Scope = TrafficScopeAccount
		if synced.Valid {
			parsed := parseDatabaseTime(synced.String)
			snapshot.SyncedAt = &parsed
			if !parsed.IsZero() {
				rate, _ := trafficRate(snapshot.UsedGB, parsed, previousUsed, previousSynced)
				snapshot.RateGBPerMinute = rate
				for _, account := range accounts {
					if account.ID != snapshot.AccountID || rate <= 0 || account.TrafficLimitGB <= 0 {
						continue
					}
					thresholdGB := account.TrafficLimitGB * account.ThresholdPercent / 100
					if thresholdGB > snapshot.UsedGB {
						minutes := (thresholdGB - snapshot.UsedGB) / rate
						snapshot.MinutesToThreshold = &minutes
					}
					break
				}
			}
		}
		traffic = append(traffic, snapshot)
	}
	return CloudOverview{Accounts: accounts, Instances: instances, Traffic: traffic}, trafficRows.Err()
}

func (s *Store) SaveCloudSync(ctx context.Context, account CloudAccount, instances []CloudInstanceUpdate, instancesValid bool, instanceError string, trafficGB float64, trafficValid bool, trafficError string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	if instancesValid {
		seen := make([]string, 0, len(instances))
		for _, instance := range instances {
			seen = append(seen, instance.InstanceID)
			if _, err := tx.ExecContext(ctx, `INSERT INTO instances(account_id,instance_id,instance_name,region_id,status,public_ip,instance_type,bandwidth_mbps,is_spot,last_synced,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)
				ON CONFLICT(instance_id) DO UPDATE SET account_id=excluded.account_id,instance_name=excluded.instance_name,region_id=excluded.region_id,status=excluded.status,public_ip=excluded.public_ip,instance_type=excluded.instance_type,bandwidth_mbps=excluded.bandwidth_mbps,is_spot=excluded.is_spot,last_synced=excluded.last_synced,updated_at=excluded.updated_at`,
				account.ID, instance.InstanceID, instance.InstanceName, instance.RegionID, instance.Status, instance.PublicIP, instance.InstanceType, instance.BandwidthMbps, boolInt(instance.IsSpot), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE relay_nodes SET cloud_account_id=?,region_id=?,public_ip=CASE WHEN ?<>'' THEN ? ELSE public_ip END WHERE ecs_instance_id=?`, account.ID, instance.RegionID, instance.PublicIP, instance.PublicIP, instance.InstanceID); err != nil {
				return err
			}
		}
		if len(seen) == 0 {
			if _, err := tx.ExecContext(ctx, `DELETE FROM instances WHERE account_id=?`, account.ID); err != nil {
				return err
			}
		} else {
			placeholders := strings.TrimSuffix(strings.Repeat("?,", len(seen)), ",")
			args := make([]interface{}, 0, len(seen)+1)
			args = append(args, account.ID)
			for _, id := range seen {
				args = append(args, id)
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM instances WHERE account_id=? AND instance_id NOT IN (`+placeholders+`)`, args...); err != nil {
				return err
			}
		}
	} else if instanceError != "" {
		if err := insertEvent(ctx, tx, "", "warning", "cloud", fmt.Sprintf("[%s] ECS sync failed: %s", account.Name, instanceError), now); err != nil {
			return err
		}
	}
	if trafficValid {
		nowText := now.Format(time.RFC3339Nano)
		if _, err := tx.ExecContext(ctx, `INSERT INTO account_traffic_snapshots(account_id,used_gb,synced_at,previous_used_gb,previous_synced_at,last_error,updated_at) VALUES(?,?,?,?,?,?,?)
			ON CONFLICT(account_id) DO UPDATE SET
				previous_used_gb=account_traffic_snapshots.used_gb,
				previous_synced_at=account_traffic_snapshots.synced_at,
				used_gb=excluded.used_gb,
				synced_at=excluded.synced_at,
				last_error='',
				updated_at=excluded.updated_at`,
			account.ID, trafficGB, nowText, nil, nil, "", nowText); err != nil {
			return err
		}
	} else if trafficError != "" {
		// Preserve the last valid used_gb. On the first failure, create a row with
		// an explicit error so 0 is never presented as a successful measurement.
		if _, err := tx.ExecContext(ctx, `INSERT INTO account_traffic_snapshots(account_id,used_gb,synced_at,last_error,updated_at) VALUES(?,0,NULL,?,?)
			ON CONFLICT(account_id) DO UPDATE SET last_error=excluded.last_error,updated_at=excluded.updated_at`, account.ID, trafficError, now.Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ApplyTrafficProtection(ctx context.Context, accountID int64, trafficGB float64) (TrafficProtectionDecision, error) {
	return s.ApplyTrafficProtectionWithWindow(ctx, accountID, trafficGB, 0)
}

// ApplyTrafficProtectionWithWindow evaluates both the configured hard
// threshold and an optional short-term forecast. When a pool with automatic
// draining (or an account using drain_relay) is projected to hit the hard
// threshold during the control-plane reaction window, the account is marked
// protected early. The predictive marker keeps that drain sticky until a
// billing-period reset, so a brief rate fluctuation cannot reopen a nearly
// exhausted account.
func (s *Store) ApplyTrafficProtectionWithWindow(ctx context.Context, accountID int64, trafficGB float64, safetyWindow time.Duration) (TrafficProtectionDecision, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TrafficProtectionDecision{}, err
	}
	defer tx.Rollback()
	var decision TrafficProtectionDecision
	var trafficLimit, threshold float64
	var triggered, predictive, actionCompleted, drainPublished int
	if err := tx.QueryRowContext(ctx, `SELECT id,name,COALESCE(instance_id,''),COALESCE(traffic_limit_gb,200),COALESCE(threshold_percent,95),
		COALESCE(protection_mode,'alert_only'),COALESCE(protection_triggered,0),COALESCE(protection_predictive,0),COALESCE(protection_action_completed,0),COALESCE(protection_drain_published,0)
		FROM accounts WHERE id=?`, accountID).Scan(
		&decision.AccountID, &decision.AccountName, &decision.InstanceID, &trafficLimit, &threshold,
		&decision.Mode, &triggered, &predictive, &actionCompleted, &drainPublished,
	); err != nil {
		return TrafficProtectionDecision{}, err
	}
	if trafficLimit <= 0 {
		trafficLimit = 200
	}
	if threshold <= 0 {
		threshold = 95
	}
	decision.Percent = trafficGB / trafficLimit * 100
	autoDrain, err := accountHasAutoDrainPoolTx(ctx, tx, accountID)
	if err != nil {
		return TrafficProtectionDecision{}, err
	}
	now := time.Now().UTC()
	if safetyWindow < 0 {
		safetyWindow = 0
	}
	if rate, reset, rateErr := s.trafficRateTx(ctx, tx, accountID, trafficGB, now); rateErr != nil {
		return TrafficProtectionDecision{}, rateErr
	} else {
		decision.RateGBPerMinute = rate
		decision.ProjectedGB = trafficGB
		if rate > 0 && safetyWindow > 0 {
			decision.ProjectedGB += rate * safetyWindow.Minutes()
		}
		thresholdGB := trafficLimit * threshold / 100
		hardExceeded := decision.Percent >= threshold
		canPredict := decision.Mode == ProtectionDrainRelay || autoDrain
		predictiveTrigger := !hardExceeded && canPredict && rate > 0 && decision.ProjectedGB+0.000001 >= thresholdGB
		// A previously predictive drain remains active until the cumulative
		// counter decreases (normally the next billing period).
		stickyPredictive := triggered != 0 && predictive != 0 && !reset && !hardExceeded
		exceeded := hardExceeded || predictiveTrigger || stickyPredictive
		decision.Predictive = predictiveTrigger || stickyPredictive || (predictive != 0 && !hardExceeded && !reset)
		if exceeded {
			decision.Triggered = true
			if triggered == 0 {
				decision.Changed = true
				if _, err := tx.ExecContext(ctx, `UPDATE accounts SET protection_triggered=1,protection_predictive=?,protection_triggered_at=?,protection_action_completed=0,protection_drain_published=0,protection_last_error='' WHERE id=?`, boolInt(predictiveTrigger), now.Format(time.RFC3339Nano), accountID); err != nil {
					return TrafficProtectionDecision{}, err
				}
				if decision.Mode == ProtectionDrainRelay || autoDrain {
					if err := bumpAccountRelayRevisions(ctx, tx, accountID); err != nil {
						return TrafficProtectionDecision{}, err
					}
					if _, err := tx.ExecContext(ctx, `UPDATE accounts SET protection_drain_published=1 WHERE id=?`, accountID); err != nil {
						return TrafficProtectionDecision{}, err
					}
					drainPublished = 1
				}
				message := fmt.Sprintf("[%s] CDT 流量达到保护阈值：%.2f%%", decision.AccountName, decision.Percent)
				if predictiveTrigger {
					message = fmt.Sprintf("[%s] CDT 流量预计在控制窗口内达到保护阈值：%.2f%%（当前速率 %.3f GB/分钟）", decision.AccountName, decision.Percent, decision.RateGBPerMinute)
				}
				if err := insertEvent(ctx, tx, "", "warning", "traffic_protection", message, now); err != nil {
					return TrafficProtectionDecision{}, err
				}
				actionCompleted = 0
			}
			// Existing installations may have a triggered account from before the
			// drain-revision marker was introduced. Publish one catch-up revision,
			// but never increment on every periodic sync.
			if triggered != 0 && (decision.Mode == ProtectionDrainRelay || autoDrain) && drainPublished == 0 {
				if err := bumpAccountRelayRevisions(ctx, tx, accountID); err != nil {
					return TrafficProtectionDecision{}, err
				}
				if _, err := tx.ExecContext(ctx, `UPDATE accounts SET protection_drain_published=1 WHERE id=?`, accountID); err != nil {
					return TrafficProtectionDecision{}, err
				}
			}
			if predictive != 0 && hardExceeded {
				// The forecast has become a measured threshold crossing; retain the
				// protection state but clear the predictive label.
				if _, err := tx.ExecContext(ctx, `UPDATE accounts SET protection_predictive=0 WHERE id=?`, accountID); err != nil {
					return TrafficProtectionDecision{}, err
				}
				decision.Predictive = false
			}
			// A predictive stop only drains Relay listeners. Do not issue an ECS
			// stop command until the measured counter actually reaches the hard
			// threshold.
			decision.NeedsStop = hardExceeded && decision.Mode == ProtectionStopECS && decision.InstanceID != "" && actionCompleted == 0
		} else {
			decision.Triggered = false
			decision.Predictive = false
			if triggered != 0 || predictive != 0 {
				decision.Changed = true
				if _, err := tx.ExecContext(ctx, `UPDATE accounts SET protection_triggered=0,protection_predictive=0,protection_triggered_at=NULL,protection_action_completed=0,protection_drain_published=0,protection_last_error='' WHERE id=?`, accountID); err != nil {
					return TrafficProtectionDecision{}, err
				}
				if decision.Mode == ProtectionDrainRelay || autoDrain {
					if err := bumpAccountRelayRevisions(ctx, tx, accountID); err != nil {
						return TrafficProtectionDecision{}, err
					}
				}
				message := fmt.Sprintf("[%s] CDT 流量已低于保护阈值：%.2f%%，保护状态已恢复", decision.AccountName, decision.Percent)
				if err := insertEvent(ctx, tx, "", "info", "traffic_protection", message, now); err != nil {
					return TrafficProtectionDecision{}, err
				}
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return TrafficProtectionDecision{}, err
	}
	return decision, nil
}

func (s *Store) MarkTrafficProtectionAction(ctx context.Context, accountID int64, actionError error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var name, oldError string
	var triggered int
	if err := tx.QueryRowContext(ctx, `SELECT name,COALESCE(protection_triggered,0),COALESCE(protection_last_error,'') FROM accounts WHERE id=?`, accountID).Scan(&name, &triggered, &oldError); err != nil {
		return err
	}
	if triggered == 0 {
		return tx.Commit()
	}
	now := time.Now().UTC()
	if actionError == nil {
		if _, err := tx.ExecContext(ctx, `UPDATE accounts SET protection_action_completed=1,manual_stopped=1,protection_last_error='' WHERE id=?`, accountID); err != nil {
			return err
		}
		if err := insertEvent(ctx, tx, "", "warning", "traffic_protection", fmt.Sprintf("[%s] 流量保护已发送 ECS 停机指令", name), now); err != nil {
			return err
		}
	} else {
		message := actionError.Error()
		if _, err := tx.ExecContext(ctx, `UPDATE accounts SET protection_action_completed=0,protection_last_error=? WHERE id=?`, message, accountID); err != nil {
			return err
		}
		if oldError != message {
			if err := insertEvent(ctx, tx, "", "warning", "traffic_protection", fmt.Sprintf("[%s] ECS 停机保护执行失败：%s", name, message), now); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (s *Store) ConfirmTrafficProtectionStop(ctx context.Context, accountID int64, instanceID string) (bool, error) {
	var authorized int
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM accounts WHERE id=? AND COALESCE(enabled,1)=1 AND COALESCE(protection_triggered,0)=1
		AND COALESCE(protection_mode,'alert_only')='stop_ecs' AND COALESCE(instance_id,'')=?
		AND COALESCE(protection_action_completed,0)=0
	)`, accountID, instanceID).Scan(&authorized)
	return authorized != 0, err
}

func bumpAccountRelayRevisions(ctx context.Context, tx *sql.Tx, accountID int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE relay_nodes SET desired_revision=desired_revision+1
		WHERE cloud_account_id=? OR (cloud_account_id IS NULL AND ecs_instance_id IN (
			SELECT instance_id FROM instances WHERE account_id=?
		))`, accountID, accountID)
	return err
}

func accountHasAutoDrainPoolTx(ctx context.Context, tx *sql.Tx, accountID int64) (bool, error) {
	var enabled int
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM relay_services rs
		JOIN relay_pools rp ON rp.id=rs.pool_id
		JOIN relay_nodes rn ON rn.id=rs.relay_node_id
		WHERE (rn.cloud_account_id=? OR (rn.cloud_account_id IS NULL AND rn.ecs_instance_id IN (
			SELECT instance_id FROM instances WHERE account_id=?
		))) AND rs.enabled=1 AND COALESCE(rp.enabled,1)=1 AND COALESCE(rp.auto_drain,1)=1
	)`, accountID, accountID).Scan(&enabled)
	return enabled != 0, err
}

// EnsureProtectionRevisions repairs the one bit of state that cannot be
// inferred by an Agent: whether a triggered account's drain revision has
// already been published. It is called by the DNS scheduler at startup so a
// controller restart cannot leave a triggered pool listening until the next
// cloud API poll.
func (s *Store) EnsureProtectionRevisions(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT a.id
		FROM accounts a JOIN relay_nodes rn ON rn.cloud_account_id=a.id OR (rn.cloud_account_id IS NULL AND rn.ecs_instance_id IN (SELECT instance_id FROM instances WHERE account_id=a.id))
		JOIN relay_services rs ON rs.relay_node_id=rn.id
		LEFT JOIN relay_pools rp ON rp.id=rs.pool_id
		WHERE COALESCE(a.enabled,1)=1 AND COALESCE(a.protection_triggered,0)=1
		AND COALESCE(a.protection_drain_published,0)=0
		AND (COALESCE(a.protection_mode,'alert_only')='drain_relay' OR (rs.pool_id IS NOT NULL AND COALESCE(rp.enabled,1)=1 AND COALESCE(rp.auto_drain,1)=1))`)
	if err != nil {
		return err
	}
	accountIDs := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		accountIDs = append(accountIDs, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, id := range accountIDs {
		if err := bumpAccountRelayRevisions(ctx, tx, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE accounts SET protection_drain_published=1 WHERE id=?`, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) CloudAccountForInstance(ctx context.Context, instanceID string) (CloudAccount, error) {
	var accountID int64
	if err := s.db.QueryRowContext(ctx, `SELECT account_id FROM instances WHERE instance_id=?`, instanceID).Scan(&accountID); err != nil {
		return CloudAccount{}, err
	}
	accounts, err := s.ListCloudAccounts(ctx, false)
	if err != nil {
		return CloudAccount{}, err
	}
	for _, account := range accounts {
		if account.ID == accountID {
			return account, nil
		}
	}
	return CloudAccount{}, sql.ErrNoRows
}

func parseDatabaseTime(value string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999", "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

func normalizeCloudAccountRequest(request CloudAccountRequest, secretOptional bool) (CloudAccountRequest, bool, error) {
	request.Name = strings.TrimSpace(request.Name)
	request.AccessKeyID = strings.TrimSpace(request.AccessKeyID)
	request.RegionID = strings.TrimSpace(request.RegionID)
	request.SiteType = strings.ToLower(strings.TrimSpace(request.SiteType))
	request.ProtectedInstanceID = strings.TrimSpace(request.ProtectedInstanceID)
	request.AutoStartTime = strings.TrimSpace(request.AutoStartTime)
	request.AutoStopTime = strings.TrimSpace(request.AutoStopTime)
	request.ProtectionMode = strings.ToLower(strings.TrimSpace(request.ProtectionMode))
	if request.Name == "" || request.AccessKeyID == "" || request.RegionID == "" || (!secretOptional && request.AccessKeySecret == "") {
		return request, false, errors.New("name, AccessKey ID, secret and region are required")
	}
	if !oneOf(request.SiteType, "china", "international") {
		return request, false, errors.New("site type must be china or international")
	}
	if request.TrafficLimitGB <= 0 {
		request.TrafficLimitGB = 200
	}
	if request.ThresholdPercent <= 0 {
		request.ThresholdPercent = 95
	}
	if request.ThresholdPercent > 100 {
		return request, false, errors.New("threshold percent must not exceed 100")
	}
	if request.ShutdownMode == "" {
		request.ShutdownMode = "StopCharging"
	}
	if !oneOf(request.ShutdownMode, "StopCharging", "KeepCharging") {
		return request, false, errors.New("shutdown mode must be StopCharging or KeepCharging")
	}
	if err := validateScheduleTime(request.AutoStartTime); err != nil {
		return request, false, fmt.Errorf("auto start time: %w", err)
	}
	if err := validateScheduleTime(request.AutoStopTime); err != nil {
		return request, false, fmt.Errorf("auto stop time: %w", err)
	}
	if request.SiteType == "china" {
		request.OutstandingThreshold = 0
	}
	if request.ProtectionMode == "" {
		request.ProtectionMode = ProtectionAlertOnly
	}
	if !oneOf(request.ProtectionMode, ProtectionAlertOnly, ProtectionDrainRelay, ProtectionStopECS) {
		return request, false, errors.New("protection mode must be alert_only, drain_relay or stop_ecs")
	}
	if request.ProtectionMode == ProtectionStopECS && request.ProtectedInstanceID == "" {
		return request, false, errors.New("protected instance ID is required when stop_ecs is selected")
	}
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	return request, enabled, nil
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

func nullIfEmpty(value string) interface{} {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func validateScheduleTime(value string) error {
	if value == "" {
		return nil
	}
	if _, err := time.Parse("15:04", value); err != nil {
		return errors.New("must use HH:MM")
	}
	return nil
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
