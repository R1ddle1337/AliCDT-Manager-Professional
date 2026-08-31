// Package dispatcher implements the optional fixed-front-door L4 tier.
//
// The dispatcher forwards encrypted bytes without terminating SS/VLESS/TLS.
// It chooses a ready CDT Relay for each TCP connection or UDP session; the
// Relay remains responsible for selecting the landing node.
package dispatcher

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultDialTimeout       = 2500 * time.Millisecond
	defaultUDPIdleTimeout    = 60 * time.Second
	defaultFailureThreshold  = 2
	defaultFailureCooldown   = 10 * time.Second
	minimumQuotaWeightFactor = 0.1
	maxSelectionWeight       = 1_000_000
	defaultMaxUDPSessions    = 65536
)

// ErrClosed is returned when an operation is attempted after Engine.Close.
var ErrClosed = errors.New("dispatcher engine is closed")

// Backend is a ready (or potentially ready) Relay endpoint. Traffic fields
// are account-level CDT measurements supplied by the controller.
type Backend struct {
	ID                     string
	Name                   string
	Address                string
	Weight                 int
	Enabled                bool
	TrafficKnown           bool
	TrafficRemainingGB     float64
	TrafficRateGBPerMinute float64
}

// Config is the live dispatcher configuration for one logical entry pool.
// An empty Backends slice is valid and causes new traffic to be rejected while
// retaining the listening socket for fast recovery.
type Config struct {
	Revision         string
	GeneratedAt      time.Time
	Network          string
	SelectionMode    string
	DialTimeout      time.Duration
	UDPIdleTimeout   time.Duration
	FailureThreshold int
	FailureCooldown  time.Duration
	MaxUDPSessions   int
	Backends         []Backend
}

type Stats struct {
	ActiveConnections   int64     `json:"active_connections"`
	TotalConnections    uint64    `json:"total_connections"`
	BytesUp             uint64    `json:"bytes_up"`
	BytesDown           uint64    `json:"bytes_down"`
	Rejected            uint64    `json:"rejected"`
	BackendFailures     uint64    `json:"backend_failures"`
	BackendCount        int       `json:"backend_count"`
	HealthyBackendCount int       `json:"healthy_backend_count"`
	UDPSessionCount     int       `json:"udp_session_count"`
	Revision            string    `json:"revision,omitempty"`
	GeneratedAt         time.Time `json:"generated_at,omitempty"`
	LastConfigError     string    `json:"last_config_error,omitempty"`
	ConfigAppliedAt     time.Time `json:"config_applied_at,omitempty"`
}

type backendState struct {
	backend        Backend
	failures       int
	unhealthyUntil time.Time
}

type udpSession struct {
	key       string
	client    *net.UDPAddr
	backend   *net.UDPConn
	backendID string
	lastSeen  time.Time
	closeOnce sync.Once
}

type Engine struct {
	mu       sync.RWMutex
	cfg      Config
	backends map[string]*backendState
	rr       uint64

	// Track TCP resources so shutdown can interrupt blocked copies.
	tcpMu        sync.Mutex
	tcpConns     map[net.Conn]struct{}
	tcpListeners map[net.Listener]struct{}

	sessionsMu sync.Mutex
	sessions   map[string]*udpSession
	listenerMu sync.RWMutex
	udp        *net.UDPConn

	activeConnections int64
	totalConnections  uint64
	bytesUp           uint64
	bytesDown         uint64
	rejected          uint64
	backendFailures   uint64
	lastConfigErrMu   sync.RWMutex
	lastConfigErr     string
	configAppliedAt   time.Time
	closed            int32
}

func NewEngine() *Engine {
	return &Engine{
		backends:     make(map[string]*backendState),
		tcpConns:     make(map[net.Conn]struct{}),
		tcpListeners: make(map[net.Listener]struct{}),
		sessions:     make(map[string]*udpSession),
	}
}

