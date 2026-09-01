# Go Relay Platform Refactor

This branch contains the Go-based relay platform and the production controller
deployed behind `https://cdt.7b.tn/`. The former FastAPI/Python runtime has been
removed; all production processes are Go-based. A small set of `/api` handlers
remains inside the Go controller solely for existing compatibility-page URLs
and bookmarks, not as a second backend service.

## Components

- `cmd/controller`: Go control-plane API and SQLite persistence.
- `cmd/relay-agent`: single-binary relay agent installed on a CDT ECS host.
- `cmd/dispatcher`: optional fixed-front-door TCP/UDP L4 gateway for stable
  public entry points.
- `internal/relay`: native TCP/UDP L4 forwarding with live config updates.
- `internal/agent`: enrollment, desired-state polling and heartbeat reporting.
- `internal/controller`: relay nodes, landing nodes and relay service APIs.
- `internal/aliyun`: signed ECS/CDT API client and cloud resource synchronization.
- `internal/protocol`: versioned JSON contract shared by controller and agents.

The fixed-front-door deployment is documented in
[`DISPATCHER.md`](DISPATCHER.md). It is intentionally separate from the CDT
Relay data plane: deploy two or more independent, high-bandwidth non-CDT
gateways and keep the controller's read-only dispatch token out of Git and
administrator credentials.

## Cloud-resource workflows

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
- Installed Agents check the controller's architecture-specific release checksum
  on the configured schedule, download over HTTPS, verify SHA-256, atomically
  replace the binary and restart through systemd or OpenRC. A pending-update
  marker retains one rollback binary; three unsuccessful boots restore the
  previous executable.
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

### Traffic accounting scope

`ListCdtInternetTraffic` is an account-level API in this integration. The
request has no ECS instance filter and the returned traffic details contain no
ECS instance ID. The controller therefore sums all returned details and stores
one snapshot per configured Alibaba Cloud account; `/api/v2/cloud/overview`
marks these entries with `scope: "account"`. The value must not be interpreted
as traffic used by an individual ECS or Relay Agent.

The configured limit and protection threshold are compared with that account
aggregate. If several Relay Agents are associated with the same cloud account,
`drain_relay` drains all of them together. Separate RAM AccessKeys belonging to
the same Alibaba Cloud account do not create independent usage counters. The
UI's default `200 GB` value is a local protection threshold, not a guarantee
that each ECS receives an independent 200 GB CDT allowance. A strict per-ECS
policy requires a billing/API scope that is independently measurable, or a
future per-Agent accounting policy; Agent byte counters are useful for
operations but are not currently the authoritative CDT billing counter.

Each account selects one mode:

- `alert_only`: record a transition event and keep the relay and ECS running.
  It remains the API/database compatibility default. The v2 console proposes
  `drain_relay` for new accounts, while an operator may explicitly choose a
  different mode.
- `drain_relay`: publish a new Agent revision that stops accepting new
  connections. Existing TCP connections drain naturally. Saved relay service
  settings are not modified, and services reopen automatically after a later
  successful traffic sync reports usage below the threshold. A new billing
  month also produces a fresh provider snapshot; it is not required for a
  recovery when the measured value has already fallen below the threshold.
- `stop_ecs`: send one stop command to a specifically selected ECS instance.
  Failed commands remain pending and retry after the next successful traffic
  sync; successful commands are not repeated.

Transitions and action failures are persisted in `relay_events`. Disabling a
cloud account releases an active drain state so cloud-sync settings cannot
silently leave a relay closed forever.

The relay is protocol-transparent. SS2022, VLESS, REALITY, WebSocket, gRPC and
other TCP/UDP protocols remain encrypted end-to-end between client and landing
server.

## Relay pool eligibility and automatic draining

A pool publishes one A record for each eligible member. A member is eligible
only when the pool and member are enabled, it has a valid public IP, its Agent
heartbeat is fresh (45 seconds by default), the current revision equals the
desired revision, and the pool service reports that its listener is active. An
account-level `drain_relay` transition, an Agent update in
`draining`/`updating`, an offline or stale Agent, an unapplied revision, or a
failed listener makes the member ineligible.

