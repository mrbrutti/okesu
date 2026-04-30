// Enriched read methods for the investigation workspace. The handler
// uses these to return *objects* instead of just IDs so the UI can
// render Findings / Runs / IOCs / Daimons / Orchestrations tabs
// without N+1 fetches.
//
// Each method narrows its result to the columns the workspace needs —
// keeps the JSON payload small and avoids leaking attributes the
// case view doesn't display (raw event JSON, full prompts, etc.).

package db

import (
	"database/sql"
	"strings"
)

// InvestigationFindingItem is a denormalised projection of `findings`
// for the workspace's Findings tab. Subset of the full Finding shape;
// excludes raw_json + ack_note + investigation-specific bookkeeping.
type InvestigationFindingItem struct {
	ID         int64
	Ts         int64
	Agent      sql.NullString
	Host       sql.NullString
	Severity   sql.NullString
	Title      sql.NullString
	Status     sql.NullString
	Tags       sql.NullString
	Subtype    sql.NullString
	LinkedAt   string // ISO RFC3339, from investigation_findings.linked_at
}

// InvestigationRunItem is the orchestration_run summary the workspace
// shows. (investigation_runs links to orchestration_runs, not the
// ad-hoc runs table — orchestration_runs is the m2m target.)
type InvestigationRunItem struct {
	ID                int64
	OrchestrationID   int64
	OrchestrationName sql.NullString
	Status            string
	TriggerKind       string
	StartedAt         string
	EndedAt           sql.NullString
	CurrentStepID     sql.NullString
	Error             sql.NullString
	LinkedAt          string
}

// InvestigationIOCItem aggregates an IOC observed via any of the
// case's linked findings. The aggregation deliberately collapses
// per-host observations: the workspace shows "this IOC was seen on
// 4 hosts in this case", and the host list is one click away on the
// IOC catalog detail.
type InvestigationIOCItem struct {
	ID               int64
	Kind             string
	Value            string
	Severity         sql.NullString
	ObservationCount int    // observations within the linked findings
	HostCount        int    // distinct hosts touched within the case
	FirstSeen        string // ISO; earliest observed_at in scope
	LastSeen         string // ISO; latest observed_at in scope
}

// InvestigationDaimonItem groups linked findings by emitting agent.
// The workspace's Daimons tab shows "which monitoring agents fired
// the findings in this case" with a count + last-seen so the
// operator can pivot to the daimon detail.
type InvestigationDaimonItem struct {
	Agent        string
	FindingCount int
	LastSeenTs   int64
}

// InvestigationOrchestrationItem groups linked runs by orchestration.
// Each row has the orchestration name + how many of its runs are on
// this case + the most recent run's started_at.
type InvestigationOrchestrationItem struct {
	OrchestrationID   sql.NullInt64
	OrchestrationName string
	RunCount          int
	LastStartedAt     string
}

