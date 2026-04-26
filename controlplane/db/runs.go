package db

import (
	"database/sql"
	"errors"
	"time"
)

// Run statuses persisted in `runs.status`.
const (
	RunStatusRunning   = "running"
	RunStatusSucceeded = "succeeded"
	RunStatusFailed    = "failed"
	RunStatusCancelled = "cancelled"
)

// Run is the projected shape of a row in `runs`.
type Run struct {
	ID              string
	NodeName        string
	Provider        sql.NullString
	Model           sql.NullString
	Effort          sql.NullString
	AgentName       sql.NullString
	Prompt          string
	Status          string
	ExitCode        int
	Error           sql.NullString
	StartedAt       time.Time
	FinishedAt      sql.NullTime
	StartedByUserID sql.NullInt64
	StartedByEmail  sql.NullString
}

// RunInsert is the input shape for CreateRun.
type RunInsert struct {
	ID              string
	NodeName        string
	Provider        string
	Model           string
	Effort          string
	AgentName       string
	Prompt          string
	StartedByUserID int64
	StartedByEmail  string
}

// RunLine is one captured stdout/stderr line.
type RunLine struct {
	ID     int64
	RunID  string
	Ts     time.Time
	Stream string
	Data   string
}

// CreateRun inserts a new run row in 'running' state. The caller already
// generated the ID so the same value is broadcast over the tunnel.
func (s *Store) CreateRun(in RunInsert) error {
	_, err := s.Exec(`
		INSERT INTO runs (id, node_name, provider, model, effort, agent_name, prompt,
		                  status, started_by_user_id, started_by_email)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'running', ?, ?)
	`,
		in.ID, in.NodeName,
		nullable(in.Provider), nullable(in.Model), nullable(in.Effort),
		nullable(in.AgentName), in.Prompt,
		nullableInt64(in.StartedByUserID), nullable(in.StartedByEmail),
	)
	return err
}

// AppendRunLine writes one stdout line for a run.
func (s *Store) AppendRunLine(runID, stream, data string) error {
	if stream == "" {
		stream = "stdout"
	}
	_, err := s.Exec(`
		INSERT INTO run_lines (run_id, stream, data) VALUES (?, ?, ?)
	`, runID, stream, data)
	return err
}

// FinishRun marks a run terminal. status must be one of succeeded|failed|cancelled.
// errMsg is optional. Idempotent: a no-op if the run is already terminal.
func (s *Store) FinishRun(runID, status string, exitCode int, errMsg string) error {
	if status != RunStatusSucceeded && status != RunStatusFailed && status != RunStatusCancelled {
		return errors.New("invalid run status: " + status)
	}
	_, err := s.Exec(`
		UPDATE runs
		   SET status = ?, exit_code = ?, error = ?, finished_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND status = 'running'
	`, status, exitCode, nullable(errMsg), runID)
	return err
}

// MarkInflightCancelled is called on CP boot to reconcile any rows left in
// 'running' state from a previous process. Returns the number of rows updated.
func (s *Store) MarkInflightCancelled() (int64, error) {
	res, err := s.Exec(`
		UPDATE runs
		   SET status = 'cancelled',
		       finished_at = CURRENT_TIMESTAMP,
		       error = COALESCE(error, 'control plane restarted while run was in flight')
		 WHERE status = 'running'
	`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// GetRun returns one run or sql.ErrNoRows if missing.
func (s *Store) GetRun(id string) (*Run, error) {
	row := s.QueryRow(`
		SELECT id, node_name, provider, model, effort, agent_name, prompt,
		       status, exit_code, error, started_at, finished_at,
		       started_by_user_id, started_by_email
		  FROM runs WHERE id = ?
	`, id)
	r := &Run{}
	err := row.Scan(
		&r.ID, &r.NodeName, &r.Provider, &r.Model, &r.Effort, &r.AgentName, &r.Prompt,
		&r.Status, &r.ExitCode, &r.Error, &r.StartedAt, &r.FinishedAt,
		&r.StartedByUserID, &r.StartedByEmail,
	)
	if err != nil {
		return nil, err
	}
	return r, nil
}

// ListRuns returns the most recent runs, newest first. Offset is applied
// against the same ordering so the UI can paginate by `?offset=` for
// infinite scroll.
func (s *Store) ListRuns(limit, offset int) ([]*Run, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.Query(`
		SELECT id, node_name, provider, model, effort, agent_name, prompt,
		       status, exit_code, error, started_at, finished_at,
		       started_by_user_id, started_by_email
		  FROM runs
		 ORDER BY started_at DESC, id DESC
		 LIMIT ? OFFSET ?
	`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Run
	for rows.Next() {
		r := &Run{}
		if err := rows.Scan(
			&r.ID, &r.NodeName, &r.Provider, &r.Model, &r.Effort, &r.AgentName, &r.Prompt,
			&r.Status, &r.ExitCode, &r.Error, &r.StartedAt, &r.FinishedAt,
			&r.StartedByUserID, &r.StartedByEmail,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RunLines returns the captured stdout/stderr for a run, oldest first.
// Capped at maxLines (use 0 for the default cap of 5000).
func (s *Store) RunLines(runID string, maxLines int) ([]RunLine, error) {
	if maxLines <= 0 || maxLines > 50000 {
		maxLines = 5000
	}
	rows, err := s.Query(`
		SELECT id, run_id, ts, stream, data
		  FROM run_lines
		 WHERE run_id = ?
		 ORDER BY id ASC
		 LIMIT ?
	`, runID, maxLines)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RunLine
	for rows.Next() {
		l := RunLine{}
		if err := rows.Scan(&l.ID, &l.RunID, &l.Ts, &l.Stream, &l.Data); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// nullableInt64 returns sql.NullInt64{Valid:false} when v == 0.
func nullableInt64(v int64) sql.NullInt64 {
	if v == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: v, Valid: true}
}
