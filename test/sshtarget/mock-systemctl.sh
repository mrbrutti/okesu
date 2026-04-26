#!/usr/bin/env bash
# A drop-in systemctl shim for the okesu sshtarget test container.
#
# This is NOT systemd. It only handles the verbs the Phase 5 deploy
# orchestrator emits, and translates `enable --now okesu-agent@NAME`
# into actually launching `okesu daemon --agent NAME` in the background.
# All other invocations are no-ops that exit 0 (so the deploy script
# doesn't trip on commands it expects to succeed).

set -uo pipefail

PIDDIR="/var/run/okesu"
LOGDIR="/var/log/okesu"
mkdir -p "$PIDDIR" "$LOGDIR"

verb="${1:-}"
shift || true

# Strip flags that systemctl accepts but we ignore.
flags=()
positional=()
while (( $# > 0 )); do
    case "$1" in
        --user|--system|--no-block|--quiet|-q|--full|--no-pager) ;;
        --now) flags+=(--now) ;;
        --*) ;;
        *) positional+=("$1") ;;
    esac
    shift
done

unit_to_agent() {
    # okesu-agent@NAME or okesu-agent@NAME.service → NAME
    local u="$1"
    u="${u%.service}"
    case "$u" in
        okesu-agent@*) printf '%s' "${u#okesu-agent@}" ;;
        *) printf '' ;;
    esac
}

start_agent() {
    local agent="$1"
    local pidfile="$PIDDIR/okesu-agent@${agent}.pid"
    local logfile="$LOGDIR/${agent}.log"

    # Already running?
    if [[ -f "$pidfile" ]] && kill -0 "$(cat "$pidfile" 2>/dev/null)" 2>/dev/null; then
        echo "[mock systemctl] okesu-agent@$agent already running (pid=$(cat "$pidfile"))"
        return 0
    fi

    # Source the per-agent env file if present (mirrors the real systemd
    # unit's `EnvironmentFile=-/etc/okesu/agents/<name>.env`).
    local env_args=()
    local envfile="/etc/okesu/agents/${agent}.env"
    if [[ -f "$envfile" ]]; then
        # shellcheck disable=SC1090
        set -a
        # shellcheck disable=SC1090
        . "$envfile"
        set +a
    fi
    : "${env_args[@]+x}"  # mark as referenced for shellcheck

    nohup /usr/local/bin/okesu daemon --agent "$agent" \
        > "$logfile" 2>&1 &
    local pid=$!
    echo "$pid" > "$pidfile"
    echo "[mock systemctl] started okesu-agent@$agent (pid=$pid, log=$logfile)"
}

stop_agent() {
    local agent="$1"
    local pidfile="$PIDDIR/okesu-agent@${agent}.pid"
    if [[ -f "$pidfile" ]]; then
        local pid
        pid="$(cat "$pidfile" 2>/dev/null)"
        if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
            kill "$pid" 2>/dev/null
            echo "[mock systemctl] stopped okesu-agent@$agent (pid=$pid)"
        fi
        rm -f "$pidfile"
    fi
}

case "$verb" in
    daemon-reload|reload)
        echo "[mock systemctl] daemon-reload (no-op)"
        ;;
    enable|disable)
        # Process each unit. With --now also start (or stop on disable).
        for u in "${positional[@]}"; do
            agent="$(unit_to_agent "$u")"
            if [[ -z "$agent" ]]; then
                echo "[mock systemctl] $verb $u (ignored — only okesu-agent@* is supported)"
                continue
            fi
            echo "[mock systemctl] $verb okesu-agent@$agent"
            if [[ " ${flags[*]} " == *" --now "* ]]; then
                if [[ "$verb" == "enable" ]]; then
                    start_agent "$agent"
                else
                    stop_agent "$agent"
                fi
            fi
        done
        ;;
    start)
        for u in "${positional[@]}"; do
            agent="$(unit_to_agent "$u")"
            [[ -n "$agent" ]] && start_agent "$agent" || echo "[mock systemctl] start $u (no-op)"
        done
        ;;
    stop|kill)
        for u in "${positional[@]}"; do
            agent="$(unit_to_agent "$u")"
            [[ -n "$agent" ]] && stop_agent "$agent" || echo "[mock systemctl] $verb $u (no-op)"
        done
        ;;
    restart)
        for u in "${positional[@]}"; do
            agent="$(unit_to_agent "$u")"
            if [[ -n "$agent" ]]; then
                stop_agent "$agent"
                sleep 0.2
                start_agent "$agent"
            fi
        done
        ;;
    status|is-active)
        for u in "${positional[@]}"; do
            agent="$(unit_to_agent "$u")"
            [[ -z "$agent" ]] && continue
            local pidfile="$PIDDIR/okesu-agent@${agent}.pid"
            if [[ -f "$pidfile" ]] && kill -0 "$(cat "$pidfile")" 2>/dev/null; then
                echo "active"
            else
                echo "inactive"
            fi
        done
        ;;
    *)
        echo "[mock systemctl] $verb ${positional[*]} (no-op)"
        ;;
esac
exit 0
