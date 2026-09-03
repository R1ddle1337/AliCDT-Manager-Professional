#!/usr/bin/env bash
set -Eeuo pipefail

# Host-side compatibility bridge for Agents that predate force_update. The
# controller only writes a marker; this process is the only component allowed
# to use the host's SSH trust chain. It upgrades one legacy Agent at a time and
# waits for the resulting heartbeat/capability report before moving on.
DATA_DIR="${CDT_AGENT_UPGRADE_DATA_DIR:-/app/alicdt-manager/data}"
DB_FILE="${CDT_AGENT_UPGRADE_DB:-$DATA_DIR/guard.db}"
REQUEST_FILE="${CDT_AGENT_UPGRADE_REQUEST_FILE:-$DATA_DIR/agent-upgrade.request}"
CONTROLLER_URL="${CDT_AGENT_UPGRADE_CONTROLLER_URL:-}"
LOCK_FILE="${CDT_AGENT_UPGRADE_LOCK_FILE:-/run/lock/alicdt-agent-upgrade.lock}"
SSH_USER="${CDT_AGENT_UPGRADE_SSH_USER:-root}"
WAIT_SECONDS="${CDT_AGENT_UPGRADE_WAIT_SECONDS:-120}"

if [ -z "$CONTROLLER_URL" ] && [ -f /root/.config/alicdt-manager/production.env ]; then
  # shellcheck disable=SC1091
  set -a; . /root/.config/alicdt-manager/production.env; set +a
  CONTROLLER_URL="${CDT_AGENT_UPGRADE_CONTROLLER_URL:-}"
fi

if [ -z "$CONTROLLER_URL" ]; then
  echo "CDT_AGENT_UPGRADE_CONTROLLER_URL is required" >&2
  exit 1
fi
case "$CONTROLLER_URL" in
  http://*|https://*) ;;
  *) echo "CDT_AGENT_UPGRADE_CONTROLLER_URL must use http or https" >&2; exit 1 ;;
esac
if ! printf '%s' "$CONTROLLER_URL" | grep -Eq '^https?://[A-Za-z0-9.-]+(:[0-9]+)?$'; then
  echo "CDT_AGENT_UPGRADE_CONTROLLER_URL contains unsupported characters" >&2
  exit 1
fi

mkdir -p "$(dirname "$LOCK_FILE")"
exec 9>"$LOCK_FILE"
if ! flock -n 9; then
  exit 0
fi

if [ ! -f "$REQUEST_FILE" ] || [ ! -f "$DB_FILE" ]; then
  exit 0
fi

marker_copy="${REQUEST_FILE}.processing.$$"
if ! mv "$REQUEST_FILE" "$marker_copy" 2>/dev/null; then
  exit 0
fi
trap 'rm -f "$marker_copy"' EXIT

sqlite_query() {
  sqlite3 -cmd '.timeout 5000' -noheader -separator $'\t' "$DB_FILE" "$1"
}

sql_escape() {
  printf '%s' "$1" | sed "s/'/''/g"
}

mark_state() {
  local id="$1" status="$2" error_message="${3:-}"
  local safe_id safe_error
  safe_id="$(sql_escape "$id")"
  safe_error="$(sql_escape "$error_message")"
  sqlite_query "UPDATE relay_nodes SET update_status='$(sql_escape "$status")',update_error='$safe_error',update_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id='$safe_id';" >/dev/null
}

failure_message() {
  local output="$1"
  output="$(printf '%s' "$output" | tr '\n' ' ' | tr -s ' ' | tail -c 420)"
  printf '%s' "宿主机兼容升级失败${output:+：$output}"
}

expected_hash="$(curl -fsSL --retry 3 --connect-timeout 8 "${CONTROLLER_URL%/}/agent/checksums.txt" | awk '$2 == "cdt-relay-agent-linux-amd64" { print tolower($1); exit }')" || expected_hash=""
if [ -z "$expected_hash" ] || ! printf '%s' "$expected_hash" | grep -Eq '^[0-9a-f]{64}$'; then
  while IFS=$'\t' read -r agent_id _; do
    [ -n "$agent_id" ] && mark_state "$agent_id" failed "无法读取控制器 Agent 校验和"
  done < <(sqlite_query "SELECT id,name FROM relay_nodes WHERE update_status='requested' AND (COALESCE(capabilities_json,'[]') NOT LIKE '%shared_meters_v1%' OR COALESCE(capabilities_json,'[]') NOT LIKE '%quota_leases_v1%');")
  exit 1
fi

while IFS=$'\t' read -r agent_id agent_name public_ip; do
  [ -n "$agent_id" ] || continue
  if [ -z "$public_ip" ] || ! printf '%s' "$public_ip" | grep -Eq '^[A-Za-z0-9][A-Za-z0-9.:-]*$'; then
    mark_state "$agent_id" failed "Relay 节点没有可用的公网地址"
    continue
  fi

  mark_state "$agent_id" updating ""
  output_file="$(mktemp)"
  remote_command="curl -fsSL --retry 3 --connect-timeout 8 '${CONTROLLER_URL%/}/agent/upgrade.sh' | sh -s -- --server '${CONTROLLER_URL%/}'"
  if ! ssh -o BatchMode=yes -o ConnectTimeout=8 -o StrictHostKeyChecking=yes "${SSH_USER}@${public_ip}" "$remote_command" >"$output_file" 2>&1; then
    mark_state "$agent_id" failed "$(failure_message "$(cat "$output_file")")"
    rm -f "$output_file"
    continue
  fi
  rm -f "$output_file"

  deadline=$((SECONDS + WAIT_SECONDS))
  confirmed=0
  while [ "$SECONDS" -lt "$deadline" ]; do
    row="$(sqlite_query "SELECT status,update_status,COALESCE(binary_sha256,''),COALESCE(capabilities_json,'[]') FROM relay_nodes WHERE id='$(sql_escape "$agent_id")';" || true)"
    IFS=$'\t' read -r node_status node_update node_hash node_capabilities <<EOF
$row
EOF
    if [ "$node_status" = "online" ] && [ "$node_update" = "idle" ] && [ "${node_hash,,}" = "$expected_hash" ] && [[ "$node_capabilities" == *shared_meters_v1* ]] && [[ "$node_capabilities" == *quota_leases_v1* ]]; then
      confirmed=1
      break
    fi
    sleep 5
  done
  if [ "$confirmed" -ne 1 ]; then
    mark_state "$agent_id" failed "Agent 已执行升级但在 ${WAIT_SECONDS} 秒内未回报新版本（${agent_name}）"
  fi
done < <(sqlite_query "SELECT id,name,COALESCE(public_ip,'') FROM relay_nodes WHERE update_status='requested' AND (COALESCE(capabilities_json,'[]') NOT LIKE '%shared_meters_v1%' OR COALESCE(capabilities_json,'[]') NOT LIKE '%quota_leases_v1%') ORDER BY created_at;")
