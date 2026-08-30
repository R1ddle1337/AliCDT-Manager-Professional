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

install -d -m 700 /var/lib/cdt-relay /var/lib/cdt-relay/backups
if [ -f "$BINARY" ]; then install -m 0700 "$BINARY" "/var/lib/cdt-relay/backups/agent-$(date +%s).bin"; fi
install -m 0755 "${TMP_DIR}/${ASSET}" "${BINARY}.new"
mv -f "${BINARY}.new" "$BINARY"

UNIT="/etc/systemd/system/${SERVICE}.service"
if [ -f "$UNIT" ]; then
  if grep -q '^ReadWritePaths=' "$UNIT"; then
    sed -i 's#^ReadWritePaths=.*#ReadWritePaths=/var/lib/cdt-relay /usr/local/bin#' "$UNIT"
  else
    sed -i '/^ProtectSystem=/a ReadWritePaths=/var/lib/cdt-relay /usr/local/bin' "$UNIT"
  fi
  systemctl daemon-reload
  systemctl restart "$SERVICE"
fi
echo "AliCDT Relay Agent upgraded to checksum ${ACTUAL}."
