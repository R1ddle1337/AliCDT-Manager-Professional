#!/usr/bin/env bash
set -Eeuo pipefail

# Install the fixed-front-door dispatcher on an independent gateway. Secrets
# are written only to a mode-0600 environment file and are never echoed.
CONTROLLER_URL="${CDT_DISPATCH_CONTROLLER_URL:-}"
POOL_ID="${CDT_DISPATCH_POOL_ID:-}"
TOKEN="${CDT_DISPATCH_TOKEN:-}"
LISTEN="${CDT_DISPATCH_LISTEN:-:8443}"
HEALTH_LISTEN="${CDT_DISPATCH_HEALTH_LISTEN:-127.0.0.1:9091}"
VERSION="${CDT_DISPATCH_VERSION:-latest}"
REPOSITORY="${CDT_DISPATCH_RELEASE_REPO:-R1ddle1337/AliCDT-Manager-Professional}"
SOURCE="${CDT_DISPATCH_RELEASE_SOURCE:-controller}"
PREFIX="${CDT_DISPATCH_PREFIX:-/usr/local/bin}"
ENV_FILE="${CDT_DISPATCH_ENV_FILE:-/etc/cdt-dispatcher.env}"
NO_SERVICE=0

usage() {
  cat <<'USAGE'
Usage: install-dispatcher.sh --controller URL --pool-id ID --token TOKEN [options]
  --listen ADDR          native listen address (default :8443)
  --health-listen ADDR   local health address (default 127.0.0.1:9091)
  --version TAG          GitHub release tag (default latest)
  --source SOURCE        github or controller (default controller)
  --binary PATH          install a local binary instead of downloading
  --no-service            install binary/config only
USAGE
}

BINARY=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --controller) CONTROLLER_URL="${2:?missing URL}"; shift 2 ;;
    --pool-id) POOL_ID="${2:?missing pool ID}"; shift 2 ;;
    --token) TOKEN="${2:?missing token}"; shift 2 ;;
    --listen) LISTEN="${2:?missing listen address}"; shift 2 ;;
    --health-listen) HEALTH_LISTEN="${2:?missing health address}"; shift 2 ;;
    --version) VERSION="${2:?missing version}"; shift 2 ;;
    --source) SOURCE="${2:?missing source}"; shift 2 ;;
    --binary) BINARY="${2:?missing binary path}"; shift 2 ;;
    --no-service) NO_SERVICE=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done

[ -n "$CONTROLLER_URL" ] || { echo "controller URL is required" >&2; exit 1; }
[ -n "$POOL_ID" ] || { echo "pool ID is required" >&2; exit 1; }
[ -n "$TOKEN" ] || { echo "dispatch token is required" >&2; exit 1; }
case "$CONTROLLER_URL$POOL_ID$TOKEN$LISTEN$HEALTH_LISTEN${CDT_DISPATCH_NETWORK:-tcp+udp}${CDT_DISPATCH_POLL_INTERVAL:-15s}${CDT_DISPATCH_STALE_AFTER:-2m}${CDT_DISPATCH_REQUEST_TIMEOUT:-10s}${CDT_DISPATCH_MAX_SNAPSHOT_AGE:-2m}" in
  *$'\n'*|*$'\r'*) echo "configuration values must not contain newlines" >&2; exit 1 ;;
esac
if [[ ! "$REPOSITORY" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]]; then
  echo "invalid GitHub repository" >&2
  exit 1
fi
if [[ "$SOURCE" != github && "$SOURCE" != controller ]]; then
  echo "invalid release source" >&2
  exit 1
fi
if [[ "$VERSION" != latest && ! "$VERSION" =~ ^[A-Za-z0-9._-]+$ ]]; then
  echo "invalid release version" >&2
  exit 1
fi

arch="$(uname -m)"
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) echo "unsupported architecture: $arch" >&2; exit 1 ;;
esac

install -d -m 0755 "$PREFIX"
if [ -n "$BINARY" ]; then
  [ -f "$BINARY" ] || { echo "binary not found" >&2; exit 1; }
  install -m 0755 "$BINARY" "$PREFIX/cdt-dispatcher"
else
  tmpdir="$(mktemp -d)"
  trap 'rm -rf "$tmpdir"' EXIT
  if [ "$SOURCE" = controller ]; then
    base="${CONTROLLER_URL%/}/dispatcher"
  elif [ "$VERSION" = latest ]; then
    base="https://github.com/$REPOSITORY/releases/latest/download"
  else
    base="https://github.com/$REPOSITORY/releases/download/$VERSION"
  fi
  asset="cdt-dispatcher-linux-$arch"
  curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 "$base/$asset" -o "$tmpdir/$asset"
  curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 "$base/checksums.txt" -o "$tmpdir/checksums.txt"
  expected="$(awk -v file="$asset" '$2 == file {print tolower($1); exit}' "$tmpdir/checksums.txt")"
  [ -n "$expected" ] || { echo "checksum entry missing" >&2; exit 1; }
  actual="$(sha256sum "$tmpdir/$asset" | awk '{print $1}')"
  [ "$actual" = "$expected" ] || { echo "checksum verification failed" >&2; exit 1; }
  install -m 0755 "$tmpdir/$asset" "$PREFIX/cdt-dispatcher"
