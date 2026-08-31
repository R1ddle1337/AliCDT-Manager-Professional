# Go Controller Production Cutover

Do not run this procedure until the real CDT ECS grey matrix in
`GREY_DEPLOYMENT.md` has passed. The objective is a reversible replacement of
the Python container while keeping nginx on the existing local port.

## Preconditions

- The branch commit intended for production has green push and PR checks.
- SS2022 TCP/UDP and VLESS REALITY have passed on the real CDT ECS.
- Primary landing failure and controller-outage recovery have been observed.
- `cdt.7b.tn` currently returns `200` and the old container is healthy.
- A root-readable `CDT_ADMIN_TOKEN` is available outside the repository.
- No other process will write `/app/alicdt-manager/data/guard.db` during the
  cutover or rollback.
- At least two independent non-CDT Dispatcher hosts have passed the grey
  protocol/failure matrix in [`DISPATCHER.md`](DISPATCHER.md), and their public
  IPs are ready for a DNS-only RRset.
- `data-go-staging/guard.db` was originally seeded from production and now
  contains the accepted Agent credentials, landing nodes and relay services.

## 1. Record the current state

```bash
docker ps --filter name=alicdt-manager
docker inspect alicdt-manager --format '{{.Image}} {{.State.Status}}'
curl -fsS -o /dev/null -w '%{http_code}\n' https://cdt.7b.tn/
```

Keep the old container; stop it during cutover rather than deleting it. This is
the fastest rollback path.

## 2. Freeze both databases and create backups

```bash
export CDT_ADMIN_TOKEN="THE_STAGING_ADMIN_TOKEN"
docker compose -f deploy/docker-compose.go.staging.yml down
docker stop alicdt-manager
install -d -m 700 /app/alicdt-manager/backups
cutover_id="$(date -u +%Y%m%dT%H%M%SZ)"
production_backup="/app/alicdt-manager/backups/guard-python-${cutover_id}.db"
staging_backup="/app/alicdt-manager/backups/guard-go-staging-${cutover_id}.db"
cp --preserve=mode,timestamps /app/alicdt-manager/data/guard.db "$production_backup"
cp --preserve=mode,timestamps data-go-staging/guard.db "$staging_backup"
chmod 600 "$production_backup" "$staging_backup"
if [ -s data-go-staging/guard.db-wal ]; then
  cp --preserve=mode,timestamps data-go-staging/guard.db-wal "${staging_backup}-wal"
  chmod 600 "${staging_backup}-wal"
fi
```

Both applications must be stopped before copying so the databases and WAL state
are consistent.

## 3. Promote the accepted staging database

The staging database is the candidate that contains all grey Agent identities
and relay rules. Promote that exact file only after both backups above exist:

```bash
for suffix in -wal -shm; do
  stale_path="/app/alicdt-manager/data/guard.db${suffix}"
  if [ -e "$stale_path" ]; then
    mv "$stale_path" "/app/alicdt-manager/backups/guard-python-${cutover_id}.db${suffix}"
  fi
done
install -m 600 data-go-staging/guard.db /app/alicdt-manager/data/guard.db
if [ -s data-go-staging/guard.db-wal ]; then
  install -m 600 data-go-staging/guard.db-wal /app/alicdt-manager/data/guard.db-wal
fi
```

## 4. Start the Go controller on the existing port

```bash
export CDT_ADMIN_TOKEN="REPLACE_WITH_ROOT_READABLE_TOKEN"
export CDT_PUBLIC_PORT=18000
docker compose -f deploy/docker-compose.go.production.yml up -d --build
docker compose -f deploy/docker-compose.go.production.yml ps
```

Wait for `healthy`, then verify both local and public paths:

```bash
curl -fsS http://127.0.0.1:18000/api/v2/auth/initialized
curl -fsS -o /dev/null -w '%{http_code}\n' http://127.0.0.1:18000/
curl -fsS -o /dev/null -w '%{http_code}\n' https://cdt.7b.tn/
```

Because nginx already proxies the public panel to local port `18000`, no root
location change is required when the Go container uses the same port. After the
local API is healthy, remove the two temporary staging locations, run `nginx -t`
and reload nginx. `/api/v2/` then reaches the promoted production database
through the normal root proxy. Agent polling may fail for a few seconds during
this change, but cached relay configs continue forwarding.

## 5. Post-cutover audit

Use the Go console and API to confirm:

- all existing cloud accounts and ECS instances are present;
- every old valid traffic snapshot remains visible;
- old accounts use `alert_only` unless explicitly changed;
- Relay Agents are online and desired/current revisions converge;
- SS2022 UDP and REALITY clients still use the same entry address and port;
- no AccessKey Secret appears in API responses or logs.

Keep the old container stopped and intact through the observation window. Every
Agent must reappear before the cutover is accepted.

## 6. Switch the fixed front door (only after the controller is healthy)

For a pool whose `front_door_mode` is `dispatcher`, verify that its DNS Provider
is empty and that both gateways report HTTP `200` from `/readyz`. Lower the old
entry TTL, publish the two gateway A/AAAA records for the fixed hostname, and
wait at least one TTL plus the client reconnect margin. Confirm TCP and UDP
traffic on both gateways and that the controller snapshot removes drained Relay
accounts. Keep the previous Relay-DNS records documented for immediate rollback;
never mix Relay and Dispatcher IPs in one RRset.

## 7. Fast rollback

If the Go controller fails a cutover gate:

```bash
export CDT_ADMIN_TOKEN="THE_SAME_TOKEN_USED_ABOVE"
docker compose -f deploy/docker-compose.go.production.yml down
docker compose -f deploy/docker-compose.go.staging.yml up -d
# Restore the two staging nginx locations, then:
nginx -t
systemctl reload nginx
docker start alicdt-manager
curl -fsS -o /dev/null -w '%{http_code}\n' https://cdt.7b.tn/
```

The Go migration only adds compatible tables and columns (including the
`relay_pools.front_door_mode` default), so the Python ORM can
normally reopen the promoted database. The restarted staging controller uses
its unchanged grey database, so Agent authentication and relay management also
return. Keep both backups untouched. Restore the Python backup only if an
integrity audit proves it is required, and only while all Python and Go
controllers are stopped.

## 8. Final cleanup

After the agreed observation period and explicit acceptance:

1. Create a version tag so release binaries and the multi-architecture image
   are reproducible.
2. Retain at least one pre-cutover database backup.
3. Remove the stopped Python container only after rollback is no longer needed.
4. Keep the Agent credentials and cached configs on every CDT ECS.
