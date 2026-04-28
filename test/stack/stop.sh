#!/usr/bin/env bash
# Tear down whatever start.sh brought up. Idempotent.

set -uo pipefail

cd "$(dirname "$0")"
RUN_DIR="$(pwd)/run"

# Kill the PID in $1 only if its argv contains $2. Without the cmd check
# we'd happily kill whatever process happens to be living at that PID
# (e.g. the demo daemon.pid pointing at an unrelated `okesu daemon`
# someone launched by hand) — that's how the macOS edr-demo got reaped
# as collateral last lab restart.
stop_pid() {
    local pidfile="$1" expected="$2" pid cmd
    [[ -f "$pidfile" ]] || return 0
    pid="$(cat "$pidfile")"
    if [[ -z "$pid" ]] || ! kill -0 "$pid" 2>/dev/null; then
        rm -f "$pidfile"
        return 0
    fi
    cmd="$(ps -p "$pid" -o args= 2>/dev/null || true)"
    if [[ -n "$expected" && "$cmd" != *"$expected"* ]]; then
        echo "skip pid $pid ($(basename "$pidfile" .pid)): not ours — argv: $cmd"
        rm -f "$pidfile"
        return 0
    fi
    kill "$pid" 2>/dev/null && echo "stopped pid $pid ($(basename "$pidfile" .pid))"
    rm -f "$pidfile"
}

stop_pid "$RUN_DIR/daemon.pid" "okesu daemon"
stop_pid "$RUN_DIR/node.pid"   "okesu node"
stop_pid "$RUN_DIR/cp.pid"     "okesu-cp serve"

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
