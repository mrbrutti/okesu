# Build the okesu daemon and Control Plane with version-stamped binaries.
#
# VERSION resolves to:
#   - the GIT_TAG when the working tree is clean and points at a tag
#   - <tag>+<short-sha>[-dirty] otherwise
#   - "dev-<short-sha>[-dirty]" when no tag is reachable
#
# The version is injected via -ldflags into:
#   github.com/section9labs/okesu/agent.buildVersion         (daemon binary)
#   github.com/section9labs/okesu/controlplane.buildVersion  (CP binary)
#
# Both are reported through normal channels: daemon → heartbeat →
# agents.version on the CP side; CP → /api/system/about and boot log.

GIT_TAG    := $(shell git describe --tags --abbrev=0 2>/dev/null)
GIT_DESC   := $(shell git describe --tags --always --dirty --abbrev=8 2>/dev/null)
VERSION    ?= $(if $(GIT_DESC),$(GIT_DESC),dev)
LDFLAGS    := -X github.com/section9labs/okesu/agent.buildVersion=$(VERSION) \
              -X github.com/section9labs/okesu/controlplane.buildVersion=$(VERSION)

# ── Cross-compile matrix ────────────────────────────────────────────
#
# DAEMON_TARGETS is the full list of (GOOS, GOARCH) pairs the okesu
# daemon ships for. This binary IS the `okesu` CLI plus the `daemon`
# and `node` subcommands — one binary per platform serves both roles.
#
# CP_TARGETS is narrower: the Control Plane is server software, so we
# ship it for linux + darwin only. Adding Windows would require
# revisiting the SSH-deploy + systemd assumptions baked into the
# install scripts.
#
# Output convention: `dist/binaries/okesu-<os>-<arch>` for daemons,
# `dist/cp/okesu-cp-<os>-<arch>` for CPs. The CP discovers daemon
# binaries by this exact filename pattern (api/deploy.go:109).

DAEMON_TARGETS := \
	linux/amd64 \
	linux/arm64 \
	linux/386 \
	linux/arm \
	darwin/amd64 \
	darwin/arm64 \
	windows/amd64 \
	windows/arm64 \
	freebsd/amd64 \
	openbsd/amd64 \
	solaris/amd64 \
	illumos/amd64

# Solaris-family targets above:
#   - solaris/amd64 — Oracle Solaris 11.4+ (the only Solaris arch
#     upstream Go supports; SPARC is not in the Go runtime).
#   - illumos/amd64 — the open-source descendants (OmniOS,
#     OpenIndiana, SmartOS, Tribblix). Same chip as solaris but a
#     separate GOOS so the runtime picks the right syscall vector.

CP_TARGETS := \
	linux/amd64 \
	linux/arm64 \
	darwin/amd64 \
	darwin/arm64

# CP is NOT in the Solaris/illumos matrix even though the daemon is —
# the pure-Go SQLite stack (modernc.org/libc) doesn't include build
# files for solaris/illumos. The CP can be made buildable on those
# platforms via a `nosqlite` tag that swaps the default state store
# to Postgres-only; tracked, but out of scope for the daemon-side
# Solaris addition. Operators running CP on Solaris today should use
# the linux/amd64 binary in a Solaris zone with linux branded brand,
# or wait for the nosqlite build.

DIST_DIR ?= dist
BINARIES_DIR := $(DIST_DIR)/binaries
CP_DIR       := $(DIST_DIR)/cp

# DAEMON_BINARIES_DIR is the CP's --daemon-binaries-dir. `make deposit`
# copies the freshly-built daemons into it so the inventory picks
# them up on the next CP scan. Override at invocation time:
#   make deposit DAEMON_BINARIES_DIR=/var/lib/okesu/binaries
DAEMON_BINARIES_DIR ?= test/stack/run/binaries

# ── Single-target shortcuts (fast iteration) ────────────────────────
# Default cross-compile targets — match the lab's primary node arch.
DAEMON_GOOS   ?= linux
DAEMON_GOARCH ?= arm64
DAEMON_OUT    ?= okesu-$(DAEMON_GOOS)-$(DAEMON_GOARCH)

