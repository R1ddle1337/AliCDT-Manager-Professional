#!/usr/bin/env bash
set -Eeuo pipefail

# Host-side updater invoked by systemd after the controller writes update.request.
# It intentionally runs outside the controller container so the web process never
# receives access to the host Docker socket.
REPO_DIR="${CDT_UPDATE_REPO:-/root/workspace/AliCDT-Manager-custom}"
BRANCH="${CDT_UPDATE_BRANCH:-refactor/go-relay-platform}"
ENV_FILE="${CDT_UPDATE_ENV_FILE:-/root/.config/alicdt-manager/production.env}"
COMPOSE_FILE="$REPO_DIR/deploy/docker-compose.go.production.yml"
DATA_DIR="${CDT_UPDATE_DATA_DIR:-/app/alicdt-manager/data}"
BACKUP_DIR="${CDT_UPDATE_BACKUP_DIR:-/app/alicdt-manager/backups}"
REQUEST_FILE="$DATA_DIR/update.request"
STATUS_FILE="$DATA_DIR/update.status.json"
LOCK_FILE="${CDT_UPDATE_LOCK_FILE:-/run/lock/alicdt-manager-update.lock}"

mkdir -p "$(dirname "$LOCK_FILE")" "$BACKUP_DIR"
exec 9>"$LOCK_FILE"
if ! flock -n 9; then
  exit 0
fi

write_status() {
  local status="$1" message="$2" request_id="${3:-}" commit="${4:-}" started="${5:-}" finished="${6:-}"
  python3 - "$STATUS_FILE" "$status" "$message" "$request_id" "$commit" "$started" "$finished" <<'PY'
import json, os, sys, tempfile

path, status, message, request_id, commit, started, finished = sys.argv[1:]
payload = {
    "status": status,
    "message": message,
    "request_id": request_id,
    "target_commit": commit,
}
if started:
    payload["started_at"] = started
if finished:
    payload["finished_at"] = finished
directory = os.path.dirname(path)
os.makedirs(directory, mode=0o700, exist_ok=True)
fd, temporary = tempfile.mkstemp(prefix=".update-status-", dir=directory)
try:
    os.fchmod(fd, 0o600)
    with os.fdopen(fd, "w", encoding="utf-8") as stream:
        json.dump(payload, stream, ensure_ascii=False)
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(temporary, path)
finally:
    if os.path.exists(temporary):
        os.unlink(temporary)
PY
}

utc_now() { date -u '+%Y-%m-%dT%H:%M:%SZ'; }

request_id=""
if [ -f "$REQUEST_FILE" ]; then
  request_id="$(python3 - "$REQUEST_FILE" <<'PY'
import json, sys
try:
    with open(sys.argv[1], encoding="utf-8") as stream:
        value = json.load(stream).get("request_id", "")
    print(value if isinstance(value, str) else "")
except Exception:
    print("")
PY
)"
fi
started_at="$(utc_now)"
target_commit=""
controller_stopped=0
rollback_image="alicdt-controller:rollback"

rollback() {
  local exit_code=$?
  trap - ERR
  if [ "$exit_code" -eq 0 ]; then
    return
  fi
  local finished_at
  finished_at="$(utc_now)"
  if [ "$controller_stopped" -eq 1 ]; then
    if docker image inspect "$rollback_image" >/dev/null 2>&1; then
      docker tag "$rollback_image" alicdt-controller:production >/dev/null 2>&1 || true
      docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" up -d --force-recreate --no-build --wait --wait-timeout 120 >/dev/null 2>&1 || true
    else
      # The Go controller is the only supported runtime. If no previous image
      # was available, leave the service stopped and report the failure rather
      # than attempting to revive the removed Python stack.
      docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" down >/dev/null 2>&1 || true
    fi
  fi
  write_status "error" "更新失败，已尝试恢复上一个版本" "$request_id" "$target_commit" "$started_at" "$finished_at" || true
  exit "$exit_code"
}
trap rollback ERR

if [ ! -d "$REPO_DIR/.git" ] || [ ! -f "$COMPOSE_FILE" ] || [ ! -f "$ENV_FILE" ]; then
  write_status "error" "更新环境不完整，请检查仓库、生产配置和 Docker Compose 文件" "$request_id" "" "$started_at" "$(utc_now)"
  exit 1
fi

write_status "running" "正在检查 GitHub 更新" "$request_id" "" "$started_at" ""
if ! git -C "$REPO_DIR" diff --quiet || ! git -C "$REPO_DIR" diff --cached --quiet; then
  write_status "error" "仓库存在未提交改动，已停止更新以保护本地配置" "$request_id" "" "$started_at" "$(utc_now)"
  exit 1
fi
current_branch="$(git -C "$REPO_DIR" symbolic-ref --short HEAD)"
if [ "$current_branch" != "$BRANCH" ]; then
  write_status "error" "当前部署分支不是预期的 $BRANCH，已停止更新" "$request_id" "" "$started_at" "$(utc_now)"
  exit 1
