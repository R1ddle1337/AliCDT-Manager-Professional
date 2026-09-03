#!/usr/bin/env sh
set -eu

CONTROLLER=""
TOKEN=""
NODE_NAME="$(hostname)"
PUBLIC_IP=""
VERSION="latest"
INSTALL_BINARY=""

while [ "$#" -gt 0 ]; do
  case "$1" in
    --controller|--server) CONTROLLER="$2"; shift 2 ;;
    --enroll-token|--token) TOKEN="$2"; shift 2 ;;
    --node-name) NODE_NAME="$2"; shift 2 ;;
    --public-ip) PUBLIC_IP="$2"; shift 2 ;;
    --version) VERSION="$2"; shift 2 ;;
    --binary) INSTALL_BINARY="$2"; shift 2 ;;
    *) echo "Unknown argument: $1" >&2; exit 2 ;;
  esac
done

if [ "$(id -u)" -ne 0 ]; then
  echo "This installer must run as root." >&2
  exit 1
fi
if [ -z "$CONTROLLER" ] || [ -z "$TOKEN" ]; then
  echo "--server and --token are required." >&2
  exit 2
fi

case "$CONTROLLER$TOKEN$NODE_NAME$PUBLIC_IP" in
  *"
"*|*""*) echo "Arguments must not contain line breaks." >&2; exit 2 ;;
esac

case "$(uname -m)" in
  x86_64|amd64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

# The Agent can run under either systemd (most Debian/Ubuntu ECS hosts) or
# OpenRC (Alpine Linux). Fail before downloading anything when no supported
# service manager is available so a container or minimal rescue shell does not
# look like a successful installation.
SERVICE_MANAGER=""
if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
  SERVICE_MANAGER="systemd"
elif command -v rc-service >/dev/null 2>&1 && command -v rc-update >/dev/null 2>&1; then
  SERVICE_MANAGER="openrc"
else
  echo "Unsupported init system: systemd or OpenRC is required." >&2
  echo "For Alpine, install and boot OpenRC; for containers, run the Agent under a container supervisor." >&2
  exit 1
fi

mkdir -p /etc/cdt-relay /var/lib/cdt-relay
chmod 700 /etc/cdt-relay /var/lib/cdt-relay

if [ -n "$INSTALL_BINARY" ]; then
  install -m 0755 "$INSTALL_BINARY" /usr/local/bin/cdt-relay-agent
else
  ASSET="cdt-relay-agent-linux-${ARCH}"
  TMP_DIR="$(mktemp -d)"
  trap 'rm -rf "$TMP_DIR"' EXIT
  PANEL_BASE="${CONTROLLER%/}/agent"
  if ! curl -fsSL --retry 3 "${PANEL_BASE}/${ASSET}" -o "${TMP_DIR}/${ASSET}" || \
     ! curl -fsSL --retry 3 "${PANEL_BASE}/checksums.txt" -o "${TMP_DIR}/checksums.txt"; then
    rm -f "${TMP_DIR}/${ASSET}" "${TMP_DIR}/checksums.txt"
    if [ "$VERSION" = "latest" ]; then
      RELEASE_BASE="https://github.com/R1ddle1337/AliCDT-Manager-Professional/releases/latest/download"
    else
      RELEASE_BASE="https://github.com/R1ddle1337/AliCDT-Manager-Professional/releases/download/${VERSION}"
    fi
    curl -fsSL --retry 3 "${RELEASE_BASE}/${ASSET}" -o "${TMP_DIR}/${ASSET}"
    curl -fsSL --retry 3 "${RELEASE_BASE}/checksums.txt" -o "${TMP_DIR}/checksums.txt"
  fi
  EXPECTED="$(awk -v asset="$ASSET" '$2 == asset { print $1 }' "${TMP_DIR}/checksums.txt")"
  if [ -z "$EXPECTED" ]; then
    echo "Checksum for ${ASSET} was not found." >&2
    exit 1
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    ACTUAL="$(sha256sum "${TMP_DIR}/${ASSET}" | awk '{ print $1 }')"
  elif command -v shasum >/dev/null 2>&1; then
    ACTUAL="$(shasum -a 256 "${TMP_DIR}/${ASSET}" | awk '{ print $1 }')"
  else
    echo "sha256sum or shasum is required to verify the Agent download." >&2
    exit 1
  fi
  if [ "$ACTUAL" != "$EXPECTED" ]; then
    echo "Agent checksum verification failed." >&2
    exit 1
  fi
  install -m 0755 "${TMP_DIR}/${ASSET}" /usr/local/bin/cdt-relay-agent
