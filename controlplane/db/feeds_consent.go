package db

import (
	"database/sql"
	"errors"
	"time"
)

// GetFeedsConsentGrantedAt reads cp_meta.feeds_consent_granted_at.
// Returns nil when consent has not yet been granted (the scheduler
// uses this to refuse refreshes on a fresh CP).
// The cp_meta singleton row is bootstrapped on first access if absent.
func (s *Store) GetFeedsConsentGrantedAt() (*time.Time, error) {
	var t sql.NullTime
	err := s.QueryRow(`SELECT feeds_consent_granted_at FROM cp_meta WHERE id = 1`).Scan(&t)
	if errors.Is(err, sql.ErrNoRows) {
		// Row not yet seeded — bootstrap it and retry.
		if _, berr := s.CPMeta(); berr != nil {
			return nil, berr
		}
		err = s.QueryRow(`SELECT feeds_consent_granted_at FROM cp_meta WHERE id = 1`).Scan(&t)
	}
	if err != nil {
		return nil, err
	}
	if !t.Valid {
		return nil, nil
	}
	v := t.Time
	return &v, nil
}

// SetFeedsConsentGrantedAt writes the consent timestamp. Idempotent —
// overwriting an existing timestamp with a newer one does not trigger
// side effects in the scheduler (the refresh gate is binary: NULL vs
// non-NULL).
func (s *Store) SetFeedsConsentGrantedAt(at time.Time) error {
	// Ensure the cp_meta singleton row exists before UPDATE; otherwise
	// the UPDATE silently affects 0 rows. Mirrors UpdateCPMeta's pattern.
	if _, err := s.CPMeta(); err != nil {
		return err
	}
	_, err := s.Exec(`UPDATE cp_meta SET feeds_consent_granted_at = ? WHERE id = 1`, at.UTC())
	return err
}
