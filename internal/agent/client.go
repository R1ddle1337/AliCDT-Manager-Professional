package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	// Relay Agents are commonly installed on minimal Alpine hosts, which do
	// not ship the system zoneinfo database. Importing the embedded database
	// keeps the configured update timezone available without requiring an OS
	// package on every Agent machine.
	_ "time/tzdata"

	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/protocol"
	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/relay"
)

type Options struct {
	ControllerURL       string
	EnrollmentToken     string
	NodeName            string
	PublicIP            string
	DataDir             string
	AgentVersion        string
	PollInterval        time.Duration
	HeartbeatEvery      time.Duration
	AutoUpdate          bool
	AutoUpdateSet       bool
	AutoFirewall        bool
	AutoFirewallSet     bool
	UpdateCheckInterval time.Duration
	UpdateTime          string
	UpdateLocation      string
	BinaryPath          string
	ServiceName         string
}

type credentials struct {
	AgentID string `json:"agent_id"`
	Secret  string `json:"secret"`
}

type Client struct {
	opts         Options
	httpClient   *http.Client
	engine       *relay.Engine
	creds        credentials
	startedAt    time.Time
	lastConfig   protocol.AgentConfig
	hasConfig    bool
	updateMu     sync.RWMutex
	updateStatus string
	updateError  string
	autoFirewall bool
	firewall     *firewallManager
}

func New(opts Options, engine *relay.Engine) (*Client, error) {
	opts.ControllerURL = strings.TrimRight(strings.TrimSpace(opts.ControllerURL), "/")
	if opts.ControllerURL == "" {
		return nil, errors.New("controller URL is required")
	}
	if opts.NodeName == "" {
		hostname, _ := os.Hostname()
		opts.NodeName = hostname
	}
	if opts.DataDir == "" {
		opts.DataDir = "/var/lib/cdt-relay"
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = 5 * time.Second
	}
	if opts.HeartbeatEvery <= 0 {
		opts.HeartbeatEvery = 10 * time.Second
	}
	if !opts.AutoUpdateSet {
		opts.AutoUpdate = true
	}
	if !opts.AutoFirewallSet {
		opts.AutoFirewall = true
	}
	if strings.TrimSpace(opts.UpdateTime) == "" {
		opts.UpdateTime = "04:00"
	}
	if strings.TrimSpace(opts.UpdateLocation) == "" {
		opts.UpdateLocation = "Asia/Shanghai"
	}
	if opts.BinaryPath == "" {
		opts.BinaryPath, _ = os.Executable()
	}
	if opts.ServiceName == "" {
		opts.ServiceName = "cdt-relay-agent"
	}
	if engine == nil {
		return nil, errors.New("relay engine is required")
	}
	return &Client{
		opts: opts,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
		engine:       engine,
		updateStatus: "idle",
		startedAt:    time.Now().UTC(),
		autoFirewall: opts.AutoFirewall,
		firewall:     newFirewallManager(opts.DataDir),
	}, nil
}

