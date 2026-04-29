package orchestrator

import (
	"strings"
	"testing"
	"time"
)

func TestParse_Minimal(t *testing.T) {
	const src = `---
name: hello
description: minimal example
steps:
  - id: greet
    agent: investigator
    prompt: hello
---
`
	s, err := Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if s.Name != "hello" {
		t.Errorf("Name = %q", s.Name)
	}
	if s.Trigger.On != "manual" {
		t.Errorf("Trigger.On default should be manual, got %q", s.Trigger.On)
	}
	if len(s.Steps) != 1 || s.Steps[0].ID != "greet" {
		t.Errorf("steps = %+v", s.Steps)
	}
}

func TestParse_FullExample(t *testing.T) {
	const src = `---
name: post-finding-deep-dive
description: Triage a HIGH/CRITICAL EDR finding, analyse, then hunt.
inputs:
  finding_id:
    type: int
    required: true
defaults:
  timeout: 5m
  cp: local
steps:
  - id: triage
    agent: investigator
    node: "{{trigger.host}}"
    prompt: "Investigate {{trigger.finding_id}}"
  - id: analyze
    when: "{{triage.findings | length > 0}}"
    agent: binary-analyzer
    node: "{{triage.findings.first.host}}"
    timeout: 10m
    prompt: "Analyse {{triage.findings.first.path}}"
  - id: respond
    approval: required
    agent: incident-responder
    node: "{{trigger.host}}"
    prompt: "Containment for {{trigger.finding_id}}"
---
# Notes
operator-facing markdown body
`
	s, err := Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := s.EffectiveTimeout(&s.Steps[0]); got != 5*time.Minute {
		t.Errorf("step[0] inherits default timeout: got %s, want 5m", got)
	}
	if got := s.EffectiveTimeout(&s.Steps[1]); got != 10*time.Minute {
		t.Errorf("step[1] explicit timeout: got %s, want 10m", got)
	}
	if got := s.EffectiveCP(&s.Steps[0]); got != "local" {
		t.Errorf("EffectiveCP = %q, want local", got)
	}
	if s.Steps[2].Approval != "required" {
		t.Errorf("step[2].Approval = %q", s.Steps[2].Approval)
	}
	if !strings.Contains(s.Body, "operator-facing markdown body") {
		t.Errorf("Body should preserve markdown notes, got %q", s.Body)
	}
}

func TestValidate_Errors(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantErr string
	}{
		{
			name: "missing name",
			src: `---
description: x
steps:
  - id: a
    agent: x
    prompt: x
---`,
			wantErr: "name is required",
		},
		{
			name: "bad slug",
			src: `---
name: NotASlug
description: x
steps:
  - id: a
    agent: x
    prompt: x
---`,
			wantErr: "name",
		},
		{
			name: "no steps",
			src: `---
name: hello
description: x
---`,
			wantErr: "at least one step",
		},
		{
			name: "duplicate step id",
			src: `---
name: hello
description: x
steps:
  - id: a
    agent: x
    prompt: x
  - id: a
    agent: x
    prompt: x
---`,
			wantErr: "duplicates",
		},
		{
			name: "forward reference",
			src: `---
name: hello
description: x
steps:
  - id: first
    agent: x
    prompt: "use {{later.output}}"
  - id: later
    agent: x
    prompt: x
---`,
			wantErr: "not defined yet",
		},
		{
			name: "unknown trigger",
			src: `---
name: hello
description: x
trigger:
  on: schedule
steps:
  - id: a
    agent: x
    prompt: x
---`,
			wantErr: "trigger.on must be",
		},
		{
			name: "cron without expression",
			src: `---
name: hello
description: x
trigger:
  on: cron
steps:
  - id: a
    agent: x
    prompt: x
---`,
			wantErr: "trigger.cron",
		},
		{
			name: "bad approval value",
			src: `---
name: hello
description: x
steps:
  - id: a
    agent: x
    prompt: x
    approval: maybe
---`,
			wantErr: "approval",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.src)
			if err == nil {
				t.Fatalf("want error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestValidate_TriggerKindsAllowed(t *testing.T) {
	for _, kind := range []string{"manual", "finding", "cron"} {
		extra := ""
		switch kind {
		case "finding":
			extra = "  filter: \"severity == 'HIGH'\""
		case "cron":
			extra = "  cron: \"0 * * * *\""
		}
		src := "---\nname: hello\ndescription: x\ntrigger:\n  on: " + kind + "\n" + extra + "\nsteps:\n  - id: a\n    agent: x\n    prompt: x\n---"
		if _, err := Parse(src); err != nil {
			t.Errorf("kind %s: %v", kind, err)
		}
	}
}
