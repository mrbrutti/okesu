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
type RBACRule struct {
	Tool    string `yaml:"tool"`              // tool name or "*"
	Pattern string `yaml:"pattern,omitempty"` // optional arg pattern (reserved)
	Reason  string `yaml:"reason,omitempty"`  // human-readable justification
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
				return true, ""
			}
		}
		return false, fmt.Sprintf("tool %q is not on the allow list", toolName)
	}

	// 3. Default allow.
	return true, ""
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
