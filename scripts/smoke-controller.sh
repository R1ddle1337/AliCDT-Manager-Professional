#!/bin/sh
set -eu

BASE_URL="${CDT_SMOKE_BASE_URL:-http://127.0.0.1:18000}"
ADMIN_TOKEN="${CDT_SMOKE_ADMIN_TOKEN:-}"

case "$BASE_URL" in
  http://*|https://*) ;;
  *) echo "smoke test base URL must use http or https" >&2; exit 2 ;;
esac

SMOKE_TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$SMOKE_TMP_DIR"' EXIT INT TERM

request_json() {
  endpoint="$1"
  expected_type="$2"
  output_file="$SMOKE_TMP_DIR/response.json"
  shift 2
  curl -fsS --connect-timeout 5 --max-time 20 "$@" -o "$output_file" "${BASE_URL%/}${endpoint}"
  python3 - "$output_file" "$expected_type" "$endpoint" <<'PY'
import json
import sys

path, expected, endpoint = sys.argv[1:]
with open(path, encoding="utf-8") as stream:
    payload = json.load(stream)
if expected == "object" and not isinstance(payload, dict):
    raise SystemExit(f"{endpoint}: expected JSON object")
if expected == "array" and not isinstance(payload, list):
    raise SystemExit(f"{endpoint}: expected JSON array")
if endpoint == "/healthz" and payload.get("status") != "ok":
    raise SystemExit(f"{endpoint}: controller is not healthy")
if endpoint == "/api/v2/auth/initialized" and not isinstance(payload.get("initialized"), bool):
    raise SystemExit(f"{endpoint}: missing initialized state")
if endpoint == "/api/v2/auth/me" and payload.get("role") != "admin":
    raise SystemExit(f"{endpoint}: token did not authenticate as administrator")
if endpoint == "/api/v2/cloud/overview":
    for key in ("accounts", "instances", "traffic"):
        if not isinstance(payload.get(key), list):
            raise SystemExit(f"{endpoint}: missing {key} list")
PY
}

request_json "/healthz" object
request_json "/api/v2/auth/initialized" object

if [ -z "$ADMIN_TOKEN" ]; then
  echo "controller public smoke checks passed"
  exit 0
fi

AUTH_HEADER="Authorization: Bearer $ADMIN_TOKEN"
request_json "/api/v2/auth/me" object -H "$AUTH_HEADER"
request_json "/api/v2/relay-nodes" array -H "$AUTH_HEADER"
request_json "/api/v2/landing-nodes" array -H "$AUTH_HEADER"
request_json "/api/v2/relay-services" array -H "$AUTH_HEADER"
request_json "/api/v2/relay-pools" array -H "$AUTH_HEADER"
request_json "/api/v2/dns/providers" array -H "$AUTH_HEADER"
request_json "/api/v2/dns/records" array -H "$AUTH_HEADER"
request_json "/api/v2/cloud/overview" object -H "$AUTH_HEADER"
request_json "/api/v2/users" array -H "$AUTH_HEADER"
request_json "/api/v2/events?limit=1" array -H "$AUTH_HEADER"
request_json "/api/v2/security/2fa" object -H "$AUTH_HEADER"

echo "controller authenticated smoke checks passed"