fi
git -C "$REPO_DIR" fetch --prune origin "$BRANCH"
if git -C "$REPO_DIR" rev-parse --verify "origin/$BRANCH" >/dev/null 2>&1 && [ "$(git -C "$REPO_DIR" rev-parse HEAD)" != "$(git -C "$REPO_DIR" rev-parse "origin/$BRANCH")" ]; then
  git -C "$REPO_DIR" merge --ff-only "origin/$BRANCH"
fi
target_commit="$(git -C "$REPO_DIR" rev-parse HEAD)"

source_fingerprint() {
  local path
  for path in "$@"; do
    git -C "$REPO_DIR" rev-parse "HEAD:$path"
  done | sha256sum | cut -c1-12
}

controller_build_version="$(git -C "$REPO_DIR" rev-parse --short=12 HEAD)"
agent_build_version="agent-$(source_fingerprint cmd/relay-agent internal/agent internal/relay internal/protocol go.mod go.sum Dockerfile.controller Dockerfile.agent)"
dispatcher_build_version="dispatcher-$(source_fingerprint cmd/dispatcher internal/dispatcher internal/protocol go.mod go.sum Dockerfile.controller Dockerfile.dispatcher)"

# Keep the legacy-Agent compatibility bridge installed on the host. New
# Agents use force_update directly; old Agents are upgraded by this isolated
# oneshot unit through the host's pre-approved SSH trust chain.
install -m 0755 "$REPO_DIR/deploy/alicdt-agent-upgrade.sh" /usr/local/sbin/alicdt-agent-upgrade
install -m 0644 "$REPO_DIR/deploy/alicdt-agent-upgrade.service" /etc/systemd/system/alicdt-agent-upgrade.service
install -m 0644 "$REPO_DIR/deploy/alicdt-agent-upgrade.path" /etc/systemd/system/alicdt-agent-upgrade.path
systemctl daemon-reload
systemctl enable --now alicdt-agent-upgrade.path

write_status "running" "正在构建 Go 控制器和 Agent 镜像" "$request_id" "$target_commit" "$started_at" ""
if docker image inspect alicdt-controller:production >/dev/null 2>&1; then
  docker tag alicdt-controller:production "$rollback_image"
fi
CDT_BUILD_VERSION="$controller_build_version" CDT_AGENT_BUILD_VERSION="$agent_build_version" CDT_DISPATCHER_BUILD_VERSION="$dispatcher_build_version" \
  docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" build --pull controller

write_status "running" "正在备份数据库并切换服务" "$request_id" "$target_commit" "$started_at" ""
docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" stop controller >/dev/null
controller_stopped=1
cutover_id="$(date -u +%Y%m%dT%H%M%SZ)"
backup_path="$BACKUP_DIR/guard-go-before-button-update-$cutover_id.db"
cp --preserve=mode,timestamps "$DATA_DIR/guard.db" "$backup_path"
chmod 600 "$backup_path"
for suffix in -wal -shm; do
  if [ -e "$DATA_DIR/guard.db$suffix" ]; then
    cp --preserve=mode,timestamps "$DATA_DIR/guard.db$suffix" "$backup_path$suffix"
    chmod 600 "$backup_path$suffix"
  fi
done
docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" up -d --force-recreate --no-build --wait --wait-timeout 120
set -a
# shellcheck disable=SC1090
. "$ENV_FILE"
set +a
CDT_SMOKE_BASE_URL="http://127.0.0.1:18000" CDT_SMOKE_ADMIN_TOKEN="${CDT_ADMIN_TOKEN:-}" "$REPO_DIR/scripts/smoke-controller.sh" >/dev/null
controller_stopped=0
finished_at="$(utc_now)"
write_status "success" "更新完成，当前版本已切换" "$request_id" "$target_commit" "$started_at" "$finished_at"

# Atomically refresh the host-side updater only after the new deployment has
# passed authenticated smoke checks. Replacing the inode keeps this currently
# running shell on its original, already validated script.
self_update=""
if self_update="$(mktemp /usr/local/sbin/.alicdt-manager-update.XXXXXX)" \
  && install -m 0755 "$REPO_DIR/deploy/alicdt-manager-update.sh" "$self_update" \
  && mv -f "$self_update" /usr/local/sbin/alicdt-manager-update; then
  install -m 0644 "$REPO_DIR/deploy/alicdt-manager-update.service" /etc/systemd/system/alicdt-manager-update.service || true
  install -m 0644 "$REPO_DIR/deploy/alicdt-manager-update.path" /etc/systemd/system/alicdt-manager-update.path || true
  systemctl daemon-reload || true
  systemctl enable --now alicdt-manager-update.path >/dev/null || true
elif [ -n "$self_update" ]; then
  rm -f "$self_update"
fi
