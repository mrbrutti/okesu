// Engine — the orchestration state machine.
//
// Each call to Engine.Run resumes (or starts) one orchestration_run
// row, walking its steps sequentially until it hits a terminal state
// or an approval gate. The engine is stateless across invocations:
// state lives in the DB, so a CP restart resumes runs cleanly on the
// next nudge from the API layer.
//
// Step lifecycle (per step):
//
//   1. Evaluate `when`. False → skip (status='skipped'), advance.
//   2. If `approval: required` and not yet approved → mark
//      'waiting_approval', flip the run to 'approval_required',
//      return. The /approve endpoint flips the step back and
//      re-invokes Run.
//   3. Render `prompt` and resolve `node`. Resolve `cp`.
//   4. Dispatch via the Dispatcher interface (local or federated).
//   5. Block until the dispatcher reports a terminal status.
//   6. Distil findings: pull `category=orchestration_result` into
//      result_json; capture last 4KB of stdout into output_summary.
//   7. Failed + !continue_on_error → halt orchestration as 'failed'.
//   8. Otherwise advance to the next step.
//
// Cross-CP execution (Phase B) plugs into the Dispatcher interface
// without touching the engine — the local impl forwards to the
// existing tunnel runtime; the federated impl posts to the child CP's
// /api/v1/federation/runs.

package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// Run statuses persisted in orchestration_runs.status.
const (
	RunStatusPending          = "pending"
	RunStatusRunning          = "running"
	RunStatusApprovalRequired = "approval_required"
	RunStatusCompleted        = "completed"
	RunStatusFailed           = "failed"
	RunStatusCancelled        = "cancelled"
)

// Step statuses persisted in orchestration_steps.status.
const (
	StepStatusPending         = "pending"
	StepStatusWaitingApproval = "waiting_approval"
	StepStatusRunning         = "running"
	StepStatusCompleted       = "completed"
	StepStatusFailed          = "failed"
	StepStatusSkipped         = "skipped"
)

// OrchestrationResultCategory is the finding `category` an agent emits
// when it wants to expose a structured payload to subsequent steps as
// `{{stepN.result}}`. The engine watches for this category in each
// step's findings and stashes the attributes blob.
const OrchestrationResultCategory = "orchestration_result"

// Store is the subset of *db.Store the engine needs. Defining it as
// an interface keeps the engine testable without spinning up a real
// SQLite (the test in this package uses a fake).
type Store interface {
	// Spec lookup
	GetOrchestration(id int64) (*Orchestration, error)

	// Run lifecycle
	GetOrchestrationRun(id int64) (*RunRecord, error)
	UpdateOrchestrationRunStatus(id int64, status string, currentStepID, errMsg string) error
	FinishOrchestrationRun(id int64, status string, errMsg string) error

	// Step lifecycle
	ListOrchestrationSteps(runID int64) ([]*StepRecord, error)
	UpsertOrchestrationStep(step *StepRecord) error
	GetOrchestrationStep(runID int64, stepID string) (*StepRecord, error)
}

// ActionApplier is the surface the engine uses to apply CP-side
// mutations requested by the agent (status changes, tagging, etc.).
// The concrete impl in package `api` wraps the db.Store finding
// methods. Engines run with applier=nil silently skip actions —
// that's the test-friendly default.
type ActionApplier interface {
	UpdateFindingStatus(findingID int64, status, reason string, runID int64, stepID string) error
	AddFindingTag(findingID int64, tag, reason string, runID int64, stepID string) error
	RemoveFindingTag(findingID int64, tag, reason string, runID int64, stepID string) error
	SetFindingSeverityOverride(findingID int64, severity, reason string, runID int64, stepID string) error
	LinkRunToFinding(findingID int64, runID int64, stepID, reason string) error
	EscalateRun(runID int64, reason, severity string) error
}

// Orchestration is what Store.GetOrchestration returns — the parsed
// spec plus identity columns. Defined in the engine package so the
// engine doesn't depend on the db package's row types directly.
type Orchestration struct {
	ID          int64
	Name        string
	Description string
	SpecYAML    string
	Spec        *Spec
	Enabled     bool
}

