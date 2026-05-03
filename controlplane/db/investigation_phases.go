// Operator-defined phases of an investigation. Each phase is a
// named time range stored as one row; rendering + drag-to-create
// gestures live in the frontend (PhasesLane). Coloring is derived
// from hash(name) on the client; no category column.
package db

import (
	"database/sql"
	"time"
)

type InvestigationPhase struct {
	ID              int64
	InvestigationID int64
	Name            string
	StartTs         int64
	EndTs           int64
	CreatedBy       sql.NullString
	CreatedAt       time.Time
}

type InvestigationPhaseInsert struct {
	InvestigationID int64
	Name            string
	StartTs         int64
	EndTs           int64
	CreatedBy       string
}

// InsertInvestigationPhase creates a new phase row. Returns the row's id.
// Validation (empty name, end < start) is enforced at the SQL level via
// CHECK constraints; the handler also pre-validates with friendly 400
// messages.
func (s *Store) InsertInvestigationPhase(in *InvestigationPhaseInsert) (int64, error) {
	res, err := s.Exec(`
		INSERT INTO investigation_phases
		  (investigation_id, name, start_ts, end_ts, created_by)
		VALUES (?, ?, ?, ?, ?)`,
		in.InvestigationID, in.Name, in.StartTs, in.EndTs,
		nullable(in.CreatedBy))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListInvestigationPhases returns the case's phases sorted ascending
// by start_ts, breaking ties on id. Empty slice (not nil) when no rows.
func (s *Store) ListInvestigationPhases(invID int64) ([]InvestigationPhase, error) {
	rows, err := s.Query(`
		SELECT id, investigation_id, name, start_ts, end_ts,
		       created_by, created_at
		FROM investigation_phases
		WHERE investigation_id = ?
		ORDER BY start_ts ASC, id ASC`, invID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []InvestigationPhase{}
	for rows.Next() {
		var p InvestigationPhase
		var createdAt sql.NullString
		if err := rows.Scan(&p.ID, &p.InvestigationID, &p.Name,
			&p.StartTs, &p.EndTs, &p.CreatedBy, &createdAt); err != nil {
			return nil, err
		}
		if createdAt.Valid {
			p.CreatedAt = ParseTimestamp(createdAt.String)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// UpdateInvestigationPhaseName renames a phase. Idempotent: if the row
// doesn't exist, the UPDATE is a no-op and the call returns nil.
func (s *Store) UpdateInvestigationPhaseName(invID, phaseID int64, name string) error {
	_, err := s.Exec(`
		UPDATE investigation_phases
		SET name = ?
		WHERE id = ? AND investigation_id = ?`,
		name, phaseID, invID)
	return err
}

// DeleteInvestigationPhase removes a phase row. Idempotent.
func (s *Store) DeleteInvestigationPhase(invID, phaseID int64) error {
	_, err := s.Exec(`
		DELETE FROM investigation_phases
		WHERE id = ? AND investigation_id = ?`,
		phaseID, invID)
	return err
}
