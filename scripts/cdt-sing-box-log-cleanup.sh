#!/usr/bin/env sh
# Keep the sing-box access log from consuming the small root disks used by
# CDT relay hosts.  The default policy is intentionally conservative: check
# once per minute and truncate the log in place after it exceeds 50 MiB.
# Truncating (rather than removing) keeps sing-box's open file descriptor
# valid and does not touch its configuration or service.
set -eu

TARGET=${CDT_LOG_CLEANUP_TARGET:-/usr/local/sbin/cdt-sing-box-log-cleanup}
CONFIG=${CDT_LOG_CLEANUP_CONFIG:-/etc/cdt-relay/sing-box-log-cleanup.env}
DEFAULT_LOG=${CDT_SINGBOX_ACCESS_LOG_DEFAULT:-/var/log/sing-box/access.log}
DEFAULT_MAX_MB=50
CRON_MARKER=cdt-sing-box-log-cleanup

load_config() {
  if [ -r "$CONFIG" ]; then
    # The file is installed root-only by this script.  It is deliberately a
    # shell-compatible file so operators can change the path/limit without
    # replacing the timer.
    . "$CONFIG"
  fi
  LOG_PATH=${CDT_SINGBOX_ACCESS_LOG:-$DEFAULT_LOG}
  MAX_MB=${CDT_SINGBOX_ACCESS_LOG_MAX_MB:-$DEFAULT_MAX_MB}
}

check_log() {
  load_config

  # Refuse relative paths and malformed limits.  A bad local override should
  # never make a scheduled root job truncate an unexpected file.
  case "$LOG_PATH" in
    /*) ;;
    *) return 0 ;;
  esac
  case "$MAX_MB" in
    ''|*[!0-9]*) return 0 ;;
  esac
  # Keep the arithmetic bounded for BusyBox ash and prevent an accidental
  # typo from selecting a multi-terabyte threshold.
  normalized_mb=$(printf '%s' "$MAX_MB" | sed 's/^0*//')
  [ -n "$normalized_mb" ] || normalized_mb=0
  [ "$normalized_mb" -gt 0 ] 2>/dev/null || return 0
  [ "$normalized_mb" -le 4096 ] 2>/dev/null || return 0
  [ -f "$LOG_PATH" ] || return 0

  size=$(wc -c < "$LOG_PATH" 2>/dev/null | tr -d '[:space:]')
  case "$size" in
    ''|*[!0-9]*) return 0 ;;
  esac
  max_bytes=$((normalized_mb * 1048576))
  if [ "$size" -gt "$max_bytes" ]; then
    # Keep the inode in place: sing-box can continue writing immediately and
    # no restart is needed.  Errors are intentionally ignored so a transient
    # rotation/race cannot make cron report a failing job.
    : > "$LOG_PATH" 2>/dev/null || true
  fi
}

write_default_config() {
  install -d -m 0755 "$(dirname "$CONFIG")"
  if [ ! -f "$CONFIG" ]; then
    umask 077
    cat > "$CONFIG" <<'EOF'
# Path written by sing-box.  Change this if the node uses a custom log path.
CDT_SINGBOX_ACCESS_LOG=/var/log/sing-box/access.log
# Truncate in place once the file exceeds this many MiB.
CDT_SINGBOX_ACCESS_LOG_MAX_MB=50
EOF
  else
    # Preserve operator overrides while making upgrades add newly introduced
    # settings to older installations.
    if ! grep -q '^CDT_SINGBOX_ACCESS_LOG=' "$CONFIG" 2>/dev/null; then
      printf '%s\n' 'CDT_SINGBOX_ACCESS_LOG=/var/log/sing-box/access.log' >> "$CONFIG"
    fi
    if ! grep -q '^CDT_SINGBOX_ACCESS_LOG_MAX_MB=' "$CONFIG" 2>/dev/null; then
      printf '%s\n' 'CDT_SINGBOX_ACCESS_LOG_MAX_MB=50' >> "$CONFIG"
    fi
  fi
  chmod 0600 "$CONFIG"
}

install_systemd_scheduler() {
  install -d -m 0755 /etc/systemd/system
  cat > /etc/systemd/system/cdt-sing-box-log-cleanup.service <<'EOF'
[Unit]
Description=Limit AliCDT sing-box access log

[Service]
Type=oneshot
ExecStart=/usr/local/sbin/cdt-sing-box-log-cleanup --check
NoNewPrivileges=true
PrivateTmp=true
ProtectHome=true
EOF
  cat > /etc/systemd/system/cdt-sing-box-log-cleanup.timer <<'EOF'
[Unit]
Description=Check AliCDT sing-box access log size every minute

[Timer]
OnBootSec=1min
OnUnitActiveSec=1min
AccuracySec=10s
Persistent=true

[Install]
WantedBy=timers.target
EOF
  systemctl daemon-reload
  systemctl enable --now cdt-sing-box-log-cleanup.timer
}

install_openrc_scheduler() {
  # Alpine's BusyBox crond reads this file and notices its mtime without a
  # reboot.  Keep the marker unique so repeated Agent upgrades are idempotent.
  install -d -m 0755 /etc/crontabs
  CRON_FILE=/etc/crontabs/root
  if [ ! -f "$CRON_FILE" ]; then
    umask 077
    : > "$CRON_FILE"
  fi
  chmod 0600 "$CRON_FILE"
  if ! grep -Fq "$CRON_MARKER" "$CRON_FILE" 2>/dev/null; then
    printf '%s\n' "* * * * * $TARGET --check >/dev/null 2>&1 # $CRON_MARKER" >> "$CRON_FILE"
  fi

  if command -v rc-update >/dev/null 2>&1; then
    rc-update add crond default >/dev/null 2>&1 || true
  fi
  if command -v rc-service >/dev/null 2>&1; then
    if rc-service crond status >/dev/null 2>&1; then
      rc-service crond reload >/dev/null 2>&1 || rc-service crond restart >/dev/null 2>&1 || true
    else
      rc-service crond start >/dev/null 2>&1 || true
    fi
  elif command -v crond >/dev/null 2>&1; then
    # This branch is useful on a minimal Alpine image without OpenRC's service
    # wrapper.  Do not fail Agent installation if a supervisor owns crond.
    crond >/dev/null 2>&1 || true
  fi
}

install_scheduler() {
  if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
    install_systemd_scheduler
  elif command -v crond >/dev/null 2>&1 || command -v rc-service >/dev/null 2>&1; then
    install_openrc_scheduler
  else
    echo "warning: no systemd or crond scheduler found; log cleanup is not active" >&2
  fi
}

install_mode() {
  install -d -m 0755 "$(dirname "$TARGET")"
  # When downloaded to a temporary path, install the exact verified script;
  # when invoked from the installed path, avoid copying onto itself.  A
  # script piped directly to `sh` has no source file to copy; fail safely
  # instead of accidentally installing the shell binary as the checker.
  if [ -f "$0" ] && [ "$0" != "$TARGET" ]; then
    install -m 0755 "$0" "$TARGET"
  elif [ -x "$TARGET" ]; then
    chmod 0755 "$TARGET"
  else
    echo "cannot install cleanup checker when script is read from stdin; save it to a file first" >&2
    return 1
  fi
  write_default_config
  install_scheduler
  "$TARGET" --check || true
  echo "AliCDT sing-box access-log cleanup scheduled (limit ${DEFAULT_MAX_MB} MiB by default)."
}

case "${1:-}" in
  --install) install_mode ;;
  --check|'') check_log ;;
  *)
    echo "usage: $0 [--install|--check]" >&2
    exit 2
    ;;
esac