// Apply atomically replaces the live backend set. Existing TCP streams are
// intentionally left alone; UDP sessions using removed backends are closed so
// their next datagram can be assigned to a healthy backend.
func (e *Engine) Apply(cfg Config) error {
	if atomic.LoadInt32(&e.closed) != 0 {
		return ErrClosed
	}
	normalized, err := normalizeConfig(cfg)
	if err != nil {
		e.setConfigError(err)
		return err
	}
	newBackends := make(map[string]*backendState, len(normalized.Backends))
	removed := make(map[string]struct{})
	e.mu.Lock()
	for _, backend := range normalized.Backends {
		previous := e.backends[backend.ID]
		if previous == nil || previous.backend.Address != backend.Address {
			newBackends[backend.ID] = &backendState{backend: backend}
			if previous != nil {
				removed[backend.ID] = struct{}{}
			}
			continue
		}
		if previous.backend.Enabled != backend.Enabled {
			removed[backend.ID] = struct{}{}
		}
		previous.backend = backend
		newBackends[backend.ID] = previous
	}
	for id := range e.backends {
		if _, ok := newBackends[id]; !ok {
			removed[id] = struct{}{}
		}
	}
	oldNetwork := e.cfg.Network
	e.backends = newBackends
	e.cfg = normalized
	e.configAppliedAt = time.Now().UTC()
	e.mu.Unlock()

	if len(removed) > 0 {
		e.closeSessionsFor(removed)
	}
	if networkAllows(oldNetwork, "udp") && !networkAllows(normalized.Network, "udp") {
		e.closeAllSessions()
	}
	e.setConfigError(nil)
	return nil
}

func normalizeConfig(cfg Config) (Config, error) {
	cfg.Revision = strings.TrimSpace(cfg.Revision)
	cfg.Network = strings.ToLower(strings.TrimSpace(cfg.Network))
	if cfg.Network == "" {
		cfg.Network = "tcp+udp"
	}
	if cfg.Network != "tcp" && cfg.Network != "udp" && cfg.Network != "tcp+udp" {
		return Config{}, fmt.Errorf("unsupported network %q", cfg.Network)
	}
	cfg.SelectionMode = strings.ToLower(strings.TrimSpace(cfg.SelectionMode))
	if cfg.SelectionMode == "" {
		cfg.SelectionMode = "quota_weighted"
	}
	switch cfg.SelectionMode {
	case "failover", "round_robin", "weighted", "quota_weighted", "ip_hash":
	default:
		return Config{}, fmt.Errorf("unsupported selection mode %q", cfg.SelectionMode)
	}
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = defaultDialTimeout
	}
	if cfg.DialTimeout > 60*time.Second {
		cfg.DialTimeout = 60 * time.Second
	}
	if cfg.UDPIdleTimeout <= 0 {
		cfg.UDPIdleTimeout = defaultUDPIdleTimeout
	}
	if cfg.UDPIdleTimeout > 24*time.Hour {
		cfg.UDPIdleTimeout = 24 * time.Hour
	}
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = defaultFailureThreshold
	}
	if cfg.FailureCooldown <= 0 {
		cfg.FailureCooldown = defaultFailureCooldown
	}
	if cfg.FailureCooldown > 24*time.Hour {
		cfg.FailureCooldown = 24 * time.Hour
	}
	if cfg.MaxUDPSessions <= 0 {
		cfg.MaxUDPSessions = defaultMaxUDPSessions
	}
	if cfg.MaxUDPSessions > 1_000_000 {
		cfg.MaxUDPSessions = 1_000_000
	}
	cfg.Backends = append([]Backend(nil), cfg.Backends...)
	seen := make(map[string]struct{}, len(cfg.Backends))
	for i := range cfg.Backends {
		backend := &cfg.Backends[i]
		backend.ID = strings.TrimSpace(backend.ID)
		backend.Name = strings.TrimSpace(backend.Name)
		backend.Address = strings.TrimSpace(backend.Address)
		if backend.ID == "" || backend.Address == "" {
			return Config{}, errors.New("backend id and address are required")
		}
		if _, ok := seen[backend.ID]; ok {
			return Config{}, fmt.Errorf("duplicate backend id %q", backend.ID)
		}
		seen[backend.ID] = struct{}{}
		host, port, splitErr := net.SplitHostPort(backend.Address)
		if splitErr != nil || strings.TrimSpace(host) == "" {
			if splitErr == nil {
				splitErr = errors.New("empty host")
			}
			return Config{}, fmt.Errorf("backend %q has invalid address: %w", backend.ID, splitErr)
		}
		portNumber, parseErr := strconv.Atoi(port)
		if parseErr != nil || portNumber < 1 || portNumber > 65535 {
			return Config{}, fmt.Errorf("backend %q has invalid port", backend.ID)
		}
		if backend.Weight <= 0 {
			backend.Weight = 1
		}
		if backend.Weight > maxSelectionWeight {
			backend.Weight = maxSelectionWeight
		}
		if math.IsNaN(backend.TrafficRemainingGB) || math.IsInf(backend.TrafficRemainingGB, 0) || math.IsNaN(backend.TrafficRateGBPerMinute) || math.IsInf(backend.TrafficRateGBPerMinute, 0) {
			return Config{}, fmt.Errorf("backend %q has invalid traffic metadata", backend.ID)
		}
		if backend.TrafficRemainingGB < 0 {
			backend.TrafficRemainingGB = 0
		}
		if backend.TrafficRateGBPerMinute < 0 {
			backend.TrafficRateGBPerMinute = 0
		}
	}
	return cfg, nil
}

