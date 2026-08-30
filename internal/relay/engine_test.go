package relay

import (
	"context"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/protocol"
)

func TestTCPFailoverWithoutAgentRestart(t *testing.T) {
	primary, stopPrimary := startTCPEcho(t, "primary:")
	defer stopPrimary()
	backup, stopBackup := startTCPEcho(t, "backup:")
	defer stopBackup()

	engine := NewEngine()
	defer engine.Close()
	cfg := protocol.AgentConfig{Revision: 1, Services: []protocol.ServiceConfig{{
		ID:      "svc-1",
		Name:    "test",
		Listen:  "127.0.0.1:0",
		Network: "tcp",
		Mode:    "failover",
		Enabled: true,
		Health: protocol.HealthConfig{
			Enabled:          true,
			IntervalSeconds:  1,
			TimeoutMillis:    100,
			FailureThreshold: 1,
			SuccessThreshold: 1,
		},
		Targets: []protocol.TargetConfig{
			{ID: "primary", Address: primary, Priority: 0, Weight: 1, Enabled: true},
			{ID: "backup", Address: backup, Priority: 10, Weight: 1, Enabled: true},
		},
	}}}
	if err := engine.Apply(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	listen := engine.services["svc-1"].tcpListener.Addr().String()
	if got := roundTripTCP(t, listen, "hello"); got != "primary:hello" {
		t.Fatalf("expected primary, got %q", got)
	}

	stopPrimary()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		statuses := engine.Snapshot()
		if len(statuses) == 1 && targetIsHealthy(statuses[0], "primary") == false {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if targetIsHealthy(engine.Snapshot()[0], "primary") {
		t.Fatal("primary was not marked unhealthy")
	}
	if got := roundTripTCP(t, listen, "hello"); got != "backup:hello" {
		t.Fatalf("expected backup, got %q", got)
	}
}

func TestUDPForwarding(t *testing.T) {
	backend, stopBackend := startUDPEcho(t, "udp:")
	defer stopBackend()
	engine := NewEngine()
	defer engine.Close()
	if err := engine.Apply(context.Background(), protocol.AgentConfig{Revision: 1, Services: []protocol.ServiceConfig{{
		ID:      "udp-1",
		Name:    "udp-test",
		Listen:  "127.0.0.1:0",
		Network: "udp",
		Mode:    "failover",
		Enabled: true,
		Targets: []protocol.TargetConfig{{ID: "target", Address: backend, Weight: 1, Enabled: true}},
	}}}); err != nil {
		t.Fatal(err)
	}
	listen := engine.services["udp-1"].udpListener.LocalAddr().String()
	addr, err := net.ResolveUDPAddr("udp", listen)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(buf[:n]); got != "udp:hello" {
		t.Fatalf("unexpected response %q", got)
	}
}

func TestDisablingTCPServiceRejectsNewConnectionsAndDrainsExisting(t *testing.T) {
	backend, stopBackend := startStreamingTCPEcho(t, "echo:")
	defer stopBackend()
	engine := NewEngine()
	defer engine.Close()
	config := protocol.AgentConfig{Revision: 1, Services: []protocol.ServiceConfig{{
		ID: "drain-1", Name: "drain-test", Listen: "127.0.0.1:0", Network: "tcp", Mode: "failover", Enabled: true,
		Targets: []protocol.TargetConfig{{ID: "target", Address: backend, Weight: 1, Enabled: true}},
	}}}
	if err := engine.Apply(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	listen := engine.services["drain-1"].tcpListener.Addr().String()
	existing, err := net.DialTimeout("tcp", listen, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer existing.Close()
	if got := exchangeTCP(t, existing, "before"); got != "echo:before" {
		t.Fatalf("unexpected response before drain %q", got)
	}

	config.Revision = 2
	config.Services[0].Enabled = false
	if err := engine.Apply(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	newConnection, err := net.DialTimeout("tcp", listen, 300*time.Millisecond)
	if err == nil {
		newConnection.Close()
		t.Fatal("disabled service still accepted a new connection")
	}
	if got := exchangeTCP(t, existing, "after"); got != "echo:after" {
		t.Fatalf("existing connection was interrupted by drain: %q", got)
	}
}

func targetIsHealthy(status protocol.ServiceStatus, id string) bool {
	for _, target := range status.Targets {
		if target.ID == id {
			return target.Healthy
		}
	}
	return false
}

func startTCPEcho(t *testing.T, prefix string) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	stopped := false
	stop := func() {
		if !stopped {
			stopped = true
			_ = listener.Close()
		}
	}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				buf := make([]byte, 1024)
				n, err := conn.Read(buf)
				if err == nil {
					_, _ = conn.Write([]byte(prefix + string(buf[:n])))
				}
			}()
		}
	}()
	return listener.Addr().String(), stop
}

func startStreamingTCPEcho(t *testing.T, prefix string) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer connection.Close()
				buffer := make([]byte, 1024)
				for {
					count, err := connection.Read(buffer)
					if err != nil {
						return
					}
					if _, err := connection.Write([]byte(prefix + string(buffer[:count]))); err != nil {
						return
					}
				}
			}()
		}
	}()
	return listener.Addr().String(), func() { _ = listener.Close() }
}

func startUDPEcho(t *testing.T, prefix string) (string, func()) {
	t.Helper()
	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		buf := make([]byte, 64*1024)
		for {
			n, client, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			_, _ = conn.WriteToUDP([]byte(prefix+string(buf[:n])), client)
		}
	}()
	return conn.LocalAddr().String(), func() { _ = conn.Close() }
}

func roundTripTCP(t *testing.T, address, payload string) string {
	t.Helper()
	conn, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := fmt.Fprint(conn, payload); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	return string(buf[:n])
}

func exchangeTCP(t *testing.T, connection net.Conn, payload string) string {
	t.Helper()
	_ = connection.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := fmt.Fprint(connection, payload); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, len("echo:")+len(payload))
	if _, err := io.ReadFull(connection, buffer); err != nil {
		t.Fatal(err)
	}
	return string(buffer)
}
