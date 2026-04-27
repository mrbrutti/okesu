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

# Default cross-compile targets — match the lab and OCI fleet.
DAEMON_GOOS   ?= linux
DAEMON_GOARCH ?= arm64
DAEMON_OUT    ?= okesu-$(DAEMON_GOOS)-$(DAEMON_GOARCH)

.PHONY: all daemon daemon-host cp ui clean version

all: ui daemon cp

daemon:
	CGO_ENABLED=0 GOOS=$(DAEMON_GOOS) GOARCH=$(DAEMON_GOARCH) \
	  go build -ldflags "$(LDFLAGS)" -o $(DAEMON_OUT) ./cmd/okesu

# daemon for the host OS/arch — used for the Mac-side edr-demo daemon.
daemon-host:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o okesu ./cmd/okesu

cp:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o okesu-cp ./cmd/cp

ui:
	cd web && npm install --silent && npm run build

clean:
	rm -f okesu okesu-cp $(DAEMON_OUT)

version:
	@echo $(VERSION)
