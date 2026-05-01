// Stale-finding garbage collection. Phase 22.10 PR β.
//
// Production analysis showed thousands of "open" findings sitting on
// the operator queue that have not been re-emitted in days — the
// underlying cause has resolved itself, the agent moved on, but the
// row stayed open because nothing actively closed it.
//
// AutoCloseStaleOpens runs in the background every hour. It looks
// for findings where status='open' AND the most recent emit
// (last_seen_at, falling back to created_at) is older than the
// threshold AND recurrence_count == 1 — i.e. the issue fired once
// and never came back. Those are the "agent saw it once, nobody
// cared, it's gone" rows.
//
// Closure is non-destructive: status moves to 'acknowledged' (it's
// effectively dismissed) with tag 'auto-stale' and a triage_note
// attributing the close. The auto-* prefix matches the existing
// queue-exclusion convention so these drop off the operator queue
// silently. Operators who want to see them re-emerge can lift the
// status back to 'open' from the drawer.

package db

import (
	"fmt"
	"strings"
	"time"
)

// StaleGCResult summarises the outcome of one GC sweep. Callers log
// the count so operators see the queue collapsing in the CP log.
type StaleGCResult struct {
	Closed    int           `json:"closed"`
	Threshold time.Duration `json:"threshold_ms"`
	Scanned   int           `json:"scanned"`
}

// AutoCloseStaleOpens closes 'open' findings that haven't been
// re-emitted within `threshold` AND fired only once
// (recurrence_count <= 1). Returns the count of rows closed.
//
// `limit` caps how many findings we touch per sweep so a backlog
// after long downtime doesn't lock the DB. The caller's tick keeps
// firing every hour; eventually the backlog drains.
func (s *Store) AutoCloseStaleOpens(threshold time.Duration, limit int) (StaleGCResult, error) {
	if threshold <= 0 {
		threshold = 24 * time.Hour
	}
	if limit <= 0 || limit > 5000 {
		limit = 1000
	}
	cutoff := time.Now().Add(-threshold).UTC()
	cutoffMs := cutoff.UnixMilli()

	// Pull candidates first so we can write one audit row each. The
	// COALESCE picks last_seen_at when present (recurrence-tracked
	// finding), else created_at (legacy row).
	rows, err := s.Query(`
		SELECT id, COALESCE(tags, '')
		  FROM findings
		 WHERE (status IS NULL OR status = 'open')
		   AND COALESCE(recurrence_count, 1) <= 1
		   AND COALESCE(
		         CAST(strftime('%s', last_seen_at) AS INTEGER) * 1000,
		         CAST(strftime('%s', created_at)   AS INTEGER) * 1000,
		         0
		       ) < ?
		   AND (tags IS NULL OR tags NOT LIKE '%keep-history%')
		 ORDER BY id
		 LIMIT ?`, cutoffMs, limit)
	if err != nil {
		return StaleGCResult{}, err
	}
	type cand struct {
		id   int64
		tags string
	}
	var candidates []cand
	for rows.Next() {
		var c cand
		if err := rows.Scan(&c.id, &c.tags); err != nil {
			rows.Close()
			return StaleGCResult{}, err
		}
		candidates = append(candidates, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return StaleGCResult{}, err
	}
	out := StaleGCResult{Threshold: threshold, Scanned: len(candidates)}
	if len(candidates) == 0 {
		return out, nil
	}

	for _, c := range candidates {
		reason := fmt.Sprintf("auto-closed: not re-seen in %s", humanDur(threshold))
		tx, err := s.Begin()
		if err != nil {
			return out, err
		}
		if _, err := tx.Exec(`
			UPDATE findings
			   SET status = 'acknowledged', triaged_at = CURRENT_TIMESTAMP,
			       triage_note = ?
			 WHERE id = ? AND (status IS NULL OR status = 'open')`,
			reason, c.id); err != nil {
			tx.Rollback() //nolint:errcheck
			return out, err
		}
		if err := writeFindingEdit(tx, c.id, "status", "open", "acknowledged", reason, EditOrigin{Reason: reason}); err != nil {
			tx.Rollback() //nolint:errcheck
			return out, err
		}
		newTags := appendTag(c.tags, "auto-stale")
		if newTags != c.tags {
			if _, err := tx.Exec(`UPDATE findings SET tags = ? WHERE id = ?`, newTags, c.id); err != nil {
				tx.Rollback() //nolint:errcheck
				return out, err
			}
			if err := writeFindingEdit(tx, c.id, "tag_add", "", "auto-stale", reason, EditOrigin{Reason: reason}); err != nil {
				tx.Rollback() //nolint:errcheck
				return out, err
			}
		}
		if err := tx.Commit(); err != nil {
			return out, err
		}
		out.Closed++
	}
	return out, nil
}

// humanDur renders a duration as the operator would say it — "24h",
// "3d", "45m" — for triage notes.
func humanDur(d time.Duration) string {
	if d >= 24*time.Hour {
		days := int(d / (24 * time.Hour))
		return fmt.Sprintf("%dd", days)
	}
	if d >= time.Hour {
		hours := int(d / time.Hour)
		return fmt.Sprintf("%dh", hours)
	}
	if d >= time.Minute {
		mins := int(d / time.Minute)
		return fmt.Sprintf("%dm", mins)
	}
	return strings.TrimSuffix(d.String(), "0s")
}
