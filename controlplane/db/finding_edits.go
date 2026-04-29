// Finding mutation methods + audit log used by the orchestrator's
// action dispatcher and the operator-triage HTTP handlers.
//
// Every mutation through these methods writes a `finding_edits` row,
// so the finding-detail UI's history view always reflects what
// actually happened. Read-only consumers (dashboard counts, etc.)
// stay on the existing finding accessors.

package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// FindingEdit is one row of the audit trail.
type FindingEdit struct {
	ID                 int64
	FindingID          int64
	Field              string  // status | severity_override | tag_add | tag_remove | linked_run
	OldValue           sql.NullString
	NewValue           sql.NullString
	Reason             sql.NullString
	EditedByUserID     sql.NullInt64
	EditedByEmail      sql.NullString
	OrchestrationRunID sql.NullInt64
	OrchestrationStep  sql.NullString
	EditedAt           time.Time
}

// EditOrigin tells the mutation methods who's doing the edit. Exactly
// one of {UserID, OrchestrationRunID} should be set per call.
type EditOrigin struct {
	UserID             int64
	UserEmail          string
	OrchestrationRunID int64
	OrchestrationStep  string
	Reason             string
}

// ListFindingEdits returns the audit trail for a finding, newest first.
func (s *Store) ListFindingEdits(findingID int64) ([]FindingEdit, error) {
	rows, err := s.Query(`
		SELECT id, finding_id, field, old_value, new_value, reason,
		       edited_by_user_id, edited_by_email,
		       orchestration_run_id, orchestration_step_id,
		       edited_at
		  FROM finding_edits
		 WHERE finding_id = ?
		 ORDER BY edited_at DESC, id DESC
	`, findingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FindingEdit
	for rows.Next() {
		var e FindingEdit
		if err := rows.Scan(
			&e.ID, &e.FindingID, &e.Field, &e.OldValue, &e.NewValue, &e.Reason,
			&e.EditedByUserID, &e.EditedByEmail,
			&e.OrchestrationRunID, &e.OrchestrationStep,
			&e.EditedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ApplyFindingStatusChange updates findings.status and writes an
// audit row. Refuses an unknown status. Refuses to act on a missing
// finding. No-ops cleanly if the value already matches.
func (s *Store) ApplyFindingStatusChange(findingID int64, newStatus, reason string, origin EditOrigin) error {
	if !validFindingStatus(newStatus) {
		return fmt.Errorf("invalid status %q (allowed: %s)", newStatus, strings.Join(allowedStatuses, ","))
	}
	tx, err := s.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var oldStatus sql.NullString
	if err := tx.QueryRow(`SELECT status FROM findings WHERE id = ?`, findingID).Scan(&oldStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("finding %d not found", findingID)
		}
		return err
	}
	if oldStatus.Valid && oldStatus.String == newStatus {
		return tx.Commit()
	}
	if _, err := tx.Exec(`
		UPDATE findings
		   SET status = ?, triaged_at = CURRENT_TIMESTAMP,
		       triaged_by_user_id = ?, triaged_by_email = ?, triage_note = ?
		 WHERE id = ?`,
		newStatus,
		nullableInt64(origin.UserID),
		sqlNullableString(origin.UserEmail),
		sqlNullableString(coalesceReason(origin.Reason, reason)),
		findingID,
	); err != nil {
		return err
	}
	if err := writeFindingEdit(tx, findingID, "status", oldStatus.String, newStatus, reason, origin); err != nil {
		return err
	}
	return tx.Commit()
}

// ApplyFindingSeverityOverride sets findings.operator_severity. Same
// audit pattern.
func (s *Store) ApplyFindingSeverityOverride(findingID int64, newSeverity, reason string, origin EditOrigin) error {
	if !validSeverity(newSeverity) {
		return fmt.Errorf("invalid severity %q (allowed: %s)", newSeverity, strings.Join(allowedSeverities, ","))
	}
	tx, err := s.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var old sql.NullString
	if err := tx.QueryRow(`SELECT operator_severity FROM findings WHERE id = ?`, findingID).Scan(&old); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("finding %d not found", findingID)
		}
		return err
	}
	if old.Valid && old.String == newSeverity {
		return tx.Commit()
	}
	if _, err := tx.Exec(`UPDATE findings SET operator_severity = ? WHERE id = ?`, newSeverity, findingID); err != nil {
		return err
	}
	if err := writeFindingEdit(tx, findingID, "severity_override", old.String, newSeverity, reason, origin); err != nil {
		return err
	}
	return tx.Commit()
}

// ApplyFindingAddTag appends a tag to findings.tags (comma-separated)
// if not already present.
func (s *Store) ApplyFindingAddTag(findingID int64, tag, reason string, origin EditOrigin) error {
	if tag == "" {
		return errors.New("tag cannot be empty")
	}
	tx, err := s.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var existing sql.NullString
	if err := tx.QueryRow(`SELECT tags FROM findings WHERE id = ?`, findingID).Scan(&existing); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("finding %d not found", findingID)
		}
		return err
	}
	current := splitTags(existing.String)
	for _, t := range current {
		if t == tag {
			return tx.Commit() // already present
		}
	}
	current = append(current, tag)
	newTags := strings.Join(current, ",")
	if _, err := tx.Exec(`UPDATE findings SET tags = ? WHERE id = ?`, newTags, findingID); err != nil {
		return err
	}
	if err := writeFindingEdit(tx, findingID, "tag_add", "", tag, reason, origin); err != nil {
		return err
	}
	return tx.Commit()
}