fi

escape_env() {
  # Keep the file valid for both systemd EnvironmentFile and an OpenRC
  # runscript sourcing it. Double-quote escaping covers shell expansion and
  # systemd's EnvironmentFile parser ($, backticks, quotes and backslashes).
  # shellcheck disable=SC2016 # The dollar sign is intentionally literal in sed.
  printf '%s' "$1" | sed 's/\\/\\\\/g; s/\$/\\$/g; s/"/\\"/g; s/`/\\`/g'
}

cat > /etc/cdt-relay/agent.env <<EOF
CDT_CONTROLLER_URL="$(escape_env "$CONTROLLER")"
CDT_ENROLL_TOKEN="$(escape_env "$TOKEN")"
CDT_NODE_NAME="$(escape_env "$NODE_NAME")"
CDT_PUBLIC_IP="$(escape_env "$PUBLIC_IP")"
CDT_AGENT_DATA_DIR=/var/lib/cdt-relay
CDT_AGENT_AUTO_UPDATE=true
CDT_AGENT_AUTO_FIREWALL=true
CDT_AGENT_UPDATE_TIME=04:00
CDT_AGENT_UPDATE_LOCATION=Asia/Shanghai
EOF
chmod 600 /etc/cdt-relay/agent.env

# Keep the small root disks used by Alpine relay hosts safe.  This is a
# separate, dependency-free checker so sing-box can continue writing to the
# same inode after the access log is truncated.  Failure to fetch this
# optional maintenance asset must not prevent the Agent itself from starting.
CLEANUP_TMP="$(mktemp -d)"
if curl -fsSL --retry 3 "${CONTROLLER%/}/agent/cdt-sing-box-log-cleanup.sh" -o "$CLEANUP_TMP/cdt-sing-box-log-cleanup.sh"; then
  if ! sh "$CLEANUP_TMP/cdt-sing-box-log-cleanup.sh" --install; then
    echo "warning: sing-box access-log cleanup could not be installed" >&2
  fi
else
  echo "warning: controller does not provide sing-box access-log cleanup; install it after the controller is updated" >&2
fi
rm -rf "$CLEANUP_TMP"

if [ "$SERVICE_MANAGER" = "systemd" ]; then
  install -d -m 0755 /etc/systemd/system
  cat > /etc/systemd/system/cdt-relay-agent.service <<'EOF'
[Unit]
Description=AliCDT Relay Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=/etc/cdt-relay/agent.env
ExecStart=/usr/local/bin/cdt-relay-agent
Restart=always
RestartSec=3
LimitNOFILE=1048576
NoNewPrivileges=true
ProtectHome=true
PrivateTmp=true
ProtectSystem=strict
ReadWritePaths=/var/lib/cdt-relay /usr/local/bin /etc/ufw
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true

[Install]
WantedBy=multi-user.target
EOF

  systemctl daemon-reload
  systemctl enable --now cdt-relay-agent
  echo "AliCDT Relay Agent installed and started with systemd."
else
  # supervise-daemon keeps the foreground Agent alive and restarts it after a
  # checksum-verified self-update (the Agent exits intentionally after swap).
  install -d -m 0755 /etc/init.d
  cat > /etc/init.d/cdt-relay-agent <<'EOF'
#!/sbin/openrc-run

name="AliCDT Relay Agent"
description="AliCDT transparent relay Agent"
command="/usr/local/bin/cdt-relay-agent"
supervisor="supervise-daemon"
pidfile="/run/${RC_SVCNAME}.pid"
output_log="/var/log/${RC_SVCNAME}.log"
error_log="/var/log/${RC_SVCNAME}.err"
respawn_delay=3
respawn_max=0

depend() {
  need net
}

start_pre() {
  if [ ! -r /etc/cdt-relay/agent.env ]; then
    eerror "missing /etc/cdt-relay/agent.env"
    return 1
  fi
  . /etc/cdt-relay/agent.env
  export CDT_CONTROLLER_URL CDT_ENROLL_TOKEN CDT_NODE_NAME CDT_PUBLIC_IP
  export CDT_AGENT_DATA_DIR CDT_AGENT_AUTO_UPDATE CDT_AGENT_AUTO_FIREWALL CDT_AGENT_UPDATE_TIME
  export CDT_AGENT_UPDATE_LOCATION CDT_AGENT_UPDATE_INTERVAL
}
EOF
  chmod 0755 /etc/init.d/cdt-relay-agent
  rc-update add cdt-relay-agent default
  rc-service cdt-relay-agent start
  echo "AliCDT Relay Agent installed and started with OpenRC."
fi
