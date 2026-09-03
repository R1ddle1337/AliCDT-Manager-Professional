package relay

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/protocol"
)

func TestServiceBillingModesAndExactQuota(t *testing.T) {
	bytesToGB := func(value uint64) float64 { return float64(value) / (1024 * 1024 * 1024) }
	tests := []struct {
		name            string
		mode            string
		wantAfterUp     uint64
		wantAfterDown   uint64
		wantDownWritten int
	}{
		{name: "upload", mode: protocol.BillingModeUpload, wantAfterUp: 3, wantAfterDown: 3, wantDownWritten: 4},
		{name: "download", mode: protocol.BillingModeDownload, wantAfterUp: 0, wantAfterDown: 4, wantDownWritten: 4},
		{name: "both", mode: protocol.BillingModeBoth, wantAfterUp: 3, wantAfterDown: 5, wantDownWritten: 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := protocol.ServiceConfig{ID: "meter", BillingMode: test.mode, TrafficLimitGB: bytesToGB(5), BillingEpoch: 7}
			runner := newServiceRunner(context.Background(), cfg, newTrafficMeter(cfg, protocol.ServiceStatus{}))
			var destination bytes.Buffer
			if written, err := runner.writeCounted(&destination, []byte("abc"), trafficIngress, true); written != 3 || err != nil {
				t.Fatalf("upload write = %d, %v", written, err)
			}
			if got := atomic.LoadUint64(&runner.meter.billedBytes); got != test.wantAfterUp {
				t.Fatalf("billed after upload = %d, want %d", got, test.wantAfterUp)
			}
			written, err := runner.writeCounted(&destination, []byte("WXYZ"), trafficEgress, true)
			if written != test.wantDownWritten {
				t.Fatalf("download write = %d, want %d", written, test.wantDownWritten)
			}
			if test.mode == protocol.BillingModeBoth && !errors.Is(err, errTrafficQuotaExceeded) {
				t.Fatalf("both-direction quota error = %v", err)
			}
			if got := atomic.LoadUint64(&runner.meter.billedBytes); got != test.wantAfterDown {
				t.Fatalf("billed after download = %d, want %d", got, test.wantAfterDown)
			}
		})
	}
}

