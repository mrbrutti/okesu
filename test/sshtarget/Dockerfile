# SSH target image used for end-to-end testing the Control Plane's Phase 5
# deploy flow. NOT a production image. Builds <DISTRO> with sshd, an
# operator-supplied authorized_keys, and a *mock* systemctl so deploy
# orchestration can complete without actually running systemd inside the
# container.
#
# Build via test/sshtarget/build.sh — that script generates a throwaway
# ed25519 keypair and copies the public key into the build context, then
# invokes docker build with the right --build-arg per distro:
#
#   ./build.sh                       # default: debian (single image)
#   ./build.sh --distro fedora       # → okesu-sshtarget-fedora:test
#   ./build.sh --all                 # build every supported distro
#
# Supported DISTRO values: debian | ubuntu | fedora | rocky
#
# The reason for going multi-distro is that real fleets are heterogeneous:
# `ps` formatting, package managers, default network tooling, and even
# sysctl trees differ enough between RHEL-likes and Debian-likes that the
# agent's collectors (and the LLM's reasoning) want to see all of them
# during dev. Alpine is intentionally excluded — its busybox userland
# diverges enough from coreutils that several agents fail to collect.

ARG BASE_IMAGE=debian:bookworm-slim
FROM ${BASE_IMAGE}
# Re-declare DISTRO/BASE_IMAGE so they're visible to RUN below (Docker
# scopes ARGs per-stage).
ARG DISTRO=debian
ARG BASE_IMAGE
ENV DEBIAN_FRONTEND=noninteractive

# ── Per-distro install: openssh-server + bash + procps + iproute2 + sudo
# Each block ends with the same standard layout so the rest of the
# Dockerfile is distro-agnostic.
RUN set -eux; \
    case "${DISTRO}" in \
      debian|ubuntu) \
        apt-get update && \
        apt-get install -y --no-install-recommends \
          openssh-server bash procps iproute2 sudo ca-certificates && \
        rm -rf /var/lib/apt/lists/* ;; \
      fedora) \
        dnf install -y --setopt=install_weak_deps=False \
          openssh-server bash procps-ng iproute sudo ca-certificates hostname && \
        ssh-keygen -A && \
        dnf clean all ;; \
      rocky) \
        dnf install -y --setopt=install_weak_deps=False \
          openssh-server bash procps-ng iproute sudo ca-certificates && \
        ssh-keygen -A && \
        dnf clean all ;; \
      *) echo "unsupported DISTRO=${DISTRO}" >&2; exit 1 ;; \
    esac; \
    mkdir -p /run/sshd /run/systemd/system /etc/okesu/agents \
             /var/run/okesu /var/log/okesu; \
    chmod 0755 /var/run/okesu /var/log/okesu

# Allow root login with key auth only — for testing only.
#
# Fedora/Rocky's openssh-server is built with PAM hardcoded — `UsePAM no` is
# rejected at runtime ("not supported in Fedora") and the default PAM
# `account` stack denies root logins. Replace /etc/pam.d/sshd with a
# permissive stub on those distros so root key-auth works inside the
# container. Debian/Ubuntu honour `UsePAM no` directly, so we set it
# everywhere as belt-and-braces and only patch PAM where the file exists
# AND PAM is actually consulted.
RUN sed -i 's/^#*PermitRootLogin.*/PermitRootLogin yes/'              /etc/ssh/sshd_config && \
    sed -i 's/^#*PasswordAuthentication.*/PasswordAuthentication no/' /etc/ssh/sshd_config && \
    sed -i 's/^#*PubkeyAuthentication.*/PubkeyAuthentication yes/'    /etc/ssh/sshd_config && \
    sed -i 's/^#*UsePAM.*/UsePAM no/'                                 /etc/ssh/sshd_config && \
    if [ -f /etc/pam.d/sshd ]; then \
        printf '%s\n' \
            '# test-only: bypass PAM so root key-auth works in the sshtarget container' \
            'auth     sufficient pam_permit.so' \
            'account  sufficient pam_permit.so' \
            'password sufficient pam_permit.so' \
            'session  sufficient pam_permit.so' \
            > /etc/pam.d/sshd; \
    fi && \
    mkdir -p /root/.ssh && chmod 700 /root/.ssh

COPY authorized_keys /root/.ssh/authorized_keys
RUN chmod 600 /root/.ssh/authorized_keys

# A *smart* systemctl mock. We don't actually run systemd in this container
# — that's overkill for a test fixture — but `enable --now okesu-agent@<NAME>`
# is rewritten to actually launch `/usr/local/bin/okesu daemon --agent <NAME>`
# in the background, with the same env file pickup the real unit would have.
# This lets the full Phase 5 deploy → daemon-runs → mgmt-plane-registers chain
# work end-to-end inside the container, on every supported distro.
COPY mock-systemctl.sh /usr/bin/systemctl-mock
RUN if [ -e /usr/bin/systemctl ]; then mv /usr/bin/systemctl /usr/bin/systemctl.real; fi && \
    if [ -e /bin/systemctl ];     then mv /bin/systemctl     /bin/systemctl.real;     fi && \
    chmod +x /usr/bin/systemctl-mock && \
    ln -sf /usr/bin/systemctl-mock /usr/bin/systemctl

# Identify the distro at the OS level so the operator sees it in `cat /etc/os-release`
# and the agent's collectors pick it up for reasoning context.
LABEL com.okesu.sshtarget.distro=${DISTRO}
LABEL com.okesu.sshtarget.base=${BASE_IMAGE}

EXPOSE 22

# All distros — start sshd in the foreground.
CMD ["/usr/sbin/sshd", "-D", "-e"]