// RunRecord mirrors orchestration_runs but lives in the engine
// package so tests can construct one without importing db.
type RunRecord struct {
	ID               int64
	OrchestrationID  int64
	Status           string
	TriggerKind      string
	TriggerPayload   string // JSON
	CurrentStepID    string
	StartedAt        time.Time
	EndedAt          *time.Time
	StartedByUserID  int64
	StartedByEmail   string
	Error            string
}

// StepRecord mirrors orchestration_steps. ResultJSON / OutputSummary
// are the persisted distillation produced by the engine after each
// step completes.
type StepRecord struct {
	OrchestrationRunID int64
	StepID             string
	StepIdx            int
	Status             string
	RunID              string // dispatched Run id, "" if not yet dispatched
	CPInstanceID       string
	NodeID             int64
	RenderedPrompt     string
	ResultJSON         string
	OutputSummary      string
	StartedAt          *time.Time
	EndedAt            *time.Time
	Error              string
	ApprovedAt         *time.Time
	ApprovedByUserID   int64
	// DataSnapshot is the JSON-encoded result of resolving the step's
	// `data:` block. Persisted so replays + audits can reconstruct
	// what the agent actually saw, even if the underlying tables
	// have moved on. Empty when the step had no data: block.
	DataSnapshot string
}

// Dispatcher is the contract between the engine and the run runtime.
// The local impl wraps the existing tunnel-based run path; the
// federated impl forwards to a child CP via the federation proxy.
//
// Dispatch returns once the run has terminated (succeeded, failed,
// timed out). The engine then reads structured outputs and decides
// whether to advance or halt.
type Dispatcher interface {
	Dispatch(ctx context.Context, req DispatchRequest) (DispatchResult, error)
}

type DispatchRequest struct {
	StepID       string // for logging / correlation
	AgentName    string
	NodeSelector string // hostname or "id:N" or "role:X"; "" = local-only step
	CPInstanceID string // "local" | "<id>" | "*"
	Prompt       string // already rendered
	Inputs       map[string]any
	Timeout      time.Duration

	// DispatchMode is the operator's preferred runtime for this
	// step: "" (auto), "tunnel" (require tunnel; auto-start if not
	// running), "jobs" (require pull queue). Resolved from spec
	// step → orchestration default → node preference → "auto".
	// The routingDispatcher reads this to pick a path.
	DispatchMode string
}

type DispatchResult struct {
	RunID         string             // id assigned by the dispatcher (a Run id for local)
	Status        string             // StepStatusCompleted | StepStatusFailed
	Findings      []DispatchedFinding
	OutputTail    string             // last 4KB of stdout
	Error         string
	CPInstanceID  string             // resolved CP (the child it ran on if federated; "local" if local)
	HostResolved  string             // hostname the run actually targeted

	// PerNode is populated only when the step fanned out to multiple
	// nodes. The engine aggregates these into a single DispatchResult
	// for the step's persisted record + bindings, but exposes them
	// individually so templates can address per-node outputs via
	// `{{stepN.byNode["host-1"].findings}}`.
	PerNode []NodeDispatch `json:"per_node,omitempty"`
}

// NodeDispatch captures one host's slice of a fan-out step.
type NodeDispatch struct {
	Host       string              `json:"host"`
	Status     string              `json:"status"`
	Findings   []DispatchedFinding `json:"findings,omitempty"`
	OutputTail string              `json:"output_tail,omitempty"`
	Error      string              `json:"error,omitempty"`
	RunID      string              `json:"run_id,omitempty"`
}

// DispatchedFinding is the minimum shape the engine needs from the
// per-step run output. It mirrors the parts of `findings.go` the
// orchestrator cares about — the Dispatcher fills it from the run's
// emitted JSONL.
type DispatchedFinding struct {
	Severity   string
	Title      string
	Category   string
	Resource   string
	DedupKey   string
	Attributes map[string]any
}