// ApplyFindingRemoveTag drops a tag. No-op when absent.
func (s *Store) ApplyFindingRemoveTag(findingID int64, tag, reason string, origin EditOrigin) error {
	if tag == "" {
		return errors.New("tag cannot be empty")
	}
	tx, err := s.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var existing sql.NullString
	if err := tx.QueryRow(`SELECT tags FROM findings WHERE id = ?`, findingID).Scan(&existing); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("finding %d not found", findingID)
		}
		return err
	}
	current := splitTags(existing.String)
	kept := make([]string, 0, len(current))
	found := false
	for _, t := range current {
		if t == tag {
			found = true
			continue
		}
		kept = append(kept, t)
	}
	if !found {
		return tx.Commit()
	}
	if _, err := tx.Exec(`UPDATE findings SET tags = ? WHERE id = ?`, strings.Join(kept, ","), findingID); err != nil {
		return err
	}
	if err := writeFindingEdit(tx, findingID, "tag_remove", tag, "", reason, origin); err != nil {
		return err
	}
	return tx.Commit()
}

// LinkRunToFinding records a finding ↔ orchestration_run association.
// Idempotent (PRIMARY KEY collision = no-op). Writes an audit row on
// first link only.
func (s *Store) LinkRunToFinding(findingID int64, runID int64, stepID, reason string, origin EditOrigin) error {
	tx, err := s.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`
		INSERT INTO finding_run_links (finding_id, orchestration_run_id, step_id)
		VALUES (?, ?, ?)
		ON CONFLICT (finding_id, orchestration_run_id, step_id) DO NOTHING`,
		findingID, runID, stepID,
	)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return tx.Commit()
	}
	new := fmt.Sprintf("run:%d/step:%s", runID, stepID)
	if err := writeFindingEdit(tx, findingID, "linked_run", "", new, reason, origin); err != nil {
		return err
	}
	return tx.Commit()
}

// ListFindingRunLinks returns every (run, step) tuple ever linked to
// this finding.
func (s *Store) ListFindingRunLinks(findingID int64) ([]FindingRunLink, error) {
	rows, err := s.Query(`
		SELECT finding_id, orchestration_run_id, step_id, linked_at
		  FROM finding_run_links WHERE finding_id = ?
		 ORDER BY linked_at DESC`, findingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FindingRunLink
	for rows.Next() {
		var l FindingRunLink
		if err := rows.Scan(&l.FindingID, &l.OrchestrationRunID, &l.StepID, &l.LinkedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ListRunLinkedFindings returns every finding the given orchestration
// run touched (any step). Used by the run-detail UI's "actions
// applied" surface.
func (s *Store) ListRunLinkedFindings(runID int64) ([]FindingRunLink, error) {
	rows, err := s.Query(`
		SELECT finding_id, orchestration_run_id, step_id, linked_at
		  FROM finding_run_links WHERE orchestration_run_id = ?
		 ORDER BY linked_at DESC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FindingRunLink
	for rows.Next() {
		var l FindingRunLink
		if err := rows.Scan(&l.FindingID, &l.OrchestrationRunID, &l.StepID, &l.LinkedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// FindingRunLink mirrors finding_run_links.
type FindingRunLink struct {
	FindingID          int64
	OrchestrationRunID int64
	StepID             sql.NullString
	LinkedAt           time.Time
}

// ── helpers ────────────────────────────────────────────────────────

var allowedStatuses = []string{
	"open", "acknowledged", "investigating",
	"resolved", "false_positive", "wontfix", "suppressed",
}

func validFindingStatus(s string) bool {
	for _, v := range allowedStatuses {
		if v == s {
			return true
		}
	}
	return false
}

var allowedSeverities = []string{"CRITICAL", "HIGH", "MEDIUM", "LOW", "INFO"}

func validSeverity(s string) bool {
	for _, v := range allowedSeverities {
		if v == s {
			return true
		}
	}
	return false
}

func splitTags(s string) []string {
	if s == "" {
		return nil
	}
	out := strings.Split(s, ",")
	clean := out[:0]
	for _, t := range out {
		t = strings.TrimSpace(t)
		if t != "" {
			clean = append(clean, t)
		}
	}
	return clean
}

func writeFindingEdit(tx *sql.Tx, findingID int64, field, oldVal, newVal, reason string, origin EditOrigin) error {
	_, err := tx.Exec(`
		INSERT INTO finding_edits
			(finding_id, field, old_value, new_value, reason,
			 edited_by_user_id, edited_by_email,
			 orchestration_run_id, orchestration_step_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		findingID, field,
		sqlNullableString(oldVal),
		sqlNullableString(newVal),
		sqlNullableString(reason),
		nullableInt64(origin.UserID),
		sqlNullableString(origin.UserEmail),
		nullableInt64(origin.OrchestrationRunID),
		sqlNullableString(origin.OrchestrationStep),
	)
	return err
}

// sqlNullableString returns nil for empty strings so SQLite stores
// NULL rather than the literal empty-string. Mirrors the existing
// `nullable` helper but kept local to avoid pulling unrelated code
// in.
func sqlNullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func coalesceReason(primary, fallback string) string {
	if primary != "" {
		return primary
	}
	return fallback
}
