package dispatcher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultRequestTimeout = 10 * time.Second
	defaultMaxSnapshotAge = 2 * time.Minute
	maxSnapshotBytes      = 2 << 20
)

// Snapshot is the credential-free JSON contract served by the controller.
// Keep this type independent from internal/controller so a gateway binary can
// be built and upgraded without importing the controller implementation.
type Snapshot struct {
	PoolID                 string            `json:"pool_id"`
	PoolName               string            `json:"pool_name"`
	FrontDoorMode          string            `json:"front_door_mode,omitempty"`
	Revision               string            `json:"revision,omitempty"`
	ListenPort             int               `json:"listen_port"`
	Network                string            `json:"network"`
	SelectionMode          string            `json:"selection_mode,omitempty"`
	DialTimeoutMillis      int               `json:"dial_timeout_ms"`
	UDPIdleTimeoutSeconds  int               `json:"udp_idle_timeout_seconds"`
	FailureThreshold       int               `json:"failure_threshold,omitempty"`
	FailureCooldownSeconds int               `json:"failure_cooldown_seconds,omitempty"`
	MaxUDPSessions         int               `json:"max_udp_sessions,omitempty"`
	Backends               []SnapshotBackend `json:"backends"`
	GeneratedAt            time.Time         `json:"generated_at"`
}

type SnapshotBackend struct {
	ID                     string  `json:"id"`
	Name                   string  `json:"name"`
	Address                string  `json:"address"`
	Port                   int     `json:"port"`
	Weight                 int     `json:"weight"`
	Enabled                bool    `json:"enabled"`
	TrafficKnown           bool    `json:"traffic_known"`
	TrafficRemainingGB     float64 `json:"traffic_remaining_gb,omitempty"`
	TrafficRateGBPerMinute float64 `json:"traffic_rate_gb_per_minute,omitempty"`
}

type ClientOptions struct {
	ControllerURL  string
	PoolID         string
	Token          string
	HTTPClient     *http.Client
	RequestTimeout time.Duration
	MaxSnapshotAge time.Duration
	Clock          func() time.Time
}

// Client polls one controller pool using a dedicated read-only bearer token.
// It never sends administrator credentials and does not log the token.
type Client struct {
	baseURL        *url.URL
	poolID         string
	token          string
	httpClient     *http.Client
	requestTimeout time.Duration
	maxSnapshotAge time.Duration
	clock          func() time.Time
}

func NewClient(opts ClientOptions) (*Client, error) {
	base := strings.TrimSpace(opts.ControllerURL)
	if base == "" {
		return nil, errors.New("controller URL is required")
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("controller URL must include a scheme and host")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("unsupported controller URL scheme %q", parsed.Scheme)
	}
	if parsed.User != nil {
		return nil, errors.New("controller URL must not contain user credentials")
	}
	poolID := strings.TrimSpace(opts.PoolID)
	if poolID == "" {
		return nil, errors.New("pool ID is required")
	}
	token := strings.TrimSpace(opts.Token)
	if token == "" {
		return nil, errors.New("dispatch token is required")
	}
	httpClient := opts.HTTPClient
	if httpClient == nil {
		origin := parsed.Scheme + "://" + parsed.Host
		httpClient = &http.Client{CheckRedirect: func(request *http.Request, _ []*http.Request) error {
			if request.URL.Scheme+"://"+request.URL.Host != origin {
				return errors.New("controller redirect changed origin")
			}
			return nil
		}}
	}
	requestTimeout := opts.RequestTimeout
	if requestTimeout <= 0 {
		requestTimeout = defaultRequestTimeout
	}
	maxAge := opts.MaxSnapshotAge
	if maxAge <= 0 {
		maxAge = defaultMaxSnapshotAge
	}
	clock := opts.Clock
	if clock == nil {
		clock = time.Now
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return &Client{baseURL: parsed, poolID: poolID, token: token, httpClient: httpClient, requestTimeout: requestTimeout, maxSnapshotAge: maxAge, clock: clock}, nil
}

func (c *Client) endpoint() *url.URL {
	endpoint := *c.baseURL
	path := strings.TrimRight(endpoint.Path, "/") + "/api/v2/dispatch/pools/" + url.PathEscape(c.poolID)
	endpoint.Path = path
	endpoint.RawPath = ""
	return &endpoint
}

// Fetch obtains and validates a fresh snapshot, converting it to the engine's
// internal configuration. Response bodies are bounded and discarded on error
// so an upstream error cannot inject secrets into logs.
func (c *Client) Fetch(ctx context.Context) (Config, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	requestCtx, cancel := context.WithTimeout(ctx, c.requestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, c.endpoint().String(), nil)
	if err != nil {
		return Config{}, fmt.Errorf("build dispatch request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("User-Agent", "alicdt-dispatcher/1")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return Config{}, fmt.Errorf("fetch dispatch snapshot: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxSnapshotBytes))
		return Config{}, fmt.Errorf("controller returned HTTP %d", response.StatusCode)
	}
	var snapshot Snapshot
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxSnapshotBytes))
	if err := decoder.Decode(&snapshot); err != nil {
		return Config{}, fmt.Errorf("decode dispatch snapshot: %w", err)
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Config{}, errors.New("dispatch snapshot contains trailing JSON")
		}
		return Config{}, fmt.Errorf("decode dispatch snapshot trailer: %w", err)
	}
	return c.configFromSnapshot(snapshot)
}

