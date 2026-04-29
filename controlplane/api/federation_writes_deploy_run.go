// Phase 9.7 (continued): forwarding wrappers for the Deploy Daimon
// and Run Agent endpoints, plus their token-authed federation
// siblings.
//
// Pattern matches `ForwardingNodeCreate`: the parent's wrapper peeks
// the request body for `target_cp_instance_id`, and when set proxies
// the same body (minus the targeting field) to the chosen child CP's
// federation endpoint. Without the field, the request runs locally.
//
// Lives in a separate file so federation_writes.go stays focused on
// the original Add-Node + read fan-out pattern; this file's wrappers
// share the same proxyIfTargetCP / requireFederationToken helpers.

package api

import (
	"net/http"
	"strings"

	"github.com/section9labs/okesu/controlplane/db"
	"github.com/section9labs/okesu/controlplane/federation"
	"github.com/section9labs/okesu/controlplane/jobs"
	"github.com/section9labs/okesu/controlplane/tunnel"
)

// ForwardingNodeDeploy wraps NodeDeploy. When the body carries
// `target_cp_instance_id`, the request is proxied to the chosen child
// CP's `/api/v1/federation/nodes/{id}/deploy` so an operator can
// deploy a daimon to a federated node from the parent UI.
//
// The {id} in the path is preserved verbatim — federated nodes keep
// their child-side ids on the parent's federated nodes list.
func ForwardingNodeDeploy(store *db.Store, reg *jobs.Registry, deployer NodeDeployer, cfg NodesConfig, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Translate the parent's local URL to the federation form.
		// /api/nodes/42/deploy → /api/v1/federation/nodes/42/deploy
		fedPath := strings.Replace(r.URL.Path, "/api/nodes/", "/api/v1/federation/nodes/", 1)
		if handled, _ := proxyIfTargetCP(w, r, agg, fedPath); handled {
			return
		}
		NodeDeploy(store, reg, deployer, cfg).ServeHTTP(w, r)
	}
}

// FederationNodeDeploy is the child-side endpoint that
// ForwardingNodeDeploy proxies to. Token-authed; otherwise identical
// to NodeDeploy.
func FederationNodeDeploy(store *db.Store, reg *jobs.Registry, deployer NodeDeployer, cfg NodesConfig) http.HandlerFunc {
	return requireFederationToken(store, NodeDeploy(store, reg, deployer, cfg))
}

// ForwardingCreateRun wraps CreateRun (the "Run Agent" handler). When
// the body carries `target_cp_instance_id`, the run is created on the
// chosen child CP — the parent never owns a row for it. The operator
// finds the resulting run via the federated runs list (which already
// merges per-CP rows + tags them with cp_source).
//
// Live output streaming for federated runs is NOT bridged in v1: the
// SSE endpoints (`/api/runs/{id}/output`) still hit the run's owning
// CP directly. The Runs detail page already proxies via ?cp= for
// federated rows, which is enough for the operator to follow along.
func ForwardingCreateRun(reg *RunRegistry, tunReg *tunnel.Registry, store *db.Store, agentDirs []string, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if handled, _ := proxyIfTargetCP(w, r, agg, "/api/v1/federation/runs"); handled {
			return
		}
		CreateRun(reg, tunReg, store, agentDirs).ServeHTTP(w, r)
	}
}

// FederationCreateRun is the child-side endpoint that
// ForwardingCreateRun proxies to. Token-authed; otherwise identical
// to CreateRun.
//
// NOTE: the path on the child side is `/api/v1/federation/runs`, NOT
// `/api/v1/federation/runs/sync`. The latter is the synchronous,
// orchestrator-step dispatch endpoint (`FederationRunSync`); this
// one is the async run-creation endpoint that returns a run id and
// streams output asynchronously.
func FederationCreateRun(reg *RunRegistry, tunReg *tunnel.Registry, store *db.Store, agentDirs []string) http.HandlerFunc {
	return requireFederationToken(store, CreateRun(reg, tunReg, store, agentDirs))
}
