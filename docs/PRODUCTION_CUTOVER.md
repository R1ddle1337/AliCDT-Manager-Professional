# Go Controller Production Operations

The Go controller is the sole supported production runtime. The historical
Python cutover procedure has been retired together with the old FastAPI image,
root Dockerfile, root Compose file, and installer.

## Production compose

Use [`deploy/docker-compose.go.production.yml`](../deploy/docker-compose.go.production.yml)
with an environment file kept outside the repository:

```bash
docker compose --env-file /root/.config/alicdt-manager/production.env \
  -f deploy/docker-compose.go.production.yml up -d --build
docker compose --env-file /root/.config/alicdt-manager/production.env \
  -f deploy/docker-compose.go.production.yml ps
```

The controller listens on the existing loopback port (`18000` by default),
uses `/app/alicdt-manager/data` for the SQLite database, and serves the Vue
console and checksum-verified Agent assets from the same image. The production
Compose default is the image's embedded Agent; set
`CDT_AGENT_RELEASE_SOURCE=github` explicitly when a published GitHub release
should be authoritative. Keep the admin and dispatch tokens out of Git and
restrict the environment file to mode `0600`.

## Updates and rollback

The panel's update action writes a request marker for the host-side systemd
unit. [`deploy/alicdt-manager-update.sh`](../deploy/alicdt-manager-update.sh)
fetches the configured branch, builds `Dockerfile.controller`, makes a private
database backup, and waits for the new container health check. It retains the
previous Go image as `alicdt-controller:rollback`; no Python fallback exists.

If an update fails, leave the controller stopped when no rollback image is
available, restore the previous Go image or database backup only after an
integrity check, and verify:

```bash
curl -fsS http://127.0.0.1:18000/api/v2/auth/initialized
curl -fsS -o /dev/null -w '%{http_code}\n' https://cdt.7b.tn/
```

Never run a second service against the production SQLite file. Relay Agents
keep their last valid configuration and continue forwarding during a brief
controller restart.

For fixed-front-door Dispatcher rollout and DNS safeguards, see
[`DISPATCHER.md`](DISPATCHER.md).