// Engine drives an orchestration run to completion (or to its next
// gate). One Engine per CP — concurrency-safe across runs because each
// Run() call walks its own DB row.
type Engine struct {
	store      Store
	dispatcher Dispatcher
	// applier is optional — when nil, action requests in agent
	// outputs are logged and ignored. Production wires this to the
	// db-backed implementation.
	applier ActionApplier
	// data resolves a step's `data:` block before dispatch, binding
	// the result into the prompt template as `{{data.<name>}}`. Nil
	// disables the feature — steps with `data:` then fail with a
	// clear "no data resolver" error rather than silently dropping
	// the bindings.
	data DataResolver
	// actionPolicy is the operator-set per-class auto-approve toggle.
	// When every kind in a step's `actions:` allowlist falls in an
	// auto-approved class, the engine bypasses the step-level
	// approval gate. Zero value (empty AutoApprove) means "gate as
	// today" — strictly additive, never relaxes existing constraints.
	actionPolicy Policy
}

// DataResolver fetches structured CP-side data on behalf of a step's
// `data:` block. The api package implements this with a registry of
// query handlers (findings.list, runs.list, ...). Each Resolve call
// is independent — handlers handle their own validation, auth, and
// rate limiting. The orchestrator package only knows about the
// interface so the engine itself stays free of CP-specific imports.
type DataResolver interface {
	Resolve(ctx context.Context, query string, params map[string]any) (any, error)
}

func NewEngine(store Store, dispatcher Dispatcher) *Engine {
	return &Engine{store: store, dispatcher: dispatcher}
}

// SetActionApplier installs the CP-mutation backend. Called once at
// boot from server.go after the db.Store is constructed. Safe to
// call zero times — the engine then runs in "log-only" mode.
func (e *Engine) SetActionApplier(a ActionApplier) {
	e.applier = a
}

// SetDataResolver installs the read-side handler for step `data:`
// blocks. Called once at boot from the coordinator. Safe to leave
// unset — steps that don't declare `data:` are unaffected; steps
// that do then fail at dispatch with a clear error.
func (e *Engine) SetDataResolver(d DataResolver) {
	e.data = d
}

// SetActionPolicy installs the per-class auto-approve toggle the
// engine consults at the step-approval gate. Called once at boot from
// the coordinator with values lifted out of the YAML config. Safe to
// leave unset — the zero value preserves today's "every approval-
// required step gates" behaviour.
func (e *Engine) SetActionPolicy(p Policy) {
	e.actionPolicy = p
}