func (c *Client) Run(ctx context.Context) error {
	if err := os.MkdirAll(c.opts.DataDir, 0700); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	if err := c.recoverPendingUpdate(); err != nil {
		return err
	}
	if err := c.loadOrEnroll(ctx); err != nil {
		return err
	}
	if err := c.loadTrafficUsage(); err != nil {
		fmt.Fprintf(os.Stderr, "traffic usage checkpoint could not be restored: %v\n", err)
	}
	defer func() { _ = c.saveTrafficUsage(c.engine.UsageSnapshot()) }()
	if err := c.loadCachedConfig(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "cached config could not be restored: %v\n", err)
	}
	if c.hasConfig {
		c.syncFirewall(ctx, c.lastConfig)
	}
	if err := c.pollConfig(ctx); err != nil {
		// The agent remains available and retries; the last valid config continues
		// serving even when the controller is temporarily unavailable.
		fmt.Fprintf(os.Stderr, "initial config poll failed: %v\n", err)
	}
	// Only clear the rollback marker after the controller has accepted a
	// heartbeat from this process. If the controller is unreachable or startup
	// is otherwise unhealthy, the marker survives the restart and the next
	// launch can count another failed attempt and roll back safely.
	if err := c.sendHeartbeat(ctx); err == nil {
		_ = c.confirmPendingUpdate()
	}

	pollTicker := time.NewTicker(c.opts.PollInterval)
	heartbeatTicker := time.NewTicker(c.opts.HeartbeatEvery)
	firewallTicker := time.NewTicker(30 * time.Second)
	defer pollTicker.Stop()
	defer heartbeatTicker.Stop()
	defer firewallTicker.Stop()
	var updateTicker *time.Ticker
	var updateTimer *time.Timer
	var updateC <-chan time.Time
	if c.opts.UpdateCheckInterval > 0 {
		updateTicker = time.NewTicker(c.opts.UpdateCheckInterval)
		defer updateTicker.Stop()
		updateC = updateTicker.C
	} else {
		updateTimer = time.NewTimer(c.durationUntilNextUpdate(time.Now()))
		defer updateTimer.Stop()
		updateC = updateTimer.C
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-pollTicker.C:
			if err := c.pollConfig(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "config poll failed: %v\n", err)
			}
		case <-heartbeatTicker.C:
			if err := c.sendHeartbeat(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "heartbeat failed: %v\n", err)
			} else {
				_ = c.confirmPendingUpdate()
			}
		case <-firewallTicker.C:
			if c.hasConfig {
				c.syncFirewall(ctx, c.lastConfig)
			}
		case <-updateC:
			if c.opts.AutoUpdate {
				if err := c.checkForUpdate(ctx); err != nil {
					if errors.Is(err, ErrRestartRequested) {
						// The executable has been replaced atomically. Exit so
						// A service supervisor (systemd, OpenRC, or a container supervisor)
						// starts the new process; the pending marker protects against bad boots.
						return err
					}
					fmt.Fprintf(os.Stderr, "agent update check failed: %v\n", err)
				}
			}
			if updateTimer != nil {
				updateTimer.Reset(c.durationUntilNextUpdate(time.Now().Add(time.Minute)))
			}
		}
	}
}

func (c *Client) durationUntilNextUpdate(now time.Time) time.Duration {
	location := agentUpdateLocation(c.opts.UpdateLocation)
	localNow := now.In(location)
	var hour, minute int
	if _, err := fmt.Sscanf(strings.TrimSpace(c.opts.UpdateTime), "%d:%d", &hour, &minute); err != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		hour, minute = 4, 0
	}
	next := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), hour, minute, 0, 0, location)
	if !next.After(localNow) {
		next = next.Add(24 * time.Hour)
	}
	delay := next.Sub(localNow)
	if delay < time.Second {
		return time.Second
	}
	return delay
}

// agentUpdateLocation always returns a non-nil location. time.LoadLocation can
// fail on stripped-down hosts when the requested zone is absent; passing the
// resulting nil pointer to Time.In or time.Date would panic the Agent during
// startup. The embedded tzdata import above handles normal named zones, while
// the fixed-zone fallback keeps startup safe even if the embedded database is
// unavailable or an invalid zone was configured.
func agentUpdateLocation(requested string) *time.Location {
	if location, err := time.LoadLocation(strings.TrimSpace(requested)); err == nil && location != nil {
		return location
	}
	if location, err := time.LoadLocation("Asia/Shanghai"); err == nil && location != nil {
		return location
	}
	return time.FixedZone("Asia/Shanghai", 8*60*60)
}

func (c *Client) credentialPath() string {
	return filepath.Join(c.opts.DataDir, "credentials.json")
}

func (c *Client) configPath() string {
	return filepath.Join(c.opts.DataDir, "last-valid-config.json")
}

func (c *Client) trafficUsagePath() string {
	return filepath.Join(c.opts.DataDir, "traffic-usage.json")
}

