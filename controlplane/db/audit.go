package db

import (
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

// AuditRow is a persisted audit-log entry.
type AuditRow struct {
	ID         int64
	Ts         time.Time
	ActorID    sql.NullInt64
	ActorEmail sql.NullString
	ActorRole  sql.NullString
	ActorIP    sql.NullString
	Action     string
	Target     sql.NullString
	Result     string
	Metadata   sql.NullString // JSON
}

// AuditEntry is the input shape for InsertAudit. Fields are optional except
// Action.
type AuditEntry struct {
	ActorID    int64           // 0 = no user actor (e.g. system event)
	ActorEmail string
	ActorRole  string
	ActorIP    string
	Action     string
	Target     string
	Result     string          // "ok" (default), "denied", "error"
	Metadata   any             // serialized to JSON, or nil to skip
}

// InsertAudit writes one audit row. Errors are returned but callers usually
// ignore them — audit is best-effort by design.
func (s *Store) InsertAudit(e AuditEntry) error {
	if e.Action == "" {
		return nil
	}
	if e.Result == "" {
		e.Result = "ok"
	}
	var metaJSON sql.NullString
	if e.Metadata != nil {
		b, err := json.Marshal(e.Metadata)
		if err == nil && len(b) > 0 {
			metaJSON = sql.NullString{String: string(b), Valid: true}
		}
	}
	var actorID sql.NullInt64
	if e.ActorID > 0 {
		actorID = sql.NullInt64{Int64: e.ActorID, Valid: true}
	}
	_, err := s.Exec(`
		INSERT INTO audit_log (actor_id, actor_email, actor_role, actor_ip,
		                      action, target, result, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`,
		actorID,
		nullable(e.ActorEmail), nullable(e.ActorRole), nullable(e.ActorIP),
		e.Action, nullable(e.Target), e.Result, metaJSON,
	)
	return err
}

// AuditFilter narrows the ListAudit query.
type AuditFilter struct {
	ActorEmail string
	Action     string // exact or "prefix.*" — see ListAudit
	Target     string
	Result     string
	SinceMs    int64
	UntilMs    int64
	Limit      int
	Offset     int
}

// ListAudit returns rows matching the filter, newest first.
func (s *Store) ListAudit(f AuditFilter) ([]*AuditRow, error) {
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 100
	}
	var (
		clauses []string
		args    []any
	)
	if f.ActorEmail != "" {
		clauses = append(clauses, "actor_email = ?")
		args = append(args, f.ActorEmail)
	}
	if f.Action != "" {
		// Support "user.*" prefix wildcard for filtering by category.
		if strings.HasSuffix(f.Action, ".*") {
			clauses = append(clauses, "action LIKE ?")
			args = append(args, strings.TrimSuffix(f.Action, "*")+"%")
		} else {
			clauses = append(clauses, "action = ?")
			args = append(args, f.Action)
		}
	}
	if f.Target != "" {
		clauses = append(clauses, "target = ?")
		args = append(args, f.Target)
	}
	if f.Result != "" {
		clauses = append(clauses, "result = ?")
		args = append(args, f.Result)
	}
	// `ts` in audit_log is a wall-clock TIMESTAMP (DEFAULT
	// CURRENT_TIMESTAMP). Operators filter by unix-ms via the API; we
	// translate that to a ts comparison using the dialect's epoch-extract.
	if f.SinceMs > 0 {
		clauses = append(clauses, tsToMillisExpr(s.Dialect)+" >= ?")
		args = append(args, f.SinceMs)
	}
	if f.UntilMs > 0 {
		clauses = append(clauses, tsToMillisExpr(s.Dialect)+" < ?")
		args = append(args, f.UntilMs)
	}

	q := `SELECT id, ts, actor_id, actor_email, actor_role, actor_ip,
	             action, target, result, metadata
	      FROM audit_log`
	if len(clauses) > 0 {
		q += " WHERE " + strings.Join(clauses, " AND ")
	}
	q += " ORDER BY ts DESC, id DESC LIMIT ? OFFSET ?"
	args = append(args, f.Limit, f.Offset)

	rows, err := s.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*AuditRow
	for rows.Next() {
		r := &AuditRow{}
		if err := rows.Scan(
			&r.ID, &r.Ts,
			&r.ActorID, &r.ActorEmail, &r.ActorRole, &r.ActorIP,
			&r.Action, &r.Target, &r.Result, &r.Metadata,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