// Run resumes the orchestration_run identified by runID. It's
// idempotent: calling it on a run that's already terminal is a no-op.
// The HTTP layer calls Run() in a goroutine when starting a new run
// and again whenever a step is approved.
func (e *Engine) Run(ctx context.Context, runID int64) error {
	run, err := e.store.GetOrchestrationRun(runID)
	if err != nil {
		return fmt.Errorf("engine: load run %d: %w", runID, err)
	}
	if isTerminal(run.Status) {
		return nil
	}

	orch, err := e.store.GetOrchestration(run.OrchestrationID)
	if err != nil {
		return fmt.Errorf("engine: load orchestration %d: %w", run.OrchestrationID, err)
	}
	if orch.Spec == nil {
		spec, err := Parse(orch.SpecYAML)
		if err != nil {
			return fmt.Errorf("engine: parse spec: %w", err)
		}
		orch.Spec = spec
	}

	steps, err := e.store.ListOrchestrationSteps(runID)
	if err != nil {
		return fmt.Errorf("engine: list steps: %w", err)
	}
	stepsByID := map[string]*StepRecord{}
	for _, st := range steps {
		stepsByID[st.StepID] = st
	}

	// Build the env scaffold. Each completed step adds bindings as we
	// walk; the trigger payload feeds {{trigger.*}}.
	env := Env{}
	if run.TriggerPayload != "" {
		var trig map[string]any
		if err := json.Unmarshal([]byte(run.TriggerPayload), &trig); err == nil {
			env["trigger"] = trig
		}
	}
	if env["trigger"] == nil {
		env["trigger"] = map[string]any{"kind": run.TriggerKind}
	}

	// Re-bind already-completed steps so a resumed run can template
	// later steps against earlier outputs.
	for _, st := range steps {
		if st.Status == StepStatusCompleted || st.Status == StepStatusFailed {
			env[st.StepID] = bindingFromRecord(st)
		}
	}

	if run.Status == RunStatusPending {
		_ = e.store.UpdateOrchestrationRunStatus(run.ID, RunStatusRunning, "", "")
	}

	for i, step := range orch.Spec.Steps {
		rec := stepsByID[step.ID]
		if rec == nil {
			rec = &StepRecord{
				OrchestrationRunID: runID,
				StepID:             step.ID,
				StepIdx:            i,
				Status:             StepStatusPending,
			}
		}

		switch rec.Status {
		case StepStatusCompleted, StepStatusFailed, StepStatusSkipped:
			continue
		case StepStatusRunning:
			// Crashed mid-step — mark failed so the run reflects truth.
			rec.Status = StepStatusFailed
			rec.Error = "engine restart while step was running"
			_ = e.store.UpsertOrchestrationStep(rec)
			if !orch.Spec.EffectiveContinueOnError(&step) {
				return e.haltFailed(run.ID, step.ID, rec.Error)
			}
			continue
		}

		// `when` evaluation — skip if false.
		if step.When != "" {
			ok, err := EvalBool(step.When, env)
			if err != nil {
				rec.Status = StepStatusFailed
				rec.Error = "when expression: " + err.Error()
				_ = e.store.UpsertOrchestrationStep(rec)
				return e.haltFailed(run.ID, step.ID, rec.Error)
			}
			if !ok {
				rec.Status = StepStatusSkipped
				now := time.Now().UTC()
				rec.EndedAt = &now
				_ = e.store.UpsertOrchestrationStep(rec)
				env[step.ID] = skippedBinding()
				continue
			}
		}

		// Approval gate — pause the run if not yet approved, unless
		// the operator-set action-class policy auto-approves every
		// kind in the step's `actions:` allowlist. The bypass only
		// kicks in for steps that declare a non-empty allowlist; a
		// bare `approval: required` (no actions) always gates.
		if step.Approval == "required" && rec.ApprovedAt == nil {
			if policyBypassesGate(step, e.actionPolicy) {
				log.Printf("orchestrator: step %q approval bypassed by action-class policy (allowlist=%v)",
					step.ID, step.Actions)
			} else {
				rec.Status = StepStatusWaitingApproval
				// Render the prompt now so the operator sees what
				// they're approving in the UI before clicking Approve.
				renderedPrompt, _ := Render(step.Prompt, env)
				rec.RenderedPrompt = renderedPrompt
				_ = e.store.UpsertOrchestrationStep(rec)
				_ = e.store.UpdateOrchestrationRunStatus(run.ID, RunStatusApprovalRequired, step.ID, "")
				return nil
			}
		}

		// Resolve the step's `data:` block before render so the
		// prompt template can reference `{{data.X}}`. Errors are
		// terminal for the step — a typo'd query name shouldn't
		// silently produce an empty binding the agent then tries to
		// reason about. The audit copy lives on rec.DataSnapshot.
		if len(step.Data) > 0 {
			if e.data == nil {
				rec.Status = StepStatusFailed
				rec.Error = "step declares data: but no resolver is configured (CP boot misconfiguration)"
				_ = e.store.UpsertOrchestrationStep(rec)
				return e.haltFailed(run.ID, step.ID, rec.Error)
			}
			bindings := make(map[string]any, len(step.Data))
			for name, src := range step.Data {
				val, derr := e.data.Resolve(ctx, src.Query, src.Params)
				if derr != nil {
					rec.Status = StepStatusFailed
					rec.Error = fmt.Sprintf("data.%s (%s): %v", name, src.Query, derr)
					_ = e.store.UpsertOrchestrationStep(rec)
					return e.haltFailed(run.ID, step.ID, rec.Error)
				}
				bindings[name] = val
			}
			env["data"] = bindings
			if snap, jerr := json.Marshal(bindings); jerr == nil {
				rec.DataSnapshot = string(snap)
			}
		}

		// Render templated fields.
		renderedPrompt, err := Render(step.Prompt, env)
		if err != nil {
			rec.Status = StepStatusFailed
			rec.Error = "render prompt: " + err.Error()
			_ = e.store.UpsertOrchestrationStep(rec)
			return e.haltFailed(run.ID, step.ID, rec.Error)
		}

		// Resolve node target(s). EffectiveNodes returns either the
		// single `node:` value or the `nodes:` array; an empty result
		// means "local-only step" (no remote dispatch). When the spec
		// declared a target but the template rendered to empty (most
		// commonly: `node: "{{trigger.host}}"` on a manual run with no
		// finding context), we fail the step with a clear message
		// rather than letting it fall through to the dispatcher's
		// generic "local-only not supported" error.
		rawTargets := step.EffectiveNodes()
		targets := make([]string, 0, len(rawTargets))
		for _, t := range rawTargets {
			r, terr := Render(t, env)
			if terr != nil {
				rec.Status = StepStatusFailed
				rec.Error = "render node: " + terr.Error()
				_ = e.store.UpsertOrchestrationStep(rec)
				return e.haltFailed(run.ID, step.ID, rec.Error)
			}
			if r != "" {
				targets = append(targets, r)
			}
		}
		if len(rawTargets) > 0 && len(targets) == 0 {
			rec.Status = StepStatusFailed
			rec.Error = fmt.Sprintf(
				"node selector(s) %v rendered to empty — most likely a {{trigger.X}} template was unresolved. "+
					"This orchestration was triggered manually but expects a finding-trigger context. "+
					"Either run via auto-trigger or add an `inputs:` block so the operator can supply the missing field.",
				rawTargets,
			)
			_ = e.store.UpsertOrchestrationStep(rec)
			return e.haltFailed(run.ID, step.ID, rec.Error)
		}
		cpSel := orch.Spec.EffectiveCP(&step)

		// Update step record + run cursor before dispatch.
		now := time.Now().UTC()
		rec.Status = StepStatusRunning
		rec.RenderedPrompt = renderedPrompt
		rec.CPInstanceID = cpSel
		rec.StartedAt = &now
		_ = e.store.UpsertOrchestrationStep(rec)
		_ = e.store.UpdateOrchestrationRunStatus(run.ID, RunStatusRunning, step.ID, "")

		// Dispatch — single-node steps go through the dispatcher
		// directly; multi-node steps fan out and aggregate.
		stepCtx := ctx
		if to := orch.Spec.EffectiveTimeout(&step); to > 0 {
			var cancel context.CancelFunc
			stepCtx, cancel = context.WithTimeout(ctx, to)
			defer cancel()
		}

		var result DispatchResult
		var dispatchErr error
		switch len(targets) {
		case 0, 1:
			node := ""
			if len(targets) == 1 {
				node = targets[0]
			}
			result, dispatchErr = e.dispatcher.Dispatch(stepCtx, DispatchRequest{
				StepID:       step.ID,
				AgentName:    step.Agent,
				NodeSelector: node,
				CPInstanceID: cpSel,
				Prompt:       renderedPrompt,
				Inputs:       step.Inputs,
				Timeout:      orch.Spec.EffectiveTimeout(&step),
				DispatchMode: orch.Spec.EffectiveDispatch(&step),
			})
		default:
			result, dispatchErr = fanOut(stepCtx, e.dispatcher, step, renderedPrompt, cpSel, targets, orch.Spec.EffectiveTimeout(&step), orch.Spec.EffectiveDispatch(&step))
		}

		// Persist step outcome.
		ended := time.Now().UTC()
		rec.EndedAt = &ended
		if dispatchErr != nil {
			rec.Status = StepStatusFailed
			rec.Error = dispatchErr.Error()
			_ = e.store.UpsertOrchestrationStep(rec)
			env[step.ID] = bindingFromRecord(rec)
			if !orch.Spec.EffectiveContinueOnError(&step) {
				return e.haltFailed(run.ID, step.ID, dispatchErr.Error())
			}
			continue
		}
		rec.RunID = result.RunID
		rec.CPInstanceID = result.CPInstanceID
		rec.OutputSummary = tail4K(result.OutputTail)
		// Result distillation: prefer the structured per-step result
		// from the agent's orchestration_result finding. For multi-node
		// steps, the fan-out aggregator stashes byNode under .result
		// directly so the binding shape is preserved.
		if r := pickResult(result.Findings); r != nil {
			b, _ := json.Marshal(r)
			rec.ResultJSON = string(b)
		} else if len(result.PerNode) > 0 {
			b, _ := json.Marshal(map[string]any{"byNode": perNodeMap(result.PerNode)})
			rec.ResultJSON = string(b)
		}
		rec.Status = result.Status
		if result.Error != "" {
			rec.Error = result.Error
		}
		_ = e.store.UpsertOrchestrationStep(rec)

		// Apply any CP-side actions the agent requested. Only on
		// success — a failed step's actions are intentionally
		// dropped so a half-failing T1 doesn't mutate state.
		if rec.Status == StepStatusCompleted {
			e.applyStepActions(run.ID, step, result)
		}

		env[step.ID] = bindingFromResult(rec, result)

		if rec.Status == StepStatusFailed && !orch.Spec.EffectiveContinueOnError(&step) {
			return e.haltFailed(run.ID, step.ID, rec.Error)
		}
	}

	// All steps walked.
	return e.store.FinishOrchestrationRun(run.ID, RunStatusCompleted, "")
}

