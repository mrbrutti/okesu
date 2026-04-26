# sshtarget — multi-distro deploy test fixture

Throwaway containers running sshd on Debian, Ubuntu, Fedora, or Rocky
Linux. Used by `test/stack/start.sh` to verify the Control Plane's
SSH-based node deploy flow against a heterogeneous fleet without needing
real remote hosts.

## What's in here

- `Dockerfile` — parameterized via `--build-arg DISTRO=...` plus a matching
  `BASE_IMAGE`. One Dockerfile, four distros: `debian` (default), `ubuntu`,
  `fedora`, `rocky`.
- `build.sh` — generates a test ed25519 keypair if absent, then builds one
  or more distro images. Per-distro tag: `okesu-sshtarget-<distro>:test`.
- `mock-systemctl.sh` — a smart `systemctl` shim that translates
  `enable --now okesu-agent@NAME` into actually backgrounding
  `/usr/local/bin/okesu daemon --agent NAME` so the deploy → daemon →
  mgmt-plane chain completes end-to-end inside the container.
- `keys/` — gitignored, holds the throwaway keypair generated on first build.

## Quick start

```bash
# 1. Build a single distro
./test/sshtarget/build.sh                       # default: debian
./test/sshtarget/build.sh --distro fedora
./test/sshtarget/build.sh --distro debian,fedora,rocky

# Or build everything supported
./test/sshtarget/build.sh --all

# 2. Run one (host port 18022)
docker run -d --name okesu-sshtarget-debian-test \
  -p 18022:22 okesu-sshtarget-debian:test

# 3. Sanity check SSH directly
ssh -i test/sshtarget/keys/id_ed25519 -p 18022 root@localhost \
  'cat /etc/os-release | head -3'

# 4. From the CP, register a node and deploy:
#      hostname:    localhost
#      ssh_port:    18022
#      private_key: paste contents of test/sshtarget/keys/id_ed25519

# 5. Cleanup
docker rm -f okesu-sshtarget-debian-test
```

For the full multi-distro fleet, just run `test/stack/start.sh` — it
builds, launches, registers, and auto-deploys agents on each.

## Per-distro notes

| Distro | Base image | Quirks |
|---|---|---|
| `debian` | `debian:bookworm-slim` | Smallest. The default. |
| `ubuntu` | `ubuntu:24.04`         | Nearly identical to debian; useful when you want to verify Ubuntu-specific package paths. |
| `fedora` | `fedora:40`            | sshd is built with PAM hardcoded — `UsePAM no` is ignored. The Dockerfile replaces `/etc/pam.d/sshd` with a permissive stub so root key-auth works. RHEL-style `procps-ng` and `iproute` (no `2`). |
| `rocky`  | `rockylinux:9`         | Same PAM treatment as Fedora. RHEL 9 base, smaller package set than Fedora 40. |

Alpine is intentionally not supported — its busybox userland diverges
enough from coreutils that several agents' collectors fail.

## Caveats

- **Not a production image.** `PermitRootLogin yes`, a checked-in test
  key, and a permissive PAM stub on RHEL-likes. Anything you would not
  do on a real host.
- **Mocked systemctl.** Real systemd doesn't run inside these containers
  — the shim just backgrounds the okesu daemon directly. Suitable for
  verifying that the deploy orchestrator runs every step and writes
  correct files; not for testing real systemd behaviour (signal
  handling, restart policies, journal capture).
- **Architecture.** The CP uploads the binary that matches the target's
  `uname -m`. `test/stack/start.sh` cross-compiles the daemon for
  `linux/$(uname -m)` automatically; if you build images on a different
  CPU than your container runtime expects, override `OKESU_TARGET_ARCH`
  before launching the stack.
