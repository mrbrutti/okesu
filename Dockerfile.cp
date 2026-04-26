# syntax=docker/dockerfile:1.6
#
# Multi-stage build for the Okesu Control Plane.
#
#   1. web-builder:  node:20 → npm ci → npm run build → controlplane/ui/dist
#   2. go-builder:   golang:1.23 → static binary that go:embeds the UI
#   3. runtime:      distroless/static — no shell, no apt, ~25 MB final image
#
# Build:
#   docker build -f Dockerfile.cp -t okesu-cp:dev .
#
# Run (single-host quickstart, self-signed cert auto-generated):
#   docker run --rm -it -p 8443:8443 -p 8444:8444 \
#     -e OKESU_CP_ADMIN_PASSWORD=changeme \
#     -e OKESU_CP_WEBHOOK_SECRET=shared-secret-1 \
#     -v okesu-cp-data:/var/lib/okesu \
#     okesu-cp:dev
#
# The data volume holds: cp.db (SQLite), CA + server certs, daemon binaries.
# Lose it and you lose user accounts + audit history.

# ─── Stage 1: web build ─────────────────────────────────────────────────────
FROM node:20-bookworm-slim AS web-builder

WORKDIR /src/web
COPY web/package.json web/package-lock.json* ./
RUN --mount=type=cache,target=/root/.npm npm ci --no-audit --no-fund

COPY web/ ./
# vite.config.ts writes to ../controlplane/ui/dist — make the parent dir exist.
RUN mkdir -p ../controlplane/ui && npm run build

# ─── Stage 2: go build ──────────────────────────────────────────────────────
FROM golang:1.25-bookworm AS go-builder

WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Copy Go sources (excluding web/ — already built into controlplane/ui/dist).
COPY agent/ ./agent/
COPY cmd/ ./cmd/
COPY controlplane/ ./controlplane/
COPY node/ ./node/
# Bring in the freshly-built UI dist — go:embed in controlplane/ui/static.go
# pulls it into the binary at compile time.
COPY --from=web-builder /src/controlplane/ui/dist ./controlplane/ui/dist

ARG VERSION=dev
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w -X github.com/section9labs/okesu/agent.buildVersion=${VERSION}" \
      -o /okesu-cp ./cmd/cp

# ─── Stage 3: runtime ───────────────────────────────────────────────────────
# distroless/static is ~2 MB and ships ca-certificates + tzdata + a non-root
# `nonroot` user (UID/GID 65532). No shell, no apt, no surface area.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=go-builder /okesu-cp /usr/local/bin/okesu-cp

# Persistent state lives here — mount a volume to survive container restarts.
# The CP creates the SQLite file, CA, and server cert on first boot.
USER nonroot:nonroot
WORKDIR /var/lib/okesu

# Defaults match what install-cp.sh / the Helm chart use, so users only need
# to override the secrets. Override with -e or values.yaml in production.
ENV OKESU_CP_LISTEN=":8443" \
    OKESU_CP_MGMT_LISTEN=":8444" \
    OKESU_CP_DB="/var/lib/okesu/cp.db" \
    OKESU_CP_DAEMON_BINARIES_DIR="/var/lib/okesu/binaries" \
    OKESU_CP_AGENT_FILES_DIR="/var/lib/okesu/agents"

EXPOSE 8443 8444

ENTRYPOINT ["/usr/local/bin/okesu-cp"]
CMD ["serve"]