// Approve flips a waiting step to pending and re-invokes Run. Called
// from the /approve endpoint.
func (e *Engine) Approve(ctx context.Context, runID int64, stepID string, userID int64) error {
	step, err := e.store.GetOrchestrationStep(runID, stepID)
	if err != nil {
		return err
	}
	if step.Status != StepStatusWaitingApproval {
		return fmt.Errorf("step %q is not waiting for approval (status=%s)", stepID, step.Status)
	}
	now := time.Now().UTC()
	step.ApprovedAt = &now
	step.ApprovedByUserID = userID
	step.Status = StepStatusPending
	if err := e.store.UpsertOrchestrationStep(step); err != nil {
		return err
	}
	return e.Run(ctx, runID)
}

// haltFailed marks the orchestration_run failed, returns a
// nil error so the caller (HTTP handler) doesn't double-log.
func (e *Engine) haltFailed(runID int64, stepID, msg string) error {
	if msg == "" {
		msg = "step " + stepID + " failed"
	}
	return e.store.FinishOrchestrationRun(runID, RunStatusFailed, msg)
}

// ── helpers ──────────────────────────────────────────────────────────

func isTerminal(s string) bool {
	switch s {
	case RunStatusCompleted, RunStatusFailed, RunStatusCancelled:
		return true
	}
	return false
}

