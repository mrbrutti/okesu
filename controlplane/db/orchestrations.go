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
	return s.ListOrchestrationRunsFiltered(OrchestrationRunFilter{Limit: limit, Offset: offset})
}

// OrchestrationRunFilter narrows the rows returned by
// ListOrchestrationRunsFiltered. Empty values mean "don't filter on
// this dimension"; the runs page passes a populated struct from URL
// params so each filter chip / status pill maps to one field.
type OrchestrationRunFilter struct {
	// Status is OR'd: any row matching any value passes. Empty = all.
	Status []string

	// OrchestrationIDs is OR'd. Empty = all.
	OrchestrationIDs []int64

	// TriggerKinds is OR'd: manual | finding | cron. Empty = all.
	TriggerKinds []string

	// SinceMs filters started_at >= this Unix-ms timestamp. 0 = no filter.
	// The UI's range picker (30m / 1h / 24h / 7d) maps to this.
	SinceMs int64

	// Search matches the run id (string), trigger payload host, or
	// trigger payload finding_id (numeric). Cheap LIKE on the JSON
	// payload — small table, no need for a fancy index.
	Search string

	// Pagination. Limit caps at 1000; default 100 when ≤0.
	Limit  int
	Offset int
}

// ListOrchestrationRunsFiltered runs the same query as the
// pagination-only variant but with optional filters applied. Kept as
// a sibling method so the existing call sites (federation aggregator,
// dashboard) can continue using the simple form.
func (s *Store) ListOrchestrationRunsFiltered(f OrchestrationRunFilter) ([]*OrchestrationRun, error) {
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 100
	}
	if f.Offset < 0 {
		f.Offset = 0
	}

	q := `SELECT id, orchestration_id, status, trigger_kind, trigger_payload,
	             current_step_id, started_at, ended_at, started_by, error
	        FROM orchestration_runs`
	where, args := buildRunsWhere(f)
	if where != "" {
		q += " WHERE " + where
	}
	q += ` ORDER BY started_at DESC LIMIT ? OFFSET ?`
	args = append(args, f.Limit, f.Offset)

	rows, err := s.Query(q, args...)
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

// TopOrchestrationStat is one row of the dashboard's "Top
// orchestrations" leaderboard. Built from the orchestration_runs +
// orchestrations tables, joined and aggregated by orchestration_id.
type TopOrchestrationStat struct {
	OrchestrationID int64
	Name            string
	Total           int64
	Completed       int64
	Failed          int64
	Running         int64
	Cancelled       int64
	Pending         int64
	ApprovalReq     int64
	AvgDurationMs   sql.NullInt64 // null when no completed rows in window
}

