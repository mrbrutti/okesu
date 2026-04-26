package db

import (
	"os"
	"time"
)

// DBStats summarises the SQLite file for the Settings → Database page and
// retention decisions.
type DBStats struct {
	Path             string    `json:"path"`
	SizeBytes        int64     `json:"size_bytes"`
	EventCount       int64     `json:"event_count"`
	OldestEventTs    int64     `json:"oldest_event_ts,omitempty"` // unix ms; 0 if no events
	FindingCount     int64     `json:"finding_count"`
	RunCount         int64     `json:"run_count"`
	SessionCount     int64     `json:"session_count"`
	DeliveryCount    int64     `json:"delivery_count"`
	GeneratedAt      time.Time `json:"generated_at"`
}

// Stats gathers row counts + file size in one shot.
func (s *Store) Stats() (DBStats, error) {
	out := DBStats{
		Path:        s.path,
		GeneratedAt: time.Now().UTC(),
	}
	if fi, err := os.Stat(s.path); err == nil {
		out.SizeBytes = fi.Size()
	}
	type counter struct {
		dst *int64
		sql string
	}
	cs := []counter{
		{&out.EventCount, `SELECT COUNT(*) FROM events`},
		{&out.FindingCount, `SELECT COUNT(*) FROM findings`},
		{&out.RunCount, `SELECT COUNT(*) FROM runs`},
		{&out.SessionCount, `SELECT COUNT(*) FROM sessions`},
		{&out.DeliveryCount, `SELECT COUNT(*) FROM notification_deliveries`},
	}
	for _, c := range cs {
		if err := s.QueryRow(c.sql).Scan(c.dst); err != nil {
			return out, err
		}
	}
	_ = s.QueryRow(`SELECT COALESCE(MIN(ts), 0) FROM events`).Scan(&out.OldestEventTs)
	return out, nil
}

// PruneEventsOlderThan deletes rows from events where ts < cutoff (Unix ms).
// Returns the number of rows deleted. Does NOT touch findings.
func (s *Store) PruneEventsOlderThan(cutoffMs int64) (int64, error) {
	res, err := s.Exec(`DELETE FROM events WHERE ts < ?`, cutoffMs)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Vacuum runs `VACUUM`. Reclaims space after large deletes; blocking, so the
// caller should expect this to take a while on big DBs.
func (s *Store) Vacuum() error {
	_, err := s.Exec(`VACUUM`)
	return err
}
