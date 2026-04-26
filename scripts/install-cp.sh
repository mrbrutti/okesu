#!/usr/bin/env bash
#
# install-cp.sh — non-Docker production install of the Okesu Control Plane.
#
# Idempotent. Safe to re-run after pulling a new binary; preserves the DB,
# CA, server certs, and any /etc/default/okesu-cp overrides.
#
# Usage (as root):
#   ./scripts/install-cp.sh ./okesu-cp [--admin-password PASSWORD] [--cert FILE --key FILE]
#
# Without --cert/--key the CP generates a self-signed cert on first boot
# (good for evaluation, not production — front it with a real certificate
# from your CA or proxy it behind an existing TLS terminator).
#
# After this script returns:
#   systemctl status okesu-cp     # daemon health
#   journalctl -u okesu-cp -f     # follow logs
#   $EDITOR /etc/default/okesu-cp # override env (admin password, OIDC, …)

set -euo pipefail

USER_NAME="okesu-cp"
GROUP_NAME="okesu-cp"
INSTALL_BIN="/usr/local/bin/okesu-cp"
DATA_DIR="/var/lib/okesu-cp"
LOG_DIR="/var/log/okesu-cp"
CONFIG_FILE="/etc/default/okesu-cp"
SYSTEMD_UNIT="/etc/systemd/system/okesu-cp.service"
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SOURCE_UNIT="$REPO_ROOT/systemd/okesu-cp.service"

# ── Args ─────────────────────────────────────────────────────────────────────
BINARY=""
ADMIN_PASSWORD=""
CERT_FILE=""
KEY_FILE=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --admin-password) ADMIN_PASSWORD="$2"; shift 2 ;;
    --cert)           CERT_FILE="$2"; shift 2 ;;
    --key)            KEY_FILE="$2"; shift 2 ;;
    -h|--help)
      grep '^#' "$0" | sed 's/^# \{0,1\}//' | head -40
      exit 0 ;;
    -*)
      echo "unknown flag: $1" >&2; exit 2 ;;
    *)
      [[ -z "$BINARY" ]] || { echo "unexpected arg: $1" >&2; exit 2; }
      BINARY="$1"; shift ;;
  esac
done

if [[ -z "$BINARY" ]]; then
  echo "usage: $0 <path-to-okesu-cp-binary> [--admin-password PW] [--cert FILE --key FILE]" >&2
  exit 2
fi
if [[ ! -x "$BINARY" ]]; then
  echo "binary not found or not executable: $BINARY" >&2
  exit 1
fi
if [[ ! -f "$SOURCE_UNIT" ]]; then
  echo "systemd unit not found at: $SOURCE_UNIT" >&2
  echo "(run this script from a checkout of the okesu repo, or copy systemd/okesu-cp.service alongside it)" >&2
  exit 1
fi
if [[ "$EUID" -ne 0 ]]; then
  echo "must run as root (try: sudo $0 ...)" >&2
  exit 1
fi

log()  { printf '\033[1;36m▸\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m!\033[0m %s\n' "$*"; }

# ── User + dirs ──────────────────────────────────────────────────────────────
if ! id -u "$USER_NAME" >/dev/null 2>&1; then
  log "creating system user $USER_NAME"
  useradd --system --home-dir "$DATA_DIR" --no-create-home \
          --shell /usr/sbin/nologin "$USER_NAME"
fi

mkdir -p "$DATA_DIR" "$DATA_DIR/binaries" "$DATA_DIR/agents" "$DATA_DIR/certs" "$LOG_DIR"
chown -R "$USER_NAME:$GROUP_NAME" "$DATA_DIR" "$LOG_DIR"
chmod 0750 "$DATA_DIR" "$DATA_DIR/certs"

# ── Binary ───────────────────────────────────────────────────────────────────
log "installing binary → $INSTALL_BIN"
install -m 0755 "$BINARY" "$INSTALL_BIN"

