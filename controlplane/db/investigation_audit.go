// Per-case audit timeline. Renders a chronological view of every
// observable event on an investigation: created, notes added,
// findings linked, runs linked, closed. Backs the workspace's Audit
// tab.
//
// We don't have a dedicated investigation_events table (and don't
// need one — every event has a natural home in an existing table).
// This module is purely a UNION-ALL query that flattens the four
// natural sources into a single chronologically-ordered stream.
//
// Sources today:
//   investigations.created_at, created_by      → "created"
//   investigations.closed_at, resolution        → "closed" (when set)
//   investigation_notes.created_at, author, body → "note"
//   investigation_findings.linked_at, link_method, linked_by → "finding_linked"
//   investigation_runs.linked_at                → "run_linked"
//
// Future expansions (status changes, unlink events, autolink reasons)
// would either come from audit_log entries the handlers emit, or new
// per-case event tables. The current handlers don't emit audit_log
// rows for investigation actions yet — that's a follow-up; see
// the api package's TODO list. For now this view is "what the schema
// already records", which is enough for the operator's "what
// happened on this case?" question.

package db

import (
	"database/sql"
	"strconv"
)

// AuditEvent is one row in a case's timeline. Fields are loose by
// design — different event kinds populate different details.
type AuditEvent struct {
	Ts    string `json:"ts"`     // ISO RFC3339, UTC
	Kind  string `json:"kind"`   // "created" | "closed" | "note" | "finding_linked" | "run_linked"
	By    string `json:"by"`     // operator email or 'system:<actor>'; empty for legacy/unknown
	Title string `json:"title"`  // short headline ("note added", "finding #12 linked")

	// Optional structured detail. The wire shape is dictated by `kind`:
	//   note          → {"body": "..."}                          (markdown)
	//   finding_linked → {"finding_id": 12, "method": "manual"}  method may be NULL
	//   run_linked    → {"run_id": 42}
	//   closed        → {"resolution": "resolved"}
	// Empty for kinds without structured detail.
	Details map[string]any `json:"details,omitempty"`
}

// ListInvestigationAudit returns the timeline for a case, oldest-
// first (so the UI can render top-down without reversing).
//
// Each row is dialect-portable — sqlite + postgres both accept
// CAST(... AS TEXT) and the explicit column-count alignment we use
// here. SQLite's UNION ALL doesn't enforce column types, but we
// keep the casts so postgres' stricter type system is happy.
func (s *Store) ListInvestigationAudit(invID int64) ([]AuditEvent, error) {
	rows, err := s.Query(`
		SELECT ts, kind, by_actor, title, find_id, link_method, run_id, body, resolution FROM (
			-- 1. Case created
			SELECT
				CAST(created_at AS TEXT) AS ts,
				'created'                 AS kind,
				COALESCE(created_by, '')  AS by_actor,
				'Case created'            AS title,
				NULL                      AS find_id,
				NULL                      AS link_method,
				NULL                      AS run_id,
				NULL                      AS body,
				NULL                      AS resolution
			FROM investigations WHERE id = ?

			UNION ALL

			-- 2. Case closed (only when closed_at is non-null)
			SELECT
				CAST(closed_at AS TEXT),
				'closed',
				COALESCE(created_by, ''),  -- no closed_by column today; surface created_by as best-effort
				'Case closed',
				NULL, NULL, NULL, NULL,
				COALESCE(resolution, '')
			FROM investigations
			WHERE id = ? AND closed_at IS NOT NULL

			UNION ALL

			-- 3. Notes
			SELECT
				CAST(created_at AS TEXT),
				'note',
				COALESCE(author, ''),
				'Note added',
				NULL, NULL, NULL,
				body,
				NULL
			FROM investigation_notes WHERE investigation_id = ?

			UNION ALL

			-- 4. Findings linked
			SELECT
				CAST(linked_at AS TEXT),
				'finding_linked',
				COALESCE(linked_by, ''),
				'Finding linked',
				finding_id,
				link_method,
				NULL, NULL, NULL
			FROM investigation_findings WHERE investigation_id = ?

			UNION ALL

			-- 5. Runs linked
			SELECT
				CAST(linked_at AS TEXT),
				'run_linked',
				'',                  -- investigation_runs has no by column today
				'Run linked',
				NULL, NULL,
				orchestration_run_id,
				NULL, NULL
			FROM investigation_runs WHERE investigation_id = ?
		)
		ORDER BY ts ASC`,
		invID, invID, invID, invID, invID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []AuditEvent{}
	for rows.Next() {
		var (
			ts, kind, by, title string
			findID              sql.NullInt64
			linkMethod          sql.NullString
			runID               sql.NullInt64
			body                sql.NullString
			resolution          sql.NullString
		)
		if err := rows.Scan(&ts, &kind, &by, &title, &findID, &linkMethod, &runID, &body, &resolution); err != nil {
			return nil, err
		}
		ev := AuditEvent{Ts: ts, Kind: kind, By: by, Title: title}

		switch kind {
		case "note":
			if body.Valid {
				ev.Details = map[string]any{"body": body.String}
			}
		case "finding_linked":
			ev.Details = map[string]any{}
			if findID.Valid {
				ev.Details["finding_id"] = findID.Int64
				ev.Title = "Finding #" + strconv.FormatInt(findID.Int64, 10) + " linked"
			}
			if linkMethod.Valid {
				ev.Details["method"] = linkMethod.String
			}
		case "run_linked":
			if runID.Valid {
				ev.Details = map[string]any{"run_id": runID.Int64}
				ev.Title = "Run #" + strconv.FormatInt(runID.Int64, 10) + " linked"
			}
		case "closed":
			if resolution.Valid && resolution.String != "" {
				ev.Details = map[string]any{"resolution": resolution.String}
				ev.Title = "Case closed (" + resolution.String + ")"
			}
		}

		out = append(out, ev)
	}
	return out, rows.Err()
}

