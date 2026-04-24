#!/usr/bin/env bash
# install.sh — install okesu and register it with systemd
#
# Usage:
#   sudo ./scripts/install.sh [--agent <name>] [--binary <path>]
#
# Options:
#   --agent   <name>   enable and start okesu-agent@<name>.service (optional)
#   --binary  <path>   path to compiled okesu binary (default: ./okesu)
#
# What this script does:
#   1. Creates the okesu system user and group (if absent)
#   2. Installs the binary to /usr/local/bin/okesu
#   3. Creates state and config directories
#   4. Installs the systemd template unit
#   5. Reloads systemd
#   6. Enables and starts the requested agent instance (if --agent is given)

set -euo pipefail

BINARY="./okesu"
AGENT_NAME=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --agent)   AGENT_NAME="$2"; shift 2 ;;
    --binary)  BINARY="$2";     shift 2 ;;
    *)         echo "Unknown option: $1" >&2; exit 1 ;;
  esac
done

if [[ $EUID -ne 0 ]]; then
  echo "error: this script must be run as root (sudo)" >&2
  exit 1
fi

# ── 1. Create okesu user/group ──────────────────────────────────────────────
if ! id okesu &>/dev/null; then
  echo "Creating system user: okesu"
  useradd --system --no-create-home --shell /sbin/nologin \
    --comment "Okesu agent daemon" okesu
fi

# ── 2. Install binary ────────────────────────────────────────────────────────
echo "Installing binary: $BINARY → /usr/local/bin/okesu"
install -m 0755 -o root -g root "$BINARY" /usr/local/bin/okesu

# ── 3. Create directories ────────────────────────────────────────────────────
echo "Creating directories"
install -d -m 0755 -o root   -g root  /etc/okesu
install -d -m 0755 -o root   -g root  /etc/okesu/agents
install -d -m 0750 -o okesu  -g okesu /var/lib/okesu

# ── 4. Install systemd unit ──────────────────────────────────────────────────
UNIT_SRC="$(dirname "$0")/../systemd/okesu-agent@.service"
UNIT_DST="/etc/systemd/system/okesu-agent@.service"

if [[ ! -f "$UNIT_SRC" ]]; then
  echo "error: systemd unit not found at $UNIT_SRC" >&2
  exit 1
fi

echo "Installing systemd unit: $UNIT_DST"
install -m 0644 -o root -g root "$UNIT_SRC" "$UNIT_DST"

# ── 5. Reload systemd ────────────────────────────────────────────────────────
echo "Reloading systemd daemon"
systemctl daemon-reload

# ── 6. Enable and start agent instance ──────────────────────────────────────
if [[ -n "$AGENT_NAME" ]]; then
  # Create per-agent state directory.
  install -d -m 0750 -o okesu -g okesu "/var/lib/okesu/$AGENT_NAME"

  echo "Enabling okesu-agent@$AGENT_NAME.service"
  systemctl enable "okesu-agent@$AGENT_NAME.service"

  echo "Starting okesu-agent@$AGENT_NAME.service"
  systemctl start "okesu-agent@$AGENT_NAME.service"

  echo ""
  echo "Agent status:"
  systemctl status "okesu-agent@$AGENT_NAME.service" --no-pager || true
fi

echo ""
echo "Installation complete."
echo "  Binary:      /usr/local/bin/okesu"
echo "  Config:      /etc/okesu/agents/<name>.md"
echo "  Env file:    /etc/okesu/agents/<name>.env"
echo "  State dir:   /var/lib/okesu/<name>/"
echo "  Certs:       /etc/okesu/agent.crt, agent.key, ca.crt"
echo ""
echo "To start an agent:  systemctl start okesu-agent@<name>"
echo "To reload config:   systemctl kill --signal=SIGHUP okesu-agent@<name>"
echo "To follow logs:     journalctl -fu okesu-agent@<name>"
