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
    "$ROOT/okesu-cp" serve \
        --db "$run_dir/cp.db" \
        --listen ":$ui_port" \
        --mgmt-listen ":$mgmt_port" \
        --admin-password "$ADMIN_PASSWORD" \
        --webhook-secret "$WEBHOOK_SECRET" \
        --daemon-binary "$DAEMON_LINUX_BIN" \
        --daemon-binaries-dir "$bin_dir" \
        --daimon-files-dir "$DEMO_AGENT_FILES_DIR" \
        --webhook-public-url "https://$PUBLIC_HOST:$ui_port/api/webhooks/events" \
        --mgmt-public-url "https://$PUBLIC_HOST:$mgmt_port" \
        --cp-region "$region" \
        --cp-display-name "$disp" \
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
    local ui_port="$1" fleet="$2" cookies="$STACK_DIR/run-fed/cookies-$ui_port.txt"
    curl -sk -c "$cookies" -X POST "https://localhost:$ui_port/api/auth/login" \
        -H 'Content-Type: application/json' \
        -d "{\"email\":\"admin@local\",\"password\":\"$ADMIN_PASSWORD\"}" > /dev/null

    local SSH_KEY_FILE="$ROOT/test/sshtarget/keys/id_ed25519"
    [[ -f "$SSH_KEY_FILE" ]] || { warn "no SSH key at $SSH_KEY_FILE — registering nodes only"; }

    while IFS= read -r line; do
        [[ -z "$line" ]] && continue
        IFS=':' read -r f_name f_distro f_port f_agents <<<"$line"
        local cname="okesu-sshtarget-${f_name}"
        "${DOCKER_CMD[@]}" rm -f "$cname" >/dev/null 2>&1 || true
        "${DOCKER_CMD[@]}" run -d --name "$cname" --hostname "$f_name" \
            -p "${f_port}:22" "okesu-sshtarget-${f_distro}:test" >/dev/null

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
            warn "register $f_name failed: $node_resp"
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
    done <<<"$fleet"
}

log "deploying east fleet (15 nodes → :8443)"
deploy_fleet 8443 "$EAST_FLEET"

log "deploying west fleet (15 nodes → :9443)"
deploy_fleet 9443 "$WEST_FLEET"

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
