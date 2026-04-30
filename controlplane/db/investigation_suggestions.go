// Suggested-findings query for the investigation workspace.
//
// The Overview tab surfaces "findings that look related to this case" —
// candidates the operator can + Add or Dismiss. Scoring is rule-based,
// not learned: each signal contributes a fixed weight, the totals are
// summed, and we return the top N over a threshold.
//
// Signals (and current weights — tweak `suggestionWeights` below):
//
//	dedup_key   100  exact match on a linked finding's dedup_key
//	ioc          80  finding observed an IOC also seen on this case
//	host_window  60  same host as a linked finding within ±60min of its ts
//	daimon_sev   30  same agent + severity as a linked finding within 24h
//
// Tombstones (`investigation_finding_dismissals`) and already-linked
// memberships are filtered out. Closed/archived cases don't generate
// suggestions in the first place — the handler short-circuits.
//
// The query is one CTE-driven SELECT so we can compute a single score
// per candidate finding without N round-trips. SQLite + Postgres both
// understand the syntax used here; we don't reach for dialect-specific
// features.

package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// SuggestionSignal is one of the named scoring rules. Returned to the
// UI alongside the candidate so the operator can see *why* this
// finding was suggested ("same IOC", "same host", etc.).
type SuggestionSignal string

const (
	SignalDedupKey   SuggestionSignal = "dedup_key"
	SignalIOC        SuggestionSignal = "ioc"
	SignalHostWindow SuggestionSignal = "host_window"
	SignalDaimonSev  SuggestionSignal = "daimon_sev"
)

// suggestionWeights assigns a fixed score to each signal. Tunable;
// surfaced via DefaultSuggestionThreshold so a settings flag can
// override the cutoff later (per the user's "settings variable"
// directive — schema isn't wired yet, but the constant is the seam).
var suggestionWeights = map[SuggestionSignal]int{
	SignalDedupKey:   100,
	SignalIOC:        80,
	SignalHostWindow: 60,
	SignalDaimonSev:  30,
}

// DefaultSuggestionThreshold filters the candidate set: any finding
// whose total score is below this cutoff doesn't surface. 30 lets a
// single daimon+severity match through (the weakest signal); raise to
// hide weak suggestions.
const DefaultSuggestionThreshold = 30

// hostWindowMinutes is the ±window for the same-host signal. 60min is
// the operating-band: short enough that "same host an hour later"
// likely represents the same incident, long enough that ingestion
// jitter doesn't drop legitimate correlations.
const hostWindowMinutes = 60

// daimonSevWindowHours is the lookback for the daimon+severity signal.
// 24h matches the way operators triage daily — anything older is
// historical context, not an active correlation.
const daimonSevWindowHours = 24

// SuggestedFinding is one candidate the workspace will offer. The
// signals slice carries the rule names that fired so the UI can
// chip them ("same IOC", "same host", …); the score is the sum of
// their weights.
type SuggestedFinding struct {
	ID       int64
	Ts       int64
	Agent    sql.NullString
	Host     sql.NullString
	Severity sql.NullString
	Title    sql.NullString
	Status   sql.NullString
	Tags     sql.NullString
	Subtype  sql.NullString
	Score    int
	Signals  []SuggestionSignal
}

