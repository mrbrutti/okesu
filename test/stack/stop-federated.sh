#!/usr/bin/env bash
# Tear down the federated lab: kill all three CPs, remove their fleet
# containers, optionally wipe state with --hard.

set -uo pipefail
cd "$(dirname "$0")"
RUN_DIR="$(pwd)/run-fed"

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
        echo "skip pid $pid ($pidfile): not ours — argv: $cmd"
        rm -f "$pidfile"
        return 0
    fi
    kill "$pid" 2>/dev/null && echo "stopped pid $pid ($(dirname "$pidfile" | xargs basename))"
    rm -f "$pidfile"
}

for child in east west global; do
    stop_pid "$RUN_DIR/$child/cp.pid" "okesu-cp serve"
done

if command -v docker >/dev/null 2>&1; then DOCKER_CMD=(docker)
elif command -v nerdctl >/dev/null 2>&1; then DOCKER_CMD=(nerdctl)
elif command -v lima >/dev/null 2>&1; then DOCKER_CMD=(lima nerdctl)
else DOCKER_CMD=()
fi

if (( ${#DOCKER_CMD[@]} > 0 )); then
    mapfile -t FLEET_CONTAINERS < <(
        "${DOCKER_CMD[@]}" ps -a --filter 'name=okesu-sshtarget-' --format '{{.Names}}' 2>/dev/null
    )
    if (( ${#FLEET_CONTAINERS[@]} > 0 )); then
        "${DOCKER_CMD[@]}" rm -f "${FLEET_CONTAINERS[@]}" >/dev/null 2>&1 || true
        echo "stopped ${#FLEET_CONTAINERS[@]} sshtarget container(s)"
    fi
fi

if [[ "${1:-}" == "--hard" ]]; then
    rm -rf "$RUN_DIR"
    echo "removed run-fed/"
fi
echo "done."