func (e *Engine) Config() Config {
	e.mu.RLock()
	defer e.mu.RUnlock()
	cfg := e.cfg
	cfg.Backends = append([]Backend(nil), cfg.Backends...)
	return cfg
}

func networkAllows(network, transport string) bool {
	network = strings.ToLower(strings.TrimSpace(network))
	return network == transport || network == "tcp+udp"
}

// ServeTCP accepts connections until the context is canceled or the listener
// fails. The listener is owned by the caller and is closed on cancellation.
func (e *Engine) ServeTCP(ctx context.Context, listener net.Listener) error {
	if listener == nil {
		return errors.New("TCP listener is required")
	}
	if atomic.LoadInt32(&e.closed) != 0 {
		return ErrClosed
	}
	e.tcpMu.Lock()
	e.tcpListeners[listener] = struct{}{}
	e.tcpMu.Unlock()
	defer func() {
		e.tcpMu.Lock()
		delete(e.tcpListeners, listener)
		e.tcpMu.Unlock()
		_ = listener.Close()
	}()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = listener.Close()
			e.closeTCPConnections()
		case <-done:
		}
	}()
	for {
		client, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil || atomic.LoadInt32(&e.closed) != 0 {
				return nil
			}
			if ne, ok := err.(net.Error); ok && ne.Temporary() {
				time.Sleep(5 * time.Millisecond)
				continue
			}
			return err
		}
		go e.handleTCP(client)
	}
}

func (e *Engine) handleTCP(client net.Conn) {
	e.tcpMu.Lock()
	e.tcpConns[client] = struct{}{}
	e.tcpMu.Unlock()
	defer func() {
		e.tcpMu.Lock()
		delete(e.tcpConns, client)
		e.tcpMu.Unlock()
		_ = client.Close()
	}()
	atomic.AddInt64(&e.activeConnections, 1)
	atomic.AddUint64(&e.totalConnections, 1)
	defer atomic.AddInt64(&e.activeConnections, -1)

	cfg := e.Config()
	if !networkAllows(cfg.Network, "tcp") {
		atomic.AddUint64(&e.rejected, 1)
		return
	}
	key := ""
	if client.RemoteAddr() != nil {
		key = stableTCPClientKey(client.RemoteAddr().String())
	}
	var upstream net.Conn
	for _, backend := range e.orderedBackends(key) {
		dialer := net.Dialer{Timeout: cfg.DialTimeout}
		conn, err := dialer.Dial("tcp", backend.Address)
		if err != nil {
			e.markFailure(backend.ID)
			continue
		}
		e.markSuccess(backend.ID)
		upstream = conn
		break
	}
	if upstream == nil {
		atomic.AddUint64(&e.rejected, 1)
		return
	}
	e.tcpMu.Lock()
	e.tcpConns[upstream] = struct{}{}
	e.tcpMu.Unlock()
	defer func() {
		e.tcpMu.Lock()
		delete(e.tcpConns, upstream)
		e.tcpMu.Unlock()
		_ = upstream.Close()
	}()
	e.copyTCP(client, upstream)
}

