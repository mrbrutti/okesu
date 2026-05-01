package db

import (
	"testing"
)

// TestSelectorParse_AcceptsValidGrammar — every supported form
// parses without error and round-trips through .String().
func TestSelectorParse_AcceptsValidGrammar(t *testing.T) {
	cases := []struct {
		in       string
		canonical string
	}{
		{"", ""},
		{"env=prod", "env=prod"},
		{"env=prod,team=security", "env=prod,team=security"},
		{"env!=staging", "env!=staging"},
		{"env", "env"},
		{"!env", "!env"},
		{"env=prod, team=security", "env=prod,team=security"}, // whitespace tolerance
		{"env=", "env="},                                       // empty value allowed
	}
	for _, tc := range cases {
		sel, err := ParseSelector(tc.in)
		if err != nil {
			t.Errorf("Parse(%q) errored: %v", tc.in, err)
			continue
		}
		if got := sel.String(); got != tc.canonical {
			t.Errorf("Parse(%q).String() = %q, want %q", tc.in, got, tc.canonical)
		}
	}
}

// TestSelectorParse_RejectsBadGrammar — known-bad inputs error out.
func TestSelectorParse_RejectsBadGrammar(t *testing.T) {
	bads := []string{
		"=value",       // missing key
		",,",           // empty requirements
		"env==prod",    // double equals (would parse as equal "=prod" then fail value check)
		"env=pr od",    // space in value
		"e nv=prod",    // space in key
	}
	for _, bad := range bads {
		if _, err := ParseSelector(bad); err == nil {
			t.Errorf("Parse(%q) should have errored", bad)
		}
	}
}

// TestSelectorMatches — semantic checks against label maps.
func TestSelectorMatches(t *testing.T) {
	type tc struct {
		sel    string
		labels map[string]string
		want   bool
	}
	cases := []tc{
		// match-all
		{"", nil, true},
		{"", map[string]string{"any": "value"}, true},
		// equal
		{"env=prod", map[string]string{"env": "prod"}, true},
		{"env=prod", map[string]string{"env": "staging"}, false},
		{"env=prod", map[string]string{}, false},
		// not-equal: missing key satisfies the inequality
		{"env!=prod", map[string]string{}, true},
		{"env!=prod", map[string]string{"env": "staging"}, true},
		{"env!=prod", map[string]string{"env": "prod"}, false},
		// exists
		{"env", map[string]string{"env": "anything"}, true},
		{"env", map[string]string{}, false},
		// not-exists
		{"!env", map[string]string{}, true},
		{"!env", map[string]string{"env": "x"}, false},
		// AND across requirements
		{"env=prod,team=sec", map[string]string{"env": "prod", "team": "sec"}, true},
		{"env=prod,team=sec", map[string]string{"env": "prod", "team": "infra"}, false},
		{"env=prod,team=sec", map[string]string{"env": "prod"}, false},
	}
	for _, c := range cases {
		sel, err := ParseSelector(c.sel)
		if err != nil {
			t.Errorf("Parse(%q): %v", c.sel, err)
			continue
		}
		if got := sel.Matches(c.labels); got != c.want {
			t.Errorf("%q.Matches(%v) = %v, want %v", c.sel, c.labels, got, c.want)
		}
	}
}

// TestSelectorIsEmpty — match-all selectors report IsEmpty.
func TestSelectorIsEmpty(t *testing.T) {
	sel, _ := ParseSelector("")
	if !sel.IsEmpty() {
		t.Errorf("empty selector should be IsEmpty=true")
	}
	sel, _ = ParseSelector("env=prod")
	if sel.IsEmpty() {
		t.Errorf("env=prod should be IsEmpty=false")
	}
}