func (c *Client) configFromSnapshot(snapshot Snapshot) (Config, error) {
	if strings.TrimSpace(snapshot.PoolID) != c.poolID {
		return Config{}, errors.New("dispatch snapshot pool ID does not match configuration")
	}
	if snapshot.ListenPort < 1 || snapshot.ListenPort > 65535 {
		return Config{}, errors.New("dispatch snapshot has an invalid listen port")
	}
	now := c.clock().UTC()
	if snapshot.GeneratedAt.IsZero() {
		return Config{}, errors.New("dispatch snapshot has no generation time")
	}
	generated := snapshot.GeneratedAt.UTC()
	if generated.After(now.Add(5 * time.Minute)) {
		return Config{}, errors.New("dispatch snapshot generation time is too far in the future")
	}
	if c.maxSnapshotAge > 0 && now.Sub(generated) > c.maxSnapshotAge {
		return Config{}, fmt.Errorf("dispatch snapshot is stale (age %s)", now.Sub(generated).Round(time.Second))
	}
	network := strings.ToLower(strings.TrimSpace(snapshot.Network))
	if network == "" {
		network = "tcp+udp"
	}
	selection := strings.ToLower(strings.TrimSpace(snapshot.SelectionMode))
	if selection == "" {
		selection = "quota_weighted"
	}
	backends := make([]Backend, 0, len(snapshot.Backends))
	seen := make(map[string]struct{}, len(snapshot.Backends))
	for _, item := range snapshot.Backends {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			return Config{}, errors.New("dispatch backend ID is required")
		}
		if _, exists := seen[id]; exists {
			return Config{}, fmt.Errorf("duplicate dispatch backend ID %q", id)
		}
		seen[id] = struct{}{}
		address := strings.TrimSpace(item.Address)
		if address == "" {
			return Config{}, fmt.Errorf("dispatch backend %q has no address", id)
		}
		// The controller only serializes eligible members, so every decoded
		// backend is enabled. (The field remains on the wire for forward
		// compatibility with a future explicit disable state.)
		backends = append(backends, Backend{ID: id, Name: strings.TrimSpace(item.Name), Address: address, Weight: item.Weight, Enabled: true, TrafficKnown: item.TrafficKnown, TrafficRemainingGB: item.TrafficRemainingGB, TrafficRateGBPerMinute: item.TrafficRateGBPerMinute})
	}
	revision := strings.TrimSpace(snapshot.Revision)
	if revision == "" {
		revision = snapshotRevision(snapshot)
	}
	return Config{Revision: revision, GeneratedAt: generated, Network: network, SelectionMode: selection, DialTimeout: time.Duration(snapshot.DialTimeoutMillis) * time.Millisecond, UDPIdleTimeout: time.Duration(snapshot.UDPIdleTimeoutSeconds) * time.Second, FailureThreshold: snapshot.FailureThreshold, FailureCooldown: time.Duration(snapshot.FailureCooldownSeconds) * time.Second, MaxUDPSessions: snapshot.MaxUDPSessions, Backends: backends}, nil
}

func snapshotRevision(snapshot Snapshot) string {
	copy := snapshot
	copy.Revision = ""
	copy.GeneratedAt = time.Time{}
	data, _ := json.Marshal(copy)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])[:16]
}
