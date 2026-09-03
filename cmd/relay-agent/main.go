package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/agent"
	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/relay"
)

var version = "dev"

func main() {
	controller := flag.String("controller", env("CDT_CONTROLLER_URL", ""), "controller base URL")
	token := flag.String("enroll-token", env("CDT_ENROLL_TOKEN", ""), "one-time enrollment token")
	nodeName := flag.String("node-name", env("CDT_NODE_NAME", ""), "relay node name")
	publicIP := flag.String("public-ip", env("CDT_PUBLIC_IP", ""), "public entry IP")
	dataDir := flag.String("data-dir", env("CDT_AGENT_DATA_DIR", "/var/lib/cdt-relay"), "agent data directory")
	poll := flag.Duration("poll", 5*time.Second, "desired config polling interval")
	heartbeat := flag.Duration("heartbeat", 10*time.Second, "heartbeat interval")
	autoUpdate := flag.Bool("auto-update", envBool("CDT_AGENT_AUTO_UPDATE", true), "automatically update the Agent binary")
	autoFirewall := flag.Bool("auto-firewall", envBool("CDT_AGENT_AUTO_FIREWALL", true), "manage enabled relay ports in the active host firewall")
	updateInterval := flag.Duration("update-interval", envDuration("CDT_AGENT_UPDATE_INTERVAL", 0), "Optional legacy Agent update check interval")
	updateTime := flag.String("update-time", env("CDT_AGENT_UPDATE_TIME", "04:00"), "Daily Agent update time")
	updateLocation := flag.String("update-location", env("CDT_AGENT_UPDATE_LOCATION", "Asia/Shanghai"), "Timezone used for the daily Agent update")
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	engine := relay.NewEngine()
	defer engine.Close()
	client, err := agent.New(agent.Options{
		ControllerURL:       *controller,
		EnrollmentToken:     *token,
		NodeName:            *nodeName,
		PublicIP:            *publicIP,
		DataDir:             *dataDir,
		AgentVersion:        version,
		PollInterval:        *poll,
		HeartbeatEvery:      *heartbeat,
		AutoUpdate:          *autoUpdate,
		AutoUpdateSet:       true,
		AutoFirewall:        *autoFirewall,
		AutoFirewallSet:     true,
		UpdateCheckInterval: *updateInterval,
		UpdateTime:          *updateTime,
		UpdateLocation:      *updateLocation,
	}, engine)
	if err != nil {
		fatal(err)
	}
	if err := client.Run(ctx); err != nil {
		fatal(err)
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	value := env(key, "")
	if value == "" {
		return fallback
	}
	return value == "1" || value == "true" || value == "yes" || value == "on"
}
func envDuration(key string, fallback time.Duration) time.Duration {
	value := env(key, "")
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "cdt-relay-agent:", err)
	os.Exit(1)
}
