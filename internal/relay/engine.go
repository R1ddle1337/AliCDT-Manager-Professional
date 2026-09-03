package relay

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/protocol"
)

type Engine struct {
	mu       sync.Mutex
	services map[string]*serviceRunner
	meters   map[string]*trafficMeter
	restored map[string]protocol.ServiceStatus
	revision int64
}

func NewEngine() *Engine {
	return &Engine{services: make(map[string]*serviceRunner), meters: make(map[string]*trafficMeter), restored: make(map[string]protocol.ServiceStatus)}
}

// RestoreUsage supplies the last durable Agent checkpoint before cached
// listeners are recreated. Configuration epochs prevent an old month's
// billed counter from being restored into a newly reset quota period.
func (e *Engine) RestoreUsage(statuses []protocol.ServiceStatus) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, status := range statuses {
		if status.ID != "" {
			e.restored[status.ID] = status
		}
	}
}

// Apply reconciles listeners without restarting the agent. Services whose
// listen address is unchanged receive a live config update; removed listeners
// stop accepting new traffic while established TCP connections drain normally.
func (e *Engine) Apply(ctx context.Context, desired protocol.AgentConfig) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	seen := make(map[string]struct{}, len(desired.Services))
	normalized := make([]protocol.ServiceConfig, 0, len(desired.Services))
	meterConfigs := make(map[string]protocol.ServiceConfig)
	for _, raw := range desired.Services {
		cfg, err := normalizeService(raw)
		if err != nil {
			return fmt.Errorf("service %q: %w", raw.Name, err)
		}
		if _, duplicate := seen[cfg.ID]; duplicate {
			return fmt.Errorf("duplicate service id %q", cfg.ID)
		}
		seen[cfg.ID] = struct{}{}
		key := serviceMeterKey(cfg)
		if previous, exists := meterConfigs[key]; exists {
			if previous.BillingMode != cfg.BillingMode || previous.BillingEpoch != cfg.BillingEpoch || previous.TrafficLimitGB != cfg.TrafficLimitGB || previous.AccessBlocked != cfg.AccessBlocked || previous.QuotaLeaseID != cfg.QuotaLeaseID || previous.QuotaLeaseBytes != cfg.QuotaLeaseBytes || previous.QuotaLeaseSequence != cfg.QuotaLeaseSequence || !sameOptionalTime(previous.QuotaLeaseExpiresAt, cfg.QuotaLeaseExpiresAt) {
				return fmt.Errorf("shared meter %q has inconsistent quota configuration", key)
			}
		} else {
			meterConfigs[key] = cfg
		}
		normalized = append(normalized, cfg)
	}

	desiredMeters := make(map[string]*trafficMeter, len(meterConfigs))
	for key, cfg := range meterConfigs {
		meter := e.meters[key]
		if meter == nil {
			var restored protocol.ServiceStatus
			for _, candidate := range e.restored {
				if candidate.MeterKey == key && candidate.BillingEpoch == cfg.BillingEpoch && (restored.ID == "" || candidate.BilledBytes > restored.BilledBytes) {
					restored = candidate
				}
			}
			if restored.ID == "" {
				restored = e.restored[cfg.ID]
			}
			meter = newTrafficMeter(cfg, restored)
		} else {
			meter.update(cfg)
		}
		desiredMeters[key] = meter
	}

	for _, cfg := range normalized {
		current := e.services[cfg.ID]
		if !cfg.Enabled {
			if current != nil {
				e.restored[cfg.ID] = current.snapshot()
				current.stop()
				delete(e.services, cfg.ID)
			}
			continue
		}

		meter := desiredMeters[serviceMeterKey(cfg)]
		if current != nil && current.sameListener(cfg) && current.meter == meter {
			current.update(cfg)
			continue
		}

		if current != nil {
			e.restored[cfg.ID] = current.snapshot()
			current.stop()
			delete(e.services, cfg.ID)
		}
		runner := newServiceRunner(ctx, cfg, meter)
		if err := runner.start(); err != nil {
			return fmt.Errorf("start service %q: %w", cfg.Name, err)
		}
		e.services[cfg.ID] = runner
		delete(e.restored, cfg.ID)
	}

	for id, runner := range e.services {
		if _, ok := seen[id]; !ok {
			e.restored[id] = runner.snapshot()
			runner.stop()
			delete(e.services, id)
		}
	}
	e.meters = desiredMeters
	e.revision = desired.Revision
	return nil
}

