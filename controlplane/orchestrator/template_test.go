package orchestrator

import (
	"strings"
	"testing"
)

func TestRender_Substitution(t *testing.T) {
	env := Env{
		"trigger": map[string]any{
			"host":       "edr-fedora-3",
			"finding_id": int64(42),
		},
		"triage": map[string]any{
			"output": "line1\nline2\nline3\nline4\nline5",
			"findings": []any{
				map[string]any{"severity": "HIGH", "host": "h1", "category": "process", "path": "/tmp/x"},
				map[string]any{"severity": "LOW", "host": "h2", "category": "file"},
			},
		},
	}
	cases := []struct {
		in, want string
	}{
		{"static", "static"},
		{"host={{trigger.host}}", "host=edr-fedora-3"},
		{"id={{trigger.finding_id}}", "id=42"},
		{"first_path={{triage.findings.first.path}}", "first_path=/tmp/x"},
		{"count={{triage.findings | length}}", "count=2"},
		{"tail={{triage.output | tail(2)}}", "tail=line4\nline5"},
		{"head={{triage.output | head(1)}}", "head=line1"},
	}
	for _, tc := range cases {
		got, err := Render(tc.in, env)
		if err != nil {
			t.Errorf("%q: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("Render(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestEval_Booleans(t *testing.T) {
	env := Env{
		"triage": map[string]any{
			"findings": []any{
				map[string]any{"severity": "HIGH", "category": "process"},
			},
			"status": "completed",
		},
	}
	cases := []struct {
		expr string
		want bool
	}{
		{"triage.findings | length > 0", true},
		{"triage.findings | length == 0", false},
		{"triage.findings | any(category='process')", true},
		{"triage.findings | any(category='ghost')", false},
		{"triage.status == 'completed'", true},
		{"triage.status != 'failed' && triage.findings | length > 0", true},
		{"!(triage.findings | length == 0)", true},
		{"'HIGH' in ['HIGH', 'CRITICAL']", true},
		{"'INFO' in ['HIGH', 'CRITICAL']", false},
		{"triage.findings.first.severity == 'HIGH'", true}, // path-style sugar (`first` is a built-in path key)
	}
	for _, tc := range cases {
		got, err := EvalBool(tc.expr, env)
		if err != nil {
			t.Errorf("%q: %v", tc.expr, err)
			continue
		}
		if got != tc.want {
			t.Errorf("EvalBool(%q) = %v, want %v", tc.expr, got, tc.want)
		}
	}
}

func TestRender_MissingPathReturnsEmpty(t *testing.T) {
	// Operators shouldn't have to defensively check for empty step
	// outputs in every prompt. Missing paths render as "".
	env := Env{"trigger": map[string]any{"host": "h"}}
	got, err := Render("missing={{ghost.findings.first.path}}", env)
	if err != nil {
		t.Fatal(err)
	}
	if got != "missing=" {
		t.Errorf("got %q, want %q", got, "missing=")
	}
}

func TestRender_UnterminatedBraces(t *testing.T) {
	_, err := Render("hello {{trigger.host", Env{})
	if err == nil || !strings.Contains(err.Error(), "unterminated") {
		t.Errorf("want unterminated error, got %v", err)
	}
}

func TestRender_TypoSurfaces(t *testing.T) {
	// A typo'd filter should error so operators see it at save time
	// instead of silently rendering empty.
	_, err := Render("{{trigger.host | upcase}}", Env{
		"trigger": map[string]any{"host": "h"},
	})
	if err == nil || !strings.Contains(err.Error(), "unknown filter") {
		t.Errorf("want unknown filter error, got %v", err)
	}
}

func TestEval_WhereFilter(t *testing.T) {
	env := Env{
		"hunt": map[string]any{
			"findings": []any{
				map[string]any{"severity": "HIGH", "host": "h1"},
				map[string]any{"severity": "LOW", "host": "h2"},
				map[string]any{"severity": "HIGH", "host": "h3"},
			},
		},
	}
	got, err := Eval("hunt.findings | where(severity='HIGH') | length", env)
	if err != nil {
		t.Fatal(err)
	}
	if got != int64(2) {
		t.Errorf("got %v, want 2", got)
	}
}

func TestRender_StripsOuterBraces(t *testing.T) {
	// `when` expressions are written as `{{ ... }}` but Eval also
	// accepts them without — the engine peels braces if present.
	got, err := EvalBool("{{ true }}", Env{})
	if err != nil || !got {
		t.Errorf("got %v err=%v, want true", got, err)
	}
}

func TestRender_TriggersErrorOnInner(t *testing.T) {
	// Invalid expression inside `{{ }}` aborts the whole render rather
	// than emitting partial output.
	_, err := Render("a {{ 1 + }} b", Env{})
	if err == nil {
		t.Error("expected parse error")
	}
}