// ListFindingsForInvestigationEnriched joins investigation_findings
// to findings and returns the narrow projection above.
func (s *Store) ListFindingsForInvestigationEnriched(invID int64) ([]InvestigationFindingItem, error) {
	rows, err := s.Query(`
		SELECT f.id, f.ts, f.agent, f.host, f.severity, f.title,
		       f.status, f.tags, f.subtype, l.linked_at
		FROM investigation_findings l
		JOIN findings f ON f.id = l.finding_id
		WHERE l.investigation_id = ?
		ORDER BY l.linked_at DESC`, invID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []InvestigationFindingItem{}
	for rows.Next() {
		var it InvestigationFindingItem
		var linkedAt sql.NullTime
		if err := rows.Scan(&it.ID, &it.Ts, &it.Agent, &it.Host,
			&it.Severity, &it.Title, &it.Status, &it.Tags,
			&it.Subtype, &linkedAt); err != nil {
			return nil, err
		}
		if linkedAt.Valid {
			it.LinkedAt = linkedAt.Time.UTC().Format(rfc3339)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ListRunsForInvestigationEnriched joins investigation_runs to
// orchestration_runs (NOT the ad-hoc runs table) and returns the
// narrow projection. Includes a denorm of the orchestration name
// so the workspace can render labels without an extra round trip.
func (s *Store) ListRunsForInvestigationEnriched(invID int64) ([]InvestigationRunItem, error) {
	rows, err := s.Query(`
		SELECT r.id, r.orchestration_id, o.name, r.status,
		       r.trigger_kind, r.started_at, r.ended_at,
		       r.current_step_id, r.error, l.linked_at
		FROM investigation_runs l
		JOIN orchestration_runs r ON r.id = l.orchestration_run_id
		LEFT JOIN orchestrations o ON o.id = r.orchestration_id
		WHERE l.investigation_id = ?
		ORDER BY l.linked_at DESC`, invID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []InvestigationRunItem{}
	for rows.Next() {
		var it InvestigationRunItem
		var startedAt, endedAt, linkedAt sql.NullTime
		if err := rows.Scan(&it.ID, &it.OrchestrationID, &it.OrchestrationName,
			&it.Status, &it.TriggerKind,
			&startedAt, &endedAt,
			&it.CurrentStepID, &it.Error,
			&linkedAt); err != nil {
			return nil, err
		}
		if startedAt.Valid {
			it.StartedAt = startedAt.Time.UTC().Format(rfc3339)
		}
		if endedAt.Valid {
			it.EndedAt = sql.NullString{
				String: endedAt.Time.UTC().Format(rfc3339),
				Valid:  true,
			}
		}
		if linkedAt.Valid {
			it.LinkedAt = linkedAt.Time.UTC().Format(rfc3339)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ListIOCsForInvestigation derives the case's IOCs from the
// observations of its linked findings. ioc_observations.finding_id
// is the only edge into IOC scope today; the orchestration_run_id
// column doesn't exist on the observations table, so we don't try
// to derive through linked runs separately. (Linked runs that
// matched IOCs already produced findings; those are linked to the
// case via investigation_findings if they're in scope.)
func (s *Store) ListIOCsForInvestigation(invID int64) ([]InvestigationIOCItem, error) {
	rows, err := s.Query(`
		SELECT i.id, i.kind, i.normalized_value AS value, i.severity_floor,
		       COUNT(obs.id) AS observation_count,
		       COUNT(DISTINCT COALESCE(obs.host, '')) AS host_count,
		       MIN(obs.observed_at) AS first_seen,
		       MAX(obs.observed_at) AS last_seen
		FROM iocs i
		JOIN ioc_observations obs ON obs.ioc_id = i.id
		WHERE obs.finding_id IN (
			SELECT finding_id FROM investigation_findings WHERE investigation_id = ?
		)
		GROUP BY i.id, i.kind, i.normalized_value, i.severity_floor
		ORDER BY last_seen DESC`, invID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []InvestigationIOCItem{}
	for rows.Next() {
		var it InvestigationIOCItem
		var firstSeen, lastSeen sql.NullTime
		if err := rows.Scan(&it.ID, &it.Kind, &it.Value, &it.Severity,
			&it.ObservationCount, &it.HostCount,
			&firstSeen, &lastSeen); err != nil {
			return nil, err
		}
		if firstSeen.Valid {
			it.FirstSeen = firstSeen.Time.UTC().Format(rfc3339)
		}
		if lastSeen.Valid {
			it.LastSeen = lastSeen.Time.UTC().Format(rfc3339)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ListDaimonsForInvestigation groups linked findings by emitting
// agent (daimon name) so the workspace can show "the case touches
// these daimons" with deep-links.
func (s *Store) ListDaimonsForInvestigation(invID int64) ([]InvestigationDaimonItem, error) {
	rows, err := s.Query(`
		SELECT COALESCE(f.agent, '') AS agent,
		       COUNT(*) AS finding_count,
		       MAX(f.ts) AS last_seen_ts
		FROM investigation_findings l
		JOIN findings f ON f.id = l.finding_id
		WHERE l.investigation_id = ? AND f.agent IS NOT NULL AND f.agent != ''
		GROUP BY f.agent
		ORDER BY last_seen_ts DESC`, invID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []InvestigationDaimonItem{}
	for rows.Next() {
		var it InvestigationDaimonItem
		if err := rows.Scan(&it.Agent, &it.FindingCount, &it.LastSeenTs); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ListOrchestrationsForInvestigation groups linked orchestration_runs
// by their owning orchestration. Returned newest-first by most-recent
// run start.
func (s *Store) ListOrchestrationsForInvestigation(invID int64) ([]InvestigationOrchestrationItem, error) {
	rows, err := s.Query(`
		SELECT r.orchestration_id,
		       COALESCE(o.name, '(deleted)') AS orch_name,
		       COUNT(r.id) AS run_count,
		       MAX(r.started_at) AS last_started_at
		FROM investigation_runs l
		JOIN orchestration_runs r ON r.id = l.orchestration_run_id
		LEFT JOIN orchestrations o ON o.id = r.orchestration_id
		WHERE l.investigation_id = ?
		GROUP BY r.orchestration_id, COALESCE(o.name, '(deleted)')
		ORDER BY last_started_at DESC`, invID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []InvestigationOrchestrationItem{}
	for rows.Next() {
		var it InvestigationOrchestrationItem
		var lastStarted sql.NullTime
		if err := rows.Scan(&it.OrchestrationID, &it.OrchestrationName,
			&it.RunCount, &lastStarted); err != nil {
			return nil, err
		}
		if lastStarted.Valid {
			it.LastStartedAt = lastStarted.Time.UTC().Format(rfc3339)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// IsInvestigationWarRoom returns true iff any linked finding carries
// the `war-bridge` tag. The workspace uses this to flip the detail
// page into "war room" mode (red banner, faster auto-refresh).
//
// Tags are stored as a comma-separated string on findings.tags;
// we check via LIKE rather than parsing here — the tag is a fixed
// literal, so a substring match keyed by `,war-bridge,` (or boundary
// equivalents) is sufficient.
func (s *Store) IsInvestigationWarRoom(invID int64) (bool, error) {
	row := s.QueryRow(`
		SELECT COUNT(*) > 0
		FROM investigation_findings l
		JOIN findings f ON f.id = l.finding_id
		WHERE l.investigation_id = ?
		  AND f.tags IS NOT NULL
		  AND (',' || f.tags || ',') LIKE ?`,
		invID, "%,war-bridge,%")
	var b bool
	if err := row.Scan(&b); err != nil {
		return false, err
	}
	return b, nil
}

// UnlinkFindingFromInvestigation deletes the join row. Idempotent —
// the row may not exist (already unlinked). Returns nil in both
// cases; callers don't need to discriminate.
func (s *Store) UnlinkFindingFromInvestigation(invID, findingID int64) error {
	_, err := s.Exec(`
		DELETE FROM investigation_findings
		WHERE investigation_id = ? AND finding_id = ?`,
		invID, findingID)
	return err
}

// UnlinkRunFromInvestigation deletes the join row. Idempotent.
// runID is the orchestration_runs.id (int64); investigation_runs is
// the m2m table for orchestration runs, not ad-hoc runs.
func (s *Store) UnlinkRunFromInvestigation(invID, runID int64) error {
	_, err := s.Exec(`
		DELETE FROM investigation_runs
		WHERE investigation_id = ? AND orchestration_run_id = ?`,
		invID, runID)
	return err
}

// rfc3339 is the ISO-8601-compatible timestamp format the api package
// uses everywhere; defined here as a const so the methods don't have
// to import time just for the layout string.
const rfc3339 = "2006-01-02T15:04:05Z07:00"

// SQL ID helper — extract column lists at one place. Not currently
// used; kept as a guard against future drift.
var _ = strings.TrimSpace
