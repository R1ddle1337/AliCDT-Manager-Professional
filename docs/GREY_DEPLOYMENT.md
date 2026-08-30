# Real CDT ECS Grey Deployment

This procedure runs the Go platform beside the current Python production
container. It does not switch the public panel until relay behavior has been
measured and accepted.

## 1. Seed and start the staging controller

Create an online SQLite backup from the running Python container. This copies
the five existing cloud accounts and traffic state without sharing one writable
database between Python and Go:

```bash
install -d -m 700 data-go-staging
test ! -e data-go-staging/guard.db
docker exec alicdt-manager python -c \
  'import sqlite3; source=sqlite3.connect("/app/data/guard.db"); target=sqlite3.connect("/app/data/guard-grey-seed.db"); source.backup(target); target.close(); source.close()'
install -m 600 /app/alicdt-manager/data/guard-grey-seed.db data-go-staging/guard.db
rm /app/alicdt-manager/data/guard-grey-seed.db
```

The temporary source path is explicit and removed after the copy. Do not rerun
this step over an existing staging database, because that database will contain
Agent credentials and relay rules after enrollment.

Create a long random admin API token and keep it in a root-readable shell or
environment file. Do not commit it:

```bash
export CDT_ADMIN_TOKEN="$(openssl rand -hex 32)"
docker compose -f deploy/docker-compose.go.staging.yml up -d --build
docker compose -f deploy/docker-compose.go.staging.yml ps
curl -fsS http://127.0.0.1:18010/api/v2/auth/initialized
```

The staging database is stored in `data-go-staging/`. It starts as a consistent
production snapshot but remains a separate writable database during grey tests.

## 2. Publish only the Agent API

Merge the directives from `deploy/nginx-go-staging.conf.example` into the
existing `cdt.7b.tn` nginx configuration, then validate before reloading:

```bash
nginx -t
systemctl reload nginx
curl -fsS https://cdt.7b.tn/api/v2/auth/initialized
curl -fsS https://cdt.7b.tn/agent/install.sh | sed -n '1,5p'
curl -fsS -o /dev/null -w '%{http_code}\n' https://cdt.7b.tn/
```

The final command must still return `200` from the current production panel.

Access the Go console without exposing a second public UI:

```bash
ssh -L 18010:127.0.0.1:18010 root@PANEL_HOST
```

Open `http://127.0.0.1:18010`, initialize the administrator if needed, and
generate a 30-minute enrollment token under **中转节点**.

## 3. Build the grey Agent

Until a version tag publishes release binaries, build the current branch on the
panel host. Select the target architecture:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
  -trimpath -ldflags='-s -w' \
  -o /tmp/cdt-relay-agent-linux-amd64 ./cmd/relay-agent
```

Use `GOARCH=arm64` and the matching filename for an ARM ECS. Copy the binary to
the selected CDT ECS with SSH/SCP. The panel never stores the root password or
private key.

## 4. Install through root SSH

On the selected CDT ECS:

```bash
curl -fsSL https://cdt.7b.tn/agent/install.sh -o /tmp/install-cdt-relay.sh
chmod 700 /tmp/install-cdt-relay.sh
/tmp/install-cdt-relay.sh \
  --server https://cdt.7b.tn \
  --token ONE_TIME_TOKEN \
  --node-name cdt-grey-01 \
  --binary /tmp/cdt-relay-agent-linux-amd64
```

Verify enrollment and local state:

```bash
systemctl status cdt-relay-agent --no-pager
journalctl -u cdt-relay-agent -n 100 --no-pager
stat -c '%a %n' /var/lib/cdt-relay/credentials.json
```

The credentials and last valid config must be mode `600`. After enrollment,
the Agent communicates outbound over HTTPS and does not expose a management
port.

## 5. Configure the first relay

In the Go console:

1. Confirm the Agent is online and automatically associated with the expected
   ECS instance ID and region.
2. Add one landing node by IP and port.
3. Add a relay service on an unused test entry port.
4. Start with `failover`, one primary and one backup landing node.
5. Use `tcp+udp` for SS2022 and `tcp` for VLESS REALITY.

Keep all existing production proxy ports unchanged during this phase.

## 6. Validation matrix

Record results for both direct and CDT-relayed paths:

| Check | Evidence |
|---|---|
| SS2022 TCP | HTTP download and repeated TCP connections succeed |
| SS2022 UDP | DNS/UDP or SOCKS5 UDP Associate traffic succeeds |
| VLESS REALITY | Client handshake and HTTP request succeed without changing REALITY parameters |
| Primary failure | New connections select the backup landing node on the same entry port |
| Existing TCP session | Remains pinned until that landing connection ends |
| Config update | Agent reaches desired/current revision equality without restart |
| Controller outage | Rebooted Agent restores `last-valid-config.json` and keeps forwarding |
| Mobile console | No horizontal overflow on cloud, node and service pages |

For return-path quality, collect at least 30 minutes of packet loss, latency,
reconnect count and throughput during the expected mainland peak period. Do not
use a successful local protocol test as proof of CDT route quality.

## 7. Safe rollback

First disable or delete the grey relay service in the Go console. This closes
the listener for new connections while established TCP connections drain.

To stop the Agent but preserve its credentials and cached config:

```bash
systemctl disable --now cdt-relay-agent
```

To restore it:

```bash
systemctl enable --now cdt-relay-agent
```

Remove the two staging nginx locations and reload nginx only after `nginx -t`
passes. Stop the staging controller while preserving its database:

```bash
docker compose -f deploy/docker-compose.go.staging.yml down
```

Do not delete `data-go-staging/` until the grey-test evidence and rollback audit
have been reviewed.
