package protocol

import "time"

const (
	BillingModeUpload   = "upload"
	BillingModeDownload = "download"
	BillingModeBoth     = "both"
	// Legacy aliases are accepted by Agent/controller normalization so cached
	// configs written by an early preview continue to work.
	BillingModeIngress = "ingress"
	BillingModeEgress  = "egress"
)

// AgentConfig is the complete desired state for one CDT relay node. Configs
// are versioned and applied atomically by the agent.
type AgentConfig struct {
	Revision int64           `json:"revision"`
	Services []ServiceConfig `json:"services"`
}

type ServiceConfig struct {
	ID string `json:"id"`
	// MeterKey identifies the shared user/entry-group meter. Services without a
	// group use their own ID, preserving the standalone service behavior.
	MeterKey string `json:"meter_key,omitempty"`
	Name     string `json:"name"`
	Listen   string `json:"listen"`
	Network  string `json:"network"` // tcp, udp, tcp+udp
	Mode     string `json:"mode"`    // failover, round_robin, ip_hash, weighted
	Enabled  bool   `json:"enabled"`
	// BillingMode controls which side of the transparent relay is charged:
	// upload is client-to-target, download is target-to-client, and both counts
	// both directions. It defaults to both to match bytes leaving a relay on
	// either side of a transparent proxy.
	BillingMode           string         `json:"billing_mode,omitempty"`
	TrafficLimitGB        float64        `json:"traffic_limit_gb,omitempty"`
	BillingEpoch          int64          `json:"billing_epoch,omitempty"`
	AccessBlocked         bool           `json:"access_blocked,omitempty"`
	DialTimeoutMillis     int            `json:"dial_timeout_ms"`
	UDPIdleTimeoutSeconds int            `json:"udp_idle_timeout_seconds"`
	Health                HealthConfig   `json:"health"`
	Targets               []TargetConfig `json:"targets"`
}

type TargetConfig struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Address  string `json:"address"`
	Weight   int    `json:"weight"`
	Priority int    `json:"priority"`
	Enabled  bool   `json:"enabled"`
}

type HealthConfig struct {
	Enabled              bool `json:"enabled"`
	IntervalSeconds      int  `json:"interval_seconds"`
	TimeoutMillis        int  `json:"timeout_ms"`
	FailureThreshold     int  `json:"failure_threshold"`
	SuccessThreshold     int  `json:"success_threshold"`
	RecoveryCooldownSecs int  `json:"recovery_cooldown_seconds"`
}

type AgentEnrollmentRequest struct {
	Token         string `json:"token"`
	NodeName      string `json:"node_name"`
	PublicIP      string `json:"public_ip,omitempty"`
	ECSInstanceID string `json:"ecs_instance_id,omitempty"`
	RegionID      string `json:"region_id,omitempty"`
	Architecture  string `json:"architecture"`
	OS            string `json:"os"`
	AgentVersion  string `json:"agent_version"`
}

type AgentEnrollmentResponse struct {
	AgentID string `json:"agent_id"`
	Secret  string `json:"secret"`
}

type AgentHeartbeat struct {
	AgentVersion    string          `json:"agent_version"`
	BinarySHA256    string          `json:"binary_sha256,omitempty"`
	UpdateStatus    string          `json:"update_status,omitempty"`
	UpdateError     string          `json:"update_error,omitempty"`
	CurrentRevision int64           `json:"current_revision"`
	StartedAt       time.Time       `json:"started_at"`
	Services        []ServiceStatus `json:"services"`
}

type AgentRelease struct {
	Available    bool   `json:"available"`
	Version      string `json:"version"`
	Architecture string `json:"architecture"`
	SHA256       string `json:"sha256"`
	URL          string `json:"url"`
	Size         int64  `json:"size"`
}

type ServiceStatus struct {
	ID                string         `json:"id"`
	MeterKey          string         `json:"meter_key,omitempty"`
	Name              string         `json:"name"`
	Listening         bool           `json:"listening"`
	ActiveConnections int64          `json:"active_connections"`
	TotalConnections  uint64         `json:"total_connections"`
	BytesUp           uint64         `json:"bytes_up"`
	BytesDown         uint64         `json:"bytes_down"`
	BilledBytes       uint64         `json:"billed_bytes"`
	BillingMode       string         `json:"billing_mode"`
	TrafficLimitGB    float64        `json:"traffic_limit_gb,omitempty"`
	BillingEpoch      int64          `json:"billing_epoch,omitempty"`
	QuotaExceeded     bool           `json:"quota_exceeded"`
	AccessBlocked     bool           `json:"access_blocked"`
	Targets           []TargetStatus `json:"targets"`
	LastError         string         `json:"last_error,omitempty"`
}

type TargetStatus struct {
	ID            string        `json:"id"`
	Healthy       bool          `json:"healthy"`
	Latency       time.Duration `json:"latency_ns"`
	Failures      int           `json:"failures"`
	Successes     int           `json:"successes"`
	LastCheckedAt time.Time     `json:"last_checked_at"`
}