func (e *Engine) copyTCP(client, backend net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(countWriter{writer: backend, counter: &e.bytesUp}, client)
		if tcp, ok := backend.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(countWriter{writer: client, counter: &e.bytesDown}, backend)
		if tcp, ok := client.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
		done <- struct{}{}
	}()
	<-done
	<-done
}

type countWriter struct {
	writer  io.Writer
	counter *uint64
}

func (w countWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	atomic.AddUint64(w.counter, uint64(n))
	return n, err
}

// ServeUDP proxies one UDP socket. A session is keyed by the client address
// and remains pinned until it is idle or its backend is removed.
func (e *Engine) ServeUDP(ctx context.Context, listener *net.UDPConn) error {
	if listener == nil {
		return errors.New("UDP listener is required")
	}
	if atomic.LoadInt32(&e.closed) != 0 {
		return ErrClosed
	}
	e.listenerMu.Lock()
	if e.udp != nil && e.udp != listener {
		e.listenerMu.Unlock()
		return errors.New("UDP listener is already running")
	}
	e.udp = listener
	e.listenerMu.Unlock()
	serveCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer func() {
		e.listenerMu.Lock()
		if e.udp == listener {
			e.udp = nil
		}
		e.listenerMu.Unlock()
		_ = listener.Close()
		e.closeAllSessions()
	}()
	go func() {
		<-serveCtx.Done()
		_ = listener.Close()
		e.closeAllSessions()
	}()
	go e.cleanupUDPSessions(serveCtx)

	buffer := make([]byte, 64*1024)
	for {
		n, client, err := listener.ReadFromUDP(buffer)
		if err != nil {
			if serveCtx.Err() != nil || atomic.LoadInt32(&e.closed) != 0 {
				return nil
			}
			return err
		}
		if n == 0 {
			continue
		}
		cfg := e.Config()
		if !networkAllows(cfg.Network, "udp") {
			atomic.AddUint64(&e.rejected, 1)
			continue
		}
		payload := append([]byte(nil), buffer[:n]...)
		session, err := e.getOrCreateUDPSession(client)
		if err != nil {
			atomic.AddUint64(&e.rejected, 1)
			continue
		}
		e.touchSession(session)
		if cfg.DialTimeout > 0 {
			_ = session.backend.SetWriteDeadline(time.Now().Add(cfg.DialTimeout))
		}
		written, writeErr := session.backend.Write(payload)
		_ = session.backend.SetWriteDeadline(time.Time{})
		atomic.AddUint64(&e.bytesUp, uint64(written))
		if writeErr != nil {
			e.markFailure(session.backendID)
			e.dropSession(session.key, session)
		}
	}
}