func (c *Client) loadTrafficUsage() error {
	data, err := os.ReadFile(c.trafficUsagePath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var statuses []protocol.ServiceStatus
	if err := json.Unmarshal(data, &statuses); err != nil {
		return err
	}
	c.engine.RestoreUsage(statuses)
	return nil
}

func (c *Client) saveTrafficUsage(statuses []protocol.ServiceStatus) error {
	encoded, err := json.Marshal(statuses)
	if err != nil {
		return err
	}
	return writeFileAtomic(c.trafficUsagePath(), encoded, 0600)
}

func (c *Client) loadCachedConfig(ctx context.Context) error {
	data, err := os.ReadFile(c.configPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var config protocol.AgentConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return err
	}
	if err := c.engine.Apply(ctx, config); err != nil {
		return err
	}
	c.lastConfig = config
	c.hasConfig = true
	c.syncFirewall(ctx, config)
	return nil
}

func (c *Client) syncFirewall(ctx context.Context, config protocol.AgentConfig) {
	if !c.autoFirewall || c.firewall == nil {
		return
	}
	syncCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := c.firewall.sync(syncCtx, config); err != nil && ctx.Err() == nil {
		// Firewall reconciliation is intentionally best-effort. A broken or
		// restricted host firewall must not stop an already-valid relay config.
		fmt.Fprintf(os.Stderr, "firewall sync failed: %v\n", err)
	}
}

func (c *Client) loadOrEnroll(ctx context.Context) error {
	data, err := os.ReadFile(c.credentialPath())
	if err == nil {
		if err := json.Unmarshal(data, &c.creds); err != nil {
			return fmt.Errorf("parse credentials: %w", err)
		}
		if c.creds.AgentID == "" || c.creds.Secret == "" {
			return errors.New("stored credentials are incomplete")
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read credentials: %w", err)
	}
	if c.opts.EnrollmentToken == "" {
		return errors.New("agent is not enrolled and no enrollment token was provided")
	}

	request := protocol.AgentEnrollmentRequest{
		Token:        c.opts.EnrollmentToken,
		NodeName:     c.opts.NodeName,
		PublicIP:     c.opts.PublicIP,
		Architecture: runtime.GOARCH,
		OS:           runtime.GOOS,
		AgentVersion: c.opts.AgentVersion,
	}
	if metadata := discoverAliyunMetadata(ctx, c.httpClient); metadata != nil {
		if request.PublicIP == "" {
			request.PublicIP = metadata.PublicIP
		}
		request.ECSInstanceID = metadata.InstanceID
		request.RegionID = metadata.RegionID
	}
	var response protocol.AgentEnrollmentResponse
	if err := c.requestJSON(ctx, http.MethodPost, "/api/v2/agents/enroll", "", request, &response); err != nil {
		return fmt.Errorf("enroll agent: %w", err)
	}
	if response.AgentID == "" || response.Secret == "" {
		return errors.New("controller returned incomplete credentials")
	}
	c.creds = credentials{AgentID: response.AgentID, Secret: response.Secret}
	encoded, _ := json.MarshalIndent(c.creds, "", "  ")
	if err := writeFileAtomic(c.credentialPath(), encoded, 0600); err != nil {
		return fmt.Errorf("save credentials: %w", err)
	}
	return nil
}

type aliyunMetadata struct {
	PublicIP   string
	InstanceID string
	RegionID   string
}

func discoverAliyunMetadata(ctx context.Context, client *http.Client) *aliyunMetadata {
	token := metadataToken(ctx, client)
	type result struct {
		key   string
		value string
	}
	keys := []string{"instance-id", "region-id", "eipv4", "public-ipv4"}
	results := make(chan result, len(keys))
	for _, key := range keys {
		key := key
		go func() {
			results <- result{key: key, value: metadataValue(ctx, client, token, key)}
		}()
	}
	values := make(map[string]string, len(keys))
	for range keys {
		item := <-results
		values[item.key] = item.value
	}
	metadata := &aliyunMetadata{
		InstanceID: values["instance-id"],
		RegionID:   values["region-id"],
		PublicIP:   values["eipv4"],
	}
	if metadata.PublicIP == "" {
		metadata.PublicIP = values["public-ipv4"]
	}
	if metadata.InstanceID == "" && metadata.RegionID == "" && metadata.PublicIP == "" {
		return nil
	}
	return metadata
}

func metadataToken(parent context.Context, client *http.Client) string {
	ctx, cancel := context.WithTimeout(parent, 500*time.Millisecond)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, "http://100.100.100.200/latest/api/token", nil)
	if err != nil {
		return ""
	}
	request.Header.Set("X-aliyun-ecs-metadata-token-ttl-seconds", "60")
	response, err := client.Do(request)
	if err != nil {
		return ""
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ""
	}
	value, _ := io.ReadAll(io.LimitReader(response.Body, 512))
	return strings.TrimSpace(string(value))
}

func metadataValue(parent context.Context, client *http.Client, token, key string) string {
	ctx, cancel := context.WithTimeout(parent, 800*time.Millisecond)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://100.100.100.200/latest/meta-data/"+key, nil)
	if err != nil {
		return ""
	}
	if token != "" {
		request.Header.Set("X-aliyun-ecs-metadata-token", token)
	}
	response, err := client.Do(request)
	if err != nil {
		return ""
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ""
	}
	value, _ := io.ReadAll(io.LimitReader(response.Body, 512))
	return strings.TrimSpace(string(value))
}

func (c *Client) pollConfig(ctx context.Context) error {
	path := fmt.Sprintf("/api/v2/agents/%s/config?after=%d", c.creds.AgentID, c.engine.Revision())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.opts.ControllerURL+path, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.creds.Secret)
	response, err := c.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNoContent {
		return nil
	}
	if response.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("controller returned %s: %s", response.Status, strings.TrimSpace(string(message)))
	}
	var config protocol.AgentConfig
	if err := json.NewDecoder(response.Body).Decode(&config); err != nil {
		return err
	}
	if config.Revision <= c.engine.Revision() {
		return nil
	}
	if err := c.engine.Apply(ctx, config); err != nil {
		if c.hasConfig {
			_ = c.engine.Apply(ctx, c.lastConfig)
		}
		return fmt.Errorf("apply revision %d: %w", config.Revision, err)
	}
	if err := c.saveCachedConfig(config); err != nil {
		if c.hasConfig {
			_ = c.engine.Apply(ctx, c.lastConfig)
		}
		return fmt.Errorf("persist revision %d: %w", config.Revision, err)
	}
	c.lastConfig = config
	c.hasConfig = true
	c.syncFirewall(ctx, config)
	return nil
}

