#!/usr/bin/env bash
#
# Bring up a full local Okesu stack for end-to-end exploration:
#
#   ┌─────────────────────────┐   webhook events   ┌──────────────┐
#   │ Control Plane           │ ←───────────────── │ daemon agent │
#   │   :8443 UI/webhook      │   mTLS register    │ (local proc) │
#   │   :8444 mgmt + tunnel   │ ←───────────────── │              │
#   └─────────────────────────┘                    └──────────────┘
#                  ▲   tunnel WS+mTLS
#                  │
#                  │           SSH 18022
#                  │      ┌───────────────┐
#                  │      │ sshtarget     │
#                  │      │ (Docker)      │
#                  │      └───────────────┘
#                  │
#                  │   reverse tunnel
#           ┌──────┴────────┐
#           │ okesu node    │
#           │ (local proc)  │
#           └───────────────┘
#
# After ./start.sh returns, open https://localhost:8443 (accept the self-signed
# cert) and log in as admin@local / okesu-demo. Drive the UI through:
#
#   - Live Events:  populated by the demo daemon's webhook output
#   - Findings:     curl helpers below seed a few demo findings
#   - Agents:       the demo daemon registers + heartbeats via mTLS
#   - Nodes:        the sshtarget container is registered; click Deploy to bootstrap it
#   - Run Agent:    the demo node tunnel is connected; pick it and try a prompt
#
# To stop: ./stop.sh

set -euo pipefail

cd "$(dirname "$0")"
ROOT="$(cd ../.. && pwd)"
STACK_DIR="$(pwd)"
RUN_DIR="$STACK_DIR/run"
CERTS_DIR="$STACK_DIR/certs"

mkdir -p "$RUN_DIR" "$CERTS_DIR"

# ── Configuration (override via env vars) ─────────────────────────────────────
ADMIN_PASSWORD="${OKESU_ADMIN_PASSWORD:-okesu-demo}"
WEBHOOK_SECRET="${OKESU_WEBHOOK_SECRET:-demo-shared-1}"
UI_PORT="${OKESU_UI_PORT:-8443}"
MGMT_PORT="${OKESU_MGMT_PORT:-8444}"

# Fleet definition — each line is "name:distro:ssh_port:agent1,agent2,...".
# Override entirely via OKESU_FLEET (same format, newlines or "|" separated).
#
# Default mix exercises three different package managers and ships agents
# whose collectors / RBAC differ enough to produce visibly different
# telemetry in the dashboard:
#   - debian → edr (process scanning, broad allowlist)
#   - fedora → instance-threat (RPM-based, IMDS / privilege-escalation focus)
#   - rocky  → instance-integrity + sre-health (FIM + service health)
#
# IMPORTANT — agent names must be unique across the fleet. The CP's
# `agents` table uses the agent name as the primary key, so two daemons
# both named e.g. "edr" on different hosts collide and only the
# last-heartbeating one stays visible. Pick a different agent file per
# host or rename one of the duplicates before deploying.
#
# Add a row by following the same shape. The agents column is comma-separated;
# each agent name must match a *.md file in examples/agents/.
DEFAULT_FLEET="\
node-debian:debian:18022:edr
node-fedora:fedora:18023:instance-threat
node-rocky:rocky:18024:instance-integrity,sre-health"
FLEET_RAW="${OKESU_FLEET:-$DEFAULT_FLEET}"
# Normalize: allow "|" as a separator alongside newlines.
FLEET_RAW="${FLEET_RAW//|/$'\n'}"

# Hostname containers can use to reach the macOS / Linux host running the CP.
#
#   - Lima nerdctl exposes the host as `host.lima.internal`
#   - Docker Desktop on macOS/Windows exposes it as `host.docker.internal`
#   - Linux Docker Desktop / colima both work via `host.docker.internal`
#
# Override with OKESU_PUBLIC_HOST if your runtime uses something different.
PUBLIC_HOST="${OKESU_PUBLIC_HOST:-host.lima.internal}"
WEBHOOK_PUBLIC_URL="${OKESU_WEBHOOK_PUBLIC_URL:-https://$PUBLIC_HOST:$UI_PORT/api/webhooks/events}"
MGMT_PUBLIC_URL="${OKESU_MGMT_PUBLIC_URL:-https://$PUBLIC_HOST:$MGMT_PORT}"