func (e *Engine) getOrCreateUDPSession(client *net.UDPAddr) (*udpSession, error) {
	if client == nil {
		return nil, errors.New("UDP client address is required")
	}
	key := client.String()
	var lastErr error
	for _, backend := range e.orderedBackends(key) {
		addr, err := net.ResolveUDPAddr("udp", backend.Address)
		if err != nil {
			lastErr = err
			e.markFailure(backend.ID)
			continue
		}
		// UDP connect has no handshake; resolving the address is the only
		// synchronous failure point before the first datagram is written.
		conn, err := net.DialUDP("udp", nil, addr)
		if err != nil {
			lastErr = err
			e.markFailure(backend.ID)
			continue
		}
		e.markSuccess(backend.ID)
		session := &udpSession{key: key, client: client, backend: conn, backendID: backend.ID, lastSeen: time.Now().UTC()}
		e.mu.RLock()
		current := e.backends[backend.ID]
		stillConfigured := current != nil && current.backend.Enabled && current.backend.Address == backend.Address
		if !stillConfigured || atomic.LoadInt32(&e.closed) != 0 {
			e.mu.RUnlock()
			_ = conn.Close()
			continue
		}
		// Hold the config read lock while publishing the session. Apply takes
		// the write lock before it scans sessions, so it cannot remove the
		// backend between this check and insertion.
		e.sessionsMu.Lock()
		if raced := e.sessions[key]; raced != nil {
			e.sessionsMu.Unlock()
			e.mu.RUnlock()
			_ = conn.Close()
			return raced, nil
		}
		if atomic.LoadInt32(&e.closed) != 0 {
			e.sessionsMu.Unlock()
			e.mu.RUnlock()
			_ = conn.Close()
			return nil, ErrClosed
		}
		if maxSessions := e.cfg.MaxUDPSessions; maxSessions > 0 && len(e.sessions) >= maxSessions {
			e.sessionsMu.Unlock()
			e.mu.RUnlock()
			_ = conn.Close()
			return nil, errors.New("UDP session limit reached")
		}
		atomic.AddInt64(&e.activeConnections, 1)
		e.sessions[key] = session
		e.sessionsMu.Unlock()
		e.mu.RUnlock()
		atomic.AddUint64(&e.totalConnections, 1)
		go e.readUDPResponses(session)
		return session, nil
	}
	if lastErr == nil {
		lastErr = errors.New("no ready dispatcher backends")
	}
	return nil, lastErr
}

func (e *Engine) readUDPResponses(session *udpSession) {
	buffer := make([]byte, 64*1024)
	for {
		n, err := session.backend.Read(buffer)
		if err != nil {
			e.dropSession(session.key, session)
			return
		}
		e.sessionsMu.Lock()
		current := e.sessions[session.key] == session
		client := session.client
		if current {
			session.lastSeen = time.Now().UTC()
		}
		e.sessionsMu.Unlock()
		if !current {
			return
		}
		listener := e.udpListener()
		if listener == nil {
			e.dropSession(session.key, session)
			return
		}
		written, writeErr := listener.WriteToUDP(buffer[:n], client)
		atomic.AddUint64(&e.bytesDown, uint64(written))
		if writeErr != nil {
			e.dropSession(session.key, session)
			return
		}
	}
}

func (e *Engine) udpListener() *net.UDPConn {
	e.listenerMu.RLock()
	defer e.listenerMu.RUnlock()
	return e.udp
}

func (e *Engine) touchSession(session *udpSession) {
	e.sessionsMu.Lock()
	if current := e.sessions[session.key]; current == session {
		session.lastSeen = time.Now().UTC()
	}
	e.sessionsMu.Unlock()
}

func (e *Engine) cleanupUDPSessions(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			idle := e.Config().UDPIdleTimeout
			var expired []*udpSession
			e.sessionsMu.Lock()
			for _, session := range e.sessions {
				if now.Sub(session.lastSeen) > idle {
					expired = append(expired, session)
				}
			}
			e.sessionsMu.Unlock()
			for _, session := range expired {
				e.dropSession(session.key, session)
			}
		}
	}
}

func (e *Engine) dropSession(key string, session *udpSession) {
	e.sessionsMu.Lock()
	if current := e.sessions[key]; current != session {
		e.sessionsMu.Unlock()
		return
	}
	delete(e.sessions, key)
	e.sessionsMu.Unlock()
	session.closeOnce.Do(func() {
		_ = session.backend.Close()
		atomic.AddInt64(&e.activeConnections, -1)
	})
}

func (e *Engine) closeSessionsFor(ids map[string]struct{}) {
	if len(ids) == 0 {
		return
	}
	e.sessionsMu.Lock()
	var sessions []*udpSession
	for _, session := range e.sessions {
		if _, ok := ids[session.backendID]; ok {
			sessions = append(sessions, session)
		}
	}
	e.sessionsMu.Unlock()
	for _, session := range sessions {
		e.dropSession(session.key, session)
	}
}

func (e *Engine) closeAllSessions() {
	e.sessionsMu.Lock()
	var sessions []*udpSession
	for _, session := range e.sessions {
		sessions = append(sessions, session)
	}
	e.sessionsMu.Unlock()
	for _, session := range sessions {
		e.dropSession(session.key, session)
	}
}

