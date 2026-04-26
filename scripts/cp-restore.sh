#!/usr/bin/env bash
#
# cp-restore.sh — restore an okesu-cp backup produced by cp-backup.sh.
#
# Refuses to overwrite a populated data dir unless --force is given —
# operators should explicitly confirm they want to replace live state.
#
# Usage:
#   sudo systemctl stop okesu-cp
#   sudo ./cp-restore.sh okesu-cp-backup-2026-04-25.tar.gz
#   sudo systemctl start okesu-cp

set -euo pipefail

DATA_DIR="/var/lib/okesu-cp"
FORCE=0
OWNER="okesu-cp:okesu-cp"

if [[ $# -lt 1 ]]; then
  echo "usage: $0 <backup.tar.gz> [--data-dir DIR] [--force] [--owner USER:GROUP]" >&2
  exit 2
fi

BUNDLE="$1"; shift || true
while [[ $# -gt 0 ]]; do
  case "$1" in
    --data-dir) DATA_DIR="$2"; shift 2 ;;
    --owner)    OWNER="$2"; shift 2 ;;
    --force)    FORCE=1; shift ;;
    *) echo "unknown flag: $1" >&2; exit 2 ;;
  esac
done

if [[ ! -f "$BUNDLE" ]]; then
  echo "bundle not found: $BUNDLE" >&2
  exit 1
fi
mkdir -p "$DATA_DIR"

if [[ -f "$DATA_DIR/cp.db" && "$FORCE" -ne 1 ]]; then
  echo "$DATA_DIR/cp.db exists — refusing to overwrite without --force" >&2
  echo "  (a running okesu-cp will lock the DB; stop it first: systemctl stop okesu-cp)" >&2
  exit 1
fi

# Sanity-check the bundle — manifest + cp.db must be present. tar may emit
# entries with a leading "./" when the archive was created from a directory,
# so accept both forms.
TAR_LISTING="$(tar -tzf "$BUNDLE" 2>/dev/null | sed 's#^\./##')"
if ! grep -qx 'manifest.txt' <<<"$TAR_LISTING"; then
  echo "bundle is missing manifest.txt — is this a cp-backup.sh archive?" >&2
  exit 1
fi
if ! grep -qx 'cp.db' <<<"$TAR_LISTING"; then
  echo "bundle is missing cp.db" >&2
  exit 1
fi

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

tar -C "$WORK" -xzf "$BUNDLE"

echo "── manifest ─────────────────────────────────"
cat "$WORK/manifest.txt"
echo "─────────────────────────────────────────────"

mkdir -p "$DATA_DIR/certs"

cp "$WORK/cp.db" "$DATA_DIR/cp.db"
# Drop stale WAL siblings — the new file's journal is freshly checkpointed
# and the old WAL/SHM are lies relative to it.
rm -f "$DATA_DIR/cp.db-wal" "$DATA_DIR/cp.db-shm"

if [[ -d "$WORK/certs" ]]; then
  cp "$WORK/certs/"*.crt "$DATA_DIR/certs/" 2>/dev/null || true
  cp "$WORK/certs/"*.key "$DATA_DIR/certs/" 2>/dev/null || true
fi

# Fix ownership/perms — the running daemon must own everything and the
# private keys + DB must not be world-readable.
if id -u "${OWNER%%:*}" >/dev/null 2>&1; then
  chown -R "$OWNER" "$DATA_DIR"
fi
chmod 0640 "$DATA_DIR/cp.db"
chmod 0640 "$DATA_DIR/certs/"*.key 2>/dev/null || true
chmod 0644 "$DATA_DIR/certs/"*.crt 2>/dev/null || true

echo
echo "restore complete → $DATA_DIR"
echo "next: sudo systemctl start okesu-cp"
