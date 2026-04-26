# test/stack — full local Okesu stack

One-command demo bringing up every Phase 1–11 component side by side. Open
the browser, drive the UI, see the whole system work.

## What you get

| Process | Where | Purpose |
|---|---|---|
| **Control Plane** | local Go process | UI / API / webhook / mgmt-plane / tunnel server |
| **sshtarget fleet** | 3 Docker containers | Heterogeneous deploy targets — Debian, Fedora, Rocky |
| **okesu node** | local Go process | Phase 6 reverse tunnel client (`node-demo`) |
| **edr-demo daemon** | local Go process | registers via mTLS, heartbeats, posts webhook events |

Plus 5 seeded finding events so the Findings dashboard isn't empty.

## Fleet topology

start.sh launches three sshtarget containers — one per Linux flavour — and
auto-registers + auto-deploys an agent set on each. The default mix:

| Node | Distro | SSH port | Agents deployed |
|---|---|---|---|
| `node-debian` | `debian:bookworm-slim` | 18022 | `edr` |
| `node-fedora` | `fedora:40`            | 18023 | `instance-threat` |
| `node-rocky`  | `rockylinux:9`         | 18024 | `instance-integrity`, `sre-health` |

Why three different distros: collectors and the LLM's reasoning context
differ enough between Debian-likes (apt, procps, iproute2 default tools)
and RHEL-likes (dnf, procps-ng, slightly different `ps` output) that the
fleet view is a much better demo with both kinds present.

> **Agent names must be unique across the fleet** — the CP's `agents`
> table uses the agent name as primary key, so two daemons named `edr`
> on different hosts collide and only one stays visible. The default
> mix above is collision-free; if you customise `OKESU_FLEET`, follow
> the same rule (or rename one of the duplicates).

To customise, set `OKESU_FLEET` (newline- or `|`-separated, same shape):

```bash
OKESU_FLEET='\
node-a:debian:18022:edr|\
node-b:fedora:18023:instance-threat|\
node-c:rocky:18024:sre-health' ./start.sh
```

## Prerequisites

- Go 1.23+
- Node.js 18+ (only on first run, to build the UI)
- A container runtime (`docker`, `nerdctl`, `lima nerdctl`, or `podman`) for the sshtarget. Optional — start.sh skips it if absent.
- `ANTHROPIC_API_KEY` exported, **only** if you want the demo daemon to actually call the LLM. Without it the daemon is skipped, but everything else still works.

## Start

```bash
cd test/stack
./start.sh
```

Open <https://localhost:8443>, accept the self-signed cert, log in:

- **Email:** `admin@local`
- **Password:** `okesu-demo` (override with `OKESU_ADMIN_PASSWORD`)

## What to try

| Page | What's there | What to do |
|---|---|---|
| **Findings** | 5 seeded findings + anything edr-demo flags | Click into one, ack it, watch the counts update |
| **Live Events** | Live SSE stream of every webhook event | Re-trigger by editing the seed loop in start.sh |
| **Agents** | `edr-demo` (if ANTHROPIC_API_KEY set) | Click in, change `max_turns`, watch the daemon hot-apply on next poll |
| **Nodes** | `sshtarget-demo` pre-registered | Click **Deploy**, paste contents of `../sshtarget/keys/id_ed25519`, watch live deploy logs |
| **Run Agent** | `node-demo` connected | Pick the node, type a prompt, watch JSONL events stream live |

## Override knobs

Environment variables read by `start.sh`:

| Var | Default | Meaning |
|---|---|---|
| `OKESU_ADMIN_PASSWORD` | `okesu-demo` | Initial admin password |
| `OKESU_WEBHOOK_SECRET` | `demo-shared-1` | HMAC secret for webhook ingestion |
| `OKESU_UI_PORT` | `8443` | UI / webhook port |
| `OKESU_MGMT_PORT` | `8444` | mgmt + tunnel port |
| `OKESU_FLEET` | the 3-row default above | Newline- or `|`-separated `name:distro:ssh_port:agents` rows |
| `DOCKER` | autodetect | Container runtime command (e.g. `lima nerdctl`) |
| `ANTHROPIC_API_KEY` | (unset) | If set, the edr-demo daemon spawns and uses this key |

## Layout

```
test/stack/
├── start.sh         # bring everything up
├── stop.sh          # tear it down
├── README.md        # this file
├── run/             # gitignored — logs, PIDs, the SQLite DB, generated certs
├── certs/           # gitignored — issued mTLS bundles for edr-demo and node-demo
└── .claude/agents/  # gitignored — the synthesized edr-demo agent file
```

## Stop

```bash
./stop.sh           # graceful — kills processes, removes the sshtarget container
./stop.sh --hard    # also wipes run/, certs/, .claude/ for a fresh start.sh
```

## Troubleshooting

**"port already in use"** — change `OKESU_UI_PORT` and friends.

**"no container runtime found"** — set `DOCKER='lima nerdctl'` (or whatever you use). Without one, the sshtarget step is skipped and the Nodes page will be empty.

**Browser won't load** — accept the self-signed cert. The CP auto-generates a fresh one in `test/stack/run/server.crt` on first start.

**"403 forbidden: requires operator"** — by default the seeded admin has `admin` role and can do everything. If you've been mucking with roles, downgrade in the DB:

```bash
sqlite3 test/stack/run/cp.db "UPDATE users SET role='admin' WHERE email='admin@local';"
```

**Changes to Go / TS code** — re-run `start.sh`. It rebuilds binaries when the timestamps change. To force a rebuild, `rm okesu okesu-cp` from the repo root first.
