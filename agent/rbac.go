package agent

import (
	"fmt"
	"strings"
)

// RBACPolicy enforces allow/deny rules on tool calls before execution.
//
// Evaluation order (first match wins):
//  1. Deny list  — if the tool+arg matches any deny rule, the call is blocked.
//  2. Allow list — if non-empty and the tool+arg matches no allow rule, the call is blocked.
//  3. Default    — allow.
//
// Rules use shell glob-style wildcards on the tool name only (e.g. "bash", "write_file").
// A finer-grained arg pattern may be added in a future phase.
type RBACPolicy struct {
	Allow []RBACRule `yaml:"allow"` // whitelist — empty means "allow all"
	Deny  []RBACRule `yaml:"deny"`  // blacklist — checked before allow
}

// RBACRule is a single allow/deny entry.
//
// Note the two distinct "reason" concepts, which are deliberately separate
// keys: `reason` is the *rule author's* prose — documentation, and the text
// shown to the model when a deny rule blocks a call. `require_reason` is an
// obligation on the *model* — it must pass a `reason` argument justifying
// each individual call, which lands in the audit trail.
type RBACRule struct {
	Tool          string `yaml:"tool"`                     // tool name or "*"
	Pattern       string `yaml:"pattern,omitempty"`        // optional arg pattern (reserved)
	Reason        string `yaml:"reason,omitempty"`         // human-readable justification for the rule
	RequireReason bool   `yaml:"require_reason,omitempty"` // model must justify each call
}

// ActionsConfig holds the RBAC policy loaded from an agent file.
type ActionsConfig struct {
	RBAC RBACPolicy `yaml:"rbac"`
}

// CheckRBAC reports whether a tool call is permitted.
// Returns (true, "") if allowed, or (false, reason) if denied.
func CheckRBAC(policy *RBACPolicy, toolName string, input map[string]interface{}) (allowed bool, reason string) {
	if policy == nil {
		return true, ""
	}

	// 1. Deny list — check first.
	for _, rule := range policy.Deny {
		if matchRule(rule, toolName) {
			r := rule.Reason
			if r == "" {
				r = fmt.Sprintf("tool %q is on the deny list", toolName)
			}
			return false, r
		}
	}

	// 2. Allow list — if non-empty, call must match at least one entry.
	if len(policy.Allow) > 0 {
		for _, rule := range policy.Allow {
			if matchRule(rule, toolName) {
				// The matching rule may oblige the model to justify the
				// call. The `reason` argument is also injected into the
				// tool's JSON Schema as a required property (see
				// WithReasonParam), so a compliant model supplies it
				// unprompted; this check is what actually enforces it and
				// what produces the action_denied audit event.
				if rule.RequireReason && !hasReason(input) {
					return false, fmt.Sprintf(
						"tool %q requires a \"reason\" argument explaining why this call is necessary", toolName)
				}
				return true, ""
			}
		}
		return false, fmt.Sprintf("tool %q is not on the allow list", toolName)
	}

	// 3. Default allow.
	return true, ""
}

// hasReason reports whether the tool input carries a usable `reason`.
// Blank and non-string values do not count — a model that answers the
// obligation with "" or `true` has not justified anything.
func hasReason(input map[string]interface{}) bool {
	r, ok := input["reason"].(string)
	return ok && strings.TrimSpace(r) != ""
}

// ReasonRequiredTools returns the set of canonical tool names whose allow
// rule sets require_reason. Used to inject a required `reason` property into
// those tools' JSON Schema before the toolset reaches the provider.
//
// Only allow rules are considered: a deny rule never reaches execution, so an
// obligation there could never be discharged. A wildcard rule binds every
// tool in the shared toolset — otherwise the runtime check would deny every
// call while the model saw no schema hint telling it what to supply.
func ReasonRequiredTools(policy *RBACPolicy) map[string]bool {
	required := make(map[string]bool)
	if policy == nil {
		return required
	}
	for _, rule := range policy.Allow {
		if !rule.RequireReason {
			continue
		}
		if rule.Tool == "" || rule.Tool == "*" {
			for _, tool := range Tools {
				required[tool.Name] = true
			}
			continue
		}
		// Lower-cased to mirror matchRule's case-insensitive comparison
		// against the canonical (lower-case) ToolDef names.
		required[strings.ToLower(rule.Tool)] = true
	}
	return required
}

// matchRule reports whether rule matches toolName.
func matchRule(rule RBACRule, toolName string) bool {
	pat := rule.Tool
	if pat == "" || pat == "*" {
		return true
	}
	// Case-insensitive exact match or simple prefix wildcard (*suffix not supported yet).
	return strings.EqualFold(pat, toolName)
}

// EmitActionDenied writes an action_denied event to the active sink.
func EmitActionDenied(toolName string, input map[string]interface{}, reason, agent, host string, tick int64) {
	Emit(Event{
		Type:     EventActionDenied,
		ToolName: toolName,
		Input:    input,
		Reason:   reason,
		Agent:    agent,
		Host:     host,
		Tick:     tick,
	})
}
