// Investigations — T2 case workspace. Operators open one when a
// finding (or set of findings) needs sustained attention; link more
// findings + orchestration runs as the case develops; capture analyst
// notes; and close the case with a resolution.
//
// See migration 036 for the schema; design rationale lives in
// docs/superpowers/specs/2026-04-29-threatcaddy-borrows-phasing-design.md
// (Phase 22.3 §"Hypothesis-driven T2").

package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Investigation is a T2 case — operators open one, link findings/runs
// to it, write notes, and close it with a resolution. Phase 22.3.
//
// Timestamp shape uses time.Time (matching Finding and IOCRecord
// precedent in this package). The store layer parses the SQL string
// representations via ParseTimestamp (controlplane/db/timestamps.go);
// callers serialize via the standard time.Time JSON encoding.
type Investigation struct {
	ID         int64
	Title      string
	Status     string
	Resolution string
	Summary    string
	CreatedBy  string
	CreatedAt  time.Time
	ClosedAt   time.Time // zero when not closed
	UpdatedAt  time.Time
}

// InvestigationInsert is the create-time payload. Title is required;
// status defaults to "active" at the SQL layer.
type InvestigationInsert struct {
	Title     string
	CreatedBy string
	Summary   string
}

// InvestigationUpdate carries optional patch fields. Empty string =
// "don't touch this column". Setting Status="closed" auto-stamps
// closed_at; setting Resolution requires Status to be (or transition to)
// "closed" — guarded in UpdateInvestigation.
type InvestigationUpdate struct {
	Title      string
	Status     string
	Resolution string
	Summary    string
}

// InvestigationNote is one analyst comment on a case.
type InvestigationNote struct {
	ID              int64
	InvestigationID int64
	Author          string
	Body            string
	CreatedAt       time.Time
}

// validStatuses / validResolutions gate the small enum vocabulary the
// SQL column allows. Centralised here so the validation lives next to
// the data shape.
var (
	validStatuses    = map[string]bool{"active": true, "closed": true, "archived": true}
	validResolutions = map[string]bool{"resolved": true, "false_positive": true, "duplicate": true, "wont_fix": true}
)

