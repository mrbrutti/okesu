// Per-host fan-out dispatch state for orchestration steps. The engine
// writes through this table so the run-detail canvas can surface live
// per-host progress without polling orchestration_steps.result_json.
//
// Aggregated byNode results continue to land in
// orchestration_steps.result_json at step completion for template
// back-compat ({{stepN.byNode["host"]}}). This file adds the fine-grained
// live tracking layer only.

package db

import (
	"database/sql"
	"time"
)

// StepNodeDispatch is a per-host slice of an orchestration step's
// fan-out execution. One row per (run_id, step_id, host).
//
// The engine writes through this table to surface live per-host
// progress on the run-detail canvas. The aggregated byNode map
// continues to land in orchestration_steps.result_json at step
// completion for template back-compat.
type StepNodeDispatch struct {
	RunID         int64
	StepID        string
	Host          string
	Status        string
	AgentRunID    sql.NullString
	FindingsCount int
	OutputTail    sql.NullString
	Error         sql.NullString
	StartedAt     sql.NullTime
	EndedAt       sql.NullTime
}

// StepNodeDispatchInsert is the input shape for InsertStepNodeDispatch.
// Engine calls this on dispatch-start with status='running'.
type StepNodeDispatchInsert struct {
	RunID     int64
	StepID    string
	Host      string
	Status    string
	StartedAt *time.Time
}

// StepNodeDispatchUpdate is the input for UpdateStepNodeDispatch.
// Engine calls this once per host on dispatch-return with the final
// status and (when present) findings count, output tail, agent run id,
// and error.
type StepNodeDispatchUpdate struct {
	RunID         int64
	StepID        string
	Host          string
	Status        string
	AgentRunID    string
	FindingsCount int
	OutputTail    string
	Error         string
	EndedAt       *time.Time
}

// InsertStepNodeDispatch records that the engine is about to dispatch
// `step_id` on `host`. Idempotent: if the row already exists (engine
// retry, fan-out replay), the row's status/started_at are reset to the
// caller's values.
func (s *Store) InsertStepNodeDispatch(in StepNodeDispatchInsert) error {
	_, err := s.Exec(`
		INSERT INTO orchestration_step_node_dispatches
		    (run_id, step_id, host, status, started_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (run_id, step_id, host) DO UPDATE SET
		    status     = excluded.status,
		    started_at = excluded.started_at,
		    ended_at   = NULL,
		    error      = NULL
	`, in.RunID, in.StepID, in.Host, in.Status, nullableTimePtr(in.StartedAt))
	return err
}

// UpdateStepNodeDispatch records the dispatch outcome for one host.
// Called when the dispatcher returns (success or failure) — never
// concurrently with another writer for the same (run, step, host).
func (s *Store) UpdateStepNodeDispatch(in StepNodeDispatchUpdate) error {
	_, err := s.Exec(`
		UPDATE orchestration_step_node_dispatches
		   SET status         = ?,
		       agent_run_id   = ?,
		       findings_count = ?,
		       output_tail    = ?,
		       error          = ?,
		       ended_at       = ?
		 WHERE run_id = ? AND step_id = ? AND host = ?
	`,
		in.Status,
		nullable(in.AgentRunID),
		in.FindingsCount,
		nullable(in.OutputTail),
		nullable(in.Error),
		nullableTimePtr(in.EndedAt),
		in.RunID, in.StepID, in.Host,
	)
	return err
}

// ListStepNodeDispatchesByRun returns every per-host row for a run,
// ordered by step_id then host (stable, so the canvas can render
// rows in a deterministic order without sorting client-side).
func (s *Store) ListStepNodeDispatchesByRun(runID int64) ([]*StepNodeDispatch, error) {
	rows, err := s.Query(`
		SELECT run_id, step_id, host, status, agent_run_id, findings_count,
		       output_tail, error, started_at, ended_at
		  FROM orchestration_step_node_dispatches
		 WHERE run_id = ?
		 ORDER BY step_id, host
	`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*StepNodeDispatch
	for rows.Next() {
		r := &StepNodeDispatch{}
		if err := rows.Scan(
			&r.RunID, &r.StepID, &r.Host, &r.Status, &r.AgentRunID,
			&r.FindingsCount, &r.OutputTail, &r.Error,
			&r.StartedAt, &r.EndedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ReconcileStepNodeDispatchesForRun marks any row still in `running`
// as `failed` with the given reason and now() as ended_at. Called when
// a run transitions to a terminal state (cancelled / failed) so we
// don't leak orphan `running` rows after an engine restart or operator
// cancellation.
//
// Safe to call on runs with no fan-out rows — UPDATE is a no-op then.
func (s *Store) ReconcileStepNodeDispatchesForRun(runID int64, reason string) error {
	now := time.Now().UTC()
	_, err := s.Exec(`
		UPDATE orchestration_step_node_dispatches
		   SET status   = 'failed',
		       error    = ?,
		       ended_at = ?
		 WHERE run_id = ? AND status = 'running'
	`, reason, now, runID)
	return err
}