func (e *Engine) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	for id, runner := range e.services {
		runner.stop()
		delete(e.services, id)
	}
}

func (e *Engine) Revision() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.revision
}

func (e *Engine) Snapshot() []protocol.ServiceStatus {
	e.mu.Lock()
	runners := make([]*serviceRunner, 0, len(e.services))
	for _, runner := range e.services {
		runners = append(runners, runner)
	}
	e.mu.Unlock()

	statuses := make([]protocol.ServiceStatus, 0, len(runners))
	for _, runner := range runners {
		statuses = append(statuses, runner.snapshot())
	}
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].Name < statuses[j].Name })
	return statuses
}

// UsageSnapshot includes stopped services retained for a later re-enable. It
// is written only to the Agent's private checkpoint; heartbeats continue to
// report active configured services through Snapshot.
func (e *Engine) UsageSnapshot() []protocol.ServiceStatus {
	e.mu.Lock()
	statuses := make(map[string]protocol.ServiceStatus, len(e.restored)+len(e.services))
	for id, status := range e.restored {
		status.Listening = false
		statuses[id] = status
	}
	runners := make([]*serviceRunner, 0, len(e.services))
	for _, runner := range e.services {
		runners = append(runners, runner)
	}
	e.mu.Unlock()
	for _, runner := range runners {
		status := runner.snapshot()
		statuses[status.ID] = status
	}
	result := make([]protocol.ServiceStatus, 0, len(statuses))
	for _, status := range statuses {
		result = append(result, status)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

type targetHealth struct {
	Healthy       bool
	Failures      int
	Successes     int
	Latency       time.Duration
	LastCheckedAt time.Time
	DownSince     time.Time
}

type udpSession struct {
	client   *net.UDPAddr
	backend  *net.UDPConn
	targetID string
	lastSeen time.Time
	closed   bool
}

type serviceRunner struct {
	ctx    context.Context
	cancel context.CancelFunc

	cfgMu sync.RWMutex
	cfg   protocol.ServiceConfig
	meter *trafficMeter

	healthMu sync.RWMutex
	health   map[string]*targetHealth

	tcpListener net.Listener
	udpListener *net.UDPConn
	stopOnce    sync.Once
	listening   int32
	rrCounter   uint64

	sessionsMu sync.Mutex
	sessions   map[string]*udpSession

	activeConnections int64
	totalConnections  uint64
	lastErrorMu       sync.RWMutex
	lastError         string
}

var errTrafficQuotaExceeded = errors.New("traffic quota exceeded")
var billingLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

// trafficMeter is shared by every listener belonging to the same user. The
// atomic reservation on billedBytes is therefore a hard aggregate quota even
// when several ports transfer concurrently.
type trafficMeter struct {
	bytesUp       uint64
	bytesDown     uint64
	billedBytes   uint64
	quotaExceeded int32
	billingEpoch  int64
	periodMu      sync.RWMutex
}

type trafficDirection uint8

const (
	trafficIngress trafficDirection = iota // client -> target
	trafficEgress                          // target -> client
)

func serviceMeterKey(cfg protocol.ServiceConfig) string {
	if strings.TrimSpace(cfg.MeterKey) != "" {
		return strings.TrimSpace(cfg.MeterKey)
	}
	return cfg.ID
}

func sameOptionalTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func quotaLimitBytes(cfg protocol.ServiceConfig) uint64 {
	if cfg.QuotaLeaseID != "" {
		return cfg.QuotaLeaseBytes
	}
	return quotaBytes(cfg.TrafficLimitGB)
}

func quotaLeaseExpired(cfg protocol.ServiceConfig) bool {
	return cfg.QuotaLeaseID != "" && (cfg.QuotaLeaseExpiresAt == nil || !time.Now().Before(*cfg.QuotaLeaseExpiresAt))
}

func hasQuotaLimit(cfg protocol.ServiceConfig) bool {
	return cfg.QuotaLeaseID != "" || quotaLimitBytes(cfg) > 0
}

func newTrafficMeter(cfg protocol.ServiceConfig, restored protocol.ServiceStatus) *trafficMeter {
	meter := &trafficMeter{billingEpoch: cfg.BillingEpoch}
	if restored.ID != "" && restored.BillingEpoch == cfg.BillingEpoch {
		meter.bytesUp = restored.BytesUp
		meter.bytesDown = restored.BytesDown
		meter.billedBytes = restored.BilledBytes
	}
	meter.refreshQuotaState(cfg)
	return meter
}

func newServiceRunner(parent context.Context, cfg protocol.ServiceConfig, meter *trafficMeter) *serviceRunner {
	ctx, cancel := context.WithCancel(parent)
	r := &serviceRunner{
		ctx:      ctx,
		cancel:   cancel,
		cfg:      cfg,
		meter:    meter,
		health:   make(map[string]*targetHealth),
		sessions: make(map[string]*udpSession),
	}
	for _, target := range cfg.Targets {
		r.health[target.ID] = &targetHealth{Healthy: true}
	}
	return r
}

func normalizeService(cfg protocol.ServiceConfig) (protocol.ServiceConfig, error) {
	if cfg.ID == "" {
		return cfg, errors.New("id is required")
	}
	if _, _, err := net.SplitHostPort(cfg.Listen); err != nil {
		return cfg, fmt.Errorf("invalid listen address: %w", err)
	}
	cfg.Network = strings.ToLower(strings.TrimSpace(cfg.Network))
	switch cfg.Network {
	case "tcp", "udp", "tcp+udp":
	default:
		return cfg, fmt.Errorf("unsupported network %q", cfg.Network)
	}
	cfg.Mode = strings.ToLower(strings.TrimSpace(cfg.Mode))
	switch cfg.Mode {
	case "failover", "round_robin", "ip_hash", "weighted":
	default:
		return cfg, fmt.Errorf("unsupported mode %q", cfg.Mode)
	}
	cfg.BillingMode = strings.ToLower(strings.TrimSpace(cfg.BillingMode))
	if cfg.BillingMode == "" {
		cfg.BillingMode = protocol.BillingModeBoth
	}
	if cfg.BillingMode == protocol.BillingModeIngress {
		cfg.BillingMode = protocol.BillingModeUpload
	}
	if cfg.BillingMode == protocol.BillingModeEgress {
		cfg.BillingMode = protocol.BillingModeDownload
	}
	if cfg.BillingMode != protocol.BillingModeUpload && cfg.BillingMode != protocol.BillingModeDownload && cfg.BillingMode != protocol.BillingModeBoth {
		return cfg, fmt.Errorf("unsupported billing mode %q", cfg.BillingMode)
	}
	if cfg.TrafficLimitGB < 0 {
		return cfg, errors.New("traffic limit must not be negative")
	}
	if cfg.BillingEpoch <= 0 {
		cfg.BillingEpoch = 1
	}
	if cfg.DialTimeoutMillis <= 0 {
		cfg.DialTimeoutMillis = 2500
	}
	if cfg.UDPIdleTimeoutSeconds <= 0 {
		cfg.UDPIdleTimeoutSeconds = 60
	}
	if cfg.Health.IntervalSeconds <= 0 {
		cfg.Health.IntervalSeconds = 4
	}
	if cfg.Health.TimeoutMillis <= 0 {
		cfg.Health.TimeoutMillis = 2000
	}
	if cfg.Health.FailureThreshold <= 0 {
		cfg.Health.FailureThreshold = 2
	}
	if cfg.Health.SuccessThreshold <= 0 {
		cfg.Health.SuccessThreshold = 3
	}
	if cfg.Health.RecoveryCooldownSecs < 0 {
		cfg.Health.RecoveryCooldownSecs = 0
	}
	enabledTargets := 0
	ids := make(map[string]struct{})
	for i := range cfg.Targets {
		target := &cfg.Targets[i]
		if target.ID == "" || target.Address == "" {
			return cfg, errors.New("target id and address are required")
		}
		if _, exists := ids[target.ID]; exists {
			return cfg, fmt.Errorf("duplicate target id %q", target.ID)
		}
		ids[target.ID] = struct{}{}
		if _, _, err := net.SplitHostPort(target.Address); err != nil {
			return cfg, fmt.Errorf("target %q has invalid address: %w", target.Name, err)
		}
		if target.Weight <= 0 {
			target.Weight = 1
		}
		if target.Enabled {
			enabledTargets++
		}
	}
	if cfg.Enabled && enabledTargets == 0 {
		return cfg, errors.New("at least one enabled target is required")
	}
	return cfg, nil
}

func (r *serviceRunner) sameListener(cfg protocol.ServiceConfig) bool {
	r.cfgMu.RLock()
	defer r.cfgMu.RUnlock()
	return r.cfg.Listen == cfg.Listen && r.cfg.Network == cfg.Network
}

func (r *serviceRunner) update(cfg protocol.ServiceConfig) {
	r.cfgMu.Lock()
	r.cfg = cfg
	r.cfgMu.Unlock()
	r.meter.update(cfg)

	r.healthMu.Lock()
	valid := make(map[string]struct{}, len(cfg.Targets))
	for _, target := range cfg.Targets {
		valid[target.ID] = struct{}{}
		if _, exists := r.health[target.ID]; !exists {
			r.health[target.ID] = &targetHealth{Healthy: true}
		}
	}
	for id := range r.health {
		if _, exists := valid[id]; !exists {
			delete(r.health, id)
		}
	}
	r.healthMu.Unlock()

	// UDP sessions stay pinned unless their target was removed from config.
	r.sessionsMu.Lock()
	for key, session := range r.sessions {
		if _, exists := valid[session.targetID]; !exists {
			session.closed = true
			_ = session.backend.Close()
			delete(r.sessions, key)
		}
	}
	r.sessionsMu.Unlock()
}

func (r *serviceRunner) config() protocol.ServiceConfig {
	r.cfgMu.RLock()
	defer r.cfgMu.RUnlock()
	return r.cfg
}

func (m *trafficMeter) refreshQuotaState(cfg protocol.ServiceConfig) {
	limit := quotaLimitBytes(cfg)
	if (cfg.QuotaLeaseID != "" && limit == 0) || limit > 0 && atomic.LoadUint64(&m.billedBytes) >= limit {
		atomic.StoreInt32(&m.quotaExceeded, 1)
		return
	}
	atomic.StoreInt32(&m.quotaExceeded, 0)
}

func (m *trafficMeter) update(cfg protocol.ServiceConfig) {
	resetBilling := atomic.LoadInt64(&m.billingEpoch) != cfg.BillingEpoch
	if resetBilling {
		m.periodMu.Lock()
		atomic.StoreInt64(&m.billingEpoch, cfg.BillingEpoch)
		atomic.StoreUint64(&m.bytesUp, 0)
		atomic.StoreUint64(&m.bytesDown, 0)
		atomic.StoreUint64(&m.billedBytes, 0)
		m.periodMu.Unlock()
	}
	m.refreshQuotaState(cfg)
}

func currentBillingEpoch(now time.Time) int64 {
	local := now.In(billingLocation)
	return time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, billingLocation).Unix()
}

func sameBillingMonth(epoch int64, now time.Time) bool {
	if epoch <= 1 {
		return false
	}
	value := time.Unix(epoch, 0).In(billingLocation)
	local := now.In(billingLocation)
	return value.Year() == local.Year() && value.Month() == local.Month()
}

func (m *trafficMeter) ensureCurrentBillingPeriod() {
	current := atomic.LoadInt64(&m.billingEpoch)
	if sameBillingMonth(current, time.Now()) {
		return
	}
	m.periodMu.Lock()
	defer m.periodMu.Unlock()
	current = atomic.LoadInt64(&m.billingEpoch)
	if sameBillingMonth(current, time.Now()) {
		return
	}
	atomic.StoreInt64(&m.billingEpoch, currentBillingEpoch(time.Now()))
	atomic.StoreUint64(&m.bytesUp, 0)
	atomic.StoreUint64(&m.bytesDown, 0)
	atomic.StoreUint64(&m.billedBytes, 0)
	atomic.StoreInt32(&m.quotaExceeded, 0)
}

func quotaBytes(limitGB float64) uint64 {
	if limitGB <= 0 {
		return 0
	}
	const gib = float64(1024 * 1024 * 1024)
	if limitGB >= float64(^uint64(0))/gib {
		return ^uint64(0)
	}
	return uint64(limitGB * gib)
}

func (r *serviceRunner) bills(direction trafficDirection) bool {
	switch r.config().BillingMode {
	case protocol.BillingModeUpload:
		return direction == trafficIngress
	case protocol.BillingModeDownload:
		return direction == trafficEgress
	default:
		return true
	}
}

func (r *serviceRunner) reserveBilledBytes(requested int) (int, error) {
	if requested <= 0 {
		return 0, nil
	}
	limit := quotaLimitBytes(r.config())
	if limit == 0 {
		if r.config().QuotaLeaseID != "" {
			atomic.StoreInt32(&r.meter.quotaExceeded, 1)
			return 0, errTrafficQuotaExceeded
		}
		atomic.AddUint64(&r.meter.billedBytes, uint64(requested))
		return requested, nil
	}
	for {
		current := atomic.LoadUint64(&r.meter.billedBytes)
		if current >= limit {
			atomic.StoreInt32(&r.meter.quotaExceeded, 1)
			return 0, errTrafficQuotaExceeded
		}
		remaining := limit - current
		allowed := uint64(requested)
		if allowed > remaining {
			allowed = remaining
		}
		if atomic.CompareAndSwapUint64(&r.meter.billedBytes, current, current+allowed) {
			if current+allowed >= limit {
				atomic.StoreInt32(&r.meter.quotaExceeded, 1)
			}
			return int(allowed), nil
		}
	}
}

func (r *serviceRunner) refundBilledBytes(reserved, written int) {
	if reserved <= written {
		return
	}
	difference := uint64(reserved - written)
	atomic.AddUint64(&r.meter.billedBytes, ^(difference - 1))
	if quotaLimitBytes(r.config()) > atomic.LoadUint64(&r.meter.billedBytes) {
		atomic.StoreInt32(&r.meter.quotaExceeded, 0)
	}
}

func (r *serviceRunner) writeCounted(w io.Writer, payload []byte, direction trafficDirection, allowPartial bool) (int, error) {
	if len(payload) == 0 {
		return 0, nil
	}
	if r.config().AccessBlocked {
		return 0, errTrafficQuotaExceeded
	}
	if quotaLeaseExpired(r.config()) {
		atomic.StoreInt32(&r.meter.quotaExceeded, 1)
		return 0, errTrafficQuotaExceeded
	}
	r.meter.ensureCurrentBillingPeriod()
	r.meter.periodMu.RLock()
	defer r.meter.periodMu.RUnlock()
	originalLength := len(payload)
	billed := r.bills(direction)
	reserved := len(payload)
	if billed {
		var err error
		reserved, err = r.reserveBilledBytes(len(payload))
		if err != nil {
			return 0, err
		}
		if !allowPartial && reserved < originalLength {
			r.refundBilledBytes(reserved, 0)
			return 0, errTrafficQuotaExceeded
		}
		payload = payload[:reserved]
	} else if hasQuotaLimit(r.config()) && atomic.LoadInt32(&r.meter.quotaExceeded) != 0 {
		return 0, errTrafficQuotaExceeded
	}
	written, err := w.Write(payload)
	if billed {
		r.refundBilledBytes(reserved, written)
	}
	if direction == trafficIngress {
		atomic.AddUint64(&r.meter.bytesUp, uint64(written))
	} else {
		atomic.AddUint64(&r.meter.bytesDown, uint64(written))
	}
	if err == nil && written < len(payload) {
		err = io.ErrShortWrite
	}
	if err == nil && billed && hasQuotaLimit(r.config()) && atomic.LoadInt32(&r.meter.quotaExceeded) != 0 {
		err = errTrafficQuotaExceeded
	}
	return written, err
}

type writerFunc struct {
	write func([]byte) (int, error)
}

func (w writerFunc) Write(payload []byte) (int, error) {
	return w.write(payload)
}

func (r *serviceRunner) start() error {
	cfg := r.config()
	var err error
	if cfg.Network == "tcp" || cfg.Network == "tcp+udp" {
		r.tcpListener, err = net.Listen("tcp", cfg.Listen)
		if err != nil {
			return err
		}
	}
	if cfg.Network == "udp" || cfg.Network == "tcp+udp" {
		addr, resolveErr := net.ResolveUDPAddr("udp", cfg.Listen)
		if resolveErr != nil {
			if r.tcpListener != nil {
				_ = r.tcpListener.Close()
			}
			return resolveErr
		}
		r.udpListener, err = net.ListenUDP("udp", addr)
		if err != nil {
			if r.tcpListener != nil {
				_ = r.tcpListener.Close()
			}
			return err
		}
	}
	atomic.StoreInt32(&r.listening, 1)
	if r.tcpListener != nil {
		go r.acceptTCP()
	}
	if r.udpListener != nil {
		go r.serveUDP()
		go r.cleanupUDPSessions()
	}
	if cfg.Health.Enabled {
		go r.healthLoop()
	}
	return nil
}

func (r *serviceRunner) stop() {
	r.stopOnce.Do(func() {
		atomic.StoreInt32(&r.listening, 0)
		r.cancel()
		if r.tcpListener != nil {
			_ = r.tcpListener.Close()
		}
		if r.udpListener != nil {
			_ = r.udpListener.Close()
		}
		r.sessionsMu.Lock()
		for key, session := range r.sessions {
			session.closed = true
			_ = session.backend.Close()
			delete(r.sessions, key)
		}
		r.sessionsMu.Unlock()
	})
}

func (r *serviceRunner) acceptTCP() {
	for {
		client, err := r.tcpListener.Accept()
		if err != nil {
			if r.ctx.Err() != nil {
				return
			}
			r.setLastError(err)
			continue
		}
		go r.handleTCP(client)
	}
}

func (r *serviceRunner) handleTCP(client net.Conn) {
	defer client.Close()
	atomic.AddInt64(&r.activeConnections, 1)
	atomic.AddUint64(&r.totalConnections, 1)
	defer atomic.AddInt64(&r.activeConnections, -1)

	cfg := r.config()
	if cfg.AccessBlocked {
		r.setLastError(errors.New("service access is blocked"))
		return
	}
	if quotaLeaseExpired(cfg) || hasQuotaLimit(cfg) && atomic.LoadInt32(&r.meter.quotaExceeded) != 0 {
		r.setLastError(errTrafficQuotaExceeded)
		return
	}
	candidates := r.candidates(client.RemoteAddr().String())
	var backend net.Conn
	var selected protocol.TargetConfig
	var err error
	for _, target := range candidates {
		backend, err = net.DialTimeout("tcp", target.Address, time.Duration(cfg.DialTimeoutMillis)*time.Millisecond)
		if err == nil {
			selected = target
			r.recordHealth(target.ID, true, 0)
			break
		}
		r.recordHealth(target.ID, false, 0)
	}
	if backend == nil {
		r.setLastError(fmt.Errorf("all targets unavailable: %w", err))
		return
	}
	defer backend.Close()
	_ = selected
	r.copyTCP(client, backend)
}

func (r *serviceRunner) copyTCP(client, backend net.Conn) {
	done := make(chan error, 2)
	go func() {
		_, err := io.Copy(writerFunc{write: func(payload []byte) (int, error) {
			return r.writeCounted(backend, payload, trafficIngress, true)
		}}, client)
		if tcp, ok := backend.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
		done <- err
	}()
	go func() {
		_, err := io.Copy(writerFunc{write: func(payload []byte) (int, error) {
			return r.writeCounted(client, payload, trafficEgress, true)
		}}, backend)
		if tcp, ok := client.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
		done <- err
	}()
	firstErr := <-done
	if errors.Is(firstErr, errTrafficQuotaExceeded) || atomic.LoadInt32(&r.meter.quotaExceeded) != 0 {
		_ = client.Close()
		_ = backend.Close()
	}
	<-done
}

func (r *serviceRunner) serveUDP() {
	buffer := make([]byte, 64*1024)
	for {
		n, client, err := r.udpListener.ReadFromUDP(buffer)
		if err != nil {
			if r.ctx.Err() != nil {
				return
			}
			r.setLastError(err)
			continue
		}
		payload := append([]byte(nil), buffer[:n]...)
		session, err := r.getOrCreateUDPSession(client)
		if err != nil {
			r.setLastError(err)
			continue
		}
		r.sessionsMu.Lock()
		session.lastSeen = time.Now()
		r.sessionsMu.Unlock()
		_, err = r.writeCounted(session.backend, payload, trafficIngress, false)
		if err != nil {
			r.dropUDPSession(client.String(), session)
			if !errors.Is(err, errTrafficQuotaExceeded) {
				r.setLastError(err)
			}
		}
	}
}

func (r *serviceRunner) getOrCreateUDPSession(client *net.UDPAddr) (*udpSession, error) {
	key := client.String()
	r.sessionsMu.Lock()
	if existing := r.sessions[key]; existing != nil && !existing.closed {
		r.sessionsMu.Unlock()
		return existing, nil
	}
	r.sessionsMu.Unlock()

	candidates := r.candidates(key)
	var lastErr error
	for _, target := range candidates {
		addr, err := net.ResolveUDPAddr("udp", target.Address)
		if err != nil {
			lastErr = err
			continue
		}
		backend, err := net.DialUDP("udp", nil, addr)
		if err != nil {
			lastErr = err
			r.recordHealth(target.ID, false, 0)
			continue
		}
		session := &udpSession{client: client, backend: backend, targetID: target.ID, lastSeen: time.Now()}
		r.sessionsMu.Lock()
		if raced := r.sessions[key]; raced != nil && !raced.closed {
			r.sessionsMu.Unlock()
			_ = backend.Close()
			return raced, nil
		}
		r.sessions[key] = session
		r.sessionsMu.Unlock()
		atomic.AddInt64(&r.activeConnections, 1)
		atomic.AddUint64(&r.totalConnections, 1)
		go r.readUDPResponses(key, session)
		return session, nil
	}
	return nil, fmt.Errorf("all UDP targets unavailable: %w", lastErr)
}

func (r *serviceRunner) readUDPResponses(key string, session *udpSession) {
	buffer := make([]byte, 64*1024)
	for {
		n, err := session.backend.Read(buffer)
		if err != nil {
			r.dropUDPSession(key, session)
			return
		}
		_, err = r.writeCounted(writerFunc{write: func(payload []byte) (int, error) {
			return r.udpListener.WriteToUDP(payload, session.client)
		}}, buffer[:n], trafficEgress, false)
		if err != nil {
			r.dropUDPSession(key, session)
			return
		}
	}
}

func (r *serviceRunner) dropUDPSession(key string, session *udpSession) {
	r.sessionsMu.Lock()
	if current := r.sessions[key]; current == session && !session.closed {
		session.closed = true
		delete(r.sessions, key)
		_ = session.backend.Close()
		atomic.AddInt64(&r.activeConnections, -1)
	}
	r.sessionsMu.Unlock()
}

func (r *serviceRunner) cleanupUDPSessions() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.ctx.Done():
			return
		case now := <-ticker.C:
			idle := time.Duration(r.config().UDPIdleTimeoutSeconds) * time.Second
			r.sessionsMu.Lock()
			for key, session := range r.sessions {
				if now.Sub(session.lastSeen) > idle && !session.closed {
					session.closed = true
					_ = session.backend.Close()
					delete(r.sessions, key)
					atomic.AddInt64(&r.activeConnections, -1)
				}
			}
			r.sessionsMu.Unlock()
		}
	}
}