fi

install -d -m 0755 "$(dirname "$ENV_FILE")"
umask 077
temporary="$(mktemp "${ENV_FILE}.XXXXXX")"
cat >"$temporary" <<EOF
CDT_DISPATCH_CONTROLLER_URL=$CONTROLLER_URL
CDT_DISPATCH_POOL_ID=$POOL_ID
CDT_DISPATCH_TOKEN=$TOKEN
CDT_DISPATCH_RELEASE_SOURCE=$SOURCE
CDT_DISPATCH_LISTEN=$LISTEN
CDT_DISPATCH_NETWORK=${CDT_DISPATCH_NETWORK:-tcp+udp}
CDT_DISPATCH_HEALTH_LISTEN=$HEALTH_LISTEN
CDT_DISPATCH_POLL_INTERVAL=${CDT_DISPATCH_POLL_INTERVAL:-15s}
CDT_DISPATCH_STALE_AFTER=${CDT_DISPATCH_STALE_AFTER:-2m}
CDT_DISPATCH_REQUEST_TIMEOUT=${CDT_DISPATCH_REQUEST_TIMEOUT:-10s}
CDT_DISPATCH_MAX_SNAPSHOT_AGE=${CDT_DISPATCH_MAX_SNAPSHOT_AGE:-2m}
EOF
chmod 0600 "$temporary"
mv -f "$temporary" "$ENV_FILE"

if [ "$NO_SERVICE" -eq 1 ]; then
  exit 0
fi
service_dir=""
if service_dir_candidate="$(cd "$(dirname "${BASH_SOURCE[0]}")/../deploy" 2>/dev/null && pwd)"; then
  service_dir="$service_dir_candidate"
fi
if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
  id -u cdt-dispatcher >/dev/null 2>&1 || useradd --system --home-dir /nonexistent --shell /usr/sbin/nologin cdt-dispatcher
  if [ -n "$service_dir" ] && [ -f "$service_dir/cdt-dispatcher.service" ]; then
    install -m 0644 "$service_dir/cdt-dispatcher.service" /etc/systemd/system/cdt-dispatcher.service
  else
    install -d -m 0755 /etc/systemd/system
    cat >/etc/systemd/system/cdt-dispatcher.service <<'UNIT'
[Unit]
Description=AliCDT fixed front-door L4 dispatcher
Wants=network-online.target
After=network-online.target
[Service]
Type=simple
User=cdt-dispatcher
Group=cdt-dispatcher
EnvironmentFile=/etc/cdt-dispatcher.env
ExecStart=/usr/local/bin/cdt-dispatcher
Restart=on-failure
RestartSec=3s
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
LimitNOFILE=262144
[Install]
WantedBy=multi-user.target
UNIT
  fi
  systemctl daemon-reload
  systemctl enable --now cdt-dispatcher.service
elif command -v rc-service >/dev/null 2>&1; then
  id cdt-dispatcher >/dev/null 2>&1 || adduser -S -D -H cdt-dispatcher
  if [ -n "$service_dir" ] && [ -f "$service_dir/cdt-dispatcher.openrc" ]; then
    install -m 0755 "$service_dir/cdt-dispatcher.openrc" /etc/init.d/cdt-dispatcher
  else
    install -d -m 0755 /etc/init.d
    cat >/etc/init.d/cdt-dispatcher <<'RC'
#!/sbin/openrc-run
[ -r /etc/cdt-dispatcher.env ] && . /etc/cdt-dispatcher.env
name="cdt-dispatcher"
command="/usr/local/bin/cdt-dispatcher"
command_user="cdt-dispatcher:cdt-dispatcher"
command_background="yes"
pidfile="/run/${RC_SVCNAME}.pid"
supervisor="supervise-daemon"
supervise_daemon_args="--respawn --respawn-delay 3"
output_log="/var/log/${RC_SVCNAME}/${RC_SVCNAME}.log"
error_log="/var/log/${RC_SVCNAME}/${RC_SVCNAME}.err"
depend() { need net; }
start_pre() { checkpath --directory --mode 0750 --owner cdt-dispatcher:cdt-dispatcher "/var/log/${RC_SVCNAME}"; checkpath --file --mode 0600 --owner root:root /etc/cdt-dispatcher.env; }
RC
    chmod 0755 /etc/init.d/cdt-dispatcher
  fi
  rc-update add cdt-dispatcher default
  rc-service cdt-dispatcher restart
else
  echo "binary installed; no systemd/OpenRC service manager detected" >&2
fi
