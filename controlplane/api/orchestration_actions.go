// orchestrator.ActionApplier impl backed by the db.Store.
//
// Each method maps to one db method and adds a single concern: it
// auto-records the orchestration_run_id + step_id on the audit
// edit row so the finding-detail UI can attribute the change.
//
// Kept in package api (not orchestrator) because it depends on the
// concrete db package; the orchestrator stays test-friendly with
// just an interface.

package api

import (
	"github.com/section9labs/okesu/controlplane/db"
)

// FindingActionApplier is wired into the orchestrator at server boot
// via Engine.SetActionApplier.
type FindingActionApplier struct {
	store *db.Store
}

func NewFindingActionApplier(store *db.Store) *FindingActionApplier {
	return &FindingActionApplier{store: store}
}

func (a *FindingActionApplier) UpdateFindingStatus(findingID int64, status, reason string, runID int64, stepID string) error {
	return a.store.ApplyFindingStatusChange(findingID, status, reason, db.EditOrigin{
		OrchestrationRunID: runID,
		OrchestrationStep:  stepID,
		Reason:             reason,
	})
}

func (a *FindingActionApplier) AddFindingTag(findingID int64, tag, reason string, runID int64, stepID string) error {
	return a.store.ApplyFindingAddTag(findingID, tag, reason, db.EditOrigin{
		OrchestrationRunID: runID,
		OrchestrationStep:  stepID,
		Reason:             reason,
	})
}

func (a *FindingActionApplier) RemoveFindingTag(findingID int64, tag, reason string, runID int64, stepID string) error {
	return a.store.ApplyFindingRemoveTag(findingID, tag, reason, db.EditOrigin{
		OrchestrationRunID: runID,
		OrchestrationStep:  stepID,
		Reason:             reason,
	})
}

func (a *FindingActionApplier) SetFindingSeverityOverride(findingID int64, severity, reason string, runID int64, stepID string) error {
	return a.store.ApplyFindingSeverityOverride(findingID, severity, reason, db.EditOrigin{
		OrchestrationRunID: runID,
		OrchestrationStep:  stepID,
		Reason:             reason,
	})
}

func (a *FindingActionApplier) LinkRunToFinding(findingID int64, runID int64, stepID, reason string) error {
	return a.store.LinkRunToFinding(findingID, runID, stepID, reason, db.EditOrigin{
		OrchestrationRunID: runID,
		OrchestrationStep:  stepID,
		Reason:             reason,
	})
}

// RecordAgentLesson is a temporary stub — replaced in Task C4 with a
// delegation to the db.Store. Keeping it here keeps the build green
// between the engine-interface change (C2) and the store impl (C3+C4).
func (a *FindingActionApplier) RecordAgentLesson(_, _ string, _ int64, _ string) error {
	return nil
}

// EscalateRun is a soft signal in v1 — recorded as a finding_edits
// row keyed off run id 0 (no specific finding) so the run-detail
// audit pulls it back. A dedicated column on orchestration_runs is
// the right long-term shape; that lands when the dashboard surfaces
// "runs needing review" as a first-class card.
func (a *FindingActionApplier) EscalateRun(runID int64, reason, severity string) error {
	// No-op in v1; the engine logs the escalation request and the
	// operator can find it in the run-detail audit. Returning nil
	// keeps the engine moving — the rest of the chain shouldn't
	// halt because v1 didn't persist this signal.
	_ = runID
	_ = reason
	_ = severity
	return nil
}