# ── TLS cert (optional) ──────────────────────────────────────────────────────
if [[ -n "$CERT_FILE" && -n "$KEY_FILE" ]]; then
  log "installing TLS cert → $DATA_DIR/certs/server.{crt,key}"
  install -m 0640 -o "$USER_NAME" -g "$GROUP_NAME" "$CERT_FILE" "$DATA_DIR/certs/server.crt"
  install -m 0640 -o "$USER_NAME" -g "$GROUP_NAME" "$KEY_FILE"  "$DATA_DIR/certs/server.key"
  CERT_ENV="OKESU_CP_CERT=$DATA_DIR/certs/server.crt"
  KEY_ENV="OKESU_CP_KEY=$DATA_DIR/certs/server.key"
else
  warn "no --cert/--key provided — the CP will generate a self-signed cert on first boot."
  warn "  for production, re-run with: --cert /path/to/server.crt --key /path/to/server.key"
  CERT_ENV=""
  KEY_ENV=""
fi

# ── Default env file ─────────────────────────────────────────────────────────
if [[ ! -f "$CONFIG_FILE" ]]; then
  log "writing default config → $CONFIG_FILE"
  install -m 0640 -o root -g "$GROUP_NAME" /dev/null "$CONFIG_FILE"
  cat > "$CONFIG_FILE" <<EOF
# Okesu Control Plane — environment overrides.
# Edit, then: sudo systemctl restart okesu-cp

# REQUIRED on first boot — set then comment out (subsequent restarts ignore it).
OKESU_CP_ADMIN_PASSWORD=${ADMIN_PASSWORD:-CHANGE-ME}

# REQUIRED if any daemon agents will POST to /api/webhooks/events.
OKESU_CP_WEBHOOK_SECRET=

# Public URLs deployed daemons should reach. Leave blank to derive from the
# listen address (only useful in single-host evaluation).
#OKESU_CP_WEBHOOK_PUBLIC_URL=https://cp.example.com:8443/api/webhooks/events
#OKESU_CP_MGMT_PUBLIC_URL=https://cp.example.com:8444

# Event retention. 0 = keep forever. Findings are kept independently.
#OKESU_CP_EVENT_TTL_DAYS=30

# Production TLS — set after running install-cp.sh with --cert/--key.
$CERT_ENV
$KEY_ENV

# Oracle Identity Domains / OIDC SSO (optional).
#OKESU_CP_OIDC_ISSUER=https://idcs-XXXX.identity.oraclecloud.com/
#OKESU_CP_OIDC_CLIENT_ID=
#OKESU_CP_OIDC_CLIENT_SECRET=
#OKESU_CP_OIDC_REDIRECT_URL=https://cp.example.com:8443/auth/oidc/callback
#OKESU_CP_OIDC_ROLE_MAP=okesu-admins:admin,okesu-ops:operator
EOF
elif [[ -n "$ADMIN_PASSWORD" ]]; then
  warn "$CONFIG_FILE already exists — leaving it alone."
  warn "  to change the admin password, use the Settings → Users UI instead."
fi

# ── systemd unit ─────────────────────────────────────────────────────────────
log "installing systemd unit"
install -m 0644 "$SOURCE_UNIT" "$SYSTEMD_UNIT"
systemctl daemon-reload

if systemctl is-active --quiet okesu-cp; then
  log "restarting okesu-cp"
  systemctl restart okesu-cp
else
  log "enabling + starting okesu-cp"
  systemctl enable --now okesu-cp
fi

# ── Status ───────────────────────────────────────────────────────────────────
sleep 1
if systemctl is-active --quiet okesu-cp; then
  log "okesu-cp is running."
  printf '\n  UI:        https://%s:8443\n' "$(hostname -f 2>/dev/null || hostname)"
  printf '  Login:     admin@local / (the password you set)\n'
  printf '  Logs:      journalctl -u okesu-cp -f\n'
  printf '  Config:    %s (restart after editing)\n\n' "$CONFIG_FILE"
else
  warn "okesu-cp failed to start — see: journalctl -u okesu-cp --no-pager -n 50"
  exit 1
fi
