// Package clickhouseevents implements ports.EventStore against
// ClickHouse — the production adapter for fleet-scale event volume.
//
// Design notes:
//
//   - ClickHouse loves batch inserts. The InsertBatch method uses a
//     single async batch to write hundreds-to-thousands of rows in one
//     network round-trip. The eventpipeline worker (consuming Kafka)
//     accumulates 100ms / 1000-row windows before flushing.
//   - Insert (single-row) is provided for completeness but is rarely
//     called in production; the webhook handler fans rows through
//     Kafka where the worker batches them. Tests + the small in-CP
//     ingest paths (api/findings/ingest) still use Insert directly.
//   - Schema is fixed (Phase 8c.next). The events table uses
//     MergeTree partitioning on toYYYYMM(received_at) and ordering on
//     (ts, agent, host) which gives the dashboard's typical filter set
//     index hits without secondary indexes. Schema bootstrap happens
//     on connect via CREATE TABLE IF NOT EXISTS.
//   - ClickHouse stores ts as a DateTime64(3) (millisecond precision)
//     to match the unix-ms ints we already use everywhere else. The
//     adapter converts at the wire boundary.
package clickhouseevents

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"github.com/section9labs/okesu/controlplane/ports"
)

// Config bundles the connection params. DSNs for OCI ClickHouse-on-OKE
// look like "clickhouse://user:pass@host:9000/dbname?secure=true" —
// we accept that as URL OR as discrete fields, parsed below.
type Config struct {
	Addr     []string // host:port pairs (multi-node cluster)
	Database string
	Username string
	Password string
	// Secure enables TLS. Off by default for dev (Redpanda + local CH).
	Secure bool
	// MaxOpenConns / MaxIdleConns: tune for the worker's concurrency.
	// Sensible defaults applied if zero.
	MaxOpenConns int
	MaxIdleConns int
}

// Adapter satisfies ports.EventStore.
type Adapter struct {
	conn driver.Conn
	db   string
}

// New connects to ClickHouse with the given config and ensures the
// events table exists. Idempotent — safe to call on every CP boot.
func New(ctx context.Context, c Config) (*Adapter, error) {
	if len(c.Addr) == 0 {
		return nil, fmt.Errorf("clickhouseevents: at least one address required")
	}
	if c.Database == "" {
		c.Database = "okesu_events"
	}
	if c.MaxOpenConns == 0 {
		c.MaxOpenConns = 10
	}
	if c.MaxIdleConns == 0 {
		c.MaxIdleConns = 5
	}
	opts := &clickhouse.Options{
		Addr: c.Addr,
		Auth: clickhouse.Auth{
			Database: c.Database,
			Username: c.Username,
			Password: c.Password,
		},
		MaxOpenConns: c.MaxOpenConns,
		MaxIdleConns: c.MaxIdleConns,
		ConnMaxLifetime: 30 * time.Minute,
		DialTimeout:    10 * time.Second,
	}
	if c.Secure {
		opts.TLS = nil // populate explicitly when we add cert pinning
	}
	conn, err := clickhouse.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("clickhouseevents: open: %w", err)
	}
	if err := conn.Ping(ctx); err != nil {
		return nil, fmt.Errorf("clickhouseevents: ping: %w", err)
	}

	a := &Adapter{conn: conn, db: c.Database}
	if err := a.ensureSchema(ctx); err != nil {
		return nil, fmt.Errorf("clickhouseevents: schema: %w", err)
	}
	return a, nil
}

// ensureSchema creates the events table if missing. The DDL mirrors
// the SQLite events table semantically — same columns, same field
// meanings — but laid out for ClickHouse's strengths. Operators with
// separate cluster-management tooling can pre-create it; the
// IF NOT EXISTS makes us a no-op in that case.
func (a *Adapter) ensureSchema(ctx context.Context) error {
	const ddl = `
CREATE TABLE IF NOT EXISTS events (
  id           UInt64,
  ts           Int64,
  type         LowCardinality(String),
  agent        LowCardinality(String),
  host         LowCardinality(String),
  severity     LowCardinality(String),
  title        String,
  raw_json     String CODEC(ZSTD(3)),
  received_at  DateTime64(3) DEFAULT now64()
)
ENGINE = MergeTree
PARTITION BY toYYYYMM(received_at)
ORDER BY (ts, agent, host)
SETTINGS index_granularity = 8192
`
	return a.conn.Exec(ctx, ddl)
}