// policyBypassesGate reports whether the engine's action-class
// auto-approve policy fully covers a step's allowlist. Bypass requires:
//
//  1. The step declares a non-empty actions: allowlist. A bare
//     `approval: required` step (no actions) cannot be bypassed —
//     there's nothing for the class taxonomy to evaluate, so the
//     operator-intent of "this step needs human eyes" wins.
//  2. EVERY kind in the allowlist resolves to a class the policy
//     auto-approves. A single restricted-class kind keeps the gate.
//
// Unknown kinds map to ClassModify via ClassFor — registering a new
// kind without updating the registry results in continued gating
// (safe default), not a silent auto-apply.
func policyBypassesGate(step StepSpec, policy Policy) bool {
	if len(step.Actions) == 0 {
		return false
	}
	for _, kind := range step.Actions {
		if !policy.AllowsClass(ClassFor(kind)) {
			return false
		}
	}
	return true
}

// applyStepActions reads the agent's `actions:` array from the
// orchestration_result, validates each entry against the step's
// allowlist, and dispatches via the registered applier. Failures
// per-action are logged but never abort the run — the engine's job
// is to keep the orchestration moving.
func (e *Engine) applyStepActions(runID int64, step StepSpec, result DispatchResult) {
	if e.applier == nil {
		return
	}
	resAttr := pickResult(result.Findings)
	if resAttr == nil {
		return
	}
	rawActions, ok := resAttr["actions"]
	if !ok {
		return
	}
	parsed, err := ParseActions(rawActions)
	if err != nil {
		log.Printf("engine: actions parse for run=%d step=%s: %v", runID, step.ID, err)
		return
	}
	allowed, rejected := AllowedByStep(step, parsed)
	for _, a := range rejected {
		log.Printf("engine: action %q rejected on run=%d step=%s (not in allowlist %v)", a.Kind, runID, step.ID, step.Actions)
	}
	for _, a := range allowed {
		if err := e.dispatchAction(runID, step.ID, a); err != nil {
			log.Printf("engine: action %q failed on run=%d step=%s: %v", a.Kind, runID, step.ID, err)
		}
	}
}