func (r *serviceRunner) candidates(clientAddress string) []protocol.TargetConfig {
	cfg := r.config()
	healthy := make([]protocol.TargetConfig, 0, len(cfg.Targets))
	fallback := make([]protocol.TargetConfig, 0, len(cfg.Targets))
	r.healthMu.RLock()
	for _, target := range cfg.Targets {
		if !target.Enabled {
			continue
		}
		fallback = append(fallback, target)
		state := r.health[target.ID]
		if state == nil || state.Healthy {
			healthy = append(healthy, target)
		}
	}
	r.healthMu.RUnlock()
	if len(healthy) == 0 {
		healthy = fallback
	}
	sort.SliceStable(healthy, func(i, j int) bool {
		if healthy[i].Priority == healthy[j].Priority {
			return healthy[i].ID < healthy[j].ID
		}
		return healthy[i].Priority < healthy[j].Priority
	})
	if len(healthy) < 2 || cfg.Mode == "failover" {
		return healthy
	}

	selected := 0
	switch cfg.Mode {
	case "ip_hash":
		host, _, err := net.SplitHostPort(clientAddress)
		if err != nil {
			host = clientAddress
		}
		h := fnv.New64a()
		_, _ = h.Write([]byte(host))
		selected = weightedIndex(healthy, h.Sum64())
	case "round_robin", "weighted":
		selected = weightedIndex(healthy, atomic.AddUint64(&r.rrCounter, 1)-1)
	}
	return rotateTargets(healthy, selected)
}