# ── Pre-flight ────────────────────────────────────────────────────────────────
have() { command -v "$1" >/dev/null 2>&1; }
log()  { printf '\n\033[1;36m▸ %s\033[0m\n' "$*"; }
warn() { printf '\033[1;33m! %s\033[0m\n' "$*"; }
fail() { printf '\033[1;31m✗ %s\033[0m\n' "$*"; exit 1; }

# Source the operator's ~/.config/.env so users who keep ANTHROPIC_API_KEY
# (and friends) in a dotenv file don't have to remember to export them.
# Explicit shell exports still win — we only set what isn't already in env.
ENV_FILE="${OKESU_DOTENV:-$HOME/.config/.env}"
if [[ -f "$ENV_FILE" ]]; then
    while IFS='=' read -r k v; do
        [[ -z "$k" || "$k" == \#* || "$k" == *' '* ]] && continue
        # Strip optional surrounding quotes from the value.
        v="${v%\"}"; v="${v#\"}"; v="${v%\'}"; v="${v#\'}"
        if [[ -z "${!k:-}" ]]; then
            export "$k=$v"
        fi
    done <"$ENV_FILE"
fi

# Build host binaries for the local CP and the local node tunnel client.
[[ -f "$ROOT/okesu" && -f "$ROOT/okesu-cp" ]] || {
    log "building host binaries (okesu, okesu-cp)"
    ( cd "$ROOT" && go build -o okesu ./cmd/okesu && go build -o okesu-cp ./cmd/cp )
}

# Build a Linux daemon binary the CP can deploy onto containers/VMs.
# Only build the architecture matching the runtime — for Lima/Docker on
# Apple Silicon that's arm64; for Intel hosts use amd64. Override with
# OKESU_TARGET_ARCH if you need a different target.
TARGET_ARCH="${OKESU_TARGET_ARCH:-$(uname -m)}"
case "$TARGET_ARCH" in
    arm64|aarch64) GO_TARGET_ARCH=arm64 ;;
    x86_64|amd64)  GO_TARGET_ARCH=amd64 ;;
    *) fail "unsupported OKESU_TARGET_ARCH=$TARGET_ARCH (set to arm64 or amd64)";;
