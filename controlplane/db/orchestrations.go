// Orchestration store methods. The orchestrator engine takes a
// narrow Store interface; an adapter in api/orchestrations.go
// satisfies it by delegating to the methods here. Keeping the db
// package free of orchestrator imports preserves the project's
// one-way dependency: api → orchestrator → (-) and api → db → (-).

package db

import (
	"database/sql"
	"errors"
	"time"
)

// ── orchestrations ──────────────────────────────────────────────────

type Orchestration struct {
	ID            int64
	Name          string
	Description   sql.NullString
	SpecYAML      string
	TriggerKind   string
	TriggerFilter sql.NullString
	TriggerCron   sql.NullString
	Enabled       bool
	CreatedAt     time.Time
	CreatedBy     sql.NullInt64
	UpdatedAt     time.Time
	LastFiredAt   sql.NullTime
	LastFiredBy   sql.NullString
}

// CreateOrchestration inserts a row. trigger_* are extracted by the
// caller (api layer parses the spec, persists trigger metadata so
// finding-trigger probes don't have to re-parse on every match).
func (s *Store) CreateOrchestration(name, description, specYAML, triggerKind, triggerFilter, triggerCron string, createdBy int64) (int64, error) {
	res, err := s.Exec(`
		INSERT INTO orchestrations (name, description, spec_yaml, trigger_kind, trigger_filter, trigger_cron, created_by)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, name, nullable(description), specYAML, triggerKind,
		nullable(triggerFilter), nullable(triggerCron),
		nullableInt64(createdBy))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateOrchestration overwrites spec + trigger metadata. Name isn't
// updated — operators rename via DELETE+CREATE; the enable toggle has
// its own setter.
func (s *Store) UpdateOrchestration(id int64, description, specYAML, triggerKind, triggerFilter, triggerCron string) error {
	_, err := s.Exec(`
		UPDATE orchestrations
		   SET description = ?, spec_yaml = ?, trigger_kind = ?, trigger_filter = ?, trigger_cron = ?,
		       updated_at = CURRENT_TIMESTAMP
		 WHERE id = ?
	`, nullable(description), specYAML, triggerKind,
		nullable(triggerFilter), nullable(triggerCron), id)
	return err
}

func (s *Store) DeleteOrchestration(id int64) error {
	_, err := s.Exec(`DELETE FROM orchestrations WHERE id = ?`, id)
	return err
}

func (s *Store) GetOrchestration(id int64) (*Orchestration, error) {
	row := s.QueryRow(`
		SELECT id, name, description, spec_yaml, trigger_kind, trigger_filter, trigger_cron,
		       enabled, created_at, created_by, updated_at, last_fired_at, last_fired_by
		  FROM orchestrations WHERE id = ?
	`, id)
	return scanOrchestration(row)
}

func (s *Store) GetOrchestrationByName(name string) (*Orchestration, error) {
	row := s.QueryRow(`
		SELECT id, name, description, spec_yaml, trigger_kind, trigger_filter, trigger_cron,
		       enabled, created_at, created_by, updated_at, last_fired_at, last_fired_by
		  FROM orchestrations WHERE name = ?
	`, name)
	return scanOrchestration(row)
}

func (s *Store) ListOrchestrations() ([]*Orchestration, error) {
	rows, err := s.Query(`
		SELECT id, name, description, spec_yaml, trigger_kind, trigger_filter, trigger_cron,
		       enabled, created_at, created_by, updated_at, last_fired_at, last_fired_by
		  FROM orchestrations ORDER BY name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Orchestration
	for rows.Next() {
		o, err := scanOrchestration(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

type orchestrationRowScanner interface {
	Scan(dest ...any) error
}

func scanOrchestration(s orchestrationRowScanner) (*Orchestration, error) {
	o := &Orchestration{}
	var enabled int64
	if err := s.Scan(
		&o.ID, &o.Name, &o.Description, &o.SpecYAML,
		&o.TriggerKind, &o.TriggerFilter, &o.TriggerCron,
		&enabled, &o.CreatedAt, &o.CreatedBy, &o.UpdatedAt,
		&o.LastFiredAt, &o.LastFiredBy,
	); err != nil {
		return nil, err
	}
	o.Enabled = enabled != 0
	return o, nil
}

// UpdateOrchestrationLastFired stamps the auto-trigger bookkeeping
// columns. `by` is the source of the trigger ("cron" | "finding:<id>"),
// used for audit + the operator-facing "last fired" caption.
func (s *Store) UpdateOrchestrationLastFired(id int64, by string) error {
	_, err := s.Exec(`
		UPDATE orchestrations
		   SET last_fired_at = CURRENT_TIMESTAMP, last_fired_by = ?
		 WHERE id = ?
	`, nullable(by), id)
	return err
}

// SetOrchestrationEnabled flips the enabled flag — used to pause an
// auto-trigger orchestration without deleting its history.
func (s *Store) SetOrchestrationEnabled(id int64, enabled bool) error {
	v := 0
	if enabled {
		v = 1
	}
	_, err := s.Exec(`UPDATE orchestrations SET enabled = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, v, id)
	return err
}

// ── orchestration runs ──────────────────────────────────────────────

type OrchestrationRun struct {
	ID              int64
	OrchestrationID int64
	Status          string
	TriggerKind     string
	TriggerPayload  sql.NullString
	CurrentStepID   sql.NullString
	StartedAt       time.Time
	EndedAt         sql.NullTime
	StartedBy       sql.NullInt64
	Error           sql.NullString
}

func (s *Store) CreateOrchestrationRun(orchID int64, triggerKind, triggerPayload string, startedBy int64) (int64, error) {
	res, err := s.Exec(`
		INSERT INTO orchestration_runs (orchestration_id, status, trigger_kind, trigger_payload, started_by)
		VALUES (?, 'pending', ?, ?, ?)
	`, orchID, triggerKind, nullable(triggerPayload), nullableInt64(startedBy))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) GetOrchestrationRun(id int64) (*OrchestrationRun, error) {
	row := s.QueryRow(`
		SELECT id, orchestration_id, status, trigger_kind, trigger_payload,
		       current_step_id, started_at, ended_at, started_by, error
		  FROM orchestration_runs WHERE id = ?
	`, id)
	return scanOrchestrationRun(row)
}

func (s *Store) ListOrchestrationRuns(limit, offset int) ([]*OrchestrationRun, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := s.Query(`
		SELECT id, orchestration_id, status, trigger_kind, trigger_payload,
		       current_step_id, started_at, ended_at, started_by, error
		  FROM orchestration_runs ORDER BY started_at DESC LIMIT ? OFFSET ?
	`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*OrchestrationRun
	for rows.Next() {
		r, err := scanOrchestrationRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func scanOrchestrationRun(s orchestrationRowScanner) (*OrchestrationRun, error) {
	r := &OrchestrationRun{}
	if err := s.Scan(
		&r.ID, &r.OrchestrationID, &r.Status, &r.TriggerKind, &r.TriggerPayload,
		&r.CurrentStepID, &r.StartedAt, &r.EndedAt, &r.StartedBy, &r.Error,
	); err != nil {
		return nil, err
	}
	return r, nil
}

func (s *Store) UpdateOrchestrationRunStatus(id int64, status, currentStepID, errMsg string) error {
	q := `UPDATE orchestration_runs SET status = ?`
	args := []any{status}
	if currentStepID != "" {
		q += `, current_step_id = ?`
		args = append(args, currentStepID)
	}
	if errMsg != "" {
		q += `, error = ?`
		args = append(args, errMsg)
	}
	q += ` WHERE id = ?`
	args = append(args, id)
	_, err := s.Exec(q, args...)
	return err
}

func (s *Store) FinishOrchestrationRun(id int64, status, errMsg string) error {
	if !isOrchestrationTerminal(status) {
		return errors.New("status must be a terminal value (completed|failed|cancelled)")
	}
	_, err := s.Exec(`
		UPDATE orchestration_runs
		   SET status = ?, error = ?, ended_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND ended_at IS NULL
	`, status, nullable(errMsg), id)
	return err
}

func isOrchestrationTerminal(s string) bool {
	switch s {
	case "completed", "failed", "cancelled":
		return true
	}
	return false
}

// ── orchestration steps ─────────────────────────────────────────────

type OrchestrationStep struct {
	ID                 int64
	OrchestrationRunID int64
	StepID             string
	StepIdx            int
	Status             string
	RunID              sql.NullString
	CPInstanceID       sql.NullString
	NodeID             sql.NullInt64
	RenderedPrompt     sql.NullString
	ResultJSON         sql.NullString
	OutputSummary      sql.NullString
	StartedAt          sql.NullTime
	EndedAt            sql.NullTime
	Error              sql.NullString
	ApprovedAt         sql.NullTime
	ApprovedBy         sql.NullInt64
}

// OrchestrationStepInsert is the input shape for UpsertOrchestrationStep.
// Pointer-time fields let the caller distinguish "not started yet"
// (nil) from "started at unix epoch" (which would be the zero time).
type OrchestrationStepInsert struct {
	OrchestrationRunID int64
	StepID             string
	StepIdx            int
	Status             string
	RunID              string
	CPInstanceID       string
	NodeID             int64
	RenderedPrompt     string
	ResultJSON         string
	OutputSummary      string
	StartedAt          *time.Time
	EndedAt            *time.Time
	Error              string
	ApprovedAt         *time.Time
	ApprovedBy         int64
}

// UpsertOrchestrationStep inserts or updates a step row keyed by
// (run_id, step_id). The engine calls this on every state transition,
// so the upsert pattern keeps the step record current without forcing
// the caller to know whether it's a first write or an update.
func (s *Store) UpsertOrchestrationStep(in OrchestrationStepInsert) error {
	_, err := s.Exec(`
		INSERT INTO orchestration_steps (
			orchestration_run_id, step_id, step_idx, status, run_id, cp_instance_id, node_id,
			rendered_prompt, result_json, output_summary, started_at, ended_at, error,
			approved_at, approved_by
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (orchestration_run_id, step_id) DO UPDATE SET
			step_idx        = excluded.step_idx,
			status          = excluded.status,
			run_id          = excluded.run_id,
			cp_instance_id  = excluded.cp_instance_id,
			node_id         = excluded.node_id,
			rendered_prompt = excluded.rendered_prompt,
			result_json     = excluded.result_json,
			output_summary  = excluded.output_summary,
			started_at      = excluded.started_at,
			ended_at        = excluded.ended_at,
			error           = excluded.error,
			approved_at     = excluded.approved_at,
			approved_by     = excluded.approved_by
	`,
		in.OrchestrationRunID, in.StepID, in.StepIdx, in.Status,
		nullable(in.RunID), nullable(in.CPInstanceID), nullableInt64(in.NodeID),
		nullable(in.RenderedPrompt), nullable(in.ResultJSON), nullable(in.OutputSummary),
		nullableTimePtr(in.StartedAt), nullableTimePtr(in.EndedAt), nullable(in.Error),
		nullableTimePtr(in.ApprovedAt), nullableInt64(in.ApprovedBy),
	)
	return err
}

func (s *Store) ListOrchestrationSteps(runID int64) ([]*OrchestrationStep, error) {
	rows, err := s.Query(`
		SELECT id, orchestration_run_id, step_id, step_idx, status, run_id, cp_instance_id, node_id,
		       rendered_prompt, result_json, output_summary, started_at, ended_at, error,
		       approved_at, approved_by
		  FROM orchestration_steps
		 WHERE orchestration_run_id = ?
		 ORDER BY step_idx
	`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*OrchestrationStep
	for rows.Next() {
		st, err := scanOrchestrationStep(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func (s *Store) GetOrchestrationStep(runID int64, stepID string) (*OrchestrationStep, error) {
	row := s.QueryRow(`
		SELECT id, orchestration_run_id, step_id, step_idx, status, run_id, cp_instance_id, node_id,
		       rendered_prompt, result_json, output_summary, started_at, ended_at, error,
		       approved_at, approved_by
		  FROM orchestration_steps
		 WHERE orchestration_run_id = ? AND step_id = ?
	`, runID, stepID)
	return scanOrchestrationStep(row)
}

func scanOrchestrationStep(s orchestrationRowScanner) (*OrchestrationStep, error) {
	st := &OrchestrationStep{}
	if err := s.Scan(
		&st.ID, &st.OrchestrationRunID, &st.StepID, &st.StepIdx, &st.Status, &st.RunID,
		&st.CPInstanceID, &st.NodeID, &st.RenderedPrompt, &st.ResultJSON, &st.OutputSummary,
		&st.StartedAt, &st.EndedAt, &st.Error, &st.ApprovedAt, &st.ApprovedBy,
	); err != nil {
		return nil, err
	}
	return st, nil
}

// nullableTimePtr converts a *time.Time into sql.NullTime. Mirrors
// the existing nullable / nullableInt64 helpers in this package.
func nullableTimePtr(t *time.Time) sql.NullTime {
	if t == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *t, Valid: true}
}