func weightedIndex(targets []protocol.TargetConfig, value uint64) int {
	total := 0
	for _, target := range targets {
		total += target.Weight
	}
	if total <= 0 {
		return int(value % uint64(len(targets)))
	}
	needle := int(value % uint64(total))
	for i, target := range targets {
		needle -= target.Weight
		if needle < 0 {
			return i
		}
	}
	return 0
}

func rotateTargets(targets []protocol.TargetConfig, selected int) []protocol.TargetConfig {
	ordered := make([]protocol.TargetConfig, 0, len(targets))
	ordered = append(ordered, targets[selected:]...)
	ordered = append(ordered, targets[:selected]...)
	return ordered
}

func (r *serviceRunner) healthLoop() {
	r.runHealthChecks()
	for {
		interval := time.Duration(r.config().Health.IntervalSeconds) * time.Second
		timer := time.NewTimer(interval)
		select {
		case <-r.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			r.runHealthChecks()
		}
	}
}

func (r *serviceRunner) runHealthChecks() {
	cfg := r.config()
	// A UDP-only service cannot be probed safely with a TCP connect: encrypted
	// protocols generally do not answer arbitrary datagrams. Keep those targets
	// eligible and let the per-client session establish the real data path.
	if cfg.Network == "udp" {
		return
	}
	for _, target := range cfg.Targets {
		if !target.Enabled {
			continue
		}
		started := time.Now()
		conn, err := net.DialTimeout("tcp", target.Address, time.Duration(cfg.Health.TimeoutMillis)*time.Millisecond)
		if err == nil {
			_ = conn.Close()
		}
		r.recordHealth(target.ID, err == nil, time.Since(started))
	}
}

