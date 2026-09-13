package controller

import (
	"context"
	"testing"
	"time"

	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/protocol"
)

func TestMinecraftIPPortTargetsCreateTransparentTCPAndUDPServices(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.CreateEnrollmentToken(ctx, "minecraft-enroll", time.Hour); err != nil {
		t.Fatal(err)
	}
	agent, err := store.EnrollAgent(ctx, protocol.AgentEnrollmentRequest{Token: "minecraft-enroll", NodeName: "minecraft-relay"})
	if err != nil {
		t.Fatal(err)
	}
	java, err := store.CreateLandingNode(ctx, CreateLandingNodeRequest{Name: "Java server", Address: "192.0.2.10", Port: 25565, Network: "tcp", Protocol: "minecraft"})
	if err != nil {
		t.Fatal(err)
	}
	bedrock, err := store.CreateLandingNode(ctx, CreateLandingNodeRequest{Name: "Bedrock server", Address: "192.0.2.11", Port: 19132, Network: "udp", Protocol: "minecraft"})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		nodeID, network string
		port            int
	}{
		{java.ID, "tcp", 25565},
		{bedrock.ID, "udp", 19132},
	} {
		service, err := store.CreateRelayService(ctx, CreateRelayServiceRequest{RelayNodeID: agent.AgentID, Name: "Minecraft " + item.network, ListenPort: item.port, Network: item.network, Mode: "failover", Targets: []CreateServiceTarget{{LandingNodeID: item.nodeID}}})
		if err != nil {
			t.Fatal(err)
		}
		if len(service.Targets) != 1 || service.Targets[0].Address == "" || service.Network != item.network {
			t.Fatalf("unexpected Minecraft service: %+v", service)
		}
	}
	config, err := store.AgentConfig(ctx, agent.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Services) != 2 || config.Services[0].Targets[0].Address != "192.0.2.11:19132" || config.Services[1].Targets[0].Address != "192.0.2.10:25565" {
		t.Fatalf("Minecraft targets were not serialized as IP:port: %+v", config)
	}
}
