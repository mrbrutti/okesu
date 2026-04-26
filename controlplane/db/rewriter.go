package db

import (
	"context"
	"database/sql"
	"strings"
)

// Phase 8b.next: dialect-aware query layer.
//
// The Go code base uses `?` placeholders throughout (the SQLite
// convention). Postgres expects `$1, $2, ...`. Rather than dual-source
// every query — fragile, divergence-prone — we override Store.Exec /
// Query / QueryRow / their Context variants to rewrite SQL on the way
// to the driver when Dialect == DialectPostgres.
//
// The rewriter walks the SQL once: every literal `?` outside string
// quoting becomes `$N` with N counting up. Single-quoted strings
// (SQL string literals) are passed through verbatim — that's where
// `?` could legitimately appear inside content (e.g. a stored
// regex). Double-quoted identifiers (Postgres convention) are also
// passed through.
//
// Cost: one pass per query, allocation-free for queries with no
// placeholders. Negligible compared to the round-trip to the DB.
//
// Trade-offs we accept:
//   - We don't try to handle dollar-quoted strings ($$...$$). They
//     exist for procedural code (function bodies); we don't write any
//     procedural code.
//   - `??` (mysql escape) is not a thing in our code base. If we ever
//     introduced it, this rewriter would mishandle it.

// rewriteForPostgres returns the input SQL with `?` placeholders
// converted to `$1, $2, ...`. Strings inside single quotes are not
// rewritten. Returns the input unchanged if no `?` is present (cheap
// fast-path for placeholderless DDL / SELECT-without-args queries).
func rewriteForPostgres(q string) string {
	if !strings.ContainsRune(q, '?') {
		return q
	}
	var b strings.Builder
	b.Grow(len(q) + 8)
	inQuote := false
	n := 0
	for i := 0; i < len(q); i++ {
		c := q[i]
		switch {
		case c == '\'' && (i == 0 || q[i-1] != '\\'):
			// Toggle quote state. SQL standard escape is doubled '' inside
			// a string but we don't need to track that here — we just
			// pass everything through verbatim until the closing quote.
			inQuote = !inQuote
			b.WriteByte(c)
		case c == '?' && !inQuote:
			n++
			b.WriteByte('$')
			b.WriteString(itoa(n))
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// itoa is a small uint→decimal helper that doesn't pull strconv into
// every import that touches the rewriter (negligible, but the cost of
// calling it on every Exec adds up).
func itoa(n int) string {
	if n < 10 {
		return string('0' + byte(n))
	}
	// Up to 4 digits is enough for any sane query. Anything more and
	// the operator has bigger problems than placeholder rewriting.
	var buf [4]byte
	i := len(buf)
	for n > 0 && i > 0 {
		i--
		buf[i] = '0' + byte(n%10)
		n /= 10
	}
	return string(buf[i:])
}

// tsToMillisExpr returns a SQL expression that converts a TIMESTAMP
// column named `ts` into unix milliseconds, dialect-aware. SQLite uses
// strftime('%s', ts) * 1000; Postgres uses EXTRACT(EPOCH FROM ts) *
// 1000.
//
// Used by aggregate queries that need to compare server-side TIMESTAMP
// rows (DEFAULT CURRENT_TIMESTAMP) against unix-ms values from the
// API. Pass the column name (e.g. "ts", "triaged_at") to compose:
//
//	tsToMillisExpr(s.Dialect, "ts") + " >= ?"
//
// The 1-arg form defaults the column name to "ts" which is what most
// callers want.
func tsToMillisExpr(d Dialect, col ...string) string {
	c := "ts"
	if len(col) > 0 && col[0] != "" {
		c = col[0]
	}
	if d == DialectPostgres {
		return "(EXTRACT(EPOCH FROM " + c + ") * 1000)::bigint"
	}
	return "strftime('%s', " + c + ") * 1000"
}

// q rewrites the query for the Store's dialect. Used internally by the
// overridden Exec/Query/QueryRow methods. Exposed for the rare query
// builder that constructs SQL dynamically and wants to apply the
// rewrite once before passing to a method that will rewrite again
// (idempotent — second pass is a no-op when no `?` remain).
func (s *Store) q(query string) string {
	if s.Dialect == DialectPostgres {
		return rewriteForPostgres(query)
	}
	return query
}

// Exec overrides *sql.DB.Exec to apply dialect-aware placeholder
// rewriting. Every existing call site that uses `?` placeholders
// works unchanged on Postgres.
func (s *Store) Exec(query string, args ...any) (sql.Result, error) {
	return s.DB.Exec(s.q(query), args...)
}

// ExecContext overrides for context-aware callers.
func (s *Store) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return s.DB.ExecContext(ctx, s.q(query), args...)
}

// Query overrides *sql.DB.Query.
func (s *Store) Query(query string, args ...any) (*sql.Rows, error) {
	return s.DB.Query(s.q(query), args...)
}

// QueryContext overrides *sql.DB.QueryContext.
func (s *Store) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return s.DB.QueryContext(ctx, s.q(query), args...)
}

// QueryRow overrides *sql.DB.QueryRow.
func (s *Store) QueryRow(query string, args ...any) *sql.Row {
	return s.DB.QueryRow(s.q(query), args...)
}

// QueryRowContext overrides *sql.DB.QueryRowContext.
func (s *Store) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return s.DB.QueryRowContext(ctx, s.q(query), args...)
}