func (e *Engine) dispatchAction(runID int64, stepID string, a Action) error {
	switch a.Kind {
	case ActionUpdateFindingStatus:
		if a.FindingID == 0 || a.Status == "" {
			return fmt.Errorf("missing finding_id/status")
		}
		return e.applier.UpdateFindingStatus(a.FindingID, a.Status, a.Reason, runID, stepID)
	case ActionAddFindingTag:
		if a.FindingID == 0 || a.Tag == "" {
			return fmt.Errorf("missing finding_id/tag")
		}
		return e.applier.AddFindingTag(a.FindingID, a.Tag, a.Reason, runID, stepID)
	case ActionRemoveFindingTag:
		if a.FindingID == 0 || a.Tag == "" {
			return fmt.Errorf("missing finding_id/tag")
		}
		return e.applier.RemoveFindingTag(a.FindingID, a.Tag, a.Reason, runID, stepID)
	case ActionSetFindingSeverityOverride:
		if a.FindingID == 0 || a.Severity == "" {
			return fmt.Errorf("missing finding_id/severity")
		}
		return e.applier.SetFindingSeverityOverride(a.FindingID, a.Severity, a.Reason, runID, stepID)
	case ActionLinkRunToFinding:
		if a.FindingID == 0 {
			return fmt.Errorf("missing finding_id")
		}
		return e.applier.LinkRunToFinding(a.FindingID, runID, stepID, a.Reason)
	case ActionEscalate:
		return e.applier.EscalateRun(runID, a.Reason, a.Severity)
	}
	return fmt.Errorf("unhandled kind %q", a.Kind)
}

// pickResult finds the orchestration_result finding in a step's
// outputs. Returns nil if the agent didn't emit one.
func pickResult(findings []DispatchedFinding) map[string]any {
	for _, f := range findings {
		if f.Category == OrchestrationResultCategory {
			if f.Attributes != nil {
				return f.Attributes
			}
			return map[string]any{
				"title":    f.Title,
				"severity": f.Severity,
				"resource": f.Resource,
			}
		}
	}
	return nil
}

func tail4K(s string) string {
	const max = 4096
	if len(s) <= max {
		return s
	}
	return s[len(s)-max:]
}

// bindingFromResult builds the `{{stepN.*}}` map a completed step
// exposes. `output` is the dispatcher's tailed transcript; result is
// the distilled orchestration_result attributes (nil if missing).
func bindingFromResult(rec *StepRecord, result DispatchResult) map[string]any {
	findings := make([]any, 0, len(result.Findings))
	for _, f := range result.Findings {
		findings = append(findings, map[string]any{
			"severity":  f.Severity,
			"title":     f.Title,
			"category":  f.Category,
			"resource":  f.Resource,
			"dedup_key": f.DedupKey,
			"attributes": f.Attributes,
		})
	}
	var resultMap map[string]any
	if rec.ResultJSON != "" {
		_ = json.Unmarshal([]byte(rec.ResultJSON), &resultMap)
	}
	binding := map[string]any{
		"status":   rec.Status,
		"output":   result.OutputTail,
		"findings": findings,
		"result":   resultMap,
		"cp":       rec.CPInstanceID,
		"host":     result.HostResolved,
	}
	// Expose per-node breakdown for fan-out steps so templates can
	// drill in: `{{stepN.byNode["web-prod-01"].findings}}`. Also
	// surface a `nodes` array of hostnames for ordered iteration.
	if len(result.PerNode) > 0 {
		byNode := perNodeMap(result.PerNode)
		hosts := make([]string, 0, len(result.PerNode))
		for _, n := range result.PerNode {
			hosts = append(hosts, n.Host)
		}
		binding["byNode"] = byNode
		binding["nodes"] = hosts
	}
	return binding
}

