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
  printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
}

cat > /etc/cdt-relay/agent.env <<EOF
CDT_CONTROLLER_URL="$(escape_env "$CONTROLLER")"
CDT_ENROLL_TOKEN="$(escape_env "$TOKEN")"
CDT_NODE_NAME="$(escape_env "$NODE_NAME")"
CDT_PUBLIC_IP="$(escape_env "$PUBLIC_IP")"
CDT_AGENT_DATA_DIR=/var/lib/cdt-relay
EOF
chmod 600 /etc/cdt-relay/agent.env

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
ReadWritePaths=/var/lib/cdt-relay
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now cdt-relay-agent
echo "AliCDT Relay Agent installed and started."