Ineligible records are disabled and removed at the next DNS reconciliation;
recovered members are added back automatically. The production scheduler runs
every 30 seconds, after which recursive resolvers and clients may retain the old
answer for the record TTL. Disabling a service closes its listener and existing
TCP connections drain naturally, but UDP sessions are closed. DNS changes do
not migrate an established connection: they affect later resolutions and new
connections only.

New pools enable `auto_drain` by default. When a bound account crosses its
configured account-level CDT threshold, the pool withdraws that account's
Relay addresses and publishes the Agent drain revision even if the account's
standalone protection mode is `alert_only`. Disable this switch only when an
operator deliberately wants an alert without automatic pool failover.

The controller also retains the preceding successful account snapshot and
derives a short-term GB/minute rate. If that rate projects the configured hard
threshold will be crossed during the control-plane safety window, an
auto-draining pool is withdrawn early. The production default is four minutes,
covering the two-minute cloud poll, DNS reconciliation, normal TTL, and a small
client-reconnect margin. Set `CDT_TRAFFIC_SAFETY_WINDOW=0s` to disable this
forecast or use a Go duration such as `3m30s` to tune it. A predictive drain is
sticky until the cumulative counter decreases at a new billing period; once
the measured value reaches the hard threshold it becomes a normal protection
event. Predictive protection never sends an early `stop_ecs` command—it only
drains Relay services until the measured hard threshold is reached.

Pool member weights do not control DNS selection. Multi-A DNS is resolver/client
selection, not latency-aware routing or quota-aware load balancing, so traffic
need not be distributed evenly across 200 GB Relay hosts. Landing-target probe
failures alone also do not remove a Relay IP: when every probe fails the Agent
still attempts the enabled targets, and pool DNS eligibility checks listener
readiness rather than end-to-end protocol success.

For the low-overhead deployment, use one independent billing account per Relay
where possible and keep the same protocol, credentials, port, and member set
for every replica in a logical pool. This keeps one stable client link while
the controller withdraws accounts that are approaching exhaustion. If exact
per-connection quota weighting or a fixed front-door IP becomes a hard
requirement, add two or more non-CDT L4 dispatchers in front of this pool; do
not put that dispatcher on a scarce CDT account. A dispatcher improves backend
selection but still cannot migrate an already-established TCP byte stream.

For a fixed front door, set the pool's `front_door_mode` to `dispatcher`. The
controller then stops reconciling Relay IPs for that pool; the hostname must be
DNS-only pointed at the independent Dispatcher hosts described in
[`DISPATCHER.md`](DISPATCHER.md). Existing pools default to `relay_dns` for
backward compatibility.

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
SHA-256 checksum, and installs a hardened systemd service or an OpenRC
`supervise-daemon` service on Alpine. A local binary can be used during
development:

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

Alpine hosts must boot with OpenRC (`rc-service` and `rc-update` available). A
container without systemd/OpenRC should run the Agent under its container
supervisor instead of using this host installer.

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
- `cdt-dispatcher-linux-amd64`
- `cdt-dispatcher-linux-arm64`
- `alicdt-controller-linux-amd64`
- `alicdt-controller-linux-arm64`
- `checksums.txt`

The same workflow pushes the multi-architecture controller image to
`ghcr.io/r1ddle1337/alicdt-controller` and the optional gateway image to
`ghcr.io/r1ddle1337/alicdt-dispatcher`.

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
- `GET|POST /api/v2/relay-pools`
- `PUT|DELETE /api/v2/relay-pools/{id}`
- `GET /api/v2/relay-pools/{id}/relay-links`
- `GET /api/v2/dispatch/pools/{poolID}` (dedicated dispatch token only;
  credential-free backend snapshot)
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
`/agent/install.sh` when built with `Dockerfile.controller`; the fixed-front-door
installer and embedded gateway assets are available at `/dispatcher/install.sh`
and `/dispatcher/{asset}`.

