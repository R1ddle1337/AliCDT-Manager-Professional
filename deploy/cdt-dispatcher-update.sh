#!/usr/bin/env bash
set -Eeuo pipefail

# Stateless gateway updater. Run this independently on each Dispatcher host;
# it never touches the controller database or Relay credentials.
REPO_DIR="${CDT_DISPATCH_UPDATE_REPO:-/opt/alicdt-manager}"
BRANCH="${CDT_DISPATCH_UPDATE_BRANCH:-refactor/go-relay-platform}"
ENV_FILE="${CDT_DISPATCH_UPDATE_ENV_FILE:-/etc/cdt-dispatcher.env}"
COMPOSE_FILE="$REPO_DIR/deploy/docker-compose.dispatcher.yml"
LOCK_FILE="${CDT_DISPATCH_UPDATE_LOCK_FILE:-/run/lock/cdt-dispatcher-update.lock}"
ROLLBACK_IMAGE="alicdt-dispatcher:rollback"

# A typo in a path must fail before any Docker operation; avoid broad cleanup
# or recursive deletion in this host-side script.
if [ ! -d "$REPO_DIR/.git" ] || [ ! -f "$COMPOSE_FILE" ] || [ ! -f "$ENV_FILE" ]; then
  echo "dispatcher update environment is incomplete" >&2
  exit 1
fi
mkdir -p "$(dirname "$LOCK_FILE")"
exec 9>"$LOCK_FILE"
flock -n 9 || exit 0

rollback() {
  local code=$?
  trap - ERR
  if [ "$code" -ne 0 ] && docker image inspect "$ROLLBACK_IMAGE" >/dev/null 2>&1; then
    docker tag "$ROLLBACK_IMAGE" alicdt-dispatcher:production >/dev/null 2>&1 || true
    docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" up -d --force-recreate --no-build >/dev/null 2>&1 || true
  fi
  exit "$code"
}
trap rollback ERR

if ! git -C "$REPO_DIR" diff --quiet || ! git -C "$REPO_DIR" diff --cached --quiet; then
  echo "repository has uncommitted changes; refusing gateway update" >&2
  exit 1
fi
current_branch="$(git -C "$REPO_DIR" symbolic-ref --short HEAD)"
[ "$current_branch" = "$BRANCH" ] || { echo "unexpected deployment branch" >&2; exit 1; }
git -C "$REPO_DIR" fetch --prune origin "$BRANCH"
if git -C "$REPO_DIR" rev-parse --verify "origin/$BRANCH" >/dev/null 2>&1 && [ "$(git -C "$REPO_DIR" rev-parse HEAD)" != "$(git -C "$REPO_DIR" rev-parse "origin/$BRANCH")" ]; then
  git -C "$REPO_DIR" merge --ff-only "origin/$BRANCH"
fi

if docker image inspect alicdt-dispatcher:production >/dev/null 2>&1; then
  docker tag alicdt-dispatcher:production "$ROLLBACK_IMAGE"
fi
docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" build --pull dispatcher
docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" up -d --force-recreate --no-build --wait --wait-timeout 120
health_port="${CDT_DISPATCH_HEALTH_PORT:-9091}"
curl -fsS "http://127.0.0.1:$health_port/readyz" >/dev/null
