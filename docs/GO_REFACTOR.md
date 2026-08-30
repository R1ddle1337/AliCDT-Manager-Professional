# Go Relay Platform Refactor

This branch contains the Go-based relay platform and the production controller
deployed behind `https://cdt.7b.tn/`. The original cloud-resource workflows are
kept through the compatibility API while the relay control plane uses the
versioned Go API.

## Components

- `cmd/controller`: Go control-plane API and SQLite persistence.
- `cmd/relay-agent`: single-binary relay agent installed on a CDT ECS host.
- `internal/relay`: native TCP/UDP L4 forwarding with live config updates.
- `internal/agent`: enrollment, desired-state polling and heartbeat reporting.
- `internal/controller`: relay nodes, landing nodes and relay service APIs.
- `internal/aliyun`: signed ECS/CDT API client and cloud resource synchronization.
- `internal/protocol`: versioned JSON contract shared by controller and agents.

## Cloud-resource compatibility

The controller keeps the original account workflow and SQLite data in place:

- CDT API Key ID/Secret management with secrets omitted from API responses.
- ECS instance sync, start, stop, rename and release operations.
- Spot-instance keep-alive checks, no-stock state, scheduled start/stop and
  monthly reset behavior.
- International-site balance/billing queries; mainland billing remains
  intentionally disabled.
- Traffic snapshots retain the last valid value when a cloud API request fails.

## Data-plane behavior

- TCP connections are pinned to their selected landing node until disconnect.
- Config updates affect new connections without restarting the agent.
- UDP clients receive a per-client session pinned to one landing node until the
  idle timeout expires.
- Supported scheduling modes: failover, weighted round robin and source IP hash.
- Local health checks run from the CDT relay host, not from the panel server.
- When all health checks fail, the agent still attempts enabled targets rather
  than silently dropping the service.
- The Agent persists the last valid desired state with an atomic private file.
  Existing relays return after a reboot even while the controller is offline.
- Invalid or unbindable revisions are rolled back to the last valid state.

## CDT traffic protection

Protection transitions are evaluated only after a successful CDT traffic
response. A timeout or API error preserves both the last valid traffic snapshot
and the current protection state.

Each account selects one mode:

- `alert_only`: record a transition event and keep the relay and ECS running.
  This is the migration-safe default for every existing account.
- `drain_relay`: publish a new Agent revision that stops accepting new
  connections. Existing TCP connections drain naturally. Saved relay service
  settings are not modified, and services reopen automatically after usage
  falls below the threshold in a new billing month.
- `stop_ecs`: send one stop command to a specifically selected ECS instance.
  Failed commands remain pending and retry after the next successful traffic
  sync; successful commands are not repeated.

Transitions and action failures are persisted in `relay_events`. Disabling a
cloud account releases an active drain state so cloud-sync settings cannot
silently leave a relay closed forever.

The relay is protocol-transparent. SS2022, VLESS, REALITY, WebSocket, gRPC and
other TCP/UDP protocols remain encrypted end-to-end between client and landing
server.

## Protocol validation

An isolated Docker environment was used to exercise the real protocol cores,
not only generic echo servers:

- Xray 26.3.27: VLESS + XTLS Vision + REALITY completed an HTTP request through
  `Xray client -> Go Relay Agent -> Xray server -> landing HTTP service`.
- sing-box 1.13.20: Shadowsocks 2022
  `2022-blake3-aes-128-gcm` completed both an HTTP-over-TCP request and a real
  SOCKS5 UDP Associate round trip through a `tcp+udp` relay service.
- The SS2022 run reported non-zero Agent counters in both directions and the
  controller converged to desired/current revision `1/1`.

These checks prove protocol-transparent forwarding and Agent accounting. They
do not replace a grey test on a real CDT ECS, which is still required to measure
mainland return-path quality, packet loss and client reconnection behavior.

## Development run

```bash
export CDT_ADMIN_TOKEN="replace-with-random-admin-token"
export CDT_BOOTSTRAP_ENROLL_TOKEN="replace-with-one-time-token"
docker compose -f deploy/docker-compose.go.yml up --build
```

The development controller listens on `127.0.0.1:18010`. The existing
production application remains unchanged.

For a real ECS rollout or rollback, follow
[`PRODUCTION_CUTOVER.md`](PRODUCTION_CUTOVER.md).
After that matrix passes, use [`PRODUCTION_CUTOVER.md`](PRODUCTION_CUTOVER.md)
for the reversible same-port cutover.

## Agent installation

Create a one-time enrollment token in the panel, then connect to the CDT ECS by
root SSH and run:

```bash
curl -fsSL https://cdt.7b.tn/agent/install.sh | \
  bash -s -- --server https://cdt.7b.tn --token ONE_TIME_TOKEN --node-name aliyun-hk-01
```

The installer selects amd64 or arm64 automatically, verifies the release
SHA-256 checksum and installs a hardened systemd service. A local binary can be
used during development:

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

The same one-time enrollment command is available directly on each account card
under **云账户 → 安装 Agent**. This keeps API-key management and the root SSH
installation flow in one place without giving the controller SSH access.

Agent enrollment reads the Aliyun ECS instance ID, region and public IP from
IMDSv2 when available. This associates the relay node with its cloud resource
without granting the Agent an AccessKey.

## Releases

Tags matching `v*` publish these checksum-protected assets:

- `cdt-relay-agent-linux-amd64`
- `cdt-relay-agent-linux-arm64`
- `alicdt-controller-linux-amd64`
- `alicdt-controller-linux-arm64`
- `checksums.txt`

The same workflow pushes the multi-architecture controller image to
`ghcr.io/r1ddle1337/alicdt-controller`.

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
- `GET /api/v2/cloud/overview`
- `POST /api/v2/cloud/sync`
- `POST|PUT|DELETE /api/v2/cloud/accounts`
- `POST /api/v2/cloud/instances/{id}/start`
- `POST /api/v2/cloud/instances/{id}/stop`
- `GET /api/v2/landing-nodes/{id}/relay-links`
- `GET|POST /api/v2/dns/providers`
- `PUT|DELETE /api/v2/dns/providers/{id}`
- `POST /api/v2/dns/providers/{id}/test`
- `POST /api/v2/dns/providers/{id}/sync`
- `GET|POST /api/v2/dns/records`
- `PUT|DELETE /api/v2/dns/records/{id}`
- `POST /api/v2/dns/sync`

Landing nodes accept complete `vless://`, `ss://`/SS2022, `vmess://`,
`trojan://`, `hysteria2://` and `tuic://` links. The generated relay link
changes only the authority host and port; protocol credentials, Reality
parameters and transport query fields are retained.

The original console paths remain available for existing clients:

- `/api/accounts`, `/api/instances`, `/api/billing/{id}`
- `/api/settings`, `/api/logs` and the original instance control endpoints

The controller also serves the Vue SPA and the root SSH installer at
`/agent/install.sh` when built with `Dockerfile.controller`.

DNS management uses a provider-neutral reconciliation layer. The first
release supports Aliyun DNS (AccessKey ID/Secret) and Cloudflare (scoped API
Token). A provider stores only its own credentials; API responses expose
configuration flags rather than secrets. Managed records are declared in the
console and are reconciled every minute or on demand. Reconciliation updates
or creates only those declared records and never performs a broad zone delete.
Use a separate hostname such as `panel.example.com` for the console and
`relay.example.com` for a multi-IP relay entry. Add one A record per healthy
CDT Relay and keep TTL around 30--60 seconds.

Admin APIs require `Authorization: Bearer $CDT_ADMIN_TOKEN`. Agent APIs use the
per-node secret returned during one-time enrollment.
