package orchestrator

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeStore is an in-memory implementation of the engine's Store
// interface — enough surface area to drive a run end-to-end without
// touching SQLite. The real wiring lives in db/orchestrations.go.
type fakeStore struct {
	mu     sync.Mutex
	orch   *Orchestration
	run    *RunRecord
	steps  map[string]*StepRecord
	stepOrder []string // preserve insert order for ListOrchestrationSteps
}

func newFakeStore(orch *Orchestration, run *RunRecord) *fakeStore {
	return &fakeStore{
		orch:  orch,
		run:   run,
		steps: map[string]*StepRecord{},
	}
}

func (f *fakeStore) GetOrchestration(_ int64) (*Orchestration, error) { return f.orch, nil }
func (f *fakeStore) GetOrchestrationRun(_ int64) (*RunRecord, error)  { return f.run, nil }

func (f *fakeStore) UpdateOrchestrationRunStatus(_ int64, status string, currentStepID, errMsg string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.run.Status = status
	if currentStepID != "" {
		f.run.CurrentStepID = currentStepID
	}
	if errMsg != "" {
		f.run.Error = errMsg
	}
	return nil
}

func (f *fakeStore) FinishOrchestrationRun(_ int64, status, errMsg string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.run.Status = status
	if errMsg != "" {
		f.run.Error = errMsg
	}
	now := time.Now().UTC()
	f.run.EndedAt = &now
	return nil
}

func (f *fakeStore) ListOrchestrationSteps(_ int64) ([]*StepRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*StepRecord, 0, len(f.stepOrder))
	for _, id := range f.stepOrder {
		out = append(out, f.steps[id])
	}
	return out, nil
}

func (f *fakeStore) UpsertOrchestrationStep(s *StepRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, exists := f.steps[s.StepID]; !exists {
		f.stepOrder = append(f.stepOrder, s.StepID)
	}
	cp := *s
	f.steps[s.StepID] = &cp
	return nil
}

func (f *fakeStore) GetOrchestrationStep(_ int64, stepID string) (*StepRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.steps[stepID], nil
}

// fakeDispatcher returns canned results per step. Useful for testing
// the engine's halt / advance / skip / approve transitions without
// actually running an agent.
type fakeDispatcher struct {
	resultByStep map[string]DispatchResult
	errByStep    map[string]error
	calls        []DispatchRequest
}

func (d *fakeDispatcher) Dispatch(_ context.Context, req DispatchRequest) (DispatchResult, error) {
	d.calls = append(d.calls, req)
	if err, ok := d.errByStep[req.StepID]; ok {
		return DispatchResult{}, err
	}
	if r, ok := d.resultByStep[req.StepID]; ok {
		return r, nil
	}
	// Default: succeed with empty output.
	return DispatchResult{Status: StepStatusCompleted, RunID: "run-" + req.StepID, CPInstanceID: req.CPInstanceID}, nil
}

func mustParse(t *testing.T, src string) *Spec {
	t.Helper()
	s, err := Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return s
}

func TestEngine_HappyPath(t *testing.T) {
	spec := mustParse(t, `---
name: simple
description: two-step chain
steps:
  - id: a
    agent: investigator
    node: h1
    prompt: "scan"
  - id: b
    agent: binary-analyzer
    node: "{{a.host}}"
    prompt: "analyze {{a.result.path}}"
---`)
	orch := &Orchestration{ID: 1, Name: "simple", Spec: spec, Enabled: true}
	run := &RunRecord{ID: 10, OrchestrationID: 1, Status: RunStatusPending, TriggerKind: "manual"}
	store := newFakeStore(orch, run)
	disp := &fakeDispatcher{
		resultByStep: map[string]DispatchResult{
			"a": {
				Status:       StepStatusCompleted,
				RunID:        "run-a",
				CPInstanceID: "local",
				HostResolved: "h1",
				OutputTail:   "scan output",
				Findings: []DispatchedFinding{
					{Category: OrchestrationResultCategory, Title: "result", Attributes: map[string]any{"path": "/tmp/m"}},
				},
			},
			// b uses the default fakeDispatcher response (success)
		},
	}

	engine := NewEngine(store, disp)
	if err := engine.Run(context.Background(), run.ID); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if run.Status != RunStatusCompleted {
		t.Errorf("run.Status = %q, want %q", run.Status, RunStatusCompleted)
	}
	if len(disp.calls) != 2 {
		t.Fatalf("want 2 dispatch calls, got %d", len(disp.calls))
	}
	// Step b should have rendered the path from step a's result.
	if !strings.Contains(disp.calls[1].Prompt, "/tmp/m") {
		t.Errorf("step b prompt didn't pick up step a's result: %q", disp.calls[1].Prompt)
	}
	// Step b's node selector should template-resolve to step a's host.
	if disp.calls[1].NodeSelector != "h1" {
		t.Errorf("step b node = %q, want h1", disp.calls[1].NodeSelector)
	}
}

