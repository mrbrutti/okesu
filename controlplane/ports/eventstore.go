package ports

import (
	"context"
	"time"
)

// EventStore is the firehose-style storage for daemon events. Append-
// only at write time; query patterns are time-bounded scans + agent/
// host/severity filters. NOT a transactional surface — findings live
// in the relational state Store because they mutate.
//
// Adapters: clickhouse (production scale on OCI Compute / OKE — handles
// 100k+ events/sec with native time-series indexes), sqliteevents
// (dev — wraps the existing events table; fine to ~1k events/sec).
//
// Why split this from Store: events are 99% of the row volume, write-
// heavy, never updated, and benefit from columnar storage at scale.
// The state Store wants OLTP semantics; the EventStore wants OLAP-style
// ingest. Different shapes, different choices.
type EventStore interface {
	// Insert persists one event. Returns the assigned ID for adapters
	// that have one (sqlite); ClickHouse uses ts+id-as-uuid and ignores
	// the int64 ID — callers don't depend on it.
	Insert(ctx context.Context, e EventRecord) (int64, error)

	// InsertBatch persists multiple events in one round-trip. Required
	// for high-volume ingest — ClickHouse loves batched inserts. The
	// in-process adapter just loops Insert.
	InsertBatch(ctx context.Context, events []EventRecord) error

	// Recent returns up to `limit` events newest-first, optionally
	// before the given ts cursor. Used by the Live Events UI's
	// infinite-scroll pagination.
	Recent(ctx context.Context, limit int, beforeTs int64) ([]EventRecord, error)
}

// EventRecord is the wire shape between EventStore adapters and the
// rest of the CP. Mirrors the historical db.Event struct but uses
// strings (with empty=null) instead of sql.NullString so the type is
// driver-independent.
type EventRecord struct {
	ID         int64
	Ts         int64 // unix ms — matches what the daemon sends
	Type       string
	Agent      string
	Host       string
	Severity   string
	Title      string
	RawJSON    string
	ReceivedAt time.Time
}
