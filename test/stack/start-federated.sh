#!/usr/bin/env bash
#
# Federated lab — boots three CPs on different ports + splits the
# 30-node fleet across two of them, with the third configured as the
# parent that aggregates from both.
#
#   ┌───────────────────────────────────────────────────────────────┐
#   │   global CP                                                    │
#   │   :7443 UI, :7444 mgmt    region=global, role=parent          │
#   │   federates from east + west                                  │
#   └───────────────────────────────────────────────────────────────┘
#                  ▲                          ▲
#                  │ /api/v1/cp/introspect    │ /api/v1/cp/introspect
#                  │   every 30s              │   every 30s
#       ┌──────────┴───────────┐    ┌─────────┴────────────┐
#       │ east CP              │    │ west CP              │
#       │ :8443/:8444          │    │ :9443/:9444          │
#       │ region=us-east-1     │    │ region=us-west-1     │
#       │ 15 daimons (containers)   │ 15 daimons (containers)
#       └──────────────────────┘    └──────────────────────┘
#
# After ./start-federated.sh returns, open https://localhost:7443
# (admin@local / okesu-demo) and look at the Federation page — both
# children should show as healthy with their per-region counts.
#
# Prerequisites: same as start.sh (Lima/Docker/nerdctl, Anthropic key
# in ~/.config/.env or the environment). Reuses everything start.sh
# already does — sshtarget images, daimon files, deploy flow.
#
# To stop: ./stop-federated.sh

set -euo pipefail
cd "$(dirname "$0")"
ROOT="$(cd ../.. && pwd)"
STACK_DIR="$(pwd)"

ADMIN_PASSWORD="${OKESU_ADMIN_PASSWORD:-okesu-demo}"
WEBHOOK_SECRET="${OKESU_WEBHOOK_SECRET:-demo-shared-1}"
FEDERATION_TOKEN="${OKESU_FEDERATION_TOKEN:-demo-fed-1}"
PUBLIC_HOST="${OKESU_PUBLIC_HOST:-host.lima.internal}"

have() { command -v "$1" >/dev/null 2>&1; }
log()  { printf '\n\033[1;36m▸ %s\033[0m\n' "$*"; }
warn() { printf '\033[1;33m! %s\033[0m\n' "$*"; }
fail() { printf '\033[1;31m✗ %s\033[0m\n' "$*"; exit 1; }

# Source ~/.config/.env so ANTHROPIC_API_KEY is available without re-export.
ENV_FILE="${OKESU_DOTENV:-$HOME/.config/.env}"
if [[ -f "$ENV_FILE" ]]; then
    while IFS='=' read -r k v; do
        [[ -z "$k" || "$k" == \#* || "$k" == *' '* ]] && continue
        v="${v%\"}"; v="${v#\"}"; v="${v%\'}"; v="${v#\'}"
        if [[ -z "${!k:-}" ]]; then export "$k=$v"; fi
    done <"$ENV_FILE"
fi

# ── Build host + linux daemon binaries (same as start.sh) ───────────────
# Always defer to `make` rather than skipping when the binaries exist —
# `make` does its own up-to-date check against the source tree, and a
# stale-binary mismatch silently breaks new flags (e.g. --cp-region).
log "building host binaries (make detects up-to-date)"
( cd "$ROOT" && make -s daemon-host cp )
TARGET_ARCH="${OKESU_TARGET_ARCH:-$(uname -m)}"
case "$TARGET_ARCH" in
    arm64|aarch64) GO_TARGET_ARCH=arm64 ;;
    x86_64|amd64)  GO_TARGET_ARCH=amd64 ;;
    *) fail "unsupported arch $TARGET_ARCH";;
esac
DAEMON_LINUX_BIN="$ROOT/okesu-linux-${GO_TARGET_ARCH}"
log "cross-compiling daemon for linux/$GO_TARGET_ARCH (make detects up-to-date)"
( cd "$ROOT" && make -s daemon DAEMON_GOOS=linux DAEMON_GOARCH=$GO_TARGET_ARCH )

