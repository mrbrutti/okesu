package db

import (
	"strings"
	"time"
)

// GetInvestigationNoteDraft returns the merged Yjs document bytes
// for the case's in-flight war-room draft. Returns sql.ErrNoRows if
// no draft exists.
func (s *Store) GetInvestigationNoteDraft(invID int64) ([]byte, error) {
	row := s.QueryRow(`SELECT ydoc_state FROM investigation_note_drafts WHERE investigation_id = ?`, invID)
	var b []byte
	if err := row.Scan(&b); err != nil {
		return nil, err
	}
	return b, nil
}

// UpsertInvestigationNoteDraft replaces (or inserts) the draft bytes
// for the case. updated_at is bumped to CURRENT_TIMESTAMP.
func (s *Store) UpsertInvestigationNoteDraft(invID int64, ydocState []byte) error {
	_, err := s.Exec(`
		INSERT INTO investigation_note_drafts (investigation_id, ydoc_state, updated_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT (investigation_id) DO UPDATE
		SET ydoc_state = excluded.ydoc_state, updated_at = CURRENT_TIMESTAMP`,
		invID, ydocState)
	return err
}

// DeleteInvestigationNoteDraft removes the draft row. Idempotent.
func (s *Store) DeleteInvestigationNoteDraft(invID int64) error {
	_, err := s.Exec(`DELETE FROM investigation_note_drafts WHERE investigation_id = ?`, invID)
	return err
}

// SweepStaleInvestigationNoteDrafts deletes draft rows older than
// `older` whose investigation_id is NOT in `liveIDs` (rooms currently
// active in memory). Returns the number of rows deleted.
//
// Called from the daily GC sweep; the live set is computed by the
// RelayHub at sweep time.
func (s *Store) SweepStaleInvestigationNoteDrafts(older time.Duration, liveIDs []int64) (int64, error) {
	cutoff := time.Now().UTC().Add(-older).Format(rfc3339)
	q := `DELETE FROM investigation_note_drafts WHERE updated_at < ?`
	args := []any{cutoff}
	if len(liveIDs) > 0 {
		placeholders := make([]string, len(liveIDs))
		for i, id := range liveIDs {
			placeholders[i] = "?"
			args = append(args, id)
		}
		q += " AND investigation_id NOT IN (" + strings.Join(placeholders, ",") + ")"
	}
	res, err := s.Exec(q, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
