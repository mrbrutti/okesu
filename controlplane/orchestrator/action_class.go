// Action-class taxonomy + per-class auto-approve policy.
//
// Step allowlists (StepSpec.Actions) name action *kinds* explicitly —
// fine for "this step can change finding status, that's it" but
// awkward when an operator wants to write blanket policy ("auto-apply
// every read-only action across every orchestration"). The class
// taxonomy gives every kind a coarse-grained label so a single
// `policy.auto_approve.read = true` toggle covers any future read-class
// kind without an enumeration update.
//
// Two halves live here:
//
//   1. ClassFor — the registry mapping kind → class. Unknown kinds
//      default to ClassModify so the engine errs on the side of
//      gating; callers who care about exact mapping should use the
//      ActionKind* constants from actions.go directly.
//
//   2. Policy — the operator-set per-class auto-approve toggle, lifted
//      out of the YAML config. Empty AutoApprove (the default) means
//      no class is auto-approved and every action proceeds through the
//      existing step-approval gate as before.
//
// See docs/superpowers/specs/2026-04-29-threatcaddy-borrows-phasing-design.md
// for the design rationale and agents/_orchestration-actions.md for
// the operator-facing reference.

package orchestrator

// Action classes — a per-kind hint that lets operators set blanket
// auto-approve policy without enumerating every action kind.
//
//   read    — pure reads (no DB writes)
//   enrich  — outbound calls that don't mutate CP state (vendor APIs)
//   fetch   — like enrich but for inbound content (URL fetch, etc.)
//   create  — inserts or links (e.g., link_run_to_finding)
//   modify  — mutations (status, severity, tags) — most restrictive
const (
	ClassRead   = "read"
	ClassEnrich = "enrich"
	ClassFetch  = "fetch"
	ClassCreate = "create"
	ClassModify = "modify"
)

// actionClassRegistry maps each known action kind to its class.
// Keys reference the ActionKind* constants from actions.go so a
// rename ripples through both files cleanly.
//
// ActionEscalate is intentionally omitted — escalate is an
// operator-attention soft signal, not a content mutation, and the
// engine treats it the same as ClassModify (most-restrictive) by
// default. If we ever want to give it its own class, add a constant
// here.
var actionClassRegistry = map[string]string{
	ActionUpdateFindingStatus:        ClassModify,
	ActionSetFindingSeverityOverride: ClassModify,
	ActionAddFindingTag:              ClassModify,
	ActionRemoveFindingTag:           ClassModify,
	ActionLinkRunToFinding:           ClassCreate,
	// Future (Phase 22.4): enrich_ioc -> ClassEnrich, link_iocs -> ClassCreate.
}

// ClassFor returns the class for an action kind. Unknown kinds default
// to ClassModify so the engine errs on the side of gating — operators
// adding a new kind without registering it here see "still gated"
// behaviour rather than silent auto-apply.
func ClassFor(kind string) string {
	if c, ok := actionClassRegistry[kind]; ok {
		return c
	}
	return ClassModify
}

// Policy is the operator-set per-class auto-approve toggle. Threaded
// from the YAML config into the engine at boot. Empty AutoApprove
// (the default) means every action class still gates as before — the
// taxonomy is purely additive, never relaxes existing constraints.
type Policy struct {
	AutoApprove map[string]bool
}

// AllowsClass reports whether the named class is auto-approved by
// this policy. Nil-safe: a zero-value Policy returns false for every
// class, matching the safe default.
func (p Policy) AllowsClass(class string) bool {
	if p.AutoApprove == nil {
		return false
	}
	return p.AutoApprove[class]
}