// CreateInvestigation inserts a new case at status='active'. Title is
// required; Summary + CreatedBy are optional (NULL when empty).
func (s *Store) CreateInvestigation(in *InvestigationInsert) (int64, error) {
	if in.Title == "" {
		return 0, fmt.Errorf("CreateInvestigation: title is required")
	}
	res, err := s.Exec(`
		INSERT INTO investigations (title, status, summary, created_by)
		VALUES (?, 'active', ?, ?)`,
		in.Title, nullable(in.Summary), nullable(in.CreatedBy))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetInvestigation returns one case by id, or sql.ErrNoRows.
func (s *Store) GetInvestigation(id int64) (*Investigation, error) {
	row := s.QueryRow(`
		SELECT id, title, status, COALESCE(resolution, ''), COALESCE(summary, ''),
		       COALESCE(created_by, ''), created_at,
		       COALESCE(closed_at, ''), updated_at
		FROM investigations WHERE id = ?`, id)
	var inv Investigation
	var createdAt, closedAt, updatedAt sql.NullString
	if err := row.Scan(&inv.ID, &inv.Title, &inv.Status, &inv.Resolution, &inv.Summary,
		&inv.CreatedBy, &createdAt, &closedAt, &updatedAt); err != nil {
		return nil, err
	}
	inv.CreatedAt = ParseTimestamp(createdAt.String)
	inv.ClosedAt = ParseTimestamp(closedAt.String)
	inv.UpdatedAt = ParseTimestamp(updatedAt.String)
	return &inv, nil
}

// UpdateInvestigation applies a partial patch. Validates the small
// status / resolution enums and rejects "set resolution while status
// remains active" combinations — resolution is meaningless on an open
// case, and silently coercing it would mask operator intent.
func (s *Store) UpdateInvestigation(id int64, up *InvestigationUpdate) error {
	if up.Status != "" && !validStatuses[up.Status] {
		return fmt.Errorf("UpdateInvestigation: invalid status %q", up.Status)
	}
	if up.Resolution != "" && !validResolutions[up.Resolution] {
		return fmt.Errorf("UpdateInvestigation: invalid resolution %q", up.Resolution)
	}
	if up.Resolution != "" && up.Status != "closed" {
		// Allow setting resolution if the case is ALREADY closed (e.g.
		// operator changing their mind about the resolution kind).
		cur, err := s.GetInvestigation(id)
		if err != nil {
			return err
		}
		if cur.Status != "closed" && up.Status != "closed" {
			return fmt.Errorf("UpdateInvestigation: resolution requires status=closed")
		}
	}

	sets := []string{"updated_at = CURRENT_TIMESTAMP"}
	args := []any{}
	if up.Title != "" {
		sets = append(sets, "title = ?")
		args = append(args, up.Title)
	}
	if up.Summary != "" {
		sets = append(sets, "summary = ?")
		args = append(args, up.Summary)
	}
	if up.Status != "" {
		sets = append(sets, "status = ?")
		args = append(args, up.Status)
		if up.Status == "closed" {
			sets = append(sets, "closed_at = CURRENT_TIMESTAMP")
		}
	}
	if up.Resolution != "" {
		sets = append(sets, "resolution = ?")
		args = append(args, up.Resolution)
	}
	args = append(args, id)
	q := "UPDATE investigations SET " + strings.Join(sets, ", ") + " WHERE id = ?"
	_, err := s.Exec(q, args...)
	return err
}

// ListInvestigations returns recent cases, optionally filtered by
// status. Limit is clamped to [1, 1000] (default 100) to prevent
// runaway scans on a long-lived deployment.
func (s *Store) ListInvestigations(status string, limit int) ([]*Investigation, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	q := `SELECT id, title, status, COALESCE(resolution, ''), COALESCE(summary, ''),
	             COALESCE(created_by, ''), created_at,
	             COALESCE(closed_at, ''), updated_at
	      FROM investigations`
	args := []any{}
	if status != "" {
		q += " WHERE status = ?"
		args = append(args, status)
	}
	q += " ORDER BY updated_at DESC LIMIT ?"
	args = append(args, limit)
	rows, err := s.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Investigation
	for rows.Next() {
		var inv Investigation
		var createdAt, closedAt, updatedAt sql.NullString
		if err := rows.Scan(&inv.ID, &inv.Title, &inv.Status, &inv.Resolution, &inv.Summary,
			&inv.CreatedBy, &createdAt, &closedAt, &updatedAt); err != nil {
			return nil, err
		}
		inv.CreatedAt = ParseTimestamp(createdAt.String)
		inv.ClosedAt = ParseTimestamp(closedAt.String)
		inv.UpdatedAt = ParseTimestamp(updatedAt.String)
		out = append(out, &inv)
	}
	return out, rows.Err()
}

// LinkFindingToInvestigation associates a finding with a case.
// Idempotent: re-linking the same pair is a no-op (ON CONFLICT DO
// NOTHING) so callers don't need to dedupe.
func (s *Store) LinkFindingToInvestigation(investigationID, findingID int64) error {
	_, err := s.Exec(`
		INSERT INTO investigation_findings (investigation_id, finding_id)
		VALUES (?, ?)
		ON CONFLICT (investigation_id, finding_id) DO NOTHING`,
		investigationID, findingID)
	return err
}

// LinkRunToInvestigation associates an orchestration run with a case.
// Same idempotency contract as LinkFindingToInvestigation.
func (s *Store) LinkRunToInvestigation(investigationID, runID int64) error {
	_, err := s.Exec(`
		INSERT INTO investigation_runs (investigation_id, orchestration_run_id)
		VALUES (?, ?)
		ON CONFLICT (investigation_id, orchestration_run_id) DO NOTHING`,
		investigationID, runID)
	return err
}

// ListFindingsForInvestigation returns the linked finding ids,
// newest-link-first.
func (s *Store) ListFindingsForInvestigation(investigationID int64) ([]int64, error) {
	rows, err := s.Query(`
		SELECT finding_id FROM investigation_findings
		WHERE investigation_id = ?
		ORDER BY linked_at DESC`, investigationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ListRunsForInvestigation returns the linked orchestration run ids,
// newest-link-first.
func (s *Store) ListRunsForInvestigation(investigationID int64) ([]int64, error) {
	rows, err := s.Query(`
		SELECT orchestration_run_id FROM investigation_runs
		WHERE investigation_id = ?
		ORDER BY linked_at DESC`, investigationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// AddInvestigationNote appends a markdown analyst note to a case and
// bumps the case's updated_at so the case rises in the recency-sorted
// list.
func (s *Store) AddInvestigationNote(investigationID int64, author, body string) (int64, error) {
	if author == "" {
		return 0, errors.New("AddInvestigationNote: author is required")
	}
	if body == "" {
		return 0, errors.New("AddInvestigationNote: body is required")
	}
	res, err := s.Exec(`
		INSERT INTO investigation_notes (investigation_id, author, body)
		VALUES (?, ?, ?)`,
		investigationID, author, body)
	if err != nil {
		return 0, err
	}
	// Best-effort updated_at bump. If this fails the note still landed,
	// so we don't surface the error — the recency ordering is a
	// nice-to-have, not load-bearing.
	_, _ = s.Exec(`UPDATE investigations SET updated_at = CURRENT_TIMESTAMP WHERE id = ?`, investigationID)
	return res.LastInsertId()
}

// ListInvestigationNotes returns notes newest-first. Tie-breaker on id
// matches the agent_lessons convention so SQLite's second-resolution
// timestamps don't yield insertion-order luck.
func (s *Store) ListInvestigationNotes(investigationID int64) ([]InvestigationNote, error) {
	rows, err := s.Query(`
		SELECT id, investigation_id, author, body, created_at
		FROM investigation_notes
		WHERE investigation_id = ?
		ORDER BY created_at DESC, id DESC`, investigationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []InvestigationNote
	for rows.Next() {
		var n InvestigationNote
		var createdAt sql.NullString
		if err := rows.Scan(&n.ID, &n.InvestigationID, &n.Author, &n.Body, &createdAt); err != nil {
			return nil, err
		}
		n.CreatedAt = ParseTimestamp(createdAt.String)
		out = append(out, n)
	}
	return out, rows.Err()
}
