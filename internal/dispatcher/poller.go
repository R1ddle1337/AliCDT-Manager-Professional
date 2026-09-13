package dispatcher

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

const (
	defaultPollInterval = 15 * time.Second
	defaultStaleAfter   = 2 * time.Minute
)

type PollerOptions struct {
	Interval   time.Duration
	StaleAfter time.Duration
	Clock      func() time.Time
	// ListenerNetwork is the transport set actually bound by the process. A
	// snapshot requiring UDP must not be considered ready by a TCP-only gateway.
	ListenerNetwork string
}

type PollerState struct {
	LastAttemptAt     time.Time `json:"last_attempt_at,omitempty"`
	LastSuccessAt     time.Time `json:"last_success_at,omitempty"`
	ConsecutiveErrors int       `json:"consecutive_errors"`
	LastError         string    `json:"last_error,omitempty"`
	Stale             bool      `json:"stale"`
}

// Poller keeps a dispatcher on the last valid controller snapshot and drains
// it after the snapshot has been unavailable for StaleAfter. This prevents a
// gateway from routing new traffic through a member that may have exhausted
// its CDT allowance while the control plane is unreachable.
type Poller struct {
	client          *Client
	engine          *Engine
	clock           func() time.Time
	interval        time.Duration
	staleAfter      time.Duration
	listenerNetwork string

	mu    sync.RWMutex
	state PollerState
}

func NewPoller(client *Client, engine *Engine, opts PollerOptions) (*Poller, error) {
	if client == nil {
		return nil, errors.New("dispatcher client is required")
	}
	if engine == nil {
		return nil, errors.New("dispatcher engine is required")
	}
	interval := opts.Interval
	if interval <= 0 {
		interval = defaultPollInterval
	}
	staleAfter := opts.StaleAfter
	if staleAfter <= 0 {
		staleAfter = defaultStaleAfter
	}
	if staleAfter < interval {
		staleAfter = interval
	}
	clock := opts.Clock
	if clock == nil {
		clock = time.Now
	}
	listenerNetwork := normalizeListenerNetwork(opts.ListenerNetwork)
	if strings.TrimSpace(opts.ListenerNetwork) != "" && listenerNetwork == "" {
		return nil, errors.New("unsupported listener network")
	}
	return &Poller{client: client, engine: engine, clock: clock, interval: interval, staleAfter: staleAfter, listenerNetwork: listenerNetwork}, nil
}

func (p *Poller) State() PollerState {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.state
}

func (p *Poller) Interval() time.Duration { return p.interval }

// Sync performs one configuration fetch. Errors are returned for logging and
// reflected in State; callers may continue serving the last valid config.
func (p *Poller) Sync(ctx context.Context) error {
	now := p.clock().UTC()
	p.mu.Lock()
	p.state.LastAttemptAt = now
	p.mu.Unlock()
	cfg, err := p.client.Fetch(ctx)
	if err != nil {
		p.recordError(now, err)
		return err
	}
	if p.listenerNetwork != "" && !networkCovers(p.listenerNetwork, cfg.Network) {
		err := errors.New("controller snapshot requires a transport not bound by this gateway")
		p.recordError(now, err)
		return err
	}
	if err := p.engine.Apply(cfg); err != nil {
		p.recordError(now, err)
		return err
	}
	p.mu.Lock()
	p.state.LastSuccessAt = now
	p.state.ConsecutiveErrors = 0
	p.state.LastError = ""
	p.state.Stale = false
	p.mu.Unlock()
	return nil
}

func normalizeListenerNetwork(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "tcp" || value == "udp" || value == "tcp+udp" {
		return value
	}
	return ""
}

func networkCovers(listener, required string) bool {
	listener = normalizeListenerNetwork(listener)
	required = normalizeListenerNetwork(required)
	if listener == "" || required == "" {
		return false
	}
	return listener == "tcp+udp" || listener == required || (listener == "tcp+udp" && required == "tcp+udp")
}

func (p *Poller) recordError(now time.Time, err error) {
	p.engine.SetConfigError(err)
	p.mu.Lock()
	p.state.ConsecutiveErrors++
	p.state.LastError = err.Error()
	lastSuccess := p.state.LastSuccessAt
	if lastSuccess.IsZero() || now.Sub(lastSuccess) >= p.staleAfter {
		p.state.Stale = true
	}
	shouldDrain := p.state.Stale
	p.mu.Unlock()
	if !shouldDrain {
		return
	}
	// Keep transport settings from the last valid configuration while removing
	// all backends. Apply is intentionally best-effort: the existing config is
	// already safer than a partially constructed replacement if it fails.
	current := p.engine.Config()
	current.Backends = nil
	current.Revision = ""
	current.GeneratedAt = now
	if applyErr := p.engine.Apply(current); applyErr != nil {
		p.engine.SetConfigError(applyErr)
		return
	}
	// Apply clears the engine's previous error on success; restore the polling
	// failure so /stats and /metrics continue to explain why the gateway is
	// intentionally drained.
	p.engine.SetConfigError(err)
}

func (p *Poller) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	_ = p.Sync(ctx)
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := p.Sync(ctx); err != nil && ctx.Err() != nil {
				return nil
			}
		}
	}
}