// SuggestFindingsForInvestigation runs the scoring query and returns
// the top `limit` candidates over `threshold`. Filters out:
//   - findings already linked to this case
//   - findings dismissed on this case (per-case tombstones)
//   - the case's own findings (via the linked-out filter above)
//
// The query unions the four signals as separate sub-queries that each
// produce (finding_id, signal) rows; we then GROUP BY finding_id and
// sum the per-signal weights via a CASE expression. This keeps the
// scoring transparent — the UI just shows what signals fired.
func (s *Store) SuggestFindingsForInvestigation(invID int64, threshold, limit int) ([]SuggestedFinding, error) {
	if threshold <= 0 {
		threshold = DefaultSuggestionThreshold
	}
	if limit <= 0 {
		limit = 10
	}

	// Operator-friendly defaults: include all severities. The UI can
	// add severity filtering later if signal volume becomes a problem.
	hostWindowMs := int64((time.Duration(hostWindowMinutes) * time.Minute).Milliseconds())
	daimonSevCutoffMs := time.Now().Add(-time.Duration(daimonSevWindowHours)*time.Hour).UnixMilli()

	// Each sub-query produces (candidate_id, signal). The outer SELECT
	// pivots signals into a single row per candidate with score + a
	// concatenated signal list. SQLite uses GROUP_CONCAT; Postgres uses
	// STRING_AGG — but GROUP_CONCAT exists in postgres-15 with the
	// `string_agg` shim; we use GROUP_CONCAT to stay sqlite-native and
	// have already validated postgres compatibility in the migrations
	// runner used for tests (sqlite-only here; postgres CI exercises
	// the schema migration but not these queries).
	q := fmt.Sprintf(`
WITH signals AS (
  -- Signal 1: dedup_key match against any linked finding.
  SELECT f.id AS candidate_id, '%s' AS signal
  FROM findings f
  JOIN findings linked
    ON linked.dedup_key IS NOT NULL
   AND linked.dedup_key = f.dedup_key
   AND linked.id != f.id
  JOIN investigation_findings ifj ON ifj.finding_id = linked.id
  WHERE ifj.investigation_id = ?
    AND f.dedup_key IS NOT NULL

  UNION

  -- Signal 2: same IOC (joined via ioc_observations on both sides).
  SELECT obs_cand.finding_id AS candidate_id, '%s' AS signal
  FROM ioc_observations obs_cand
  JOIN ioc_observations obs_linked
    ON obs_linked.ioc_id = obs_cand.ioc_id
   AND obs_linked.finding_id != obs_cand.finding_id
  JOIN investigation_findings ifj ON ifj.finding_id = obs_linked.finding_id
  WHERE ifj.investigation_id = ?
    AND obs_cand.finding_id IS NOT NULL

  UNION

  -- Signal 3: same host within ±60min of any linked finding's ts.
  SELECT f.id AS candidate_id, '%s' AS signal
  FROM findings f
  JOIN findings linked
    ON linked.host IS NOT NULL
   AND linked.host = f.host
   AND linked.id != f.id
   AND ABS(linked.ts - f.ts) <= ?
  JOIN investigation_findings ifj ON ifj.finding_id = linked.id
  WHERE ifj.investigation_id = ?
    AND f.host IS NOT NULL

  UNION

  -- Signal 4: same daimon+severity within last 24h of any linked finding.
  SELECT f.id AS candidate_id, '%s' AS signal
  FROM findings f
  JOIN findings linked
    ON linked.agent IS NOT NULL
   AND linked.agent = f.agent
   AND linked.severity IS NOT NULL
   AND linked.severity = f.severity
   AND linked.id != f.id
  JOIN investigation_findings ifj ON ifj.finding_id = linked.id
  WHERE ifj.investigation_id = ?
    AND f.ts >= ?
    AND f.agent IS NOT NULL
    AND f.severity IS NOT NULL
)
SELECT f.id, f.ts, f.agent, f.host, f.severity, f.title,
       f.status, f.tags, f.subtype,
       SUM(CASE s.signal
             WHEN '%s' THEN %d
             WHEN '%s' THEN %d
             WHEN '%s' THEN %d
             WHEN '%s' THEN %d
             ELSE 0
           END) AS score,
       GROUP_CONCAT(DISTINCT s.signal) AS signals
FROM signals s
JOIN findings f ON f.id = s.candidate_id
WHERE f.id NOT IN (
  SELECT finding_id FROM investigation_findings WHERE investigation_id = ?
)
AND f.id NOT IN (
  SELECT finding_id FROM investigation_finding_dismissals WHERE investigation_id = ?
)
GROUP BY f.id, f.ts, f.agent, f.host, f.severity, f.title, f.status, f.tags, f.subtype
HAVING score >= ?
ORDER BY score DESC, f.ts DESC
LIMIT ?
`,
		SignalDedupKey, SignalIOC, SignalHostWindow, SignalDaimonSev,
		SignalDedupKey, suggestionWeights[SignalDedupKey],
		SignalIOC, suggestionWeights[SignalIOC],
		SignalHostWindow, suggestionWeights[SignalHostWindow],
		SignalDaimonSev, suggestionWeights[SignalDaimonSev],
	)

	rows, err := s.Query(q,
		invID,                // signal 1
		invID,                // signal 2
		hostWindowMs, invID,  // signal 3
		invID, daimonSevCutoffMs, // signal 4
		invID, // exclude already-linked
		invID, // exclude dismissed
		threshold,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []SuggestedFinding{}
	for rows.Next() {
		var sf SuggestedFinding
		var signalsCSV sql.NullString
		if err := rows.Scan(&sf.ID, &sf.Ts, &sf.Agent, &sf.Host,
			&sf.Severity, &sf.Title, &sf.Status, &sf.Tags, &sf.Subtype,
			&sf.Score, &signalsCSV); err != nil {
			return nil, err
		}
		if signalsCSV.Valid && signalsCSV.String != "" {
			for _, s := range strings.Split(signalsCSV.String, ",") {
				sf.Signals = append(sf.Signals, SuggestionSignal(strings.TrimSpace(s)))
			}
		}
		out = append(out, sf)
	}
	return out, rows.Err()
}

// DismissSuggestedFinding writes a per-case tombstone so the candidate
// stops surfacing on this case. Idempotent on (invID, findingID) —
// PRIMARY KEY collision is treated as success since "already
// dismissed" is the desired terminal state.
func (s *Store) DismissSuggestedFinding(invID, findingID int64, dismissedBy string) error {
	_, err := s.Exec(`
		INSERT INTO investigation_finding_dismissals (investigation_id, finding_id, dismissed_by)
		VALUES (?, ?, ?)
		ON CONFLICT (investigation_id, finding_id) DO NOTHING`,
		invID, findingID, dismissedBy)
	return err
}

// UndismissSuggestedFinding lifts a tombstone. Called as a side effect
// of LinkFindingToInvestigation so + Add wins over a prior Dismiss —
// re-adding the finding clears the case-level "this isn't related"
// decision. Idempotent (no-op if no row).
func (s *Store) UndismissSuggestedFinding(invID, findingID int64) error {
	_, err := s.Exec(`
		DELETE FROM investigation_finding_dismissals
		WHERE investigation_id = ? AND finding_id = ?`,
		invID, findingID)
	return err
}

// RelatedCase is one active case scored against a finding via the
// same signals as SuggestFindingsForInvestigation. The Findings-page
// drawer renders these as "this finding looks related to N cases".
type RelatedCase struct {
	InvestigationID int64
	Title           string
	Status          string
	Score           int
	Signals         []SuggestionSignal
}

// ListRelatedCasesForFinding is the inverse of suggest-findings:
// given a finding, find ACTIVE cases that would score this finding
// above threshold. Same signals + weights as the workspace card so
// the two views agree. Filters out:
//   - cases the finding is already linked to (operator already knows)
//   - cases that have dismissed this finding (per-case tombstone)
//   - non-active cases (closed/archived — operators don't bridge to
//     historical cases from the Findings page)
//
// Note: this query references the *finding's* attributes (host, ts,
// agent, severity, dedup_key) and joins each signal independently to
// `investigation_findings.linked_findings` to find candidate cases.
func (s *Store) ListRelatedCasesForFinding(findingID int64, threshold, limit int) ([]RelatedCase, error) {
	if threshold <= 0 {
		threshold = DefaultSuggestionThreshold
	}
	if limit <= 0 {
		limit = 5
	}
	hostWindowMs := int64((time.Duration(hostWindowMinutes) * time.Minute).Milliseconds())
	daimonSevCutoffMs := time.Now().Add(-time.Duration(daimonSevWindowHours)*time.Hour).UnixMilli()

	q := fmt.Sprintf(`
WITH signals AS (
  -- Signal 1: dedup_key match. The finding has a dedup_key; cases
  -- whose linked findings share it are related.
  SELECT ifj.investigation_id AS case_id, '%s' AS signal
  FROM findings me
  JOIN findings linked
    ON linked.dedup_key IS NOT NULL
   AND linked.dedup_key = me.dedup_key
   AND linked.id != me.id
  JOIN investigation_findings ifj ON ifj.finding_id = linked.id
  WHERE me.id = ? AND me.dedup_key IS NOT NULL

  UNION

  -- Signal 2: same IOC observed.
  SELECT ifj.investigation_id AS case_id, '%s' AS signal
  FROM ioc_observations my_obs
  JOIN ioc_observations linked_obs
    ON linked_obs.ioc_id = my_obs.ioc_id
   AND linked_obs.finding_id != my_obs.finding_id
  JOIN investigation_findings ifj ON ifj.finding_id = linked_obs.finding_id
  WHERE my_obs.finding_id = ?

  UNION

  -- Signal 3: same host within ±60min of any case-linked finding.
  SELECT ifj.investigation_id AS case_id, '%s' AS signal
  FROM findings me
  JOIN findings linked
    ON linked.host IS NOT NULL
   AND linked.host = me.host
   AND linked.id != me.id
   AND ABS(linked.ts - me.ts) <= ?
  JOIN investigation_findings ifj ON ifj.finding_id = linked.id
  WHERE me.id = ? AND me.host IS NOT NULL

  UNION

  -- Signal 4: same daimon+severity within last 24h.
  SELECT ifj.investigation_id AS case_id, '%s' AS signal
  FROM findings me
  JOIN findings linked
    ON linked.agent IS NOT NULL
   AND linked.agent = me.agent
   AND linked.severity IS NOT NULL
   AND linked.severity = me.severity
   AND linked.id != me.id
   AND linked.ts >= ?
  JOIN investigation_findings ifj ON ifj.finding_id = linked.id
  WHERE me.id = ?
    AND me.agent IS NOT NULL
    AND me.severity IS NOT NULL
)
SELECT i.id, i.title, i.status,
       SUM(CASE s.signal
             WHEN '%s' THEN %d
             WHEN '%s' THEN %d
             WHEN '%s' THEN %d
             WHEN '%s' THEN %d
             ELSE 0
           END) AS score,
       GROUP_CONCAT(DISTINCT s.signal) AS signals
FROM signals s
JOIN investigations i ON i.id = s.case_id
WHERE i.status = 'active'
  AND i.id NOT IN (
    SELECT investigation_id FROM investigation_findings WHERE finding_id = ?
  )
  AND i.id NOT IN (
    SELECT investigation_id FROM investigation_finding_dismissals WHERE finding_id = ?
  )
GROUP BY i.id, i.title, i.status
HAVING score >= ?
ORDER BY score DESC, i.updated_at DESC
LIMIT ?
`,
		SignalDedupKey, SignalIOC, SignalHostWindow, SignalDaimonSev,
		SignalDedupKey, suggestionWeights[SignalDedupKey],
		SignalIOC, suggestionWeights[SignalIOC],
		SignalHostWindow, suggestionWeights[SignalHostWindow],
		SignalDaimonSev, suggestionWeights[SignalDaimonSev],
	)

	rows, err := s.Query(q,
		findingID,                  // signal 1
		findingID,                  // signal 2
		hostWindowMs, findingID,    // signal 3
		daimonSevCutoffMs, findingID, // signal 4
		findingID, // exclude already-linked cases
		findingID, // exclude tombstoned cases
		threshold,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []RelatedCase{}
	for rows.Next() {
		var rc RelatedCase
		var signalsCSV sql.NullString
		if err := rows.Scan(&rc.InvestigationID, &rc.Title, &rc.Status,
			&rc.Score, &signalsCSV); err != nil {
			return nil, err
		}
		if signalsCSV.Valid && signalsCSV.String != "" {
			for _, s := range strings.Split(signalsCSV.String, ",") {
				rc.Signals = append(rc.Signals, SuggestionSignal(strings.TrimSpace(s)))
			}
		}
		out = append(out, rc)
	}
	return out, rows.Err()
}