.PHONY: all daemon daemon-host daemons cp cp-all release ui clean version deposit

all: ui daemon cp

# Single fast-path targets for inner-loop dev.
daemon:
	CGO_ENABLED=0 GOOS=$(DAEMON_GOOS) GOARCH=$(DAEMON_GOARCH) \
	  go build -ldflags "$(LDFLAGS)" -o $(DAEMON_OUT) ./cmd/okesu

daemon-host:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o okesu ./cmd/okesu

cp:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o okesu-cp ./cmd/cp

ui:
	cd web && npm install --silent && npm run build

# ── Full matrix builds ──────────────────────────────────────────────
# `make daemons` rebuilds every platform in DAEMON_TARGETS into
# dist/binaries/. Add `-j$(nproc)` for parallel builds.
daemons: $(BINARIES_DIR)
	@echo "Building daemon for $(words $(DAEMON_TARGETS)) targets — VERSION=$(VERSION)"
	@for t in $(DAEMON_TARGETS); do \
	  os=$${t%/*} arch=$${t#*/}; \
	  out=$(BINARIES_DIR)/okesu-$$os-$$arch; \
	  printf "  %-22s → %s\n" "$$os/$$arch" "$$out"; \
	  CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
	    go build -ldflags "$(LDFLAGS)" -o $$out ./cmd/okesu \
	    || { echo "build failed for $$os/$$arch"; exit 1; }; \
	done
	@echo "Done. Run \`make deposit\` to copy them into the CP's --daemon-binaries-dir."

cp-all: $(CP_DIR)
	@echo "Building okesu-cp for $(words $(CP_TARGETS)) targets — VERSION=$(VERSION)"
	@for t in $(CP_TARGETS); do \
	  os=$${t%/*} arch=$${t#*/}; \
	  out=$(CP_DIR)/okesu-cp-$$os-$$arch; \
	  printf "  %-22s → %s\n" "$$os/$$arch" "$$out"; \
	  CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
	    go build -ldflags "$(LDFLAGS)" -o $$out ./cmd/cp \
	    || { echo "build failed for $$os/$$arch"; exit 1; }; \
	done
	@echo "Done."

# `make release` rebuilds the UI + every cross-compiled daemon and CP.
# Use this when cutting a tag or shipping to multiple operators.
release: ui daemons cp-all
	@printf "\nRelease %s ready in %s/\n" "$(VERSION)" "$(DIST_DIR)"
	@ls -la $(BINARIES_DIR) $(CP_DIR)

# Copy the freshly-built daemon binaries into the CP's library dir.
# The CP scans that dir on every Settings → Deploy load and exposes
# them in the multi-arch deploy picker. Symlinks rather than copies
# keep the dir in sync after the next `make daemons`.
deposit: daemons
	@mkdir -p $(DAEMON_BINARIES_DIR)
	@echo "Symlinking $(words $(DAEMON_TARGETS)) binaries into $(DAEMON_BINARIES_DIR)/"
	@for t in $(DAEMON_TARGETS); do \
	  os=$${t%/*} arch=$${t#*/}; \
	  src="$$(pwd)/$(BINARIES_DIR)/okesu-$$os-$$arch"; \
	  dst="$(DAEMON_BINARIES_DIR)/okesu-$$os-$$arch"; \
	  ln -sf "$$src" "$$dst"; \
	done
	@ls -la $(DAEMON_BINARIES_DIR)

$(BINARIES_DIR) $(CP_DIR):
	@mkdir -p $@

clean:
	rm -f okesu okesu-cp $(DAEMON_OUT)
	rm -rf $(DIST_DIR)

version:
	@echo $(VERSION)

# ─────────────────────────────────────────────────────────────────────
# OCI end-to-end deploy
# ─────────────────────────────────────────────────────────────────────
#
# `make oci-deploy MODE=<standalone|parent|child>` takes a clean checkout
# to a running Okesu CP on Oracle Cloud. See deploy/oci/README.md.