func TestBillingEpochResetsAndRestoresUsage(t *testing.T) {
	engine := NewEngine()
	defer engine.Close()
	epoch := currentBillingEpoch(time.Now())
	restored := protocol.ServiceStatus{ID: "meter", BytesUp: 11, BytesDown: 13, BilledBytes: 24, BillingMode: protocol.BillingModeBoth, BillingEpoch: epoch}
	engine.RestoreUsage([]protocol.ServiceStatus{restored})
	config := protocol.AgentConfig{Revision: 1, Services: []protocol.ServiceConfig{{
		ID: "meter", Name: "meter", Listen: "127.0.0.1:0", Network: "tcp", Mode: "failover", Enabled: true,
		BillingMode: protocol.BillingModeBoth, BillingEpoch: epoch,
		Targets: []protocol.TargetConfig{{ID: "target", Address: "127.0.0.1:1", Enabled: true}},
	}}}
	if err := engine.Apply(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	status := engine.Snapshot()[0]
	if status.BilledBytes != 24 || status.BytesUp != 11 || status.BytesDown != 13 {
		t.Fatalf("usage was not restored: %+v", status)
	}
	config.Revision++
	config.Services[0].BillingEpoch++
	if err := engine.Apply(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	status = engine.Snapshot()[0]
	if status.BilledBytes != 0 || status.BytesUp != 0 || status.BytesDown != 0 {
		t.Fatalf("new billing epoch did not reset counters: %+v", status)
	}
}

func TestConcurrentBidirectionalWritesCannotExceedQuota(t *testing.T) {
	const limitBytes = uint64(100)
	cfg := protocol.ServiceConfig{
		ID: "concurrent", BillingMode: protocol.BillingModeBoth,
		TrafficLimitGB: float64(limitBytes) / (1024 * 1024 * 1024), BillingEpoch: currentBillingEpoch(time.Now()),
	}
	runner := newServiceRunner(context.Background(), cfg, newTrafficMeter(cfg, protocol.ServiceStatus{}))
	var wait sync.WaitGroup
	for index := 0; index < 100; index++ {
		wait.Add(1)
		go func(direction trafficDirection) {
			defer wait.Done()
			_, _ = runner.writeCounted(io.Discard, []byte{1, 2}, direction, true)
		}(trafficDirection(index % 2))
	}
	wait.Wait()
	status := runner.snapshot()
	if status.BilledBytes != limitBytes || !status.QuotaExceeded {
		t.Fatalf("concurrent quota was not exact: %+v", status)
	}
	if status.BytesUp+status.BytesDown != limitBytes {
		t.Fatalf("forwarded bytes = %d, want %d", status.BytesUp+status.BytesDown, limitBytes)
	}
}

func TestServicesWithSameMeterKeyShareExactQuota(t *testing.T) {
	const limitBytes = uint64(10)
	epoch := currentBillingEpoch(time.Now())
	base := protocol.ServiceConfig{
		MeterKey: "user:42", BillingMode: protocol.BillingModeBoth,
		TrafficLimitGB: float64(limitBytes) / (1024 * 1024 * 1024), BillingEpoch: epoch,
	}
	meter := newTrafficMeter(base, protocol.ServiceStatus{})
	first := base
	first.ID = "port-20000"
	second := base
	second.ID = "port-20001"
	firstRunner := newServiceRunner(context.Background(), first, meter)
	secondRunner := newServiceRunner(context.Background(), second, meter)
	if written, err := firstRunner.writeCounted(io.Discard, []byte("123456"), trafficIngress, true); written != 6 || err != nil {
		t.Fatalf("first port write = %d, %v", written, err)
	}
	written, err := secondRunner.writeCounted(io.Discard, []byte("abcdef"), trafficEgress, true)
	if written != 4 || !errors.Is(err, errTrafficQuotaExceeded) {
		t.Fatalf("second port boundary write = %d, %v", written, err)
	}
	firstStatus, secondStatus := firstRunner.snapshot(), secondRunner.snapshot()
	if firstStatus.BilledBytes != limitBytes || secondStatus.BilledBytes != limitBytes || !firstStatus.QuotaExceeded || !secondStatus.QuotaExceeded {
		t.Fatalf("shared meter was not enforced: first=%+v second=%+v", firstStatus, secondStatus)
	}
}

func TestQuotaLeaseOverridesUserLimitAndExpiresLocally(t *testing.T) {
	future := time.Now().Add(time.Minute)
	cfg := protocol.ServiceConfig{
		ID: "leased", MeterKey: "user:9", BillingMode: protocol.BillingModeBoth, TrafficLimitGB: 100,
		BillingEpoch: currentBillingEpoch(time.Now()), QuotaLeaseID: "lease-1", QuotaLeaseBytes: 5, QuotaLeaseExpiresAt: &future,
	}
	runner := newServiceRunner(context.Background(), cfg, newTrafficMeter(cfg, protocol.ServiceStatus{}))
	if written, err := runner.writeCounted(io.Discard, []byte("1234567"), trafficIngress, true); written != 5 || !errors.Is(err, errTrafficQuotaExceeded) {
		t.Fatalf("lease boundary write = %d, %v", written, err)
	}
	past := time.Now().Add(-time.Second)
	cfg.QuotaLeaseID = "lease-2"
	cfg.QuotaLeaseBytes = 100
	cfg.QuotaLeaseExpiresAt = &past
	expired := newServiceRunner(context.Background(), cfg, newTrafficMeter(cfg, protocol.ServiceStatus{}))
	if written, err := expired.writeCounted(io.Discard, []byte("x"), trafficIngress, true); written != 0 || !errors.Is(err, errTrafficQuotaExceeded) {
		t.Fatalf("expired lease write = %d, %v", written, err)
	}
	cfg.QuotaLeaseID = "lease-empty"
	cfg.QuotaLeaseBytes = 0
	cfg.QuotaLeaseExpiresAt = &future
	empty := newServiceRunner(context.Background(), cfg, newTrafficMeter(cfg, protocol.ServiceStatus{}))
	if written, err := empty.writeCounted(io.Discard, []byte("x"), trafficIngress, true); written != 0 || !errors.Is(err, errTrafficQuotaExceeded) {
		t.Fatalf("empty lease write = %d, %v", written, err)
	}
}

func TestUDPDatagramIsNeverPartiallyForwardedAtQuotaBoundary(t *testing.T) {
	const limitBytes = uint64(5)
	cfg := protocol.ServiceConfig{
		ID: "udp-quota", BillingMode: protocol.BillingModeUpload,
		TrafficLimitGB: float64(limitBytes) / (1024 * 1024 * 1024), BillingEpoch: currentBillingEpoch(time.Now()),
	}
	runner := newServiceRunner(context.Background(), cfg, newTrafficMeter(cfg, protocol.ServiceStatus{}))
	var destination bytes.Buffer
	if written, err := runner.writeCounted(&destination, []byte("123"), trafficIngress, false); written != 3 || err != nil {
		t.Fatalf("first datagram = %d, %v", written, err)
	}
	written, err := runner.writeCounted(&destination, []byte("456"), trafficIngress, false)
	if written != 0 || !errors.Is(err, errTrafficQuotaExceeded) {
		t.Fatalf("boundary datagram = %d, %v", written, err)
	}
	if destination.String() != "123" || runner.snapshot().BilledBytes != 3 {
		t.Fatalf("partial datagram was forwarded: payload=%q status=%+v", destination.String(), runner.snapshot())
	}
}

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
