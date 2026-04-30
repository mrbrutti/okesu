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
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/section9labs/okesu/controlplane/api/enrichment"
	"github.com/section9labs/okesu/controlplane/db"
)

// FindingActionApplier is wired into the orchestrator at server boot
// via Engine.SetActionApplier.
type FindingActionApplier struct {
	store      *db.Store
	enrichment *enrichment.Service
}

func NewFindingActionApplier(store *db.Store) *FindingActionApplier {
	return &FindingActionApplier{store: store}
}

// SetEnrichmentService installs the IOC-enrichment orchestrator backing
// the EnrichIOC action. Called at boot from server.go after the
// enrichment.Service is constructed. Safe to leave unset — EnrichIOC
// then returns an error rather than silently swallowing the request.
func (a *FindingActionApplier) SetEnrichmentService(s *enrichment.Service) {
	a.enrichment = s
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

// RecordAgentLesson delegates to the db.Store's bounded KV. Daemons
// read the top-N for the same agent_name on tick prep and prepend
// them to the system prompt.
func (a *FindingActionApplier) RecordAgentLesson(agentName, text string, runID int64, stepID string) error {
	return a.store.RecordAgentLesson(agentName, text, runID, stepID)
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

// FindingAgent satisfies orchestrator.FindingAgentLookup. The
// engine's auto-lesson hook calls this after closing a finding as
// false_positive (or dropping its severity to INFO/LOW) to find
// the daemon agent that originally emitted it. Returns "" when the
// finding has no agent stamped — auto-lessons silently skip.
func (a *FindingActionApplier) FindingAgent(findingID int64) (string, error) {
	f, err := a.store.FindingByID(findingID)
	if err != nil {
		return "", err
	}
	if f.Agent.Valid {
		return f.Agent.String, nil
	}
	return "", nil
}

// EnrichIOC routes to the enrichment service: cache check, live vendor
// calls, persist results in ioc_enrichments. Best-effort — partial
// vendor failures are logged but don't fail the orchestration.
func (a *FindingActionApplier) EnrichIOC(iocID, runID int64, stepID string) error {
	if a.enrichment == nil {
		return errors.New("enrichment service not configured")
	}
	rec, err := a.store.GetIOC(iocID)
	if err != nil {
		return fmt.Errorf("EnrichIOC: lookup ioc %d: %w", iocID, err)
	}
	_, err = a.enrichment.Enrich(context.Background(), rec.ID, rec.Kind, rec.NormalizedValue)
	return err
}

// enrichmentStoreAdapter bridges *db.Store to enrichment.Store. The
// adapter holds the default TTL so Upsert can stamp expires_at.
type enrichmentStoreAdapter struct {
	store *db.Store
	ttl   time.Duration
}

// NewEnrichmentStoreAdapter constructs the cache-bridge that wraps a
// *db.Store as an enrichment.Store. ttl is the default cache lifetime
// applied to Upserts whose Result.TTL is zero.
func NewEnrichmentStoreAdapter(store *db.Store, ttl time.Duration) enrichment.Store {
	return &enrichmentStoreAdapter{store: store, ttl: ttl}
}

func (a *enrichmentStoreAdapter) GetFresh(iocID int64, adapter string) (*enrichment.Result, error) {
	rec, err := a.store.GetFreshIOCEnrichment(iocID, adapter)
	if err != nil {
		return nil, err
	}
	return &enrichment.Result{
		Adapter: rec.Adapter,
		Verdict: rec.Verdict,
		Score:   rec.Score,
		RawJSON: rec.RawJSON,
	}, nil
}

func (a *enrichmentStoreAdapter) Upsert(iocID int64, adapter string, r *enrichment.Result) error {
	ttl := r.TTL
	if ttl == 0 {
		ttl = a.ttl
	}
	_, err := a.store.UpsertIOCEnrichment(&db.IOCEnrichmentInsert{
		IOCID:     iocID,
		Adapter:   adapter,
		Verdict:   r.Verdict,
		Score:     r.Score,
		RawJSON:   r.RawJSON,
		ExpiresAt: time.Now().Add(ttl),
	})
	return err
}
