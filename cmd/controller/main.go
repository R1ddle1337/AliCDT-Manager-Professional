package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/controller"
)

var (
	version      = "dev"
	agentVersion = "dev"
)

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	listen := flag.String("listen", env("CDT_CONTROLLER_LISTEN", ":8080"), "HTTP listen address")
	database := flag.String("database", env("CDT_DATABASE", "/app/data/guard.db"), "SQLite database path")
	adminToken := flag.String("admin-token", env("CDT_ADMIN_TOKEN", ""), "admin API bearer token")
	bootstrapToken := flag.String("enroll-token", env("CDT_BOOTSTRAP_ENROLL_TOKEN", ""), "optional initial one-time agent token")
	frontendDir := flag.String("frontend-dir", env("CDT_FRONTEND_DIR", ""), "optional built frontend directory")
	agentInstaller := flag.String("agent-installer", env("CDT_AGENT_INSTALLER", ""), "optional agent installer script")
	agentReleaseVersion := flag.String("agent-version", env("CDT_AGENT_VERSION", agentVersion), "Agent release label exposed to enrolled agents")
	agentReleaseSource := flag.String("agent-release-source", env("CDT_AGENT_RELEASE_SOURCE", "github"), "Agent update source: github or embedded")
	agentReleaseRepo := flag.String("agent-release-repo", env("CDT_AGENT_RELEASE_REPO", "R1ddle1337/AliCDT-Manager-Professional"), "GitHub repository containing Agent releases")
	agentReleaseChannel := flag.String("agent-release-channel", env("CDT_AGENT_RELEASE_CHANNEL", "latest"), "GitHub release tag or latest")
	agentReleaseCacheDir := flag.String("agent-release-cache-dir", env("CDT_AGENT_RELEASE_CACHE_DIR", "/app/data/agent-releases"), "Agent release cache directory")
	updateRequestFile := flag.String("update-request-file", env("CDT_UPDATE_REQUEST_FILE", "/app/data/update.request"), "host update request marker")
	updateStatusFile := flag.String("update-status-file", env("CDT_UPDATE_STATUS_FILE", "/app/data/update.status.json"), "host update status file")
	agentUpgradeRequestFile := flag.String("agent-upgrade-request-file", env("CDT_AGENT_UPGRADE_REQUEST_FILE", "/app/data/agent-upgrade.request"), "host Agent upgrade request marker")
	cloudSyncInterval := flag.Duration("cloud-sync-interval", 2*time.Minute, "Aliyun ECS/CDT synchronization interval")
	dnsSyncInterval := flag.Duration("dns-sync-interval", time.Minute, "managed DNS synchronization interval")
	trafficSafetyWindow := flag.Duration("traffic-safety-window", durationEnv("CDT_TRAFFIC_SAFETY_WINDOW", 4*time.Minute), "forecast window reserved before the CDT protection threshold")
	dispatchToken := flag.String("dispatch-token", env("CDT_DISPATCH_TOKEN", ""), "read-only token for front-door L4 dispatchers")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}

	store, err := controller.OpenStore(*database)
	if err != nil {
		fatal(err)
	}
	defer store.Close()
	if *bootstrapToken != "" {
		if err := store.CreateEnrollmentToken(context.Background(), *bootstrapToken, 24*time.Hour); err != nil {
			fatal(err)
		}
	}
	server, err := controller.NewServer(store, controller.ServerOptions{AdminToken: *adminToken, FrontendDir: *frontendDir, AgentInstallerPath: *agentInstaller, AgentVersion: *agentReleaseVersion, AgentReleaseSource: *agentReleaseSource, AgentReleaseRepo: *agentReleaseRepo, AgentReleaseChannel: *agentReleaseChannel, AgentReleaseCacheDir: *agentReleaseCacheDir, UpdateRequestFile: *updateRequestFile, UpdateStatusFile: *updateStatusFile, AgentUpgradeRequestFile: *agentUpgradeRequestFile, TrafficSafetyWindow: *trafficSafetyWindow, TrafficSafetyWindowSet: true, DispatchToken: *dispatchToken})
	if err != nil {
		fatal(err)
	}
	httpServer := &http.Server{
		Addr:              *listen,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		// Agent/Dispatcher binaries share this HTTP server. Allow weak links
		// enough time to finish a verified download without leaving writes
		// completely unbounded.
		WriteTimeout:   5 * time.Minute,
		IdleTimeout:    60 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go server.RunCloudScheduler(ctx, *cloudSyncInterval)
	go server.RunDNSScheduler(ctx, *dnsSyncInterval)
	go server.RunMaintenanceScheduler(ctx, 6*time.Hour)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()
	fmt.Printf("AliCDT controller listening on %s\n", *listen)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fatal(err)
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func durationEnv(key string, fallback time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed < 0 {
		return fallback
	}
	return parsed
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "alicdt-controller:", err)
	os.Exit(1)
}
