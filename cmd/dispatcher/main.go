// cdt-dispatcher is a fixed-front-door TCP/UDP L4 gateway. It polls the Go
// controller for a credential-free Relay pool snapshot and forwards encrypted
// bytes to one healthy CDT Relay per connection/session.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/dispatcher"
)

var version = "dev"

func main() {
	listen := flag.String("listen", env("CDT_DISPATCH_LISTEN", ":8443"), "public TCP/UDP listen address")
	network := flag.String("network", env("CDT_DISPATCH_NETWORK", "tcp+udp"), "listener transports: tcp, udp or tcp+udp")
	healthListen := flag.String("health-listen", env("CDT_DISPATCH_HEALTH_LISTEN", "127.0.0.1:9091"), "local health HTTP listen address; empty disables")
	controllerURL := flag.String("controller", env("CDT_DISPATCH_CONTROLLER_URL", ""), "controller base URL")
	poolID := flag.String("pool-id", env("CDT_DISPATCH_POOL_ID", ""), "relay pool ID")
	token := flag.String("dispatch-token", env("CDT_DISPATCH_TOKEN", ""), "dedicated read-only controller token")
	pollInterval := flag.Duration("poll-interval", envDuration("CDT_DISPATCH_POLL_INTERVAL", 15*time.Second), "controller snapshot poll interval")
	staleAfter := flag.Duration("stale-after", envDuration("CDT_DISPATCH_STALE_AFTER", 2*time.Minute), "time without a valid snapshot before draining")
	requestTimeout := flag.Duration("request-timeout", envDuration("CDT_DISPATCH_REQUEST_TIMEOUT", 10*time.Second), "controller HTTP request timeout")
	maxSnapshotAge := flag.Duration("max-snapshot-age", envDuration("CDT_DISPATCH_MAX_SNAPSHOT_AGE", 2*time.Minute), "maximum accepted snapshot age")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}

	transport, err := normalizeNetwork(*network)
	if err != nil {
		fatal(err)
	}
	client, err := dispatcher.NewClient(dispatcher.ClientOptions{ControllerURL: *controllerURL, PoolID: *poolID, Token: *token, RequestTimeout: *requestTimeout, MaxSnapshotAge: *maxSnapshotAge})
	if err != nil {
		fatal(err)
	}
	engine := dispatcher.NewEngine()
	defer engine.Close()
	poller, err := dispatcher.NewPoller(client, engine, dispatcher.PollerOptions{Interval: *pollInterval, StaleAfter: *staleAfter, ListenerNetwork: transport})
	if err != nil {
		fatal(err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	// A first failure is non-fatal: the engine starts with no backends and the
	// poller retries. This keeps a gateway restart safe during a controller
	// deploy while avoiding use of an unverified/stale route.
	if err := poller.Sync(ctx); err != nil {
		logf("initial controller sync failed; gateway is draining until recovery: %v", err)
	}

	tcpListener, udpListener, err := bindListeners(transport, *listen)
	if err != nil {
		fatal(err)
	}
	var listenersReady int32 = 1
	defer func() {
		atomic.StoreInt32(&listenersReady, 0)
		if tcpListener != nil {
			_ = tcpListener.Close()
		}
		if udpListener != nil {
			_ = udpListener.Close()
		}
	}()

	healthServer := startHealthServer(*healthListen, engine, poller, func() bool { return atomic.LoadInt32(&listenersReady) == 1 })
	if healthServer != nil {
		defer func() {
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer shutdownCancel()
			_ = healthServer.Shutdown(shutdownCtx)
		}()
	}

	errCh := make(chan error, 3)
	var workers sync.WaitGroup
	if tcpListener != nil {
		workers.Add(1)
		go func() {
			defer workers.Done()
			errCh <- engine.ServeTCP(ctx, tcpListener)
		}()
	}
	if udpListener != nil {
		workers.Add(1)
		go func() {
			defer workers.Done()
			errCh <- engine.ServeUDP(ctx, udpListener)
		}()
	}
	workers.Add(1)
	go func() {
		defer workers.Done()
		errCh <- poller.Run(ctx)
	}()

	select {
	case <-ctx.Done():
	case err := <-errCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			logf("dispatcher worker stopped: %v", err)
			cancel()
		}
	}
	cancel()
	engine.Close()
	workers.Wait()
}

func bindListeners(network, listen string) (net.Listener, *net.UDPConn, error) {
	if strings.TrimSpace(listen) == "" {
		return nil, nil, errors.New("listen address is required")
	}
	var tcpListener net.Listener
	var udpListener *net.UDPConn
	var err error
	if network == "tcp" || network == "tcp+udp" {
		tcpListener, err = net.Listen("tcp", listen)
		if err != nil {
			return nil, nil, fmt.Errorf("listen TCP %s: %w", listen, err)
		}
	}
	if network == "udp" || network == "tcp+udp" {
		udpListen := listen
		// When tests or an orchestrator request :0, reuse the TCP-assigned
		// ephemeral port so TCP and UDP remain one logical entry point.
		if network == "tcp+udp" && tcpListener != nil {
			if host, port, splitErr := net.SplitHostPort(listen); splitErr == nil && port == "0" {
				if tcpAddr, ok := tcpListener.Addr().(*net.TCPAddr); ok {
					udpListen = net.JoinHostPort(host, strconv.Itoa(tcpAddr.Port))
				}
			}
		}
		addr, resolveErr := net.ResolveUDPAddr("udp", udpListen)
		if resolveErr != nil {
			if tcpListener != nil {
				_ = tcpListener.Close()
			}
			return nil, nil, fmt.Errorf("resolve UDP %s: %w", listen, resolveErr)
		}
		udpListener, err = net.ListenUDP("udp", addr)
		if err != nil {
			if tcpListener != nil {
				_ = tcpListener.Close()
			}
			return nil, nil, fmt.Errorf("listen UDP %s: %w", listen, err)
		}
	}
	return tcpListener, udpListener, nil
}

func normalizeNetwork(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		value = "tcp+udp"
	}
	if value != "tcp" && value != "udp" && value != "tcp+udp" {
		return "", fmt.Errorf("unsupported network %q", value)
	}
	return value, nil
}

func startHealthServer(address string, engine *dispatcher.Engine, poller *dispatcher.Poller, listenersReady func() bool) *http.Server {
	if strings.TrimSpace(address) == "" {
		return nil
	}
	server := &http.Server{Addr: address, Handler: dispatcher.HealthHandler{Engine: engine, Poller: poller, ListenersReady: listenersReady}.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second}
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logf("health server stopped: %v", err)
		}
	}()
	return server
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func logf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "cdt-dispatcher: "+format+"\n", args...)
}

func fatal(err error) {
	logf("fatal: %v", err)
	os.Exit(1)
}