func (e *Engine) closeTCPConnections() {
	e.tcpMu.Lock()
	connections := make([]net.Conn, 0, len(e.tcpConns))
	for conn := range e.tcpConns {
		connections = append(connections, conn)
	}
	e.tcpMu.Unlock()
	for _, conn := range connections {
		_ = conn.Close()
	}
}

// Close stops all active sessions and listeners. It is safe to call more than
// once and does not modify the persisted controller configuration.
func (e *Engine) Close() {
	if !atomic.CompareAndSwapInt32(&e.closed, 0, 1) {
		return
	}
	e.closeAllSessions()
	e.closeTCPConnections()
	e.tcpMu.Lock()
	listeners := make([]net.Listener, 0, len(e.tcpListeners))
	for listener := range e.tcpListeners {
		listeners = append(listeners, listener)
	}
	e.tcpMu.Unlock()
	for _, listener := range listeners {
		_ = listener.Close()
	}
	listener := e.udpListener()
	if listener != nil {
		_ = listener.Close()
	}
}

func (e *Engine) orderedBackends(clientKey string) []Backend {
	e.mu.RLock()
	now := time.Now()
	items := make([]Backend, 0, len(e.cfg.Backends))
	for _, backend := range e.cfg.Backends {
		if !backend.Enabled {
			continue
		}
		state := e.backends[backend.ID]
		if state == nil || !state.unhealthyUntil.After(now) {
			items = append(items, backend)
		}
	}
	// If every backend is in cooldown, fail open to the configured set. This
	// avoids a permanent outage when a transient event affects every gateway.
	if len(items) == 0 {
		for _, backend := range e.cfg.Backends {
			if backend.Enabled {
				items = append(items, backend)
			}
		}
	}
	mode := e.cfg.SelectionMode
	e.mu.RUnlock()
	items = removeExhaustedWhenPossible(items)
	if len(items) < 2 || mode == "failover" {
		return items
	}
	// Config order is meaningful for failover but map/order-independent for all
	// other modes, making two dispatchers converge on the same hash ring.
	sort.SliceStable(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	weights := quotaWeights(items, mode)
	var value uint64
	switch mode {
	case "ip_hash":
		value = hashString(clientKey)
	case "round_robin":
		value = atomic.AddUint64(&e.rr, 1) - 1
	default:
		value = hashString(clientKey) ^ (atomic.AddUint64(&e.rr, 1) - 1)
	}
	selected := weightedIndex(weights, value)
	ordered := make([]Backend, 0, len(items))
	ordered = append(ordered, items[selected:]...)
	ordered = append(ordered, items[:selected]...)
	return ordered
}

func removeExhaustedWhenPossible(backends []Backend) []Backend {
	available := false
	for _, backend := range backends {
		if !backend.TrafficKnown || backend.TrafficRemainingGB > 0 {
			available = true
			break
		}
	}
	if !available {
		// Every known account is exhausted. Fail closed for new sessions until
		// the controller reports a billing reset or a new healthy member.
		return nil
	}
	result := make([]Backend, 0, len(backends))
	for _, backend := range backends {
		if backend.TrafficKnown && backend.TrafficRemainingGB <= 0 {
			continue
		}
		result = append(result, backend)
	}
	return result
}

func quotaWeights(backends []Backend, modes ...string) []int {
	mode := "quota_weighted"
	if len(modes) > 0 {
		mode = modes[0]
	}
	weights := make([]int, len(backends))
	maxScore := 0.0
	scores := make([]float64, len(backends))
	for i, backend := range backends {
		score := 1.0
		if backend.TrafficKnown && (mode == "quota_weighted" || mode == "ip_hash") {
			remaining := backend.TrafficRemainingGB
			if remaining <= 0 {
				score = 0
			} else if mode == "quota_weighted" && backend.TrafficRateGBPerMinute > 0 {
				// Minutes of safe headroom is a better signal than raw GB when
				// accounts are consuming at different rates.
				score = remaining / backend.TrafficRateGBPerMinute
			} else {
				score = remaining
			}
		}
		scores[i] = score
		if score > maxScore {
			maxScore = score
		}
	}
	for i, backend := range backends {
		factor := 1.0
		if maxScore > 0 && backend.TrafficKnown {
			factor = scores[i] / maxScore
			if factor < minimumQuotaWeightFactor {
				factor = minimumQuotaWeightFactor
			}
		}
		weight := int(float64(max(backend.Weight, 1)) * factor * 100)
		if weight < 1 {
			weight = 1
		}
		if weight > maxSelectionWeight {
			weight = maxSelectionWeight
		}
		weights[i] = weight
	}
	return weights
}

func weightedIndex(weights []int, value uint64) int {
	if len(weights) == 0 {
		return 0
	}
	total := 0
	for _, weight := range weights {
		if weight > 0 && total <= int(^uint(0)>>1)-weight {
			total += weight
		}
	}
	if total <= 0 {
		return 0
	}
	needle := int(value % uint64(total))
	for index, weight := range weights {
		needle -= weight
		if needle < 0 {
			return index
		}
	}
	return len(weights) - 1
}

func hashString(value string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(value))
	return h.Sum64()
}

