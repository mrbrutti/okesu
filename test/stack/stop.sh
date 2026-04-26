#!/usr/bin/env bash
# Tear down whatever start.sh brought up. Idempotent.

set -uo pipefail

cd "$(dirname "$0")"
RUN_DIR="$(pwd)/run"

stop_pid() {
    local pidfile="$1"
    [[ -f "$pidfile" ]] || return 0
    local pid
    pid="$(cat "$pidfile")"
    if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
        kill "$pid" 2>/dev/null && echo "stopped pid $pid ($(basename "$pidfile" .pid))"
    fi
    rm -f "$pidfile"
}

stop_pid "$RUN_DIR/daemon.pid"
stop_pid "$RUN_DIR/node.pid"
stop_pid "$RUN_DIR/cp.pid"

if [[ -n "${DOCKER:-}" ]]; then
    DOCKER_CMD=($DOCKER)
elif command -v docker >/dev/null 2>&1; then
    DOCKER_CMD=(docker)
elif command -v nerdctl >/dev/null 2>&1; then
    DOCKER_CMD=(nerdctl)
elif command -v lima >/dev/null 2>&1; then
    DOCKER_CMD=(lima nerdctl)
else
    DOCKER_CMD=()
fi

if (( ${#DOCKER_CMD[@]} > 0 )); then
    # Reap every fleet member matching the okesu-sshtarget-* name prefix
    # (also catches the legacy okesu-sshtarget-demo container).
    mapfile -t FLEET_CONTAINERS < <(
        "${DOCKER_CMD[@]}" ps -a \
            --filter 'name=okesu-sshtarget-' \
            --format '{{.Names}}' 2>/dev/null
    )
    if (( ${#FLEET_CONTAINERS[@]} > 0 )); then
        "${DOCKER_CMD[@]}" rm -f "${FLEET_CONTAINERS[@]}" >/dev/null 2>&1 || true
        echo "stopped ${#FLEET_CONTAINERS[@]} sshtarget container(s): ${FLEET_CONTAINERS[*]}"
    fi
fi

# Optional cleanup: --hard removes DB / certs / agent files.
if [[ "${1:-}" == "--hard" ]]; then
    rm -rf "$RUN_DIR" "$(pwd)/certs" "$(pwd)/.claude"
    echo "removed run/, certs/, .claude/"
fi

echo "done."