// TopOrchestrations returns the orchestrations with the most runs in
// the given window, sorted by total descending. Used by the dashboard's
// "Top orchestrations" card so an operator can see which spec is
// generating the most load + which is failing most without paging
// through the runs list.
//
// avg_duration_ms is computed only over rows where status='completed'
// (running rows would skew the average; failed rows often abort
// fast). Empty when no completed rows are in window.
func (s *Store) TopOrchestrations(sinceMs int64, limit int) ([]TopOrchestrationStat, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	const q = `
SELECT
  r.orchestration_id,
  o.name,
  COUNT(*) AS total,
  SUM(CASE WHEN r.status='completed'         THEN 1 ELSE 0 END) AS completed,
  SUM(CASE WHEN r.status='failed'            THEN 1 ELSE 0 END) AS failed,
  SUM(CASE WHEN r.status='running'           THEN 1 ELSE 0 END) AS running,
  SUM(CASE WHEN r.status='cancelled'         THEN 1 ELSE 0 END) AS cancelled,
  SUM(CASE WHEN r.status='pending'           THEN 1 ELSE 0 END) AS pending,
  SUM(CASE WHEN r.status='approval_required' THEN 1 ELSE 0 END) AS approval_required,
  CAST(AVG(CASE
    WHEN r.status='completed' AND r.ended_at IS NOT NULL
    THEN (CAST(strftime('%s', r.ended_at) AS INTEGER) - CAST(strftime('%s', r.started_at) AS INTEGER)) * 1000
  END) AS INTEGER) AS avg_duration_ms
FROM orchestration_runs r
JOIN orchestrations o ON o.id = r.orchestration_id
WHERE CAST(strftime('%s', r.started_at) AS INTEGER) * 1000 >= ?
GROUP BY r.orchestration_id, o.name
ORDER BY total DESC, completed DESC
LIMIT ?
`
	rows, err := s.Query(q, sinceMs, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TopOrchestrationStat
	for rows.Next() {
		var t TopOrchestrationStat
		if err := rows.Scan(
			&t.OrchestrationID, &t.Name, &t.Total,
			&t.Completed, &t.Failed, &t.Running, &t.Cancelled,
			&t.Pending, &t.ApprovalReq, &t.AvgDurationMs,
		); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// CountOrchestrationRunsByStatus returns one count per status that
// would match `f` if status were ignored. Used by the runs-tab
// status pills so each tab shows its own total without a roundtrip
// per pill.
func (s *Store) CountOrchestrationRunsByStatus(f OrchestrationRunFilter) (map[string]int, error) {
	g := f
	g.Status = nil // we project this dimension out
	q := `SELECT status, COUNT(*) FROM orchestration_runs`
	where, args := buildRunsWhere(g)
	if where != "" {
		q += " WHERE " + where
	}
	q += ` GROUP BY status`
	rows, err := s.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return nil, err
		}
		out[status] = n
	}
	return out, rows.Err()
}

// buildRunsWhere is shared between the list + count queries so a
// status pill's count always reflects the same filter set as the
// list it pages through.
func buildRunsWhere(f OrchestrationRunFilter) (string, []any) {
	var clauses []string
	var args []any

	if len(f.Status) > 0 {
		ph := placeholders(len(f.Status))
		clauses = append(clauses, "status IN ("+ph+")")
		for _, s := range f.Status {
			args = append(args, s)
		}
	}
	if len(f.OrchestrationIDs) > 0 {
		ph := placeholders(len(f.OrchestrationIDs))
		clauses = append(clauses, "orchestration_id IN ("+ph+")")
		for _, id := range f.OrchestrationIDs {
			args = append(args, id)
		}
	}
	if len(f.TriggerKinds) > 0 {
		ph := placeholders(len(f.TriggerKinds))
		clauses = append(clauses, "trigger_kind IN ("+ph+")")
		for _, k := range f.TriggerKinds {
			args = append(args, k)
		}
	}
	if f.SinceMs > 0 {
		// started_at is stored as ISO timestamp. Compare against the
		// formatted value rather than parsing per-row — rough but fast
		// for our scale, and the UI's resolution doesn't need ms.
		clauses = append(clauses, "started_at >= datetime(?/1000.0, 'unixepoch')")
		args = append(args, f.SinceMs)
	}
	if f.Search != "" {
		// LIKE across id, current_step_id, and the trigger_payload
		// JSON blob — covers run_id search, step name, host, and
		// finding_id all in one expression.
		clauses = append(clauses, "(CAST(id AS TEXT) LIKE ? OR current_step_id LIKE ? OR trigger_payload LIKE ?)")
		needle := "%" + f.Search + "%"
		args = append(args, needle, needle, needle)
	}
	if len(clauses) == 0 {
		return "", nil
	}
	out := clauses[0]
	for _, c := range clauses[1:] {
		out += " AND " + c
	}
	return out, args
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	out := "?"
	for i := 1; i < n; i++ {
		out += ",?"
	}
	return out
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
	if err != nil {
		return err
	}
	// Reconcile any orphan fan-out rows: if a step was mid-dispatch
	// when this run hit terminal, the per-host row would otherwise
	// stay `running` forever. The reason mirrors the run's terminal
	// reason (e.g. "operator cancelled", or the failure message that
	// halted the engine) so the host drawer shows a coherent error.
	reason := errMsg
	if reason == "" {
		reason = "run " + status
	}
	return s.ReconcileStepNodeDispatchesForRun(id, reason)
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
	// DataSnapshot is the JSON-encoded resolved `data:` block — see
	// migration 028. NULL/empty for steps that didn't declare data:.
	DataSnapshot sql.NullString
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
	// DataSnapshot is the JSON-encoded resolved `data:` block — see
	// migration 028. Empty for steps without a data block.
	DataSnapshot string
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
			approved_at, approved_by, data_snapshot
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
			approved_by     = excluded.approved_by,
			data_snapshot   = excluded.data_snapshot
	`,
		in.OrchestrationRunID, in.StepID, in.StepIdx, in.Status,
		nullable(in.RunID), nullable(in.CPInstanceID), nullableInt64(in.NodeID),
		nullable(in.RenderedPrompt), nullable(in.ResultJSON), nullable(in.OutputSummary),
		nullableTimePtr(in.StartedAt), nullableTimePtr(in.EndedAt), nullable(in.Error),
		nullableTimePtr(in.ApprovedAt), nullableInt64(in.ApprovedBy), nullable(in.DataSnapshot),
	)
	return err
}

func (s *Store) ListOrchestrationSteps(runID int64) ([]*OrchestrationStep, error) {
	rows, err := s.Query(`
		SELECT id, orchestration_run_id, step_id, step_idx, status, run_id, cp_instance_id, node_id,
		       rendered_prompt, result_json, output_summary, started_at, ended_at, error,
		       approved_at, approved_by, data_snapshot
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
		       approved_at, approved_by, data_snapshot
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
		&st.DataSnapshot,
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