OCI_MODE ?= $(or $(MODE),standalone)
OCI_DIR  ?= deploy/oci/terraform
OCI_TFVARS ?= deploy/oci/terraform/$(OCI_MODE).tfvars
OCI_ENV  ?= .env.oci

# Default ssh user for Oracle Linux on OCI.
OCI_SSH_USER ?= opc

# Render CLI built into dist/.
OCI_RENDER ?= dist/oci-render

.PHONY: oci-build oci-plan oci-apply oci-render oci-install oci-deploy \
        oci-redeploy oci-destroy oci-print oci-test \
        _oci-preflight _oci-render-cli

# ── Pre-flight: hard-fail before terraform/scp if anything's missing.
_oci-preflight:
	@if [ ! -e "$(OCI_ENV)" ]; then \
	  echo "✖ missing $(OCI_ENV) — copy deploy/oci/.env.oci.example and fill it in" >&2; exit 1; fi
	@. "$(OCI_ENV)"; \
	  if [ -z "$$ANTHROPIC_API_KEY" ]; then \
	    echo "✖ ANTHROPIC_API_KEY unset in $(OCI_ENV)" >&2; exit 1; fi
	@if [ ! -e "$(OCI_TFVARS)" ]; then \
	  echo "✖ missing $(OCI_TFVARS) — copy deploy/oci/modes/$(OCI_MODE).tfvars.example" >&2; exit 1; fi
	@if [ "$(OCI_MODE)" = "child" ]; then \
	  for k in parent_federation_bucket parent_federation_endpoint parent_federation_region parent_federation_access_key; do \
	    grep -E "^[[:space:]]*$$k[[:space:]]*=[[:space:]]*\"[^\"]+\"" $(OCI_TFVARS) >/dev/null || { \
	      echo "✖ child mode: $$k not set in $(OCI_TFVARS)" >&2; exit 1; }; \
	  done; \
	  . "$(OCI_ENV)"; \
	  for k in PARENT_FEDERATION_TOKEN PARENT_FEDERATION_ACCESS_KEY PARENT_FEDERATION_SECRET_KEY; do \
	    eval "v=\$$$$k"; \
	    [ -n "$$v" ] || { echo "✖ child mode: $$k unset in $(OCI_ENV)" >&2; exit 1; }; \
	  done; \
	fi
	@command -v oci >/dev/null   || { echo "✖ oci CLI not in PATH" >&2; exit 1; }
	@command -v terraform >/dev/null || { echo "✖ terraform not in PATH" >&2; exit 1; }
	@oci iam region list >/dev/null 2>&1 || { echo "✖ oci CLI not authenticated — run 'oci session refresh -p $$OCI_CLI_PROFILE' (or oci session authenticate)" >&2; exit 1; }
	@echo "▶ pre-flight ok (mode=$(OCI_MODE), dir=$(OCI_DIR))"

_oci-render-cli: $(OCI_RENDER)
$(OCI_RENDER): cmd/oci-render/main.go deploy/oci/render/render.go deploy/oci/render/templates/cp.yaml.tmpl deploy/oci/render/templates/okesu-cp.env.tmpl
	@mkdir -p $(@D)
	go build -o $@ ./cmd/oci-render

# ── oci-build: cross-compile cp + UI bundle (alias for ui + cp-all).
oci-build: ui cp-all $(OCI_RENDER)
	@echo "▶ oci-build done — binaries in $(CP_DIR)/, render in $(OCI_RENDER)"

# ── oci-plan: terraform plan in $(OCI_DIR) with the right tfvars.
oci-plan: _oci-preflight
	cd $(OCI_DIR) && terraform init -input=false
	cd $(OCI_DIR) && terraform plan -var-file=$(abspath $(OCI_TFVARS))

# ── oci-apply: terraform apply.
oci-apply: _oci-preflight
	cd $(OCI_DIR) && terraform init -input=false
	cd $(OCI_DIR) && terraform apply -auto-approve -var-file=$(abspath $(OCI_TFVARS))
	@echo "▶ terraform apply done"
