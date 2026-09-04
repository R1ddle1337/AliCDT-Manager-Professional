#!/usr/bin/env sh
set -eu

ATTEMPTS=${CDT_NPM_AUDIT_ATTEMPTS:-3}
TIMEOUT_SECONDS=${CDT_NPM_AUDIT_TIMEOUT_SECONDS:-45}
ALLOW_UNAVAILABLE=${CDT_NPM_AUDIT_ALLOW_UNAVAILABLE:-0}

case "$ATTEMPTS:$TIMEOUT_SECONDS" in
  *[!0-9:]*|:*|*:) echo "invalid npm audit retry configuration" >&2; exit 2 ;;
esac
if [ "$ATTEMPTS" -le 0 ] || [ "$TIMEOUT_SECONDS" -le 0 ]; then
  echo "npm audit retries and timeout must be greater than zero" >&2
  exit 2
fi
case "$ALLOW_UNAVAILABLE" in
  0|1) ;;
  *) echo "CDT_NPM_AUDIT_ALLOW_UNAVAILABLE must be 0 or 1" >&2; exit 2 ;;
esac

AUDIT_TMP_DIR=$(mktemp -d)
trap 'rm -rf "$AUDIT_TMP_DIR"' EXIT INT TERM

attempt=1
while [ "$attempt" -le "$ATTEMPTS" ]; do
  output="$AUDIT_TMP_DIR/output.json"
  errors="$AUDIT_TMP_DIR/errors.log"
  if timeout "${TIMEOUT_SECONDS}s" npm audit --audit-level=high --json --loglevel=silent >"$output" 2>"$errors"; then
    cat "$output"
    exit 0
  fi

  classification=$(python3 - "$output" <<'PY'
import json
import sys

try:
    with open(sys.argv[1], encoding="utf-8") as stream:
        payload = json.load(stream)
except Exception:
    print("unavailable")
    raise SystemExit

counts = payload.get("metadata", {}).get("vulnerabilities")
if not isinstance(counts, dict):
    print("unavailable")
elif int(counts.get("high", 0) or 0) + int(counts.get("critical", 0) or 0) > 0:
    print("vulnerable")
else:
    print("clean")
PY
  )
  case "$classification" in
    vulnerable)
      cat "$output" >&2
      exit 1
      ;;
    clean)
      cat "$output"
      exit 0
      ;;
  esac

  if [ "$attempt" -lt "$ATTEMPTS" ]; then
    sleep $((attempt * 5))
  fi
  attempt=$((attempt + 1))
done

if [ "$ALLOW_UNAVAILABLE" = "1" ]; then
  echo "::warning::npm advisory service remained unavailable after ${ATTEMPTS} attempts; dependency tests and build continue" >&2
  exit 0
fi

cat "$AUDIT_TMP_DIR/errors.log" >&2
cat "$AUDIT_TMP_DIR/output.json" >&2
echo "npm advisory service remained unavailable after ${ATTEMPTS} attempts" >&2
exit 1
