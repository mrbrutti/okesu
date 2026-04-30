// Engine-applied action protocol — the surface agents use to request
// CP-side mutations on findings (status change, tagging, severity
// override, run linkage). Agents never get CP credentials; they
// emit a structured `actions:` array inside their orchestration_result
// and the engine validates + applies on their behalf.
//
// Two halves live here:
//
//   1. ActionKind* constants + the validator. The spec-level
//      `actions:` allowlist names each kind; the agent's emitted
//      action carries the same kind. ParseAction parses one wire
//      entry; AllowedActionKinds is the canonical list of kinds for
//      humans + the spec validator.
//
//   2. Action — the post-step entries the engine pulls out of the
//      finding the agent emits. Application happens in the engine
//      (engine.applyActions), not here, because we need the DB
//      handle and the run/step context. This file only knows about
//      the wire format + intent.

package orchestrator

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ActionKind names the CP-side mutation an agent is requesting.
// Adding a new kind: register it here, add a case in
// engine.applyActions, and document it in docs/orchestration-actions.md.
const (
	// ActionUpdateFindingStatus changes findings.status.
	// Payload: { finding_id: int, status: string, reason?: string }
	ActionUpdateFindingStatus = "update_finding_status"

	// ActionAddFindingTag appends to findings.tags.
	// Payload: { finding_id: int, tag: string, reason?: string }
	ActionAddFindingTag = "add_finding_tag"

	// ActionRemoveFindingTag drops a tag from findings.tags.
	// Payload: { finding_id: int, tag: string, reason?: string }
	ActionRemoveFindingTag = "remove_finding_tag"

	// ActionSetFindingSeverityOverride writes findings.operator_severity.
	// Payload: { finding_id: int, severity: CRITICAL|HIGH|MEDIUM|LOW|INFO, reason?: string }
	ActionSetFindingSeverityOverride = "set_finding_severity_override"

	// ActionLinkRunToFinding records a finding ↔ run relationship in
	// finding_run_links so the finding-detail page can show
	// "auto-handled by run #N". Payload: { finding_id: int, reason?: string }
	ActionLinkRunToFinding = "link_run_to_finding"

	// ActionEscalate flags the run as needing operator attention even
	// after it completes — surfaces in the dashboard's "Pending
	// review" tile. Payload: { reason: string, severity?: SEV-N }
	ActionEscalate = "escalate"

	// ActionReflectWithLessons writes one or more lesson strings to the
	// agent_lessons KV. Daemons read top-N on tick prep and prepend them
	// to the system prompt. Agent-emitted shape:
	//
	//   { "kind": "reflect_with_lessons", "lessons": ["short text", ...] }
	ActionReflectWithLessons = "reflect_with_lessons"

	// ActionEnrichIOC kicks off vendor enrichment for an IOC. Class:
	// "enrich" — operators can auto-approve all enrichment actions
	// without granting every per-vendor approval gate.
	//
	// Wire shape:
	//   { "kind": "enrich_ioc", "ioc_id": 42 }
	ActionEnrichIOC = "enrich_ioc"
)

// AllowedActionKinds is the source of truth for the spec validator
// + UI hints. Keep alphabetised — diff-friendly when somebody adds
// a new kind.
var AllowedActionKinds = []string{
	ActionAddFindingTag,
	ActionEnrichIOC,
	ActionEscalate,
	ActionLinkRunToFinding,
	ActionReflectWithLessons,
	ActionRemoveFindingTag,
	ActionSetFindingSeverityOverride,
	ActionUpdateFindingStatus,
}

// IsAllowedActionKind reports whether the given string is a registered
// kind. Case-sensitive (kinds are wire identifiers).
func IsAllowedActionKind(k string) bool {
	for _, v := range AllowedActionKinds {
		if v == k {
			return true
		}
	}
	return false
}

