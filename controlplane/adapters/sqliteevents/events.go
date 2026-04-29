// Package sqliteevents implements ports.EventStore against the same
// SQLite database the relational state Store uses. Used in dev and for
// small-fleet single-instance deployments. Production-scale OCI
// deployments use the clickhouse adapter.
//
// Implementation note: this is intentionally a thin shim over the
// existing db.Store methods (InsertEvent, RecentEvents) — the goal of
// Phase 8c is to land the seam, not to rewrite working code. As we
// migrate the rest of the CP to query through ports.EventStore, the
// sqlite-specific helpers in db/ become candidates for deletion.
package sqliteevents

import (
	"context"
	"database/sql"

	"github.com/section9labs/okesu/controlplane/db"
	"github.com/section9labs/okesu/controlplane/ports"
)

// Adapter satisfies ports.EventStore by delegating to db.Store.
type Adapter struct {
	store *db.Store
}

// New returns an EventStore backed by the given Store.
func New(s *db.Store) *Adapter { return &Adapter{store: s} }

func (a *Adapter) Insert(_ context.Context, e ports.EventRecord) (int64, error) {
	return a.store.InsertEvent(toDB(&e))
}

func (a *Adapter) InsertBatch(ctx context.Context, events []ports.EventRecord) error {
	// SQLite doesn't natively benefit from explicit batching the way
	// ClickHouse does — we just loop in a transaction so the inserts
	// are atomic-ish from the writer's perspective.
	tx, err := a.store.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO events (ts, type, agent, host, severity, title, raw_json)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, e := range events {
		if _, err := stmt.ExecContext(ctx,
			e.Ts, e.Type,
			nullable(e.Agent), nullable(e.Host),
			nullable(e.Severity), nullable(e.Title),
			e.RawJSON,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (a *Adapter) Recent(_ context.Context, limit int, beforeTs int64) ([]ports.EventRecord, error) {
	rows, err := a.store.RecentEvents(limit, beforeTs)
	if err != nil {
		return nil, err
	}
	out := make([]ports.EventRecord, 0, len(rows))
	for _, r := range rows {
		out = append(out, fromDB(r))
	}
	return out, nil
}

func (a *Adapter) RecentFiltered(_ context.Context, f ports.EventFilter, limit int, beforeTs int64) ([]ports.EventRecord, error) {
	rows, err := a.store.RecentEventsFiltered(f.Agent, f.Host, limit, beforeTs)
	if err != nil {
		return nil, err
	}
	out := make([]ports.EventRecord, 0, len(rows))
	for _, r := range rows {
		out = append(out, fromDB(r))
	}
	return out, nil
}

func toDB(e *ports.EventRecord) *db.Event {
	return &db.Event{
		Ts:       e.Ts,
		Type:     e.Type,
		Agent:    nullable(e.Agent),
		Host:     nullable(e.Host),
		Severity: nullable(e.Severity),
		Title:    nullable(e.Title),
		RawJSON:  e.RawJSON,
	}
}

func fromDB(e *db.Event) ports.EventRecord {
	return ports.EventRecord{
		ID:         e.ID,
		Ts:         e.Ts,
		Type:       e.Type,
		Agent:      e.Agent.String,
		Host:       e.Host.String,
		Severity:   e.Severity.String,
		Title:      e.Title.String,
		RawJSON:    e.RawJSON,
		ReceivedAt: e.ReceivedAt,
	}
}

// nullable mirrors db.nullable for our limited use here. Empty string
// becomes a NULL column value; non-empty becomes a present TEXT.
func nullable(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

// Compile-time assertion.
var _ ports.EventStore = (*Adapter)(nil)
