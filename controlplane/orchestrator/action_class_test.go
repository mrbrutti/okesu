package orchestrator

import "testing"

func TestClassFor_KnownKinds(t *testing.T) {
	cases := []struct {
		kind, want string
	}{
		{"update_finding_status", "modify"},
		{"set_finding_severity_override", "modify"},
		{"add_finding_tag", "modify"},
		{"link_run_to_finding", "create"},
		{"unknown_kind", "modify"}, // default = most-restrictive
	}
	for _, c := range cases {
		if got := ClassFor(c.kind); got != c.want {
			t.Errorf("ClassFor(%q) = %q, want %q", c.kind, got, c.want)
		}
	}
}

func TestPolicyAllows(t *testing.T) {
	p := Policy{AutoApprove: map[string]bool{"read": true, "enrich": true}}
	if !p.AllowsClass("read") || !p.AllowsClass("enrich") {
		t.Errorf("policy should allow read and enrich")
	}
	if p.AllowsClass("modify") {
		t.Errorf("policy should not allow modify by default")
	}
}
