package protocol

import "time"

// AgentConfig is the complete desired state for one CDT relay node. Configs
// are versioned and applied atomically by the agent.
type AgentConfig struct {
	Revision int64           `json:"revision"`
	Services []ServiceConfig `json:"services"`
}

type ServiceConfig struct {
	ID                    string         `json:"id"`
	Name                  string         `json:"name"`
	Listen                string         `json:"listen"`
	Network               string         `json:"network"` // tcp, udp, tcp+udp
	Mode                  string         `json:"mode"`    // failover, round_robin, ip_hash, weighted
	Enabled               bool           `json:"enabled"`
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
	Token        string `json:"token"`
	NodeName     string `json:"node_name"`
	PublicIP     string `json:"public_ip,omitempty"`
	Architecture string `json:"architecture"`
	OS           string `json:"os"`
	AgentVersion string `json:"agent_version"`
}

type AgentEnrollmentResponse struct {
	AgentID string `json:"agent_id"`
	Secret  string `json:"secret"`
}

type AgentHeartbeat struct {
	AgentVersion    string          `json:"agent_version"`
	CurrentRevision int64           `json:"current_revision"`
	StartedAt       time.Time       `json:"started_at"`
	Services        []ServiceStatus `json:"services"`
}

type ServiceStatus struct {
	ID                string         `json:"id"`
	Name              string         `json:"name"`
	Listening         bool           `json:"listening"`
	ActiveConnections int64          `json:"active_connections"`
	TotalConnections  uint64         `json:"total_connections"`
	BytesUp           uint64         `json:"bytes_up"`
	BytesDown         uint64         `json:"bytes_down"`
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
