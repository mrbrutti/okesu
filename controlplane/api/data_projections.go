// Data-resolver projections.
//
// The store types use sql.NullString / NullInt64 for nullable columns,
// which JSON-marshal as {"String":"...","Valid":true} blobs with
// Title-case keys. That shape is unreadable in an agent prompt and —
// more importantly — bypasses the orchestrator's typed entity
// classifier (orchestrator.BuildPromptEntities) AND the UI's
// SmartPayload duck-type detector, both of which expect lowercase
// keys (id / severity / title / category / hostname / status / …).
//
// The functions below project each store row into the canonical
// map[string]any shape that satisfies both. Net effect on the wire:
//   - {{data.findings | json}} renders as
//       [{"id":4275,"severity":"INFO","title":"…","category":"network", …}, …]
//     — readable in the prompt, classifiable on the server, chip-able
//     in the UI.
//   - prompt_entities side-channel actually populates, so the run
//     detail's expanded step shows entity chips at the substitution
//     site rather than a wall of raw JSON.

package api

import (
	"encoding/json"
	"strings"

	"github.com/section9labs/okesu/controlplane/db"
)

// projectFindings flattens []*db.Finding to the entity-detection shape.
// Keep the field set wide — agents reason about findings using these
// fields, so include resource / evidence / dedup / tags even though
// the chip itself uses just id+severity+title+category+host.
func projectFindings(rows []*db.Finding) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, f := range rows {
		if f == nil {
			continue
		}
		m := map[string]any{
			"id":         f.ID,
			"event_id":   f.EventID,
			"ts":         f.Ts,
			"agent":      f.Agent.String,
			"host":       f.Host.String,
			"severity":   f.EffectiveSeverity(),
			"title":      f.Title.String,
			"category":   f.Category.String,
			"resource":   f.Resource.String,
			"evidence":   f.Evidence.String,
			"dedup_key":  f.DedupKey.String,
			"status":     f.Status.String,
			"created_at": f.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		}
		if f.Tags.Valid && f.Tags.String != "" {
			m["tags"] = strings.Split(f.Tags.String, ",")
		}
		if f.NetworkEndpoint.Valid {
			m["network_endpoint"] = f.NetworkEndpoint.String
		}
		if f.Path.Valid {
			m["path"] = f.Path.String
		}
		if f.ProcessName.Valid {
			m["process_name"] = f.ProcessName.String
		}
		if f.CVE.Valid {
			m["cve"] = f.CVE.String
		}
		if f.Subtype != "" {
			m["subtype"] = f.Subtype
		}
		if f.Attributes.Valid && f.Attributes.String != "" {
			var attrs any
			if err := json.Unmarshal([]byte(f.Attributes.String), &attrs); err == nil {
				m["attributes"] = attrs
			}
		}
		out = append(out, m)
	}
	return out
}

// projectNodes flattens []*db.Node. The detector requires id+hostname
// without severity (which a Node doesn't have), so the shape naturally
// matches.
func projectNodes(rows []*db.Node) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, n := range rows {
		if n == nil {
			continue
		}
		m := map[string]any{
			"id":       n.ID,
			"name":     n.Name,
			"hostname": n.Hostname,
			"status":   n.Status,
			"ssh_user": n.SSHUser,
			"ssh_port": n.SSHPort,
		}
		if n.DaemonHostname.Valid {
			m["daemon_hostname"] = n.DaemonHostname.String
		}
		if n.OSRelease.Valid {
			m["os_release"] = n.OSRelease.String
		}
		if n.KernelRelease.Valid {
			m["kernel_release"] = n.KernelRelease.String
		}
		if n.Arch.Valid {
			m["arch"] = n.Arch.String
		}
		if n.OkesuVersion.Valid {
			m["okesu_version"] = n.OkesuVersion.String
		}
		out = append(out, m)
	}
	return out
}

// projectAgents flattens []*db.Agent. The daimon detector wants
// name+host plus agent_id-or-suspended; we expose both so chips light
// up regardless of which marker the consumer keys off.
func projectAgents(rows []*db.Agent) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, a := range rows {
		if a == nil {
			continue
		}
		m := map[string]any{
			"name":      a.Name,
			"host":      a.Host,
			"agent_id":  a.Name + "@" + a.Host, // synthetic stable id
			"suspended": a.DesiredSuspended,
			"provider":  a.Provider.String,
			"model":     a.Model.String,
			"version":   a.Version.String,
		}
		if a.LastHeartbeatAt.Valid {
			m["last_heartbeat_at"] = a.LastHeartbeatAt.Time.UTC().Format("2006-01-02T15:04:05Z")
		}
		if a.DefinitionVersion.Valid {
			m["definition_version"] = a.DefinitionVersion.String
		}
		if a.CurrentDefinitionHash.Valid {
			m["definition_hash"] = a.CurrentDefinitionHash.String
		}
		out = append(out, m)
	}
	return out
}

// projectRuns flattens []*db.OrchestrationRun. The run detector wants
// id+status plus started_at-or-agent_name; runs don't have an
// agent_name, but started_at is always populated.
func projectRuns(rows []*db.OrchestrationRun) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		if r == nil {
			continue
		}
		m := map[string]any{
			"id":               r.ID,
			"orchestration_id": r.OrchestrationID,
			"status":           r.Status,
			"trigger_kind":     r.TriggerKind,
			"started_at":       r.StartedAt.UTC().Format("2006-01-02T15:04:05Z"),
		}
		if r.EndedAt.Valid {
			m["ended_at"] = r.EndedAt.Time.UTC().Format("2006-01-02T15:04:05Z")
		}
		if r.CurrentStepID.Valid {
			m["current_step_id"] = r.CurrentStepID.String
		}
		if r.Error.Valid {
			m["error"] = r.Error.String
		}
		out = append(out, m)
	}
	return out
}