esac
DAEMON_LINUX_BIN="$ROOT/okesu-linux-${GO_TARGET_ARCH}"
[[ -f "$DAEMON_LINUX_BIN" ]] || {
    log "cross-compiling daemon for linux/$GO_TARGET_ARCH"
    ( cd "$ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH=$GO_TARGET_ARCH \
        go build -o "$DAEMON_LINUX_BIN" ./cmd/okesu )
}

# Stage the cross-compiled binary in the multi-arch directory the CP scans
# at boot. A new arch can be added by dropping another okesu-linux-<arch>
# file into this directory (or by uploading via Settings → Deploy in the UI).
DAEMON_BIN_DIR="$RUN_DIR/binaries"
mkdir -p "$DAEMON_BIN_DIR"
cp "$DAEMON_LINUX_BIN" "$DAEMON_BIN_DIR/okesu-linux-${GO_TARGET_ARCH}"
[[ -f "$ROOT/controlplane/ui/dist/index.html" ]] || {
    log "building web UI"
    have npm || fail "npm required to build the web UI"
    ( cd "$ROOT/web" && npm install --silent && npm run build )
}

# Build a demo-paced copy of every agent file with the production intervals
# (2m / 3m / 10m / cron …) replaced by 30 s. Without this, fleet daemons in
# the test stack look frozen — a 10-minute `sre-health` cadence is fine in
# production but useless when an operator wants to see ticks land in the UI.
# The CP serves these to the deploy flow via --agent-files-dir, so each
# fleet member ends up with its own copy in /etc/okesu/agents/.
DEMO_AGENT_FILES_DIR="$RUN_DIR/agents"
mkdir -p "$DEMO_AGENT_FILES_DIR"
for f in "$ROOT"/examples/agents/*.md; do
    [[ -f "$f" ]] || continue
    # 1) Replace any `interval: <N><unit>` with 30s.
    # 2) Comment out `cron:` lines (interval wins when both present).
    sed -E \
        -e 's/^(interval:[[:space:]]*).*/\130s/' \
        -e 's/^(cron:[[:space:]]*.*)/# DEMO: \1/' \
        "$f" > "$DEMO_AGENT_FILES_DIR/$(basename "$f")"
done

if [[ -n "${DOCKER:-}" ]]; then
    DOCKER_CMD=($DOCKER)
elif have docker; then
    DOCKER_CMD=(docker)
elif have nerdctl; then
    DOCKER_CMD=(nerdctl)
elif have lima; then
    DOCKER_CMD=(lima nerdctl)
else
    DOCKER_CMD=()
fi

# ── 1. Start the Control Plane ────────────────────────────────────────────────
log "starting Control Plane on https://localhost:$UI_PORT"
rm -f "$RUN_DIR/cp.db" "$RUN_DIR/cp.db-"* "$RUN_DIR"/*.crt "$RUN_DIR"/*.key 2>/dev/null || true
"$ROOT/okesu-cp" serve \
    --db "$RUN_DIR/cp.db" \
    --listen ":$UI_PORT" \
    --mgmt-listen ":$MGMT_PORT" \
    --admin-password "$ADMIN_PASSWORD" \
    --webhook-secret "$WEBHOOK_SECRET" \
    --daemon-binary "$DAEMON_LINUX_BIN" \
    --daemon-binaries-dir "$DAEMON_BIN_DIR" \
    --agent-files-dir "$DEMO_AGENT_FILES_DIR" \
    --webhook-public-url "$WEBHOOK_PUBLIC_URL" \
    --mgmt-public-url "$MGMT_PUBLIC_URL" \
    > "$RUN_DIR/cp.log" 2>&1 &
echo $! > "$RUN_DIR/cp.pid"
sleep 2
if ! kill -0 "$(cat "$RUN_DIR/cp.pid")" 2>/dev/null; then
    fail "Control Plane failed to start. See $RUN_DIR/cp.log"
fi

# ── 2. Issue mTLS certs for the demo daemon and demo node ─────────────────────
log "issuing mTLS certs"
rm -rf "$CERTS_DIR/edr-demo" "$CERTS_DIR/node-demo"
"$ROOT/okesu-cp" issue-cert      --agent edr-demo  --out "$CERTS_DIR/edr-demo"  --db "$RUN_DIR/cp.db" 2>&1 | sed 's/^/  /'
"$ROOT/okesu-cp" issue-node-cert --node  node-demo --out "$CERTS_DIR/node-demo" --db "$RUN_DIR/cp.db" 2>&1 | sed 's/^/  /'

# ── 3. Start the multi-distro sshtarget fleet ─────────────────────────────────
# Parse FLEET_RAW into parallel arrays so we can iterate it multiple times
# (start, register, deploy, summarize) without re-parsing.
FLEET_NAMES=()
FLEET_DISTROS=()
FLEET_PORTS=()
FLEET_AGENTS=()
while IFS= read -r line; do
    line="${line## }"; line="${line%% }"
    [[ -z "$line" || "$line" == \#* ]] && continue
    IFS=':' read -r f_name f_distro f_port f_agents <<<"$line"
    [[ -z "$f_name" || -z "$f_distro" || -z "$f_port" || -z "$f_agents" ]] && \
        fail "bad fleet row: '$line' (expected name:distro:port:agent1,agent2)"
    FLEET_NAMES+=("$f_name")
    FLEET_DISTROS+=("$f_distro")
    FLEET_PORTS+=("$f_port")
    FLEET_AGENTS+=("$f_agents")
done <<<"$FLEET_RAW"

if (( ${#DOCKER_CMD[@]} == 0 )); then
    warn "no container runtime found — skipping fleet. Set \$DOCKER to use lima/podman/etc."
    FLEET_NAMES=()  # nothing to launch downstream
fi

# Build any missing distro images up-front so the start loop is fast.
NEEDED_DISTROS=()
for d in "${FLEET_DISTROS[@]:-}"; do
    [[ -z "$d" ]] && continue
    seen=0
    for s in "${NEEDED_DISTROS[@]:-}"; do [[ "$s" == "$d" ]] && seen=1; done
    (( seen )) || NEEDED_DISTROS+=("$d")
done

if (( ${#DOCKER_CMD[@]} > 0 && ${#NEEDED_DISTROS[@]} > 0 )); then
    MISSING=()
    for d in "${NEEDED_DISTROS[@]}"; do
        if ! "${DOCKER_CMD[@]}" image inspect "okesu-sshtarget-${d}:test" >/dev/null 2>&1; then
            MISSING+=("$d")
        fi
    done
    if (( ${#MISSING[@]} > 0 )); then
        log "building sshtarget images (first run): ${MISSING[*]}"
        DOCKER="${DOCKER_CMD[*]}" "$STACK_DIR/../sshtarget/build.sh" \
            --distro "$(IFS=,; echo "${MISSING[*]}")" \
            >>"$RUN_DIR/sshtarget-build.log" 2>&1 || \
            fail "sshtarget build failed — see $RUN_DIR/sshtarget-build.log"
    fi
fi

# Launch one container per fleet entry. Container names use a uniform
# `okesu-sshtarget-<node-name>` prefix so stop.sh can reap them all.
for i in "${!FLEET_NAMES[@]}"; do
    name="${FLEET_NAMES[$i]}"
    distro="${FLEET_DISTROS[$i]}"
    port="${FLEET_PORTS[$i]}"
    cname="okesu-sshtarget-${name}"
    log "starting fleet member ${name} (distro=${distro}, ssh=:${port})"
    "${DOCKER_CMD[@]}" rm -f "$cname" >/dev/null 2>&1 || true
    "${DOCKER_CMD[@]}" run -d --name "$cname" \
        -p "${port}:22" "okesu-sshtarget-${distro}:test" >/dev/null
done

# Give every container a beat to finish sshd init before deploys try to dial it.
if (( ${#FLEET_NAMES[@]} > 0 )); then
    sleep 2
fi

# ── 4. Start the okesu node tunnel client ─────────────────────────────────────
log "starting okesu node tunnel client (name=node-demo)"
"$ROOT/okesu" node \
    --cp-url "https://localhost:$MGMT_PORT" \
    --cert-dir "$CERTS_DIR/node-demo" \
    --name node-demo \
    > "$RUN_DIR/node.log" 2>&1 &
echo $! > "$RUN_DIR/node.pid"

# ── 5. Build a demo daemon agent file with mgmt + webhook wired up ────────────
DEMO_AGENT_DIR="$STACK_DIR/.claude/agents"
mkdir -p "$DEMO_AGENT_DIR"
cat > "$DEMO_AGENT_DIR/edr-demo.md" <<EOF
---
name: edr-demo
provider: claude
model: claude-haiku-4-5-20251001
# (no effort — haiku does not support extended-thinking effort)
maxTurns: 8

mode: daemon
interval: 30s

stateDir: $RUN_DIR/edr-demo-state
dedupeTtl: 1h

tools: [bash, read_file, list_files]

actions:
  rbac:
    allow:
      - tool: bash
      - tool: read_file
      - tool: list_files

# Lightweight collectors that work on macOS too.
collectors:
  - name: processes
    command: "ps -A -o pid,user,%cpu,comm | head -30"
    timeout: 5s
  - name: uptime
    command: "uptime"
    timeout: 3s

outputs:
  - type: stdout
  - type: webhook
    url: "https://localhost:$UI_PORT/api/webhooks/events"
    secret: "$WEBHOOK_SECRET"
    retries: 2
    bufferCap: 256
    insecureSkipVerify: true   # demo only — CP's user-facing cert is self-signed

management:
  url: "https://localhost:$MGMT_PORT"
  certDir: "$CERTS_DIR/edr-demo"
  heartbeatSec: 15
  pollSec: 30
---

You are a DEMO endpoint detection agent on host {{.HostID}}.
Tick {{.Tick}} | Time {{.TickTime}} | Last run {{.LastRunISO}}

{{range .CollectorsList}}
### {{.Name}}
\`\`\`
{{.Output}}
\`\`\`
{{end}}

This is a demonstration tick. Respond with the single word \`CLEAR\`.
Do not call any tools.
EOF

# ── 6. Start the demo daemon (registers + heartbeats + posts webhooks) ────────
# We start the daemon regardless of whether ANTHROPIC_API_KEY is set: when
# absent, every tick still emits tick_start / collector_result / tick_done /
# api_unavailable events through the webhook, so the Live Events timeline
# stays alive. With a key, you also get real LLM ticks + findings.
log "starting demo daemon (edr-demo) — webhook + mgmt plane wired"
pushd "$STACK_DIR" >/dev/null
"$ROOT/okesu" daemon --agent edr-demo \
    > "$RUN_DIR/daemon.log" 2>&1 &
echo $! > "$RUN_DIR/daemon.pid"
popd >/dev/null
if [[ -z "${ANTHROPIC_API_KEY:-}" ]]; then
    warn "ANTHROPIC_API_KEY not set — daemon will emit api_unavailable each tick."
    warn "  export ANTHROPIC_API_KEY=sk-ant-… and re-run for real findings."
fi

# ── 7. Seed a few demo finding events so the dashboard isn't empty ────────────
log "seeding demo finding events"
seed_finding() {
    # All fields stay as strings to match the webhook wire contract.
    # Multi-line evidence is split into a list by the UI on \n.
    local sev="$1" title="$2" resource="$3" evidence="$4" recommended="$5" dedup="$6"
    local ts
    ts=$(python3 -c 'import time; print(int(time.time()*1000))' 2>/dev/null || date +%s)000
    local body
    body=$(SEV="$sev" TITLE="$title" RESOURCE="$resource" EVIDENCE="$evidence" REC="$recommended" DEDUP="$dedup" \
      python3 -c "
import json, os
print(json.dumps({
    'type': 'finding',
    'ts': $ts,
    'agent': 'edr-demo',
    'host': 'prod-web-01',
    'severity': os.environ['SEV'],
    'title': os.environ['TITLE'],
    'resource': os.environ['RESOURCE'],
    'evidence': os.environ['EVIDENCE'],
    'recommended_action': os.environ['REC'],
    'dedup_key': os.environ['DEDUP'],
}))
")
    local sig
    sig=$(printf '%s' "$body" | openssl dgst -sha256 -hmac "$WEBHOOK_SECRET" -hex | awk '{print $NF}')
    curl -sk -X POST "https://localhost:$UI_PORT/api/webhooks/events" \
        -H 'Content-Type: application/x-ndjson' \
        -H "X-Okesu-Signature: sha256=$sig" \
        -H 'X-Okesu-Agent: edr-demo' \
        -H 'X-Okesu-Host: prod-web-01' \
        -d "$body" > /dev/null
    sleep 0.05
}

# CRITICAL — memfd process indicates fileless malware
seed_finding CRITICAL "Memfd process detected (no disk-backed executable)" "pid:1337" \
  $'1337 root      0.5  python3 /memfd:exploit (deleted)\nESTABLISHED tcp 1337 -> 45.83.91.22:8080\n/proc/1337/exe -> /memfd:1337 (deleted)' \
  'Isolate the host from the network and capture process memory before terminating. The exec path indicates the binary was loaded directly into memory, a classic fileless-malware technique.' \
  'pid:1337+memfd'

# HIGH — SSH from a source not on the allowlist
seed_finding HIGH "SSH login from unexpected source" "ip:1.2.3.4" \
  $'sshd[2381]: Accepted publickey for root from 1.2.3.4 port 51234 ssh2\n1.2.3.4 not in CIDR allowlist (10.0.0.0/8, 172.16.0.0/12)' \
  'Verify whether 1.2.3.4 is an authorized administrator IP. If not, rotate the SSH keys for the root account and review recent audit logs for actions taken during this session.' \
  'ip:1.2.3.4+ssh-root'

# HIGH — new setuid binary in a writable location
seed_finding HIGH "New setuid binary in writable directory" "path:/tmp/.x/sudo" \
  $'-rwsr-xr-x 1 root root 184072 /tmp/.x/sudo\nsha256: 7b3e...cd02 (does not match system /usr/bin/sudo)\ncreated: just now' \
  'Remove the binary, audit /tmp for other suspicious files, and review the bash history of users who recently logged in. A setuid binary placed in a writable directory is a classic privilege escalation staging artifact.' \
  'path:/tmp/.x/sudo'

# MEDIUM — world-writable cron file
seed_finding MEDIUM "World-writable cron file" "path:/etc/cron.d/maintenance" \
  '-rw-rw-rw- 1 root root 285 /etc/cron.d/maintenance' \
  'Restore mode 0644 on /etc/cron.d/maintenance. World-writable cron files allow any local user to schedule arbitrary code execution as root.' \
  'path:/etc/cron.d/maintenance'

# LOW — stale package mirror, mostly informational
seed_finding LOW "Apt mirror has not been updated in 14 days" "host:apt.example.com" \
  $'last update 14d ago\napt-get update returned 304 Not Modified' \
  'Check whether apt.example.com is being updated. Stale mirrors cause delayed security patches but no immediate risk.' \
  'apt-stale'

# Plus a duplicate of the CRITICAL finding from a few minutes ago,
# so the "Related occurrences" section in the drawer has content.
seed_finding CRITICAL "Memfd process detected (no disk-backed executable)" "pid:1337" \
  $'(prior occurrence at 09:14:22)\n1337 root      0.5  python3 /memfd:exploit (deleted)' \
  'Isolate the host from the network and capture process memory before terminating.' \
  'pid:1337+memfd'

# ── 8. Register every fleet member, then trigger deploy on each ───────────────
SSH_KEY_FILE="$ROOT/test/sshtarget/keys/id_ed25519"
if (( ${#FLEET_NAMES[@]} > 0 && ${#DOCKER_CMD[@]} > 0 )); then
    log "registering ${#FLEET_NAMES[@]} fleet member(s) with the Control Plane"
    curl -sk -c "$RUN_DIR/cookies.txt" -X POST "https://localhost:$UI_PORT/api/auth/login" \
        -H 'Content-Type: application/json' \
        -d "{\"email\":\"admin@local\",\"password\":\"$ADMIN_PASSWORD\"}" > /dev/null

    if [[ ! -f "$SSH_KEY_FILE" ]]; then
        warn "SSH key $SSH_KEY_FILE not found — fleet members will be registered but auto-deploy will be skipped."
    fi

    for i in "${!FLEET_NAMES[@]}"; do
        name="${FLEET_NAMES[$i]}"
        distro="${FLEET_DISTROS[$i]}"
        port="${FLEET_PORTS[$i]}"
        agents_csv="${FLEET_AGENTS[$i]}"

        # Register the node. Use the public PUBLIC_HOST so deploys reach the
        # Docker-mapped port from inside the daemon's webhook output.
        node_payload=$(python3 -c "
import json, sys
print(json.dumps({
  'name': '$name',
  'hostname': 'localhost',
  'ssh_user': 'root',
  'ssh_port': $port,
  'notes': 'Auto-fleet member — distro=$distro, agents=$agents_csv'
}))")
        node_resp=$(curl -sk -b "$RUN_DIR/cookies.txt" -X POST \
            "https://localhost:$UI_PORT/api/nodes" \
            -H 'Content-Type: application/json' \
            -d "$node_payload")
        node_id=$(printf '%s' "$node_resp" | python3 -c 'import json,sys;print(json.load(sys.stdin).get("id",""))' 2>/dev/null || true)
        if [[ -z "$node_id" ]]; then
            warn "could not register $name — skipping deploy. Server said: $node_resp"
            continue
        fi

        # Trigger deploy. The CP returns a job_id immediately; we don't wait.
        # Operators see deploy progress in the UI's Nodes page (live SSE log).
        # ANTHROPIC_API_KEY / OPENAI_API_KEY are forwarded into each daemon's
        # /etc/okesu/agents/<name>.env so the deployed agents can actually
        # call the LLM. Without them the agent ticks emit `api_unavailable`.
        if [[ -f "$SSH_KEY_FILE" ]]; then
            ANTHROPIC_KEY="${ANTHROPIC_API_KEY:-}"
            OPENAI_KEY="${OPENAI_API_KEY:-}"
            export AGENTS_CSV="$agents_csv" SSH_KEY_FILE ANTHROPIC_KEY OPENAI_KEY
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
            deploy_resp=$(curl -sk -b "$RUN_DIR/cookies.txt" -X POST \
                "https://localhost:$UI_PORT/api/nodes/$node_id/deploy" \
                -H 'Content-Type: application/json' \
                -d "$deploy_payload")
            job_id=$(printf '%s' "$deploy_resp" | python3 -c 'import json,sys;print(json.load(sys.stdin).get("job_id",""))' 2>/dev/null || true)
            if [[ -n "$job_id" ]]; then
                if [[ -z "$ANTHROPIC_KEY" && -z "$OPENAI_KEY" ]]; then
                    log "  $name: deploy queued (job=$job_id, agents=[$agents_csv]) — ⚠ no API key, ticks will 401"
                else
                    log "  $name: deploy queued (job=$job_id, agents=[$agents_csv])"
                fi
            else
                warn "  $name: deploy POST failed — $deploy_resp"
            fi
        fi
    done
fi

# ── Done ──────────────────────────────────────────────────────────────────────
DAEMON_PID="$(cat "$RUN_DIR/daemon.pid" 2>/dev/null || echo "(skipped — no ANTHROPIC_API_KEY)")"

printf '\n\033[1;32m✓ Stack is up.\033[0m\n\n'
cat <<EOF
  UI:         https://localhost:$UI_PORT
  Login:      admin@local / $ADMIN_PASSWORD
  Webhook:    https://localhost:$UI_PORT/api/webhooks/events  (HMAC: $WEBHOOK_SECRET)
  Mgmt:       https://localhost:$MGMT_PORT  (mTLS)
  Public host (containers reach CP at): $PUBLIC_HOST
  Tunnel:     node-demo connected → try Run Agent in the UI
EOF
if (( ${#FLEET_NAMES[@]} > 0 )); then
    printf '  Fleet:      %d node(s) — distros + ports below (SSH key: %s)\n' \
           "${#FLEET_NAMES[@]}" "$SSH_KEY_FILE"
    for i in "${!FLEET_NAMES[@]}"; do
        printf '              · %-12s  %-7s  ssh=:%-5s  agents=%s\n' \
               "${FLEET_NAMES[$i]}" "${FLEET_DISTROS[$i]}" "${FLEET_PORTS[$i]}" \
               "${FLEET_AGENTS[$i]}"
    done
fi
cat <<EOF

  Logs:
    $RUN_DIR/cp.log
    $RUN_DIR/node.log
    $RUN_DIR/daemon.log

  PIDs:
    $RUN_DIR/cp.pid       $(cat "$RUN_DIR/cp.pid"     2>/dev/null)
    $RUN_DIR/node.pid     $(cat "$RUN_DIR/node.pid"   2>/dev/null)
    $RUN_DIR/daemon.pid   $DAEMON_PID

To stop everything:  ./stop.sh
EOF