// Insert persists a single event. Returns the assigned id (we generate
// it client-side from a snowflake-ish ts*shard counter to avoid a
// round-trip; ClickHouse doesn't care).
func (a *Adapter) Insert(ctx context.Context, e ports.EventRecord) (int64, error) {
	id := nextID(e.Ts)
	err := a.conn.Exec(ctx, `
		INSERT INTO events (id, ts, type, agent, host, severity, title, raw_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, id, e.Ts, e.Type, e.Agent, e.Host, e.Severity, e.Title, e.RawJSON)
	if err != nil {
		return 0, err
	}
	return int64(id), nil //nolint:gosec
}

// InsertBatch is the production hot path. ClickHouse-go's PrepareBatch
// gives us a single network round-trip regardless of batch size.
func (a *Adapter) InsertBatch(ctx context.Context, events []ports.EventRecord) error {
	if len(events) == 0 {
		return nil
	}
	batch, err := a.conn.PrepareBatch(ctx, `
		INSERT INTO events (id, ts, type, agent, host, severity, title, raw_json)
	`)
	if err != nil {
		return err
	}
	for _, e := range events {
		if err := batch.Append(
			nextID(e.Ts), e.Ts, e.Type, e.Agent, e.Host, e.Severity, e.Title, e.RawJSON,
		); err != nil {
			return err
		}
	}
	return batch.Send()
}

// Recent returns up to `limit` events newest-first, optionally before
// the given ts cursor. Used by the Live Events UI's infinite-scroll
// pagination.
func (a *Adapter) Recent(ctx context.Context, limit int, beforeTs int64) ([]ports.EventRecord, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	var query string
	var args []any
	if beforeTs > 0 {
		query = `SELECT id, ts, type, agent, host, severity, title, raw_json, received_at
		         FROM events
		         WHERE ts < ?
		         ORDER BY ts DESC
		         LIMIT ?`
		args = []any{beforeTs, limit}
	} else {
		query = `SELECT id, ts, type, agent, host, severity, title, raw_json, received_at
		         FROM events
		         ORDER BY ts DESC
		         LIMIT ?`
		args = []any{limit}
	}
	rows, err := a.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]ports.EventRecord, 0, limit)
	for rows.Next() {
		var (
			r          ports.EventRecord
			id         uint64
			receivedAt time.Time
		)
		if err := rows.Scan(&id, &r.Ts, &r.Type, &r.Agent, &r.Host, &r.Severity, &r.Title, &r.RawJSON, &receivedAt); err != nil {
			return nil, err
		}
		r.ID = int64(id) //nolint:gosec
		r.ReceivedAt = receivedAt
		out = append(out, r)
	}
	return out, rows.Err()
}

// RecentFiltered is Recent with optional agent/host equality
// constraints — pushed into the WHERE clause so the columnar scan
// only touches matching rows.
func (a *Adapter) RecentFiltered(ctx context.Context, f ports.EventFilter, limit int, beforeTs int64) ([]ports.EventRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	if f.Agent == "" && f.Host == "" {
		if limit > 1000 {
			limit = 1000
		}
	} else if limit > 5000 {
		limit = 5000
	}
	conds := []string{}
	args := []any{}
	if f.Agent != "" {
		conds = append(conds, "agent = ?")
		args = append(args, f.Agent)
	}
	if f.Host != "" {
		conds = append(conds, "host = ?")
		args = append(args, f.Host)
	}
	if beforeTs > 0 {
		conds = append(conds, "ts < ?")
		args = append(args, beforeTs)
	}
	query := `SELECT id, ts, type, agent, host, severity, title, raw_json, received_at FROM events`
	if len(conds) > 0 {
		query += " WHERE " + strings.Join(conds, " AND ")
	}
	query += ` ORDER BY ts DESC LIMIT ?`
	args = append(args, limit)
	rows, err := a.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ports.EventRecord, 0, limit)
	for rows.Next() {
		var (
			r          ports.EventRecord
			id         uint64
			receivedAt time.Time
		)
		if err := rows.Scan(&id, &r.Ts, &r.Type, &r.Agent, &r.Host, &r.Severity, &r.Title, &r.RawJSON, &receivedAt); err != nil {
			return nil, err
		}
		r.ID = int64(id) //nolint:gosec
		r.ReceivedAt = receivedAt
		out = append(out, r)
	}
	return out, rows.Err()
}

// Close releases the ClickHouse connection pool.
func (a *Adapter) Close() error {
	if a.conn == nil {
		return nil
	}
	return a.conn.Close()
}

// idCounter generates monotonic IDs without a round-trip to the DB.
// Format: (ts_ms << 16) | (counter & 0xFFFF). Two events arriving in
// the same millisecond on the same CP get sequential IDs; cross-CP
// collisions are theoretically possible but the IDs only have to be
// unique-enough for ClickHouse ordering, not globally unique.
var idCounter atomic.Uint64

func nextID(tsMs int64) uint64 {
	c := idCounter.Add(1)
	return uint64(tsMs)<<16 | (c & 0xFFFF) //nolint:gosec
}

// Compile-time assertion.
var _ ports.EventStore = (*Adapter)(nil)