func stableTCPClientKey(value string) string {
	host, _, err := net.SplitHostPort(value)
	if err == nil && host != "" {
		return host
	}
	return value
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (e *Engine) markFailure(id string) {
	atomic.AddUint64(&e.backendFailures, 1)
	e.mu.Lock()
	defer e.mu.Unlock()
	state := e.backends[id]
	if state == nil {
		return
	}
	state.failures++
	if state.failures >= e.cfg.FailureThreshold {
		state.unhealthyUntil = time.Now().Add(e.cfg.FailureCooldown)
		state.failures = 0
	}
}

func (e *Engine) markSuccess(id string) {
	e.mu.Lock()
	if state := e.backends[id]; state != nil {
		state.failures = 0
		state.unhealthyUntil = time.Time{}
	}
	e.mu.Unlock()
}

// SetConfigError records a non-fatal polling error while retaining the last
// valid backend set. A successful Apply clears it.
func (e *Engine) SetConfigError(err error) {
	e.setConfigError(err)
}

func (e *Engine) setConfigError(err error) {
	e.lastConfigErrMu.Lock()
	if err == nil {
		e.lastConfigErr = ""
	} else {
		e.lastConfigErr = err.Error()
	}
	e.lastConfigErrMu.Unlock()
}

func (e *Engine) Stats() Stats {
	e.mu.RLock()
	backendCount := 0
	healthyBackendCount := 0
	now := time.Now()
	for _, backend := range e.cfg.Backends {
		if !backend.Enabled {
			continue
		}
		backendCount++
		if backend.TrafficKnown && backend.TrafficRemainingGB <= 0 {
			continue
		}
		if state := e.backends[backend.ID]; state != nil && !state.unhealthyUntil.After(now) {
			healthyBackendCount++
		}
	}
	revision, generated, applied := e.cfg.Revision, e.cfg.GeneratedAt, e.configAppliedAt
	e.mu.RUnlock()
	e.lastConfigErrMu.RLock()
	lastErr := e.lastConfigErr
	e.lastConfigErrMu.RUnlock()
	e.sessionsMu.Lock()
	sessionCount := len(e.sessions)
	e.sessionsMu.Unlock()
	return Stats{
		ActiveConnections:   atomic.LoadInt64(&e.activeConnections),
		TotalConnections:    atomic.LoadUint64(&e.totalConnections),
		BytesUp:             atomic.LoadUint64(&e.bytesUp),
		BytesDown:           atomic.LoadUint64(&e.bytesDown),
		Rejected:            atomic.LoadUint64(&e.rejected),
		BackendFailures:     atomic.LoadUint64(&e.backendFailures),
		BackendCount:        backendCount,
		HealthyBackendCount: healthyBackendCount,
		UDPSessionCount:     sessionCount,
		Revision:            revision,
		GeneratedAt:         generated,
		LastConfigError:     lastErr,
		ConfigAppliedAt:     applied,
	}
}