func TestEngine_HaltOnError(t *testing.T) {
	spec := mustParse(t, `---
name: halts
description: stops after first failure
steps:
  - id: a
    agent: x
    prompt: x
  - id: b
    agent: x
    prompt: x
---`)
	store := newFakeStore(
		&Orchestration{ID: 1, Spec: spec},
		&RunRecord{ID: 1, OrchestrationID: 1, Status: RunStatusPending, TriggerKind: "manual"},
	)
	disp := &fakeDispatcher{
		resultByStep: map[string]DispatchResult{
			"a": {Status: StepStatusFailed, Error: "boom"},
		},
	}
	if err := NewEngine(store, disp).Run(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if store.run.Status != RunStatusFailed {
		t.Errorf("run.Status = %q, want %q", store.run.Status, RunStatusFailed)
	}
	if len(disp.calls) != 1 {
		t.Errorf("step b should not have been dispatched, got %d calls", len(disp.calls))
	}
}

func TestEngine_ContinueOnError(t *testing.T) {
	spec := mustParse(t, `---
name: continues
description: advances past a failure
steps:
  - id: a
    agent: x
    prompt: x
    continue_on_error: true
  - id: b
    agent: x
    prompt: x
---`)
	store := newFakeStore(
		&Orchestration{ID: 1, Spec: spec},
		&RunRecord{ID: 1, OrchestrationID: 1, Status: RunStatusPending, TriggerKind: "manual"},
	)
	disp := &fakeDispatcher{
		resultByStep: map[string]DispatchResult{
			"a": {Status: StepStatusFailed, Error: "boom"},
		},
	}
	if err := NewEngine(store, disp).Run(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if store.run.Status != RunStatusCompleted {
		t.Errorf("run.Status = %q, want %q", store.run.Status, RunStatusCompleted)
	}
	if len(disp.calls) != 2 {
		t.Errorf("want 2 dispatch calls, got %d", len(disp.calls))
	}
}

func TestEngine_WhenSkipsStep(t *testing.T) {
	spec := mustParse(t, `---
name: gated
description: skip second step when first has no findings
steps:
  - id: a
    agent: x
    prompt: x
  - id: b
    when: "a.findings | length > 0"
    agent: x
    prompt: x
---`)
	store := newFakeStore(
		&Orchestration{ID: 1, Spec: spec},
		&RunRecord{ID: 1, OrchestrationID: 1, Status: RunStatusPending, TriggerKind: "manual"},
	)
	disp := &fakeDispatcher{
		resultByStep: map[string]DispatchResult{
			"a": {Status: StepStatusCompleted, Findings: nil},
		},
	}
	if err := NewEngine(store, disp).Run(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if store.run.Status != RunStatusCompleted {
		t.Errorf("run.Status = %q, want %q", store.run.Status, RunStatusCompleted)
	}
	if len(disp.calls) != 1 {
		t.Errorf("step b should have been skipped, got %d total calls", len(disp.calls))
	}
	if store.steps["b"].Status != StepStatusSkipped {
		t.Errorf("step b.Status = %q, want %q", store.steps["b"].Status, StepStatusSkipped)
	}
}

func TestEngine_ApprovalGate(t *testing.T) {
	spec := mustParse(t, `---
name: gated
description: pauses for approval
steps:
  - id: triage
    agent: x
    prompt: x
  - id: contain
    approval: required
    agent: x
    prompt: x
---`)
	store := newFakeStore(
		&Orchestration{ID: 1, Spec: spec},
		&RunRecord{ID: 1, OrchestrationID: 1, Status: RunStatusPending, TriggerKind: "manual"},
	)
	disp := &fakeDispatcher{}
	engine := NewEngine(store, disp)

	// First Run: triage executes, contain pauses.
	if err := engine.Run(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if store.run.Status != RunStatusApprovalRequired {
		t.Errorf("run.Status = %q, want %q", store.run.Status, RunStatusApprovalRequired)
	}
	if store.steps["contain"].Status != StepStatusWaitingApproval {
		t.Errorf("contain.Status = %q, want %q", store.steps["contain"].Status, StepStatusWaitingApproval)
	}
	if len(disp.calls) != 1 {
		t.Errorf("only triage should have dispatched, got %d", len(disp.calls))
	}

	// Approve and run again — contain dispatches, run completes.
	if err := engine.Approve(context.Background(), 1, "contain", 99); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if store.run.Status != RunStatusCompleted {
		t.Errorf("after approve: run.Status = %q, want %q", store.run.Status, RunStatusCompleted)
	}
	if len(disp.calls) != 2 {
		t.Errorf("want 2 dispatch calls after approve, got %d", len(disp.calls))
	}
	if store.steps["contain"].ApprovedByUserID != 99 {
		t.Errorf("ApprovedByUserID = %d, want 99", store.steps["contain"].ApprovedByUserID)
	}
}

// TestEngine_ApprovalGate_PolicyBypass exercises the action-class
// auto-approve policy: a step that would normally pause for operator
// approval is allowed to execute when every kind in its allowlist
// falls in an auto-approved class. The dispatcher returns an
// orchestration_result carrying a link_run_to_finding action; a fake
// applier asserts the action was actually delivered (not just the
// dispatch). Backwards-compat sibling test below confirms the bypass
// requires *every* listed kind to be auto-approved.
func TestEngine_ApprovalGate_PolicyBypass(t *testing.T) {
	spec := mustParse(t, `---
name: gated
description: pauses for approval unless policy auto-approves the action class
steps:
  - id: contain
    approval: required
    actions:
      - link_run_to_finding
    agent: x
    prompt: x
---`)
	store := newFakeStore(
		&Orchestration{ID: 1, Spec: spec},
		&RunRecord{ID: 1, OrchestrationID: 1, Status: RunStatusPending, TriggerKind: "manual"},
	)
	disp := &fakeDispatcher{
		resultByStep: map[string]DispatchResult{
			"contain": {
				Status:       StepStatusCompleted,
				RunID:        "run-contain",
				CPInstanceID: "local",
				HostResolved: "h1",
				Findings: []DispatchedFinding{
					{
						Category: OrchestrationResultCategory,
						Title:    "result",
						Attributes: map[string]any{
							"actions": []any{
								map[string]any{
									"kind":       "link_run_to_finding",
									"finding_id": float64(42),
									"reason":     "auto-linked by policy bypass",
								},
							},
						},
					},
				},
			},
		},
	}
	applier := &fakeActionApplier{}
	engine := NewEngine(store, disp)
	engine.SetActionApplier(applier)
	// Auto-approve every "create" class action; link_run_to_finding is
	// the only kind in the step's allowlist and it's class=create.
	engine.SetActionPolicy(Policy{AutoApprove: map[string]bool{ClassCreate: true}})

	if err := engine.Run(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if store.run.Status != RunStatusCompleted {
		t.Errorf("run.Status = %q, want %q (policy should have bypassed the gate)", store.run.Status, RunStatusCompleted)
	}
	if len(disp.calls) != 1 {
		t.Errorf("step should have dispatched directly, got %d calls", len(disp.calls))
	}
	if store.steps["contain"].Status != StepStatusCompleted {
		t.Errorf("contain.Status = %q, want %q", store.steps["contain"].Status, StepStatusCompleted)
	}
	if len(applier.linkCalls) != 1 {
		t.Fatalf("expected exactly one LinkRunToFinding call after bypass; got %d", len(applier.linkCalls))
	}
	if applier.linkCalls[0].findingID != 42 {
		t.Errorf("LinkRunToFinding finding_id = %d, want 42", applier.linkCalls[0].findingID)
	}
}

// fakeActionApplier records every action it receives so tests can
// assert the engine actually applied (not just dispatched) an action.
type fakeActionApplier struct {
	linkCalls []struct {
		findingID int64
		runID     int64
		stepID    string
		reason    string
	}
	lessonCalls []struct {
		agentName string
		text      string
		runID     int64
		stepID    string
	}
}

func (f *fakeActionApplier) UpdateFindingStatus(int64, string, string, int64, string) error {
	return nil
}
func (f *fakeActionApplier) AddFindingTag(int64, string, string, int64, string) error    { return nil }
func (f *fakeActionApplier) RemoveFindingTag(int64, string, string, int64, string) error { return nil }
func (f *fakeActionApplier) SetFindingSeverityOverride(int64, string, string, int64, string) error {
	return nil
}
func (f *fakeActionApplier) LinkRunToFinding(findingID, runID int64, stepID, reason string) error {
	f.linkCalls = append(f.linkCalls, struct {
		findingID int64
		runID     int64
		stepID    string
		reason    string
	}{findingID, runID, stepID, reason})
	return nil
}
func (f *fakeActionApplier) EscalateRun(int64, string, string) error { return nil }
func (f *fakeActionApplier) RecordAgentLesson(agentName, text string, runID int64, stepID string) error {
	f.lessonCalls = append(f.lessonCalls, struct {
		agentName string
		text      string
		runID     int64
		stepID    string
	}{agentName, text, runID, stepID})
	return nil
}

// TestEngine_ApprovalGate_PolicyMixedActionsStillGates confirms the
// bypass requires every kind in the step's allowlist to be
// auto-approved — a single non-auto-approved kind keeps the gate.
func TestEngine_ApprovalGate_PolicyMixedActionsStillGates(t *testing.T) {
	spec := mustParse(t, `---
name: gated
description: gate stays when allowlist mixes auto-approved + restricted classes
steps:
  - id: contain
    approval: required
    actions:
      - link_run_to_finding
      - update_finding_status
    agent: x
    prompt: x
---`)
	store := newFakeStore(
		&Orchestration{ID: 1, Spec: spec},
		&RunRecord{ID: 1, OrchestrationID: 1, Status: RunStatusPending, TriggerKind: "manual"},
	)
	disp := &fakeDispatcher{}
	engine := NewEngine(store, disp)
	// Only "create" is auto-approved. update_finding_status is class
	// "modify", which is NOT auto-approved → gate must stay.
	engine.SetActionPolicy(Policy{AutoApprove: map[string]bool{ClassCreate: true}})

	if err := engine.Run(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if store.run.Status != RunStatusApprovalRequired {
		t.Errorf("run.Status = %q, want %q (modify-class action keeps gate)", store.run.Status, RunStatusApprovalRequired)
	}
	if len(disp.calls) != 0 {
		t.Errorf("step should NOT have dispatched, got %d calls", len(disp.calls))
	}
}

// TestEngine_ApprovalGate_EmptyPolicy confirms backwards-compat: a
// zero-value Policy preserves today's gate-everything behaviour.
func TestEngine_ApprovalGate_EmptyPolicy(t *testing.T) {
	spec := mustParse(t, `---
name: gated
description: empty policy keeps existing approval gate
steps:
  - id: contain
    approval: required
    actions:
      - link_run_to_finding
    agent: x
    prompt: x
---`)
	store := newFakeStore(
		&Orchestration{ID: 1, Spec: spec},
		&RunRecord{ID: 1, OrchestrationID: 1, Status: RunStatusPending, TriggerKind: "manual"},
	)
	disp := &fakeDispatcher{}
	engine := NewEngine(store, disp)
	// No SetActionPolicy call → zero Policy → no class is auto-approved.

	if err := engine.Run(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if store.run.Status != RunStatusApprovalRequired {
		t.Errorf("run.Status = %q, want %q (empty policy must gate)", store.run.Status, RunStatusApprovalRequired)
	}
}

func TestEngine_TriggerPayloadInTemplate(t *testing.T) {
	spec := mustParse(t, `---
name: triggered
description: trigger inputs reach prompts
steps:
  - id: triage
    agent: x
    prompt: "scan host {{trigger.host}} finding {{trigger.finding_id}}"
---`)
	store := newFakeStore(
		&Orchestration{ID: 1, Spec: spec},
		&RunRecord{
			ID:              1,
			OrchestrationID: 1,
			Status:          RunStatusPending,
			TriggerKind:     "finding",
			TriggerPayload:  `{"host":"edr-fedora-3","finding_id":42}`,
		},
	)
	disp := &fakeDispatcher{}
	if err := NewEngine(store, disp).Run(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if got := disp.calls[0].Prompt; !strings.Contains(got, "edr-fedora-3") || !strings.Contains(got, "42") {
		t.Errorf("rendered prompt didn't pick up trigger context: %q", got)
	}
}

func TestRenderParams_StringsAreTemplated(t *testing.T) {
	env := map[string]any{
		"trigger": map[string]any{
			"ioc_kind": "sha256",
			"ioc":      "deadbeef",
		},
	}
	in := map[string]any{
		"kind":  "{{trigger.ioc_kind}}",
		"value": "{{trigger.ioc}}",
		"limit": 50, // non-string passes through unchanged
	}
	got, err := renderParams(in, env)
	if err != nil {
		t.Fatalf("renderParams: %v", err)
	}
	if got["kind"] != "sha256" {
		t.Errorf("kind = %q, want %q", got["kind"], "sha256")
	}
	if got["value"] != "deadbeef" {
		t.Errorf("value = %q, want %q", got["value"], "deadbeef")
	}
	if got["limit"] != 50 {
		t.Errorf("limit = %v, want 50 (non-string should pass through)", got["limit"])
	}
	// Original map should not be mutated.
	if in["kind"] != "{{trigger.ioc_kind}}" {
		t.Errorf("renderParams mutated input map: kind = %q", in["kind"])
	}
}

// TestEngine_DataParamsRendered confirms the engine substitutes
// {{trigger.*}} placeholders in `data:` params before calling the
// resolver. Without this, the t2-fleet-ioc-hunt rewrite would silently
// look up the literal string `{{trigger.ioc}}` and always miss.
func TestEngine_DataParamsRendered(t *testing.T) {
	spec := mustParse(t, `---
name: data-params
description: data params should be templated against trigger payload
steps:
  - id: lookup
    agent: x
    prompt: "ok"
    data:
      ioc:
        query: iocs.lookup
        params: { kind: "{{trigger.ioc_kind}}", value: "{{trigger.ioc}}" }
---`)
	store := newFakeStore(
		&Orchestration{ID: 1, Spec: spec},
		&RunRecord{
			ID:              1,
			OrchestrationID: 1,
			Status:          RunStatusPending,
			TriggerKind:     "finding",
			TriggerPayload:  `{"ioc_kind":"sha256","ioc":"deadbeef"}`,
		},
	)
	resolver := &recordingResolver{}
	engine := NewEngine(store, &fakeDispatcher{})
	engine.SetDataResolver(resolver)

	if err := engine.Run(context.Background(), 1); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(resolver.calls) != 1 {
		t.Fatalf("expected one resolver call; got %d", len(resolver.calls))
	}
	c := resolver.calls[0]
	if c.query != "iocs.lookup" {
		t.Errorf("query = %q, want iocs.lookup", c.query)
	}
	if c.params["kind"] != "sha256" {
		t.Errorf("params.kind = %q, want %q (was the {{trigger.ioc_kind}} placeholder rendered?)", c.params["kind"], "sha256")
	}
	if c.params["value"] != "deadbeef" {
		t.Errorf("params.value = %q, want %q (was the {{trigger.ioc}} placeholder rendered?)", c.params["value"], "deadbeef")
	}
}

type recordingResolver struct {
	calls []struct {
		query  string
		params map[string]any
	}
}

func (r *recordingResolver) Resolve(_ context.Context, query string, params map[string]any) (any, error) {
	r.calls = append(r.calls, struct {
		query  string
		params map[string]any
	}{query, params})
	return map[string]any{"valid": true}, nil
}

// TestEngine_AppliesReflectWithLessons confirms the engine dispatches
// each lesson string from a reflect_with_lessons action through the
// applier, with step.Agent threaded as the agent_name. Without this
// test, a regression in the dispatchAction switch (e.g. a `break` that
// silently no-ops the case) would not be caught — the per-action
// allowlist + class registry would still report the action as valid.
func TestEngine_AppliesReflectWithLessons(t *testing.T) {
	spec := mustParse(t, `---
name: reflective
description: agent emits two lessons after the step
steps:
  - id: classify
    agent: investigator
    actions:
      - reflect_with_lessons
    prompt: x
---`)
	store := newFakeStore(
		&Orchestration{ID: 1, Spec: spec},
		&RunRecord{ID: 1, OrchestrationID: 1, Status: RunStatusPending, TriggerKind: "manual"},
	)
	disp := &fakeDispatcher{
		resultByStep: map[string]DispatchResult{
			"classify": {
				Status:       StepStatusCompleted,
				RunID:        "run-classify",
				CPInstanceID: "local",
				HostResolved: "h1",
				Findings: []DispatchedFinding{
					{
						Category: OrchestrationResultCategory,
						Title:    "result",
						Attributes: map[string]any{
							"actions": []any{
								map[string]any{
									"kind":    "reflect_with_lessons",
									"lessons": []any{"keep grep tight", "validate before delete"},
								},
							},
						},
					},
				},
			},
		},
	}
	applier := &fakeActionApplier{}
	engine := NewEngine(store, disp)
	engine.SetActionApplier(applier)

	if err := engine.Run(context.Background(), 1); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(applier.lessonCalls) != 2 {
		t.Fatalf("expected 2 RecordAgentLesson calls; got %d", len(applier.lessonCalls))
	}
	for i, want := range []string{"keep grep tight", "validate before delete"} {
		if applier.lessonCalls[i].text != want {
			t.Errorf("lessonCalls[%d].text = %q, want %q", i, applier.lessonCalls[i].text, want)
		}
		if applier.lessonCalls[i].agentName != "investigator" {
			t.Errorf("lessonCalls[%d].agentName = %q, want investigator (from step.Agent)", i, applier.lessonCalls[i].agentName)
		}
	}
}