// Action is the parsed in-memory form of one entry from the agent's
// `actions:` array. Fields are loosely typed because the agent might
// emit a finding_id as a string; the engine coerces in applyActions.
type Action struct {
	Kind     string         `json:"kind"`
	Payload  map[string]any `json:"-"` // raw, for kinds we don't constrain
	// Convenience views for the common fields (filled by ParseAction).
	FindingID int64  `json:"finding_id,omitempty"`
	Status    string `json:"status,omitempty"`
	Severity  string `json:"severity,omitempty"`
	Tag       string `json:"tag,omitempty"`
	Reason    string `json:"reason,omitempty"`
	// Lessons is populated for ActionReflectWithLessons actions.
	Lessons []string `json:"lessons,omitempty"`
	// IOCID is populated for ActionEnrichIOC.
	IOCID int64 `json:"ioc_id,omitempty"`
}

// ParseActions decodes the agent's wire array into Action structs.
// Tolerates missing / extra fields — the engine validates each
// entry against the step's allowlist before applying.
func ParseActions(raw any) ([]Action, error) {
	if raw == nil {
		return nil, nil
	}
	// The agent's orchestration_result usually serialises through a
	// JSON codec, so we accept either a typed slice or an arbitrary
	// any. Re-marshal-and-unmarshal is the simplest path.
	bytes, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("marshal actions: %w", err)
	}
	var rawList []map[string]any
	if err := json.Unmarshal(bytes, &rawList); err != nil {
		return nil, fmt.Errorf("decode actions: %w", err)
	}
	out := make([]Action, 0, len(rawList))
	for i, m := range rawList {
		a, err := decodeAction(m)
		if err != nil {
			return nil, fmt.Errorf("action[%d]: %w", i, err)
		}
		out = append(out, a)
	}
	return out, nil
}

func decodeAction(m map[string]any) (Action, error) {
	kindAny, ok := m["kind"]
	if !ok {
		return Action{}, errors.New("missing `kind`")
	}
	kind, _ := kindAny.(string)
	if kind == "" {
		return Action{}, errors.New("`kind` must be a non-empty string")
	}
	if !IsAllowedActionKind(kind) {
		return Action{}, fmt.Errorf("unknown kind %q (allowed: %v)", kind, AllowedActionKinds)
	}
	a := Action{Kind: kind, Payload: m}
	a.FindingID = anyInt64(m["finding_id"])
	a.Status, _ = m["status"].(string)
	a.Severity, _ = m["severity"].(string)
	a.Tag, _ = m["tag"].(string)
	a.Reason, _ = m["reason"].(string)

	switch kind {
	case ActionReflectWithLessons:
		raw, _ := m["lessons"].([]any)
		out := make([]string, 0, len(raw))
		for _, x := range raw {
			if s, ok := x.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		if len(out) == 0 {
			return Action{}, fmt.Errorf("%s: lessons must be a non-empty array of strings", kind)
		}
		a.Lessons = out
	case ActionEnrichIOC:
		a.IOCID = anyInt64(m["ioc_id"])
		if a.IOCID == 0 {
			return Action{}, fmt.Errorf("%s: ioc_id is required", kind)
		}
	}
	return a, nil
}

// anyInt64 coerces 1, 1.0, "1" → int64(1). Returns 0 on failure.
func anyInt64(v any) int64 {
	switch x := v.(type) {
	case int:
		return int64(x)
	case int64:
		return x
	case float64:
		return int64(x)
	case json.Number:
		n, _ := x.Int64()
		return n
	case string:
		var n int64
		_, _ = fmt.Sscanf(x, "%d", &n)
		return n
	}
	return 0
}

// AllowedByStep returns the subset of the supplied actions that the
// step's allowlist permits. Caller logs the rejected ones (with
// rejected[i].Kind included) so operators see exactly which actions
// the agent tried that it wasn't permitted to.
func AllowedByStep(step StepSpec, actions []Action) (allowed, rejected []Action) {
	if len(step.Actions) == 0 {
		return nil, actions
	}
	allowSet := make(map[string]struct{}, len(step.Actions))
	for _, k := range step.Actions {
		allowSet[k] = struct{}{}
	}
	for _, a := range actions {
		if _, ok := allowSet[a.Kind]; ok {
			allowed = append(allowed, a)
		} else {
			rejected = append(rejected, a)
		}
	}
	return allowed, rejected
}