// bindingFromRecord rebuilds a step's environment binding from its
// persisted record — used when resuming a run after restart or after
// an approval. The output transcript isn't persisted in full, so
// `{{stepN.output}}` reflects the tail summary.
func bindingFromRecord(rec *StepRecord) map[string]any {
	var resultMap map[string]any
	if rec.ResultJSON != "" {
		_ = json.Unmarshal([]byte(rec.ResultJSON), &resultMap)
	}
	return map[string]any{
		"status":   rec.Status,
		"output":   rec.OutputSummary,
		"findings": []any{}, // TODO: hydrate from run findings on resume (Phase A.4 follow-up)
		"result":   resultMap,
		"cp":       rec.CPInstanceID,
		"host":     "",
	}
}

func skippedBinding() map[string]any {
	return map[string]any{
		"status":   StepStatusSkipped,
		"output":   "",
		"findings": []any{},
		"result":   nil,
		"cp":       "",
		"host":     "",
	}
}

// fanOut dispatches a step to N nodes in parallel and aggregates
// results into a single DispatchResult. The step's overall status is
// `completed` if every node completed; `failed` if any node failed
// (the engine then decides whether to halt or advance based on
// continue_on_error).
//
// Findings are flattened so chained templates can read
// `{{stepN.findings}}` without caring whether the upstream was a
// single-node or fan-out step. Per-node detail stays available under
// PerNode for operators who want to drill in.
func fanOut(
	ctx context.Context,
	disp Dispatcher,
	step StepSpec,
	renderedPrompt string,
	cpSel string,
	targets []string,
	timeout time.Duration,
	dispatchMode string,
) (DispatchResult, error) {
	type one struct {
		node   string
		result DispatchResult
		err    error
	}
	results := make([]one, len(targets))
	var wg sync.WaitGroup
	for i, node := range targets {
		wg.Add(1)
		go func(i int, n string) {
			defer wg.Done()
			r, err := disp.Dispatch(ctx, DispatchRequest{
				StepID:       step.ID + "@" + n,
				AgentName:    step.Agent,
				NodeSelector: n,
				CPInstanceID: cpSel,
				Prompt:       renderedPrompt,
				Inputs:       step.Inputs,
				Timeout:      timeout,
				DispatchMode: dispatchMode,
			})
			results[i] = one{node: n, result: r, err: err}
		}(i, node)
	}
	wg.Wait()

	// Aggregate.
	out := DispatchResult{
		Status:       StepStatusCompleted,
		CPInstanceID: cpSel,
	}
	allFindings := make([]DispatchedFinding, 0)
	allTails := make([]string, 0, len(results))
	failedNodes := make([]string, 0)
	for _, r := range results {
		nd := NodeDispatch{
			Host:       r.node,
			OutputTail: r.result.OutputTail,
			RunID:      r.result.RunID,
			Findings:   r.result.Findings,
		}
		if r.err != nil {
			nd.Status = StepStatusFailed
			nd.Error = r.err.Error()
			failedNodes = append(failedNodes, r.node)
		} else {
			nd.Status = r.result.Status
			if r.result.Status == StepStatusFailed {
				if r.result.Error != "" {
					nd.Error = r.result.Error
				}
				failedNodes = append(failedNodes, r.node)
			}
		}
		out.PerNode = append(out.PerNode, nd)
		allFindings = append(allFindings, nd.Findings...)
		if r.result.OutputTail != "" {
			allTails = append(allTails, "── "+r.node+" ──\n"+r.result.OutputTail)
		}
	}
	out.Findings = allFindings
	out.OutputTail = strings.Join(allTails, "\n")
	if len(failedNodes) > 0 {
		out.Status = StepStatusFailed
		out.Error = "fan-out failed on: " + strings.Join(failedNodes, ", ")
	}
	return out, nil
}

// perNodeMap converts the engine's PerNode slice into a map keyed by
// hostname, ready to drop into the step's result_json under "byNode".
// Templates address it as `{{stepN.byNode["web-prod-01"]}}`.
func perNodeMap(nodes []NodeDispatch) map[string]any {
	out := make(map[string]any, len(nodes))
	for _, n := range nodes {
		out[n.Host] = map[string]any{
			"status":      n.Status,
			"findings":    n.Findings,
			"output_tail": n.OutputTail,
			"error":       n.Error,
			"run_id":      n.RunID,
		}
	}
	return out
}

// Spec is exported via the orchestrator package; keep import quiet.
var _ = errors.New
var _ = strings.Contains
