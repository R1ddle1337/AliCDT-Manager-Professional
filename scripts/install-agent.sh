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
    --controller) CONTROLLER="$2"; shift 2 ;;
    --enroll-token) TOKEN="$2"; shift 2 ;;
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
  echo "--controller and --enroll-token are required." >&2
  exit 2
fi

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
  if [ "$VERSION" = "latest" ]; then
    URL="https://github.com/R1ddle1337/AliCDT-Manager-Professional/releases/latest/download/cdt-relay-agent-linux-${ARCH}"
  else
    URL="https://github.com/R1ddle1337/AliCDT-Manager-Professional/releases/download/${VERSION}/cdt-relay-agent-linux-${ARCH}"
  fi
  TMP="$(mktemp)"
  trap 'rm -f "$TMP"' EXIT
  curl -fL --retry 3 "$URL" -o "$TMP"
  install -m 0755 "$TMP" /usr/local/bin/cdt-relay-agent
fi

cat > /etc/cdt-relay/agent.env <<EOF
CDT_CONTROLLER_URL=$CONTROLLER
CDT_ENROLL_TOKEN=$TOKEN
CDT_NODE_NAME=$NODE_NAME
CDT_PUBLIC_IP=$PUBLIC_IP
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

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now cdt-relay-agent
echo "AliCDT Relay Agent installed and started."
