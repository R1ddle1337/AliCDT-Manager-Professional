package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"strconv"
	"strings"
	"time"
)

// DispatcherPoolSnapshot returns only Relay members that are ready to accept
// a new connection. The dispatcher polls this endpoint instead of receiving
// the admin token or any landing-node credentials.
func (s *Store) DispatcherPoolSnapshot(ctx context.Context, poolID string) (DispatcherPoolSnapshot, error) {
	pool, err := s.GetRelayPool(ctx, poolID)
	if err != nil {
		return DispatcherPoolSnapshot{}, err
	}
	snapshot := DispatcherPoolSnapshot{
		PoolID:                pool.ID,
		PoolName:              pool.Name,
		FrontDoorMode:         pool.FrontDoorMode,
		ListenPort:            pool.ListenPort,
		Network:               pool.Network,
		SelectionMode:         dispatcherSelectionMode(pool.Mode),
		DialTimeoutMillis:     pool.DialTimeoutMillis,
		UDPIdleTimeoutSeconds: pool.UDPIdleTimeoutSeconds,
		FailureThreshold:      pool.Health.FailureThreshold,
		FailureCooldownSecs:   pool.Health.RecoveryCooldownSecs,
		MaxUDPSessions:        65536,
		Backends:              make([]DispatcherBackend, 0),
		GeneratedAt:           time.Now().UTC(),
	}
	if pool.Enabled {
		for _, member := range pool.Members {
			if !member.Enabled || !strings.EqualFold(member.Status, "online") || !validRelayIP(member.PublicIP) {
				continue
			}
			backend := DispatcherBackend{
				ID:                     member.RelayNodeID,
				Name:                   member.RelayNodeName,
				Address:                net.JoinHostPort(member.PublicIP, strconv.Itoa(pool.ListenPort)),
				Port:                   pool.ListenPort,
				Weight:                 member.Weight,
				TrafficKnown:           member.TrafficKnown,
				TrafficRemainingGB:     member.TrafficRemainingGB,
				TrafficRateGBPerMinute: member.TrafficRateGBPerMinute,
			}
			snapshot.Backends = append(snapshot.Backends, backend)
		}
	}
	snapshot.Revision = dispatcherSnapshotRevision(snapshot)
	return snapshot, nil
}

// A failover pool is still replicated to every CDT Relay. At the fixed front
// door we use quota-aware distribution by default so one account does not
// consume its entire allowance while its replicas sit idle. Explicit weighted,
// round-robin and IP-hash modes remain available to operators.
func dispatcherSelectionMode(poolMode string) string {
	switch strings.ToLower(strings.TrimSpace(poolMode)) {
	case "weighted", "round_robin", "ip_hash":
		return strings.ToLower(strings.TrimSpace(poolMode))
	default:
		return "quota_weighted"
	}
}

func dispatcherSnapshotRevision(snapshot DispatcherPoolSnapshot) string {
	copy := snapshot
	copy.Revision = ""
	copy.GeneratedAt = time.Time{}
	data, _ := json.Marshal(copy)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])[:16]
}
