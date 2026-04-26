#!/usr/bin/env bash
#
# cp-backup.sh — hot snapshot of the Okesu Control Plane.
#
# Captures everything an operator needs to stand the CP back up on another
# host: the SQLite DB (via the online `.backup` API so it's safe to run
# without stopping the daemon), the CA cert + key, and the mgmt-plane
# server cert. User-facing TLS certs are NOT included by default — they
# are usually managed externally (cert-manager, an ACME client, etc).
#
# Usage:
#   ./cp-backup.sh                                # uses /var/lib/okesu-cp
#   ./cp-backup.sh --data-dir /opt/okesu          # custom data dir
#   ./cp-backup.sh --out backups/2026-04-25.tgz   # custom output path
#
# Restore: see ./cp-restore.sh

set -euo pipefail

DATA_DIR="/var/lib/okesu-cp"
OUT=""
INCLUDE_SERVER_CERT=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --data-dir)            DATA_DIR="$2"; shift 2 ;;
    --out)                 OUT="$2"; shift 2 ;;
    --include-server-cert) INCLUDE_SERVER_CERT=1; shift ;;
    -h|--help)
      grep '^#' "$0" | sed 's/^# \{0,1\}//' | head -25
      exit 0 ;;
    *) echo "unknown flag: $1" >&2; exit 2 ;;
  esac
done

if [[ ! -d "$DATA_DIR" ]]; then
  echo "data dir not found: $DATA_DIR" >&2
  exit 1
fi

DB="$DATA_DIR/cp.db"
if [[ ! -f "$DB" ]]; then
  echo "DB not found: $DB" >&2
  exit 1
fi
if ! command -v sqlite3 >/dev/null 2>&1; then
  echo "sqlite3 is required (apt install sqlite3 / brew install sqlite)" >&2
  exit 1
fi

if [[ -z "$OUT" ]]; then
  OUT="okesu-cp-backup-$(date -u +%Y%m%dT%H%M%SZ).tar.gz"
fi
OUT_ABS="$(cd "$(dirname "$OUT")" && pwd)/$(basename "$OUT")"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

# Online snapshot — safe while okesu-cp is running. Uses SQLite's backup API
# so we never copy a file mid-write.
sqlite3 "$DB" ".backup '$WORK/cp.db'"

# CA + mgmt server cert (operators typically auto-rotate user-facing TLS
# externally; the CA must be preserved or every deployed daemon's mgmt
# client cert becomes invalid).
#
# Look in both layouts: $DATA_DIR/certs/<name> (systemd install — see
# install-cp.sh) and $DATA_DIR/<name> (single-file dev/test layouts).
mkdir -p "$WORK/certs"
copy_if_present() {
  local name="$1"
  if [[ -f "$DATA_DIR/certs/$name" ]]; then
    cp "$DATA_DIR/certs/$name" "$WORK/certs/$name"
  elif [[ -f "$DATA_DIR/$name" ]]; then
    cp "$DATA_DIR/$name" "$WORK/certs/$name"
  fi
}
for f in ca.crt ca.key mgmt-server.crt mgmt-server.key; do
  copy_if_present "$f"
done
if [[ "$INCLUDE_SERVER_CERT" -eq 1 ]]; then
  for f in server.crt server.key; do
    copy_if_present "$f"
  done
fi

# Manifest — used by cp-restore.sh to verify the bundle isn't gibberish.
cat > "$WORK/manifest.txt" <<EOF
okesu-cp backup
created_at: $(date -u +%FT%TZ)
created_on: $(hostname -f 2>/dev/null || hostname)
data_dir:   $DATA_DIR
sqlite_size_bytes: $(stat -c '%s' "$WORK/cp.db" 2>/dev/null || stat -f '%z' "$WORK/cp.db")
include_server_cert: $INCLUDE_SERVER_CERT
EOF

tar -C "$WORK" -czf "$OUT_ABS" .

echo "wrote: $OUT_ABS"
echo "       $(du -h "$OUT_ABS" | cut -f1)"