# Phase 21.1 — cross-compile a linux okesu-cp so the bundle endpoint
# can hand fresh child CPs a runnable binary inside the dockerfile
# format. CGO disabled because runtime/cgo's setresgid macro requires
# a Linux SDK we don't ship with the macOS toolchain. Skips
# silently when the binary is up-to-date.
log "cross-compiling okesu-cp for linux/$GO_TARGET_ARCH (CP bootstrap bundle)"
( cd "$ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH=$GO_TARGET_ARCH go build -o "okesu-cp-linux-${GO_TARGET_ARCH}" ./cmd/cp ) || warn "cp linux cross-compile failed; bundle endpoint will be disabled"
[[ -f "$ROOT/controlplane/ui/dist/index.html" ]] || {
    log "building web UI"
    have npm || fail "npm required to build web UI"
    ( cd "$ROOT/web" && npm install --silent && npm run build )
}

# ── Container runtime ───────────────────────────────────────────────────
if [[ -n "${DOCKER:-}" ]]; then DOCKER_CMD=($DOCKER)
elif have docker;  then DOCKER_CMD=(docker)
elif have nerdctl; then DOCKER_CMD=(nerdctl)
elif have lima;    then DOCKER_CMD=(lima nerdctl)
else fail "need docker / nerdctl / lima"
fi

# ── Pre-flight: every CP port must be free ──────────────────────────────
port_owner() { lsof -nP -iTCP:"$1" -sTCP:LISTEN -t 2>/dev/null | head -1; }
for port in 7443 7444 8443 8444 9443 9444; do
    if [[ -n "$(port_owner "$port")" ]]; then
        fail "port :$port already in use (pid $(port_owner "$port")). Run ./stop-federated.sh first."
    fi
done

# ── Demo-paced agent files (30s interval — same as start.sh) ────────────
DEMO_AGENT_FILES_DIR="$STACK_DIR/run-fed/agents"
mkdir -p "$DEMO_AGENT_FILES_DIR"
for f in "$ROOT"/examples/agents/*.md; do
    [[ -f "$f" ]] || continue
    sed -E \
        -e 's/^(interval:[[:space:]]*).*/\130s/' \
        -e 's/^(cron:[[:space:]]*.*)/# DEMO: \1/' \
        "$f" > "$DEMO_AGENT_FILES_DIR/$(basename "$f")"
done

# ── boot_cp <name> <ui_port> <mgmt_port> <region> <display_name> [federation_token] ─
boot_cp() {
    local name="$1" ui_port="$2" mgmt_port="$3" region="$4" disp="$5" fed_tok="${6:-}"
    local run_dir="$STACK_DIR/run-fed/$name"
    local certs_dir="$STACK_DIR/run-fed/certs/$name"
    local bin_dir="$run_dir/binaries"
    mkdir -p "$run_dir" "$certs_dir" "$bin_dir"
    cp "$DAEMON_LINUX_BIN" "$bin_dir/okesu-linux-${GO_TARGET_ARCH}"

    log "starting CP \"$name\" on https://localhost:$ui_port (region=$region)"
    local fed_flag=""
    if [[ -n "$fed_tok" ]]; then fed_flag=(--federation-token "$fed_tok"); fi
    # Phase 21.1 bundle generator wants a linux build of okesu-cp it
    # can embed in dockerfile-format CP bootstrap bundles. We
    # cross-compile once at the top of start-federated.sh; pass the
    # path here when present so the bundle endpoint is enabled.
    local cp_bootstrap_args=()
    if [[ -f "$ROOT/okesu-cp-linux-${GO_TARGET_ARCH}" ]]; then
        cp_bootstrap_args+=(--cp-bootstrap-binary "$ROOT/okesu-cp-linux-${GO_TARGET_ARCH}")
    fi

    "$ROOT/okesu-cp" serve \
        --db "$run_dir/cp.db" \
        --listen ":$ui_port" \
        --mgmt-listen ":$mgmt_port" \
        --admin-password "$ADMIN_PASSWORD" \
        --webhook-secret "$WEBHOOK_SECRET" \
        --daemon-binary "$DAEMON_LINUX_BIN" \
        --daemon-binaries-dir "$bin_dir" \
        --daimon-files-dir "$DEMO_AGENT_FILES_DIR" \
        --agent-files-dir "$ROOT/agents" \
        --orchestration-seed-dir "$ROOT/examples/orchestrations" \
        --webhook-public-url "https://$PUBLIC_HOST:$ui_port/api/webhooks/events" \
        --mgmt-public-url "https://$PUBLIC_HOST:$mgmt_port" \
        --cp-region "$region" \
        --cp-display-name "$disp" \
        ${cp_bootstrap_args[@]+"${cp_bootstrap_args[@]}"} \
        ${fed_flag[@]+"${fed_flag[@]}"} \
        > "$run_dir/cp.log" 2>&1 &
    local pid=$!
    echo "$pid" > "$run_dir/cp.pid"
    sleep 2
    if ! kill -0 "$pid" 2>/dev/null; then
        fail "$name CP failed to start. See $run_dir/cp.log"
    fi
}

# Boot the three CPs. Both children carry the same federation token so
# the parent uses one shared secret for both peers.
boot_cp east   8443 8444 "us-east-1" "East CP"   "$FEDERATION_TOKEN"
boot_cp west   9443 9444 "us-west-1" "West CP"   "$FEDERATION_TOKEN"
boot_cp global 7443 7444 "global"    "Global CP" ""   # parent doesn't expose introspect

# ── Fleet definition (15 east + 15 west) ────────────────────────────────
EAST_FLEET="\
edr-debian-1:debian:18022:edr
edr-debian-2:debian:18023:edr
edr-debian-3:debian:18024:edr
edr-fedora-1:fedora:18025:edr
edr-fedora-2:fedora:18026:edr
edr-rocky-1:rocky:18027:edr
sre-debian-1:debian:18028:sre-health
sre-fedora-1:fedora:18029:sre-health
sre-rocky-1:rocky:18030:sre-health
threat-debian-1:debian:18031:instance-threat
threat-fedora-1:fedora:18032:instance-threat
threat-rocky-1:rocky:18033:instance-threat
fim-debian-1:debian:18034:instance-integrity
fim-fedora-1:fedora:18035:instance-integrity
mixed-east-1:debian:18036:instance-integrity,sre-health"

WEST_FLEET="\
edr-debian-4:debian:18037:edr
edr-fedora-3:fedora:18038:edr
edr-rocky-2:rocky:18039:edr
sre-debian-2:debian:18040:sre-health
sre-fedora-2:fedora:18041:sre-health
sre-fedora-3:fedora:18042:sre-health
sre-rocky-2:rocky:18043:sre-health
threat-debian-2:debian:18044:instance-threat
threat-fedora-2:fedora:18045:instance-threat
threat-rocky-2:rocky:18046:instance-threat
fim-fedora-2:fedora:18047:instance-integrity
fim-rocky-1:rocky:18048:instance-integrity
fim-rocky-2:rocky:18049:instance-integrity
mixed-west-1:rocky:18050:edr,sre-health
mixed-west-2:debian:18051:edr,instance-threat"

# Make sure sshtarget images are built (same logic as start.sh).
NEEDED=(debian fedora rocky)
MISSING=()
for d in "${NEEDED[@]}"; do
    "${DOCKER_CMD[@]}" image inspect "okesu-sshtarget-${d}:test" >/dev/null 2>&1 || MISSING+=("$d")
done
if (( ${#MISSING[@]} > 0 )); then
    log "building sshtarget images: ${MISSING[*]}"
    DOCKER="${DOCKER_CMD[*]}" "$STACK_DIR/../sshtarget/build.sh" \
        --distro "$(IFS=,; echo "${MISSING[*]}")" \
        > "$STACK_DIR/run-fed/sshtarget-build.log" 2>&1 || \
        fail "sshtarget build failed"
fi

# ── deploy_fleet <ui_port> <fleet> ──────────────────────────────────────
deploy_fleet() {
    local ui_port="$1"
    local fleet="$2"
    local cookies="$STACK_DIR/run-fed/cookies-$ui_port.txt"
    curl -sk -c "$cookies" -X POST "https://localhost:$ui_port/api/auth/login" \
        -H 'Content-Type: application/json' \
        -d "{\"email\":\"admin@local\",\"password\":\"$ADMIN_PASSWORD\"}" > /dev/null

    local SSH_KEY_FILE="$ROOT/test/sshtarget/keys/id_ed25519"
    [[ -f "$SSH_KEY_FILE" ]] || { warn "no SSH key at $SSH_KEY_FILE — registering nodes only"; }

    # Pass 1 — launch all containers in parallel. Doing the SSH deploy
    # in the same loop would race sshd's first-boot init (we'd dial
    # before the in-container daemon listens on :22), which is what
    # caused the "all 15 nodes failed: connection refused" report.
    while IFS= read -r line <&3; do
        [[ -z "$line" ]] && continue
        IFS=':' read -r f_name f_distro f_port f_agents <<<"$line"
        local cname="okesu-sshtarget-${f_name}"
        "${DOCKER_CMD[@]}" rm -f "$cname" >/dev/null 2>&1 || true
        "${DOCKER_CMD[@]}" run -d --name "$cname" --hostname "$f_name" \
            -p "${f_port}:22" "okesu-sshtarget-${f_distro}:test" >/dev/null
    done 3<<<"$fleet"

    # Wait for sshd in every container before deploying. Probe each
    # mapped port with `nc -z` until it accepts a connection (or
    # bail after a generous timeout). Faster than a fixed sleep on a
    # warm machine, safe on a cold one.
    log "  waiting for sshd in $(printf '%s\n' "$fleet" | grep -c .) container(s)..."
    while IFS= read -r line <&3; do
        [[ -z "$line" ]] && continue
        IFS=':' read -r _ _ f_port _ <<<"$line"
        local tries=0
        while ! nc -z -w 1 localhost "$f_port" 2>/dev/null; do
            tries=$((tries+1))
            if (( tries > 30 )); then
                warn "    sshd on :$f_port did not come up within 30s"
                break
            fi
            sleep 1
        done
    done 3<<<"$fleet"

    # Pass 2 — register + deploy. Now sshd is listening so the deploy
    # job's first ssh dial succeeds.
    #
    # On a re-run the nodes table already has rows for these names; the
    # POST returns a UNIQUE-constraint error and the response carries no
    # id. We catch that and look the id up by name from /api/nodes so the
    # deploy still happens — keeps re-runs idempotent.
    local existing_nodes_json
    existing_nodes_json=$(curl -sk -b "$cookies" "https://localhost:$ui_port/api/nodes")
    while IFS= read -r line <&3; do
        [[ -z "$line" ]] && continue
        IFS=':' read -r f_name f_distro f_port f_agents <<<"$line"
        local node_payload
        node_payload=$(python3 -c "
import json
print(json.dumps({
  'name': '$f_name',
  'hostname': 'localhost',
  'ssh_user': 'root',
  'ssh_port': $f_port,
  'notes': 'Federated lab — agents=$f_agents'
}))")
        local node_resp
        node_resp=$(curl -sk -b "$cookies" -X POST \
            "https://localhost:$ui_port/api/nodes" \
            -H 'Content-Type: application/json' \
            -d "$node_payload")
        local node_id
        node_id=$(printf '%s' "$node_resp" | python3 -c 'import json,sys;print(json.load(sys.stdin).get("id",""))' 2>/dev/null || true)
        if [[ -z "$node_id" ]]; then
            # Re-run path: registration failed (likely UNIQUE on
            # nodes.name). Look up the existing row by name from the
            # snapshot we took above so the deploy can continue.
            node_id=$(printf '%s' "$existing_nodes_json" | NAME="$f_name" python3 -c '
import json, os, sys
target = os.environ["NAME"]
for r in json.load(sys.stdin):
    if r.get("name") == target:
        print(r["id"]); break
' 2>/dev/null || true)
        fi
        if [[ -z "$node_id" ]]; then
            warn "register $f_name failed and no existing row: $node_resp"
            continue
        fi

        if [[ -f "$SSH_KEY_FILE" ]]; then
            export AGENTS_CSV="$f_agents" SSH_KEY_FILE \
                ANTHROPIC_KEY="${ANTHROPIC_API_KEY:-}" \
                OPENAI_KEY="${OPENAI_API_KEY:-}"
            local deploy_payload
            deploy_payload=$(python3 -c '
import json, os
print(json.dumps({
  "agents":            os.environ["AGENTS_CSV"].split(","),
  "private_key":       open(os.environ["SSH_KEY_FILE"]).read(),
  "anthropic_api_key": os.environ.get("ANTHROPIC_KEY", ""),
  "openai_api_key":    os.environ.get("OPENAI_KEY", ""),
  "include_webhook":   True,
  "include_mgmt_cert": True,
}))')
            curl -sk -b "$cookies" -X POST \
                "https://localhost:$ui_port/api/nodes/$node_id/deploy" \
                -H 'Content-Type: application/json' \
                -d "$deploy_payload" > /dev/null
            printf '  · %-18s ssh=:%-5s agents=%s\n' "$f_name" "$f_port" "$f_agents"
        fi
    done 3<<<"$fleet"
}

# ── bulk_start_processes — work around the lab's mocked systemctl ──────
#
# The deploy step writes systemd unit files, but the sshtarget images
# no-op `systemctl enable --now`. So the units land on disk and never
# launch. This walks every okesu-sshtarget-* container and spawns the
# two long-running processes the orchestrator needs:
#
#   1. okesu-agent-<name>.service  → `okesu daemon --agent <name>`
#      (one per agent on the node — the per-tick collector loop)
#   2. okesu-jobs.service          → `okesu jobs --cp-url … --name …`
#      (the pull-mode receiver for orchestration step dispatches)
#
# Idempotent: skips containers where the process is already running.
# Reads the unit's ExecStart line to recover per-node flags so this
# stays in lock-step with whatever the deploy code wrote.
bulk_start_processes() {
    local container started_d=0 started_j=0 skipped=0
    # Snapshot the container list up front. If we used a pipe / process
    # substitution, the inner `nerdctl exec` calls would inherit the
    # loop's stdin and gobble the heredoc after one iteration.
    local containers
    containers=$("${DOCKER_CMD[@]}" ps --filter 'name=okesu-sshtarget-' --format '{{.Names}}')
    while IFS= read -r container <&3; do
        [[ -z "$container" ]] && continue

        # ── daimons (one per okesu-agent-*.service file) ─────────────
        local agent_units
        agent_units=$("${DOCKER_CMD[@]}" exec "$container" sh -c \
            'ls /etc/systemd/system/okesu-agent-*.service 2>/dev/null' </dev/null 2>/dev/null || true)
        local unit
        for unit in $agent_units; do
            local agent_name
            agent_name=$(basename "$unit" .service)
            agent_name=${agent_name#okesu-agent-}
            if "${DOCKER_CMD[@]}" exec "$container" pgrep -f "okesu daemon --agent ${agent_name}\b" </dev/null >/dev/null 2>&1; then
                skipped=$((skipped + 1))
                continue
            fi
            "${DOCKER_CMD[@]}" exec -d "$container" sh -c "
                set -a
                [ -f /etc/okesu/agents/${agent_name}.env ] && . /etc/okesu/agents/${agent_name}.env
                set +a
                mkdir -p /var/lib/okesu/${agent_name} /var/log/okesu
                cd /var/lib/okesu/${agent_name}
                nohup /usr/local/bin/okesu daemon --agent ${agent_name} \
                    > /var/log/okesu/${agent_name}.log 2>&1 &
            "
            started_d=$((started_d + 1))
        done

        # ── jobs runtime (one per container) ─────────────────────────
        if "${DOCKER_CMD[@]}" exec "$container" pgrep -f 'okesu jobs' </dev/null >/dev/null 2>&1; then
            skipped=$((skipped + 1))
        else
            local exec_line
            exec_line=$("${DOCKER_CMD[@]}" exec "$container" sh -c \
                "grep '^ExecStart=' /etc/systemd/system/okesu-jobs.service 2>/dev/null | cut -d= -f2-" </dev/null 2>/dev/null || true)
            if [[ -n "$exec_line" ]]; then
                "${DOCKER_CMD[@]}" exec -d "$container" sh -c "
                    set -a
                    [ -f /etc/okesu/jobs.env ] && . /etc/okesu/jobs.env
                    set +a
                    mkdir -p /var/log/okesu
                    nohup ${exec_line} > /var/log/okesu/jobs.log 2>&1 &
                "
                started_j=$((started_j + 1))
            fi
        fi
    done 3<<<"$containers"

    printf '  ✓ %d daimon process(es) started, %d jobs runtime(s) started, %d already running\n' \
        "$started_d" "$started_j" "$skipped"
}

# wait_for_deploys polls /api/nodes on the given CP until every fleet
# node's binary has actually landed in its container. The deploy
# endpoint returns synchronously with a job id but the SSH copy +
# unit-file write happens in a goroutine, so we have to gate the
# bulk-start on completion or we'll race and find no service files
# to read. Times out after 180s with a warning.
wait_for_deploys() {
    local fleet="$1"
    local expected
    expected=$(printf '%s\n' "$fleet" | grep -c .)
    local deadline=$((SECONDS + 180))
    local ready=0
    while (( SECONDS < deadline )); do
        ready=0
        # Read fleet via FD 3 — `nerdctl exec` (and friends) inherit the
        # caller's stdin, so feeding the loop on stdin gets the heredoc
        # gobbled up after the first iteration.
        while IFS= read -r line <&3; do
            [[ -z "$line" ]] && continue
            IFS=':' read -r f_name _ _ _ <<<"$line"
            local probe_rc=0
            "${DOCKER_CMD[@]}" exec "okesu-sshtarget-$f_name" \
                test -x /usr/local/bin/okesu </dev/null >/dev/null 2>&1 || probe_rc=$?
            if (( probe_rc == 0 )); then
                ready=$((ready + 1))
            fi
        done 3<<<"$fleet"
        if (( ready == expected )); then
            printf '  ✓ %d/%d nodes have okesu binary in place\n' "$ready" "$expected"
            return 0
        fi
        printf '  · %d/%d ready, waiting…\n' "$ready" "$expected"
        sleep 5
    done
    warn "deploy wait timed out: only $ready/$expected nodes have okesu binary"
    return 1
}

log "deploying east fleet (15 nodes → :8443)"
deploy_fleet 8443 "$EAST_FLEET"

log "deploying west fleet (15 nodes → :9443)"
deploy_fleet 9443 "$WEST_FLEET"

# Wait for the async deploy goroutines to actually finish copying the
# binary + unit files before we try to bulk-start anything inside the
# containers. Without this gate the bulk-start runs against an empty
# /etc/systemd/system on every container.
log "waiting for deploys to land binaries + units"
wait_for_deploys "$EAST_FLEET"
wait_for_deploys "$WEST_FLEET"

# Lab containers' systemctl is a no-op, so the deploy's `systemctl
# enable --now` never actually launches anything. Manually spawn the
# daimon + jobs-runtime processes inside each container.
log "bulk-starting daimon + jobs processes inside lab containers"
bulk_start_processes

# ── Register both children as federation peers on the global CP ─────────
log "registering federation peers on global CP"
GLOBAL_COOKIES="$STACK_DIR/run-fed/cookies-7443.txt"
curl -sk -c "$GLOBAL_COOKIES" -X POST "https://localhost:7443/api/auth/login" \
    -H 'Content-Type: application/json' \
    -d "{\"email\":\"admin@local\",\"password\":\"$ADMIN_PASSWORD\"}" > /dev/null

for child in east:8443 west:9443; do
    name="${child%%:*}"; port="${child##*:}"
    body=$(python3 -c "
import json
print(json.dumps({
  'url': 'https://localhost:$port',
  'token': '$FEDERATION_TOKEN',
  'display_name': '$name'
}))")
    resp=$(curl -sk -b "$GLOBAL_COOKIES" -X POST \
        "https://localhost:7443/api/federation/peers" \
        -H 'Content-Type: application/json' \
        -d "$body")
    if printf '%s' "$resp" | grep -q '"id":'; then
        printf '  ✓ %s peer added\n' "$name"
    else
        warn "  add $name peer failed: $resp"
    fi
done

# ── Done ────────────────────────────────────────────────────────────────
printf '\n\033[1;32m✓ Federated stack is up.\033[0m\n\n'
cat <<EOF
  Global CP:  https://localhost:7443       (the federation parent — open this!)
  East CP:    https://localhost:8443       (15 daimons, region us-east-1)
  West CP:    https://localhost:9443       (15 daimons, region us-west-1)
  Login:      admin@local / $ADMIN_PASSWORD

  PIDs:
    east   $(cat "$STACK_DIR/run-fed/east/cp.pid")
    west   $(cat "$STACK_DIR/run-fed/west/cp.pid")
    global $(cat "$STACK_DIR/run-fed/global/cp.pid")

  Federation token: $FEDERATION_TOKEN  (shared between east + west)

  Logs in: $STACK_DIR/run-fed/{east,west,global}/cp.log

To stop: ./stop-federated.sh
EOF