func (r *serviceRunner) recordHealth(targetID string, success bool, latency time.Duration) {
	cfg := r.config()
	r.healthMu.Lock()
	defer r.healthMu.Unlock()
	state := r.health[targetID]
	if state == nil {
		state = &targetHealth{Healthy: true}
		r.health[targetID] = state
	}
	state.LastCheckedAt = time.Now()
	if latency > 0 {
		state.Latency = latency
	}
	if success {
		state.Successes++
		state.Failures = 0
		cooldown := time.Duration(cfg.Health.RecoveryCooldownSecs) * time.Second
		if !state.Healthy && state.Successes >= cfg.Health.SuccessThreshold && time.Since(state.DownSince) >= cooldown {
			state.Healthy = true
			state.DownSince = time.Time{}
		}
		return
	}
	state.Failures++
	state.Successes = 0
	if state.Healthy && state.Failures >= cfg.Health.FailureThreshold {
		state.Healthy = false
		state.DownSince = time.Now()
	}
}

func (r *serviceRunner) setLastError(err error) {
	if err == nil {
		return
	}
	r.lastErrorMu.Lock()
	r.lastError = err.Error()
	r.lastErrorMu.Unlock()
}

func (r *serviceRunner) snapshot() protocol.ServiceStatus {
	r.meter.ensureCurrentBillingPeriod()
	r.meter.periodMu.RLock()
	defer r.meter.periodMu.RUnlock()
	cfg := r.config()
	status := protocol.ServiceStatus{
		ID:                  cfg.ID,
		MeterKey:            serviceMeterKey(cfg),
		Name:                cfg.Name,
		Listening:           atomic.LoadInt32(&r.listening) == 1,
		ActiveConnections:   atomic.LoadInt64(&r.activeConnections),
		TotalConnections:    atomic.LoadUint64(&r.totalConnections),
		BytesUp:             atomic.LoadUint64(&r.meter.bytesUp),
		BytesDown:           atomic.LoadUint64(&r.meter.bytesDown),
		BilledBytes:         atomic.LoadUint64(&r.meter.billedBytes),
		BillingMode:         cfg.BillingMode,
		TrafficLimitGB:      cfg.TrafficLimitGB,
		BillingEpoch:        atomic.LoadInt64(&r.meter.billingEpoch),
		QuotaLeaseID:        cfg.QuotaLeaseID,
		QuotaLeaseBytes:     cfg.QuotaLeaseBytes,
		QuotaLeaseSequence:  cfg.QuotaLeaseSequence,
		QuotaLeaseExpiresAt: cfg.QuotaLeaseExpiresAt,
		QuotaExceeded:       quotaLeaseExpired(cfg) || hasQuotaLimit(cfg) && atomic.LoadInt32(&r.meter.quotaExceeded) != 0,
		AccessBlocked:       cfg.AccessBlocked,
	}
	r.healthMu.RLock()
	for _, target := range cfg.Targets {
		state := r.health[target.ID]
		if state == nil {
			continue
		}
		status.Targets = append(status.Targets, protocol.TargetStatus{
			ID:            target.ID,
			Healthy:       state.Healthy,
			Latency:       state.Latency,
			Failures:      state.Failures,
			Successes:     state.Successes,
			LastCheckedAt: state.LastCheckedAt,
		})
	}
	r.healthMu.RUnlock()
	r.lastErrorMu.RLock()
	status.LastError = r.lastError
	r.lastErrorMu.RUnlock()
	return status
}
