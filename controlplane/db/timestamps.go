// Timestamp parsing helpers shared by store types that need to convert
// SQLite/Postgres timestamp strings into time.Time. Co-located in one
// file (rather than per-table) so a single grep finds the layout list.
//
// We scan timestamps as sql.NullString and run them through ParseTimestamp
// because the cross-driver shape is inconsistent: pgx-stdlib emits
// RFC3339, SQLite's CURRENT_TIMESTAMP emits "YYYY-MM-DD HH:MM:SS"
// without a TZ marker. Using sql.NullTime would normalize this for
// pgx but not for the modernc.org/sqlite driver in use.

package db

import (
	"log"
	"time"
)

// timestampLayouts lists the formats we try, in order of empirical
// likelihood. Ordered so the most common shape (SQLite default) hits
// first; later entries cover Postgres TIMESTAMPTZ via pgx-stdlib.
var timestampLayouts = []string{
	"2006-01-02 15:04:05",
	"2006-01-02 15:04:05.999999",
	"2006-01-02T15:04:05Z",
	"2006-01-02T15:04:05.000000Z",
	time.RFC3339,
	time.RFC3339Nano,
}

// ParseTimestamp parses one of the timestamp string formats SQLite +
// Postgres scan as TEXT into a time.Time. Returns the zero time on
// unparseable input and logs a debug warning so a deployment-time
// driver/format mismatch is observable rather than silently rendering
// "0001-01-01" everywhere.
//
// TODO(phase-22.4): if a fifth layout creeps in, switch to a single
// parser via dateparse or similar; right now the linear list is
// cheaper than pulling in a dep.
func ParseTimestamp(ts string) time.Time {
	if ts == "" {
		return time.Time{}
	}
	for _, layout := range timestampLayouts {
		if t, err := time.Parse(layout, ts); err == nil {
			return t
		}
	}
	log.Printf("db.ParseTimestamp: unparseable timestamp %q (returning zero); consider adding a layout", ts)
	return time.Time{}
}
