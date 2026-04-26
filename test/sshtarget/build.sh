#!/usr/bin/env bash
# Build the SSH target test image — single-distro or fleet.
#
# Generates a throwaway ed25519 keypair under test/sshtarget/keys/ on first
# run and reuses it on subsequent builds. The keypair is gitignored — it is
# only used to authenticate to the local test container.
#
# Usage:
#   ./build.sh                           # default: debian (okesu-sshtarget-debian:test)
#   ./build.sh --distro fedora           # one distro
#   ./build.sh --distro debian,fedora    # comma list
#   ./build.sh --all                     # every supported distro

set -euo pipefail

cd "$(dirname "$0")"

KEY_DIR="keys"
KEY_FILE="${KEY_DIR}/id_ed25519"
PUB_FILE="${KEY_FILE}.pub"
TAG_PREFIX="${OKESU_TEST_IMAGE_PREFIX:-okesu-sshtarget}"

# distro → base image. New distros added here flow through the rest.
declare -A BASE
BASE[debian]="debian:bookworm-slim"
BASE[ubuntu]="ubuntu:24.04"
BASE[fedora]="fedora:40"
BASE[rocky]="rockylinux:9"

ALL_DISTROS=(debian ubuntu fedora rocky)
DISTROS=()

while [[ $# -gt 0 ]]; do
    case "$1" in
        --distro)
            IFS=',' read -ra DISTROS <<<"$2"
            shift 2 ;;
        --all)
            DISTROS=("${ALL_DISTROS[@]}")
            shift ;;
        -h|--help)
            grep '^#' "$0" | sed 's/^# \{0,1\}//' | head -15
            exit 0 ;;
        *)
            echo "unknown flag: $1" >&2
            exit 2 ;;
    esac
done

if [[ ${#DISTROS[@]} -eq 0 ]]; then
    DISTROS=(debian)
fi

# Validate.
for d in "${DISTROS[@]}"; do
    if [[ -z "${BASE[$d]:-}" ]]; then
        echo "error: unsupported distro '$d' (try: ${ALL_DISTROS[*]})" >&2
        exit 2
    fi
done

# Pick a container runtime. Honors $DOCKER if set, otherwise tries docker /
# nerdctl / podman in that order.
if [[ -n "${DOCKER:-}" ]]; then
    # shellcheck disable=SC2206  # intentional word-splitting on $DOCKER
    DOCKER_CMD=($DOCKER)
elif command -v docker >/dev/null 2>&1; then
    DOCKER_CMD=(docker)
elif command -v nerdctl >/dev/null 2>&1; then
    DOCKER_CMD=(nerdctl)
elif command -v lima >/dev/null 2>&1; then
    DOCKER_CMD=(lima nerdctl)
elif command -v podman >/dev/null 2>&1; then
    DOCKER_CMD=(podman)
else
    echo "error: no container runtime found (tried docker, nerdctl, lima, podman)" >&2
    echo "       set \$DOCKER to override, e.g. DOCKER='lima nerdctl' ./build.sh" >&2
    exit 1
fi

mkdir -p "$KEY_DIR"

if [[ ! -f "$KEY_FILE" ]]; then
    echo "==> generating throwaway test keypair at $KEY_FILE"
    ssh-keygen -t ed25519 -f "$KEY_FILE" -N "" -C "okesu-cp-test" -q
fi

# Copy pub into build context as authorized_keys.
cp "$PUB_FILE" "authorized_keys"
trap 'rm -f authorized_keys' EXIT

for d in "${DISTROS[@]}"; do
    base="${BASE[$d]}"
    tag="${TAG_PREFIX}-${d}:test"
    echo "==> building $tag (base=$base, runtime=${DOCKER_CMD[*]})"
    "${DOCKER_CMD[@]}" build \
        --build-arg "DISTRO=$d" \
        --build-arg "BASE_IMAGE=$base" \
        -t "$tag" \
        .
done

# Ergonomic message — adapt for one or many distros.
if [[ ${#DISTROS[@]} -eq 1 ]]; then
    d="${DISTROS[0]}"
    tag="${TAG_PREFIX}-${d}:test"
    cat <<EOF

✓ Built $tag.

To start the target on port 18022:

  ${DOCKER_CMD[*]} run -d --name okesu-sshtarget-${d}-test -p 18022:22 $tag

Verify:

  ssh -i $(pwd)/$KEY_FILE -p 18022 root@localhost 'cat /etc/os-release | head -3'

When done:

  ${DOCKER_CMD[*]} rm -f okesu-sshtarget-${d}-test
EOF
else
    echo
    echo "✓ Built ${#DISTROS[@]} images:"
    for d in "${DISTROS[@]}"; do
        echo "  - ${TAG_PREFIX}-${d}:test  (base ${BASE[$d]})"
    done
    echo
    echo "Use test/stack/start.sh to launch them as a fleet."
fi
