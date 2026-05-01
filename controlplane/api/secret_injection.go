// Phase 22.8 PR γ wire-through — selector-driven secret injection
// into agent.JobPayload.Env at dispatch time.
//
// One helper, two consumers (ad-hoc CreateRun + orchestration step
// dispatcher). Both call attachEnvSecrets(store, nodeID, scope,
// payload) before persisting the node_jobs row; the daemon-side
// runner then merges the resulting Env into the spawned child's
// environment.
//
// Resolver semantics (from PR γ): pulls the node's labels, evaluates
// every binding's selector, returns the matching env_var secrets,
// decrypts each value via the master key. Missing master key, sealed
// failures, or zero matches all degrade gracefully — the run still
// goes ahead, just without the per-node env block.

package api

import (
	"log"

	"github.com/section9labs/okesu/agent"
	"github.com/section9labs/okesu/controlplane/db"
)

// attachEnvSecrets mutates payload.Env in place with the env_var
// secrets bound to the target node. Idempotent: a re-call won't
// double-add (map keys are unique).
//
// scope filters the resolver: pass `agent.JobKindAgentRun`-mapped
// scope or 'daimon' for scheduled runs. Empty / 'any' inputs use
// the broadest match — secrets bound with scope='any' apply across
// every consumer.
//
// Errors are logged + swallowed by design: a bind misconfiguration
// shouldn't block a run. Operators see audit trails for which
// secrets resolved (logged below) plus whatever the binding's
// origin was.
func attachEnvSecrets(store *db.Store, nodeID int64, scope string, payload *agent.JobPayload) {
	if store == nil || nodeID == 0 || payload == nil {
		return
	}
	mk, err := store.MasterKeyFromMeta()
	if err != nil {
		log.Printf("secrets: master key unavailable, skipping injection for node=%d: %v", nodeID, err)
		return
	}
	resolved, err := store.ListSecretsForNode(nodeID, scope, db.SecretKindEnvVar)
	if err != nil {
		log.Printf("secrets: resolve for node=%d scope=%s: %v", nodeID, scope, err)
		return
	}
	if len(resolved) == 0 {
		return
	}
	// Lazy-init the map so callers don't have to.
	if payload.Env == nil {
		payload.Env = make(map[string]string, len(resolved))
	}
	injected := make([]string, 0, len(resolved))
	for _, r := range resolved {
		// The secret name doubles as the env-var key — operators
		// already use SHOUTING_SNAKE_CASE conventions on these.
		// If a node has multiple bindings landing the same secret
		// (rare but possible via different selectors), the resolver
		// returns one row per binding; we keep the first.
		if _, exists := payload.Env[r.Secret.Name]; exists {
			continue
		}
		val, err := store.GetSecretValue(r.Secret.ID, mk)
		if err != nil {
			log.Printf("secrets: decrypt %q for node=%d: %v", r.Secret.Name, nodeID, err)
			continue
		}
		payload.Env[r.Secret.Name] = val
		injected = append(injected, r.Secret.Name)
	}
	if len(injected) > 0 {
		log.Printf("secrets: injected %d env var(s) for node=%d scope=%s: %v",
			len(injected), nodeID, scope, injected)
	}
}
