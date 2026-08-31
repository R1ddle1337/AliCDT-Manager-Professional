package dispatcher

import (
	"context"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestEngineTCPFailoverAndCounters(t *testing.T) {
	backend, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	var accepted sync.WaitGroup
	accepted.Add(1)
	go func() {
		defer accepted.Done()
		conn, acceptErr := backend.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		_, _ = io.Copy(conn, conn)
	}()
	// Reserve and release an address so the first backend is guaranteed to
	// fail without relying on a special/invalid port number.
	dead, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadAddress := dead.Addr().String()
	_ = dead.Close()

	engine := NewEngine()
	defer engine.Close()
	if err := engine.Apply(Config{Network: "tcp", SelectionMode: "failover", DialTimeout: 100 * time.Millisecond, FailureThreshold: 1, FailureCooldown: time.Second, Backends: []Backend{{ID: "dead", Address: deadAddress, Enabled: true}, {ID: "live", Address: backend.Addr().String(), Enabled: true}}}); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveDone := make(chan error, 1)
	go func() { serveDone <- engine.ServeTCP(ctx, listener) }()

	client, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	message := "transparent relay payload"
	if _, err := client.Write([]byte(message)); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, len(message))
	if _, err := io.ReadFull(client, response); err != nil {
		t.Fatal(err)
	}
	if string(response) != message {
		t.Fatalf("unexpected echo %q", response)
	}
	_ = client.Close()
	accepted.Wait()
	stats := engine.Stats()
	if stats.TotalConnections != 1 || stats.BytesUp != uint64(len(message)) || stats.BytesDown != uint64(len(message)) {
		t.Fatalf("unexpected counters: %+v", stats)
	}
	if stats.BackendFailures == 0 {
		t.Fatalf("expected failed first backend: %+v", stats)
	}
	cancel()
	select {
	case <-serveDone:
	case <-time.After(time.Second):
		t.Fatal("TCP server did not stop")
	}
}

func TestEngineUDPSessionPinningAndApplyDrain(t *testing.T) {
	backend, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	stopBackend := make(chan struct{})
	defer close(stopBackend)
	go func() {
		buffer := make([]byte, 2048)
		for {
			_ = backend.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			n, source, readErr := backend.ReadFromUDP(buffer)
			if readErr != nil {
				if ne, ok := readErr.(net.Error); ok && ne.Timeout() {
					select {
					case <-stopBackend:
						return
					default:
						continue
					}
				}
				return
			}
			_, _ = backend.WriteToUDP(buffer[:n], source)
		}
	}()

	engine := NewEngine()
	defer engine.Close()
	if err := engine.Apply(Config{Network: "udp", UDPIdleTimeout: time.Second, Backends: []Backend{{ID: "udp-live", Address: backend.LocalAddr().String(), Enabled: true}}}); err != nil {
		t.Fatal(err)
	}
	listener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveDone := make(chan error, 1)
	go func() { serveDone <- engine.ServeUDP(ctx, listener) }()

	client, err := net.DialUDP("udp", nil, listener.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Write([]byte("udp-one")); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 64)
	_ = client.SetReadDeadline(time.Now().Add(time.Second))
	n, _, err := client.ReadFromUDP(buffer)
	if err != nil {
		t.Fatal(err)
	}
	if string(buffer[:n]) != "udp-one" {
		t.Fatalf("unexpected UDP response %q", buffer[:n])
	}
	if stats := engine.Stats(); stats.UDPSessionCount != 1 || stats.TotalConnections != 1 {
		t.Fatalf("unexpected UDP stats: %+v", stats)
	}
	if err := engine.Apply(Config{Network: "udp", Backends: nil}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for engine.Stats().UDPSessionCount != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if engine.Stats().UDPSessionCount != 0 {
		t.Fatalf("session survived backend removal: %+v", engine.Stats())
	}
	cancel()
	select {
	case <-serveDone:
	case <-time.After(time.Second):
		t.Fatal("UDP server did not stop")
	}
}

func TestEngineQuotaSelectionSkipsExhaustedBackend(t *testing.T) {
	engine := NewEngine()
	defer engine.Close()
	if err := engine.Apply(Config{Network: "tcp", SelectionMode: "quota_weighted", Backends: []Backend{{ID: "empty", Address: "127.0.0.1:1", Enabled: true, TrafficKnown: true, TrafficRemainingGB: 0}, {ID: "room", Address: "127.0.0.1:2", Enabled: true, TrafficKnown: true, TrafficRemainingGB: 10}}}); err != nil {
		t.Fatal(err)
	}
	ordered := engine.orderedBackends("client")
	if len(ordered) != 1 || ordered[0].ID != "room" {
		t.Fatalf("exhausted backend was selected: %+v", ordered)
	}
	if err := engine.Apply(Config{Network: "tcp", Backends: []Backend{{ID: "empty-a", Address: "127.0.0.1:1", Enabled: true, TrafficKnown: true}, {ID: "empty-b", Address: "127.0.0.1:2", Enabled: true, TrafficKnown: true}}}); err != nil {
		t.Fatal(err)
	}
	if ordered := engine.orderedBackends("client"); len(ordered) != 0 {
		t.Fatalf("all exhausted backends should fail closed: %+v", ordered)
	}
}

func TestEngineApplyRejectsInvalidConfigWithoutReplacingLiveState(t *testing.T) {
	engine := NewEngine()
	defer engine.Close()
	valid := Config{Network: "tcp", Revision: "good", Backends: []Backend{{ID: "one", Address: "127.0.0.1:443", Enabled: true}}}
	if err := engine.Apply(valid); err != nil {
		t.Fatal(err)
	}
	if err := engine.Apply(Config{Network: "sctp", Backends: []Backend{{ID: "bad", Address: "127.0.0.1:443", Enabled: true}}}); err == nil {
		t.Fatal("expected invalid network error")
	}
	if got := engine.Config(); got.Revision != "good" || len(got.Backends) != 1 {
		t.Fatalf("invalid apply replaced live state: %+v", got)
	}
	if !strings.Contains(engine.Stats().LastConfigError, "unsupported network") {
		t.Fatalf("invalid apply error was not recorded: %+v", engine.Stats())
	}
}

func TestEngineConcurrentApplyAndSelection(t *testing.T) {
	engine := NewEngine()
	defer engine.Close()
	base := Config{Network: "tcp+udp", SelectionMode: "quota_weighted", Backends: []Backend{{ID: "a", Address: "127.0.0.1:10001", Enabled: true, TrafficKnown: true, TrafficRemainingGB: 10}, {ID: "b", Address: "127.0.0.1:10002", Enabled: true, TrafficKnown: true, TrafficRemainingGB: 20}}}
	if err := engine.Apply(base); err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for i := 0; i < 4; i++ {
		group.Add(1)
		go func(worker int) {
			defer group.Done()
			for n := 0; n < 100; n++ {
				if worker%2 == 0 {
					_ = engine.Apply(base)
				} else {
					_ = engine.Stats()
					_ = engine.orderedBackends("client")
				}
			}
		}(i)
	}
	group.Wait()
}
