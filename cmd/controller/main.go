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

func main() {
	listen := flag.String("listen", env("CDT_CONTROLLER_LISTEN", ":8080"), "HTTP listen address")
	database := flag.String("database", env("CDT_DATABASE", "/app/data/guard.db"), "SQLite database path")
	adminToken := flag.String("admin-token", env("CDT_ADMIN_TOKEN", ""), "admin API bearer token")
	bootstrapToken := flag.String("enroll-token", env("CDT_BOOTSTRAP_ENROLL_TOKEN", ""), "optional initial one-time agent token")
	frontendDir := flag.String("frontend-dir", env("CDT_FRONTEND_DIR", ""), "optional built frontend directory")
	agentInstaller := flag.String("agent-installer", env("CDT_AGENT_INSTALLER", ""), "optional agent installer script")
	updateRequestFile := flag.String("update-request-file", env("CDT_UPDATE_REQUEST_FILE", "/app/data/update.request"), "host update request marker")
	updateStatusFile := flag.String("update-status-file", env("CDT_UPDATE_STATUS_FILE", "/app/data/update.status.json"), "host update status file")
	cloudSyncInterval := flag.Duration("cloud-sync-interval", 2*time.Minute, "Aliyun ECS/CDT synchronization interval")
	dnsSyncInterval := flag.Duration("dns-sync-interval", time.Minute, "managed DNS synchronization interval")
	flag.Parse()

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
	server, err := controller.NewServer(store, controller.ServerOptions{AdminToken: *adminToken, FrontendDir: *frontendDir, AgentInstallerPath: *agentInstaller, UpdateRequestFile: *updateRequestFile, UpdateStatusFile: *updateStatusFile})
	if err != nil {
		fatal(err)
	}
	httpServer := &http.Server{
		Addr:              *listen,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go server.RunCloudScheduler(ctx, *cloudSyncInterval)
	go server.RunDNSScheduler(ctx, *dnsSyncInterval)
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

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "alicdt-controller:", err)
	os.Exit(1)
}
