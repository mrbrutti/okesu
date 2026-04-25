FROM golang:1.23-bookworm AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /okesu .

# ─── Runtime ────────────────────────────────────────────────────────────────
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates procps iproute2 findutils coreutils util-linux \
    && rm -rf /var/lib/apt/lists/*

# Create the okesu user and directories matching the systemd unit expectations.
RUN useradd --system --create-home okesu \
    && mkdir -p /etc/okesu/agents \
                /var/lib/okesu \
                /var/log/okesu \
    && chown -R okesu:okesu /var/lib/okesu /var/log/okesu /etc/okesu

COPY --from=builder /okesu /usr/local/bin/okesu

# Install all sample agent files.
COPY examples/agents/ /etc/okesu/agents/
# Also place them where ParseAgentFile searches (workdir-relative .claude/agents/).
RUN mkdir -p /home/okesu/.claude/agents \
    && cp /etc/okesu/agents/*.md /home/okesu/.claude/agents/ \
    && chown -R okesu:okesu /home/okesu

USER okesu
WORKDIR /home/okesu

# The API key must be injected at runtime via -e or --env-file.
# ANTHROPIC_API_KEY=sk-ant-...

ENTRYPOINT ["okesu"]
CMD ["daemon", "--agent", "edr"]
