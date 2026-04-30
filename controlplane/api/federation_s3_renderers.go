// Renderers for the federation S3 publisher's per-tick assets.
//
// Each `RenderFederation*` function returns the JSON body the matching
// /api/v1/federation/* endpoint would emit at default filters. The
// publisher writes these under the bucket prefix; the parent's
// aggregator caches them and serves federated reads off the cache for
// peers whose transport is s3_dead_drop.
//
// Phase A.2 lit up findings.json. This file adds the rest of the
// resources the federated UI cares about: daimons (a.k.a. agents),
// nodes, orchestrations. Same shape — `RenderXxx(store, limit) ([]byte, error)` —
// so server.go's federationS3Assets list stays uniform.

package api

import (
	"encoding/json"

	"github.com/section9labs/okesu/controlplane/db"
)

// RenderFederationDaimons mirrors AgentsList's default response:
// every registered agent (capped at limit), each row decorated with
// its open-finding count. Same wire shape FederationDaimons emits
// over HTTPS.
func RenderFederationDaimons(store *db.Store, limit int) ([]byte, error) {
	if limit <= 0 || limit > 5000 {
		limit = 1000
	}
	agents, err := store.ListAgents(limit, 0)
	if err != nil {
		return nil, err
	}
	counts, err := store.OpenFindingCountsByAgentHost()
	if err != nil {
		return nil, err
	}
	out := make([]agentJSON, 0, len(agents))
	for _, a := range agents {
		row := toAgentJSON(a)
		row.OpenFindings = counts[db.AgentHostKey{Agent: a.Name, Host: a.Host}]
		out = append(out, row)
	}
	return json.Marshal(out)
}

// RenderFederationNodes mirrors NodesList — every registered node
// (capped at limit). The federated nodes view on the parent reads
// this for s3 peers.
func RenderFederationNodes(store *db.Store, limit int) ([]byte, error) {
	if limit <= 0 || limit > 5000 {
		limit = 1000
	}
	ns, err := store.ListNodes(limit, 0)
	if err != nil {
		return nil, err
	}
	out := make([]nodeJSON, 0, len(ns))
	for _, n := range ns {
		out = append(out, toNodeJSON(n))
	}
	return json.Marshal(out)
}

// RenderFederationOrchestrations mirrors OrchestrationsList. Used by
// the federated /api/orchestrations view so an operator on the parent
// CP can see (and run) orchestrations defined on s3-mode children.
//
// Note: parent → child orchestration writes (creating, editing,
// running) still need the write pipe (Phase B). This is read-only.
func RenderFederationOrchestrations(store *db.Store) ([]byte, error) {
	rows, err := store.ListOrchestrations()
	if err != nil {
		return nil, err
	}
	out := make([]orchestrationJSON, 0, len(rows))
	for _, o := range rows {
		out = append(out, toOrchestrationJSON(o))
	}
	return json.Marshal(out)
}

// RenderFederationInvestigations mirrors ListInvestigationsHandler's
// default-filter output: every investigation regardless of status,
// newest-first, capped at limit. Federated investigation cases let
// an operator on the parent CP see open work on s3-mode children
// without leaving the global UI.
//
// Returns the bare list (no enrichment) — the workspace tabs
// (findings/runs/IOCs/etc) per case stay scoped to the owning CP
// and load via the existing ?cp= proxy when an operator clicks
// through to detail. That keeps the per-tick payload bounded
// regardless of how many cases a child has.
func RenderFederationInvestigations(store *db.Store, limit int) ([]byte, error) {
	if limit <= 0 || limit > 5000 {
		limit = 500
	}
	rows, err := store.ListInvestigations("", limit)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []*db.Investigation{}
	}
	return json.Marshal(rows)
}
