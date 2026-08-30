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
	"time"

	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/protocol"
	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/relay"
)

type Options struct {
	ControllerURL   string
	EnrollmentToken string
	NodeName        string
	PublicIP        string
	DataDir         string
	AgentVersion    string
	PollInterval    time.Duration
	HeartbeatEvery  time.Duration
}

type credentials struct {
	AgentID string `json:"agent_id"`
	Secret  string `json:"secret"`
}

type Client struct {
	opts       Options
	httpClient *http.Client
	engine     *relay.Engine
	creds      credentials
	startedAt  time.Time
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
	if engine == nil {
		return nil, errors.New("relay engine is required")
	}
	return &Client{
		opts: opts,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
		engine:    engine,
		startedAt: time.Now().UTC(),
	}, nil
}

func (c *Client) Run(ctx context.Context) error {
	if err := os.MkdirAll(c.opts.DataDir, 0700); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	if err := c.loadOrEnroll(ctx); err != nil {
		return err
	}
	if err := c.pollConfig(ctx); err != nil {
		// The agent remains available and retries; the last valid config continues
		// serving even when the controller is temporarily unavailable.
		fmt.Fprintf(os.Stderr, "initial config poll failed: %v\n", err)
	}
	_ = c.sendHeartbeat(ctx)

	pollTicker := time.NewTicker(c.opts.PollInterval)
	heartbeatTicker := time.NewTicker(c.opts.HeartbeatEvery)
	defer pollTicker.Stop()
	defer heartbeatTicker.Stop()

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
			}
		}
	}
}

func (c *Client) credentialPath() string {
	return filepath.Join(c.opts.DataDir, "credentials.json")
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
	var response protocol.AgentEnrollmentResponse
	if err := c.requestJSON(ctx, http.MethodPost, "/api/v2/agents/enroll", "", request, &response); err != nil {
		return fmt.Errorf("enroll agent: %w", err)
	}
	if response.AgentID == "" || response.Secret == "" {
		return errors.New("controller returned incomplete credentials")
	}
	c.creds = credentials{AgentID: response.AgentID, Secret: response.Secret}
	encoded, _ := json.MarshalIndent(c.creds, "", "  ")
	if err := os.WriteFile(c.credentialPath(), encoded, 0600); err != nil {
		return fmt.Errorf("save credentials: %w", err)
	}
	return nil
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
		return fmt.Errorf("apply revision %d: %w", config.Revision, err)
	}
	return nil
}

func (c *Client) sendHeartbeat(ctx context.Context) error {
	heartbeat := protocol.AgentHeartbeat{
		AgentVersion:    c.opts.AgentVersion,
		CurrentRevision: c.engine.Revision(),
		StartedAt:       c.startedAt,
		Services:        c.engine.Snapshot(),
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
