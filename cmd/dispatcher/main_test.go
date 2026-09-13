package main

import (
	"net"
	"testing"
)

func TestNormalizeNetwork(t *testing.T) {
	for _, value := range []string{"tcp", "udp", "tcp+udp", " TCP "} {
		if _, err := normalizeNetwork(value); err != nil {
			t.Fatalf("network %q rejected: %v", value, err)
		}
	}
	if _, err := normalizeNetwork("sctp"); err == nil {
		t.Fatal("unsupported network was accepted")
	}
}

func TestBindListenersSharesEphemeralPort(t *testing.T) {
	tcp, udp, err := bindListeners("tcp+udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tcp.Close()
	defer udp.Close()
	tcpPort := tcp.Addr().(*net.TCPAddr).Port
	udpPort := udp.LocalAddr().(*net.UDPAddr).Port
	if tcpPort != udpPort {
		t.Fatalf("TCP and UDP listeners use different ports: %d/%d", tcpPort, udpPort)
	}
}