func (c *Client) saveCachedConfig(config protocol.AgentConfig) error {
	encoded, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(c.configPath(), encoded, 0600)
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) (err error) {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		if err != nil {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err = temporary.Chmod(mode); err != nil {
		return err
	}
	if _, err = temporary.Write(data); err != nil {
		return err
	}
	if err = temporary.Sync(); err != nil {
		return err
	}
	if err = temporary.Close(); err != nil {
		return err
	}
	if err = os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return nil
}

func (c *Client) sendHeartbeat(ctx context.Context) error {
	binaryHash, _ := fileSHA256(c.opts.BinaryPath)
	status, updateErr := c.currentUpdateState()
	services := c.engine.Snapshot()
	if err := c.saveTrafficUsage(c.engine.UsageSnapshot()); err != nil {
		return fmt.Errorf("save traffic usage: %w", err)
	}
	heartbeat := protocol.AgentHeartbeat{
		AgentVersion:    c.opts.AgentVersion,
		BinarySHA256:    binaryHash,
		UpdateStatus:    status,
		UpdateError:     updateErr,
		CurrentRevision: c.engine.Revision(),
		StartedAt:       c.startedAt,
		Services:        services,
	}
	path := fmt.Sprintf("/api/v2/agents/%s/heartbeat", c.creds.AgentID)
	return c.requestJSON(ctx, http.MethodPost, path, c.creds.Secret, heartbeat, nil)
}

func (c *Client) requestJSON(ctx context.Context, method, path, bearer string, payload, output interface{}) error {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.opts.ControllerURL+path, body)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("controller returned %s: %s", response.Status, strings.TrimSpace(string(message)))
	}
	if output != nil && response.StatusCode != http.StatusNoContent {
		return json.NewDecoder(response.Body).Decode(output)
	}
	return nil
}
