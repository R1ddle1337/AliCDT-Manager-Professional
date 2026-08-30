# Go Relay Platform Refactor

This branch introduces the next-generation relay platform alongside the current
Python production application. It does not replace the running deployment yet.

## Components

- `cmd/controller`: Go control-plane API and SQLite persistence.
- `cmd/relay-agent`: single-binary relay agent installed on a CDT ECS host.
- `internal/relay`: native TCP/UDP L4 forwarding with live config updates.
- `internal/agent`: enrollment, desired-state polling and heartbeat reporting.
- `internal/controller`: relay nodes, landing nodes and relay service APIs.
- `internal/protocol`: versioned JSON contract shared by controller and agents.

## Data-plane behavior

- TCP connections are pinned to their selected landing node until disconnect.
- Config updates affect new connections without restarting the agent.
- UDP clients receive a per-client session pinned to one landing node until the
  idle timeout expires.
- Supported scheduling modes: failover, weighted round robin and source IP hash.
- Local health checks run from the CDT relay host, not from the panel server.
- When all health checks fail, the agent still attempts enabled targets rather
  than silently dropping the service.

The relay is protocol-transparent. SS2022, VLESS, REALITY, WebSocket, gRPC and
other TCP/UDP protocols remain encrypted end-to-end between client and landing
server.

## Development run

```bash
export CDT_ADMIN_TOKEN="replace-with-random-admin-token"
export CDT_BOOTSTRAP_ENROLL_TOKEN="replace-with-one-time-token"
docker compose -f deploy/docker-compose.go.yml up --build
```

The development controller listens on `127.0.0.1:18010`. The existing
production application remains unchanged.

## Agent installation

After producing a local agent binary:

```bash
go build -o /tmp/cdt-relay-agent ./cmd/relay-agent
sudo scripts/install-agent.sh \
  --controller https://cdt.7b.tn \
  --enroll-token ONE_TIME_TOKEN \
  --node-name aliyun-hk-01 \
  --binary /tmp/cdt-relay-agent
```

The panel never needs to store a root password or SSH key. SSH is used only for
the initial installation.

## Current API surface

- `POST /api/v2/agents/enroll`
- `GET /api/v2/agents/{id}/config`
- `POST /api/v2/agents/{id}/heartbeat`
- `POST /api/v2/enrollment-tokens`
- `GET /api/v2/relay-nodes`
- `GET|POST /api/v2/landing-nodes`
- `GET|POST /api/v2/relay-services`
- `DELETE /api/v2/relay-services/{id}`
- `PUT /api/v2/landing-nodes/{id}`
- `PUT /api/v2/relay-services/{id}`

The controller also serves the Vue SPA and the root SSH installer at
`/agent/install.sh` when built with `Dockerfile.controller`.

Admin APIs require `Authorization: Bearer $CDT_ADMIN_TOKEN`. Agent APIs use the
per-node secret returned during one-time enrollment.
