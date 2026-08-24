package agent

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestCheckRBACRequireReason covers the require_reason obligation on allow
// rules: a matching call must carry a non-blank `reason` argument.
func TestCheckRBACRequireReason(t *testing.T) {
	cases := []struct {
		name        string
		policy      *RBACPolicy
		tool        string
		input       map[string]interface{}
		wantAllowed bool
		wantReason  string // substring expected in the denial message
	}{
		{
			name: "reason present satisfies require_reason",
			policy: &RBACPolicy{Allow: []RBACRule{
				{Tool: "bash", RequireReason: true},
			}},
			tool:        "bash",
			input:       map[string]interface{}{"command": "ps aux", "reason": "triaging a suspicious process"},
			wantAllowed: true,
		},
		{
			name: "missing reason is denied",
			policy: &RBACPolicy{Allow: []RBACRule{
				{Tool: "bash", RequireReason: true},
			}},
			tool:        "bash",
			input:       map[string]interface{}{"command": "ps aux"},
			wantAllowed: false,
			wantReason:  `requires a "reason"`,
		},
		{
			name: "whitespace-only reason is denied",
			policy: &RBACPolicy{Allow: []RBACRule{
				{Tool: "bash", RequireReason: true},
			}},
			tool:        "bash",
			input:       map[string]interface{}{"command": "ps aux", "reason": "   \t\n "},
			wantAllowed: false,
			wantReason:  `requires a "reason"`,
		},
		{
			name: "empty reason is denied",
			policy: &RBACPolicy{Allow: []RBACRule{
				{Tool: "bash", RequireReason: true},
			}},
			tool:        "bash",
			input:       map[string]interface{}{"command": "ps aux", "reason": ""},
			wantAllowed: false,
			wantReason:  `requires a "reason"`,
		},
		{
			name: "non-string reason is denied",
			policy: &RBACPolicy{Allow: []RBACRule{
				{Tool: "bash", RequireReason: true},
			}},
			tool:        "bash",
			input:       map[string]interface{}{"command": "ps aux", "reason": true},
			wantAllowed: false,
			wantReason:  `requires a "reason"`,
		},
		{
			name: "denial message names the tool",
			policy: &RBACPolicy{Allow: []RBACRule{
				{Tool: "write_file", RequireReason: true},
			}},
			tool:        "write_file",
			input:       map[string]interface{}{"path": "/tmp/x"},
			wantAllowed: false,
			wantReason:  "write_file",
		},
		{
			name: "require_reason false allows a call with no reason",
			policy: &RBACPolicy{Allow: []RBACRule{
				{Tool: "bash", RequireReason: false},
			}},
			tool:        "bash",
			input:       map[string]interface{}{"command": "ps aux"},
			wantAllowed: true,
		},
		{
			name: "require_reason on one rule does not bind a different tool",
			policy: &RBACPolicy{Allow: []RBACRule{
				{Tool: "bash", RequireReason: true},
				{Tool: "read_file"},
			}},
			tool:        "read_file",
			input:       map[string]interface{}{"path": "/etc/hosts"},
			wantAllowed: true,
		},
		{
			name: "deny still wins over an allow rule that requires a reason",
			policy: &RBACPolicy{
				Allow: []RBACRule{{Tool: "bash", RequireReason: true}},
				Deny:  []RBACRule{{Tool: "bash", Reason: "no shell in prod"}},
			},
			tool:        "bash",
			input:       map[string]interface{}{"command": "ps aux", "reason": "triage"},
			wantAllowed: false,
			wantReason:  "no shell in prod",
		},
		{
			name: "nil input with require_reason is denied, not a panic",
			policy: &RBACPolicy{Allow: []RBACRule{
				{Tool: "bash", RequireReason: true},
			}},
			tool:        "bash",
			input:       nil,
			wantAllowed: false,
			wantReason:  `requires a "reason"`,
		},
		{
			name: "prose reason is preserved alongside require_reason",
			policy: &RBACPolicy{Allow: []RBACRule{
				{Tool: "bash", RequireReason: true, Reason: "targeted investigation only"},
			}},
			tool:        "bash",
			input:       map[string]interface{}{"command": "ps aux", "reason": "triage"},
			wantAllowed: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			allowed, reason := CheckRBAC(tc.policy, tc.tool, tc.input)
			if allowed != tc.wantAllowed {
				t.Fatalf("CheckRBAC allowed = %v, want %v (reason %q)", allowed, tc.wantAllowed, reason)
			}
			if tc.wantAllowed && reason != "" {
				t.Errorf("allowed call returned non-empty reason %q", reason)
			}
			if tc.wantReason != "" && !strings.Contains(reason, tc.wantReason) {
				t.Errorf("reason = %q, want substring %q", reason, tc.wantReason)
			}
		})
	}
}

