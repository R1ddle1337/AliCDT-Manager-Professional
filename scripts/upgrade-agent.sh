#!/usr/bin/env sh
set -eu

CONTROLLER=""
SERVICE="cdt-relay-agent"
BINARY="/usr/local/bin/cdt-relay-agent"
while [ "$#" -gt 0 ]; do
  case "$1" in
    --server|--controller) CONTROLLER="$2"; shift 2 ;;
    --service) SERVICE="$2"; shift 2 ;;
    --binary) BINARY="$2"; shift 2 ;;
    *) echo "Unknown argument: $1" >&2; exit 2 ;;
  esac
done
if [ "$(id -u)" -ne 0 ]; then echo "This upgrade must run as root." >&2; exit 1; fi
if [ -z "$CONTROLLER" ]; then echo "--server is required." >&2; exit 2; fi
case "$CONTROLLER$SERVICE$BINARY" in *"\n"*|*"\r"*) echo "Arguments must not contain line breaks." >&2; exit 2 ;; esac
case "$(uname -m)" in x86_64|amd64) ARCH=amd64 ;; aarch64|arm64) ARCH=arm64 ;; *) echo "Unsupported architecture." >&2; exit 1 ;; esac

TMP_DIR="$(mktemp -d)"; trap 'rm -rf "$TMP_DIR"' EXIT
BASE="${CONTROLLER%/}/agent"
ASSET="cdt-relay-agent-linux-${ARCH}"
curl -fsSL --retry 3 "${BASE}/${ASSET}" -o "${TMP_DIR}/${ASSET}"
curl -fsSL --retry 3 "${BASE}/checksums.txt" -o "${TMP_DIR}/checksums.txt"
EXPECTED="$(awk -v asset="$ASSET" '$2 == asset { print $1 }' "${TMP_DIR}/checksums.txt")"
if [ -z "$EXPECTED" ]; then echo "Checksum for ${ASSET} was not found." >&2; exit 1; fi
if command -v sha256sum >/dev/null 2>&1; then ACTUAL="$(sha256sum "${TMP_DIR}/${ASSET}" | awk '{print $1}')"; else ACTUAL="$(shasum -a 256 "${TMP_DIR}/${ASSET}" | awk '{print $1}')"; fi
if [ "$ACTUAL" != "$EXPECTED" ]; then echo "Agent checksum verification failed." >&2; exit 1; fi

# Migrate older installations from the 10-minute polling default to the
# daily Beijing schedule. Existing explicit interval overrides are retained
# only when the administrator has removed the legacy line themselves.
ENV_FILE="/etc/cdt-relay/agent.env"
if [ -f "$ENV_FILE" ]; then
  sed -i '/^CDT_AGENT_UPDATE_INTERVAL=/d' "$ENV_FILE"
  if ! grep -q '^CDT_AGENT_UPDATE_TIME=' "$ENV_FILE"; then echo 'CDT_AGENT_UPDATE_TIME=04:00' >> "$ENV_FILE"; fi
  if ! grep -q '^CDT_AGENT_UPDATE_LOCATION=' "$ENV_FILE"; then echo 'CDT_AGENT_UPDATE_LOCATION=Asia/Shanghai' >> "$ENV_FILE"; fi
fi

install -d -m 700 /var/lib/cdt-relay /var/lib/cdt-relay/backups
if [ -f "$BINARY" ]; then install -m 0700 "$BINARY" "/var/lib/cdt-relay/backups/agent-$(date +%s).bin"; fi
install -m 0755 "${TMP_DIR}/${ASSET}" "${BINARY}.new"
mv -f "${BINARY}.new" "$BINARY"

SYSTEMD_UNIT="/etc/systemd/system/${SERVICE}.service"
OPENRC_UNIT="/etc/init.d/${SERVICE}"
if [ -f "$SYSTEMD_UNIT" ] && command -v systemctl >/dev/null 2>&1; then
  if grep -q '^ReadWritePaths=' "$SYSTEMD_UNIT"; then
    sed -i 's#^ReadWritePaths=.*#ReadWritePaths=/var/lib/cdt-relay /usr/local/bin#' "$SYSTEMD_UNIT"
  else
    sed -i '/^ProtectSystem=/a ReadWritePaths=/var/lib/cdt-relay /usr/local/bin' "$SYSTEMD_UNIT"
  fi
  systemctl daemon-reload
  systemctl restart "$SERVICE"
elif [ -f "$OPENRC_UNIT" ] && command -v rc-service >/dev/null 2>&1; then
  rc-service "$SERVICE" restart
else
  echo "Agent binary upgraded, but no systemd/OpenRC service was found; restart ${SERVICE} manually." >&2
fi
echo "AliCDT Relay Agent upgraded to checksum ${ACTUAL}."
