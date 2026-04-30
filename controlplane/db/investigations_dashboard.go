// Dashboard rollups for investigations.
//
// Three signals the operator landing page surfaces:
//   • Active count — "do I have unresolved cases right now?"
//   • Closed-in-last-24h — "is the team shipping?"
//   • Autolinked-in-last-24h — "is the engine doing useful work?"
//     (counts investigation_findings rows where link_method='autolink'
//     created in the last 24h — a proxy for engine-driven case
//     enrichment so operators see autolink contributing without
//     opening every case.)
//
// Plus a small list of top-5 most-recently-updated active cases so
// the dashboard's "Recent investigations" card can deep-link without
// a follow-up call. Each row carries (id, title, finding_count) —
// enough for a useful glance.

package db

import (
	"database/sql"
)

// DashboardInvestigations is the rollup the dashboard endpoint
// embeds. Counts are scoped to the local CP.
type DashboardInvestigations struct {
	Active           int                          `json:"active"`
	Closed24h        int                          `json:"closed_24h"`
	AutolinkedFindings24h int                     `json:"autolinked_findings_24h"`
	RecentActive     []DashboardInvestigationItem `json:"recent_active"`
}

// DashboardInvestigationItem is one row of the Recent Investigations
// card. Title is taken as-is; finding_count is the number of linked
// findings (handy "size" indicator next to each row).
type DashboardInvestigationItem struct {
	ID            int64  `json:"id"`
	Title         string `json:"title"`
	FindingCount  int    `json:"finding_count"`
	UpdatedAt     string `json:"updated_at"`
}

// DashboardInvestigationStats reads all four pieces in one round
// of cheap queries — none of these touch large tables, so we don't
// bother with a CTE union.
//
// We deliberately don't proxy this through the federation layer in
// v1; the dashboard endpoint already aggregates only LOCAL fields
// (per the existing comment block on dashboardResponse). A federated
// rollup is a future follow-up.
func (s *Store) DashboardInvestigationStats(limit int) (*DashboardInvestigations, error) {
	if limit <= 0 || limit > 50 {
		limit = 5
	}
	out := &DashboardInvestigations{RecentActive: []DashboardInvestigationItem{}}

	// Active count.
	if err := s.QueryRow(`
		SELECT COUNT(*) FROM investigations WHERE status = 'active'`).Scan(&out.Active); err != nil {
		return nil, err
	}

	// Closed in last 24h.
	if err := s.QueryRow(`
		SELECT COUNT(*) FROM investigations
		 WHERE status = 'closed'
		   AND closed_at IS NOT NULL
		   AND closed_at > datetime('now', '-24 hours')
	`).Scan(&out.Closed24h); err != nil {
		return nil, err
	}

	// Autolinked findings in last 24h (engine-driven case enrichment).
	// link_method was added in PR #61; legacy NULL rows pre-date it
	// and don't count.
	if err := s.QueryRow(`
		SELECT COUNT(*) FROM investigation_findings
		 WHERE link_method = 'autolink'
		   AND linked_at > datetime('now', '-24 hours')
	`).Scan(&out.AutolinkedFindings24h); err != nil {
		return nil, err
	}

	// Recent active cases.
	rows, err := s.Query(`
		SELECT i.id, i.title,
		       (SELECT COUNT(*) FROM investigation_findings WHERE investigation_id = i.id) AS finding_count,
		       CAST(i.updated_at AS TEXT) AS updated_at
		  FROM investigations i
		 WHERE i.status = 'active'
		 ORDER BY i.updated_at DESC
		 LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var it DashboardInvestigationItem
		var updated sql.NullString
		if err := rows.Scan(&it.ID, &it.Title, &it.FindingCount, &updated); err != nil {
			return nil, err
		}
		if updated.Valid {
			it.UpdatedAt = updated.String
		}
		out.RecentActive = append(out.RecentActive, it)
	}
	return out, rows.Err()
}