// TestReasonRequiredTools checks which tool names the policy obliges to
// justify their calls. Only allow rules can carry the obligation — a deny
// rule never reaches execution, so require_reason there is meaningless.
func TestReasonRequiredTools(t *testing.T) {
	cases := []struct {
		name   string
		policy *RBACPolicy
		want   map[string]bool
	}{
		{
			name:   "nil policy requires nothing",
			policy: nil,
			want:   map[string]bool{},
		},
		{
			name:   "empty policy requires nothing",
			policy: &RBACPolicy{},
			want:   map[string]bool{},
		},
		{
			name: "single allow rule",
			policy: &RBACPolicy{Allow: []RBACRule{
				{Tool: "read_file"},
				{Tool: "bash", RequireReason: true},
			}},
			want: map[string]bool{"bash": true},
		},
		{
			name: "multiple allow rules",
			policy: &RBACPolicy{Allow: []RBACRule{
				{Tool: "bash", RequireReason: true},
				{Tool: "write_file", RequireReason: true},
				{Tool: "search"},
			}},
			want: map[string]bool{"bash": true, "write_file": true},
		},
		{
			name: "deny rules are ignored",
			policy: &RBACPolicy{Deny: []RBACRule{
				{Tool: "bash", RequireReason: true},
			}},
			want: map[string]bool{},
		},
		{
			name: "tool name is matched case-insensitively",
			policy: &RBACPolicy{Allow: []RBACRule{
				{Tool: "Bash", RequireReason: true},
			}},
			want: map[string]bool{"bash": true},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ReasonRequiredTools(tc.policy)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ReasonRequiredTools() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestReasonRequiredToolsWildcard: a wildcard allow rule binds every tool.
// Without this, layer B would deny every call at runtime while layer A
// injected no schema hint telling the model what to supply.
func TestReasonRequiredToolsWildcard(t *testing.T) {
	want := map[string]bool{}
	for _, tool := range Tools {
		want[tool.Name] = true
	}

	for _, wildcard := range []string{"*", ""} {
		policy := &RBACPolicy{Allow: []RBACRule{{Tool: wildcard, RequireReason: true}}}
		got := ReasonRequiredTools(policy)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("ReasonRequiredTools(tool: %q) = %v, want every tool %v", wildcard, got, want)
		}
	}
}

func TestReasonRequiredToolsWildcardUnchecked(t *testing.T) {
	cases := []struct {
		name   string
		policy *RBACPolicy
		want   map[string]bool
	}{
		{
			name: "wildcard without require_reason binds nothing",
			policy: &RBACPolicy{Allow: []RBACRule{
				{Tool: "*"},
			}},
			want: map[string]bool{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ReasonRequiredTools(tc.policy)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ReasonRequiredTools() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestRBACRuleYAMLRoundTrip proves the new key parses off an agent file's
// `actions:` block without disturbing the existing prose `reason:` field.
func TestRBACRuleYAMLRoundTrip(t *testing.T) {
	src := `
rbac:
  allow:
    - tool: read_file
    - tool: bash
      require_reason: true
      reason: "targeted investigation only"
  deny:
    - tool: write_file
      reason: "read-only agent"
`
	var cfg ActionsConfig
	if err := yaml.Unmarshal([]byte(src), &cfg); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}

	if got := len(cfg.RBAC.Allow); got != 2 {
		t.Fatalf("len(Allow) = %d, want 2", got)
	}
	bash := cfg.RBAC.Allow[1]
	if bash.Tool != "bash" {
		t.Fatalf("Allow[1].Tool = %q, want %q", bash.Tool, "bash")
	}
	if !bash.RequireReason {
		t.Error("Allow[1].RequireReason = false, want true")
	}
	if bash.Reason != "targeted investigation only" {
		t.Errorf("Allow[1].Reason = %q, want the prose justification preserved", bash.Reason)
	}
	if cfg.RBAC.Allow[0].RequireReason {
		t.Error("Allow[0].RequireReason = true, want false when the key is absent")
	}
	if cfg.RBAC.Deny[0].RequireReason {
		t.Error("deny rule should default RequireReason to false")
	}
}

// TestRequireReasonThroughAgentFrontmatter parses a real agent file end to
// end. The round-trip test above unmarshals ActionsConfig directly; this one
// proves the key survives the frontmatter path an operator actually uses.
func TestRequireReasonThroughAgentFrontmatter(t *testing.T) {
	def, err := ParseAgentFile(filepath.Join("..", "examples", "agents", "edr.md"))
	if err != nil {
		t.Fatalf("ParseAgentFile(edr.md): %v", err)
	}

	var bash *RBACRule
	for i := range def.Actions.RBAC.Allow {
		if def.Actions.RBAC.Allow[i].Tool == "bash" {
			bash = &def.Actions.RBAC.Allow[i]
			break
		}
	}
	if bash == nil {
		t.Fatal("edr.md has no bash allow rule")
	}
	if !bash.RequireReason {
		t.Error("edr.md bash rule did not parse require_reason: true")
	}
	if bash.Reason == "" {
		t.Error("edr.md bash rule lost its prose reason alongside require_reason")
	}

	if !ReasonRequiredTools(&def.Actions.RBAC)["bash"] {
		t.Error("ReasonRequiredTools did not pick up bash from the parsed agent file")
	}
}