Agents installed before automatic upgrades were introduced need one bootstrap
command from **中转节点 → 升级已安装 Agent**. It downloads the same
checksum-verified binary and updates the existing systemd or OpenRC service to
allow future atomic replacements. The command does not enroll a new node or
consume an enrollment token. Containerized Agents must instead be updated by
rebuilding/restarting their container image.

### Small-disk sing-box log guard

Agent installation and upgrade also installs
`/usr/local/sbin/cdt-sing-box-log-cleanup`. It checks
`/var/log/sing-box/access.log` every minute and truncates the file in place
when it exceeds 50 MiB, preserving sing-box's open file descriptor. The path
and limit can be changed in
`/etc/cdt-relay/sing-box-log-cleanup.env` with
`CDT_SINGBOX_ACCESS_LOG` and `CDT_SINGBOX_ACCESS_LOG_MAX_MB`. Alpine uses
BusyBox `crond`; systemd hosts use `cdt-sing-box-log-cleanup.timer`. The task
never removes sing-box configuration or error logs.

## DNS-managed relay pools

DNS management uses a provider-neutral reconciliation layer. The first
release supports Aliyun DNS (AccessKey ID/Secret) and Cloudflare (scoped API
Token). A provider stores only its own credentials; API responses expose
configuration flags rather than secrets. Managed records are declared in the
console and are reconciled every 30 seconds or on demand. Reconciliation updates
or creates only those declared records and never performs a broad zone delete.
Use a separate hostname such as `panel.example.com` for the console and
`relay.example.com` for a multi-IP relay entry. Add one A record per healthy
CDT Relay; use Cloudflare automatic TTL or 60 seconds when fast transitions are
important.

Relay pools replicate one logical service to each selected Agent. A pool can
bind a DNS Provider; online members create managed A records, draining or
offline members are disabled and removed on the next DNS reconciliation. For
Cloudflare, TTL `1` means provider-managed automatic TTL; choose 60 seconds
when the fastest health-transition propagation is preferred. The landing-node
link generator emits the pool hostname and port, so users keep a single
logical node instead of importing one link per Relay.

Deleting a landing node detaches it from every route; when that was a pool's
last landing target, the pool and its generated Relay services are removed in
the same transaction. Deleting a pool also removes its pool-owned managed DNS
records (including the provider-side records); a temporary provider failure
leaves a non-publishable cleanup tombstone for the next synchronization pass.

### Hostname and port constraints

- One pool defines one hostname, one listen port and one transport. DNS maps the
  hostname to Relay IPs only; it cannot select a port, protocol or landing node.
- Set `front_door_mode=dispatcher` when the hostname is owned by fixed L4
  gateways. In that mode the controller deliberately does not publish Relay
  IPs; add the gateway A/AAAA records separately and keep them DNS-only.
- Multiple pools may reuse one hostname on different ports. Each generated
  client node still includes its own port, and every Relay selected for that
  hostname must listen on every advertised port. Use the same member set and
  lifecycle for those pools; otherwise DNS may return an Agent that does not
  serve the requested port.
- On one Agent, listeners cannot overlap on the same address, transport and
  port. A `tcp+udp` service occupies both transports. Use a distinct port for
  overlapping pools unless one service is TCP-only and the other is UDP-only.
- A pool's landing targets are replicas or failover choices for the same
  logical encrypted service. Because the Relay does not decrypt or inspect the
  protocol, unrelated SS/VLESS nodes with different credentials cannot safely
  share one hostname and one port; create a separate pool/port for each such
  node.
- A provider stores only one managed row for the same hostname/type/Relay IP.
  Port-specific pools may share that row; the controller keeps a pool binding
  table and advertises the address only while every bound route is ready. Use
  the same member set and lifecycle for shared hostnames, or use separate
  hostnames when routes have different failure domains.

Standalone managed DNS records can either use a manually entered value or be
attached to one or more registered Relay Agents. The controller stores one
managed A/AAAA row per selected Agent, reads each Agent's latest public IP,
follows IP changes, and disables that row while the Agent is offline or its
account is draining.

Admin APIs require `Authorization: Bearer $CDT_ADMIN_TOKEN`. Agent APIs use the
per-node secret returned during one-time enrollment.
