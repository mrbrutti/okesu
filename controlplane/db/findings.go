package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Finding is a structured projection of a finding-type event.
type Finding struct {
	ID              int64
	EventID         int64
	Ts              int64
	Agent           sql.NullString
	Host            sql.NullString
	Severity        sql.NullString
	Title           sql.NullString
	Resource        sql.NullString
	Evidence        sql.NullString
	DedupKey        sql.NullString
	RawJSON         string
	Acknowledged    bool
	AckedAt         sql.NullTime
	AckedBy         sql.NullInt64
	AckNote         sql.NullString
	CreatedAt       time.Time

	// Phase 12: structured indexed fields. All optional; agents fill what
	// they extract, the rest stay NULL.
	Category        sql.NullString // process|file|network|cert|cloud|identity|config|other
	ProcessPID      sql.NullInt64
	ProcessName     sql.NullString
	Path            sql.NullString
	NetworkEndpoint sql.NullString
	CVE             sql.NullString
	Tags            sql.NullString // comma-separated, LIKE-searchable
	Attributes      sql.NullString // JSON catch-all

	// Phase 13: triage state. Drives the dashboard pill AND the daemon's
	// known-issues feedback loop (false_positive → silent suppression).
	Status         sql.NullString // open|acknowledged|investigating|resolved|false_positive|wontfix
	TriageNote     sql.NullString
	TriagedAt      sql.NullTime
	TriagedByUser  sql.NullInt64
	TriagedByEmail sql.NullString

	// Phase 14: operator severity override. The agent's original assignment
	// stays in `Severity`; an operator (or matching fingerprint rule) may
	// set `OperatorSeverity` to override it. Effective severity =
	// OperatorSeverity if Valid, else Severity. The wire JSON computes
	// this so callers see one canonical value.
	OperatorSeverity     sql.NullString
	SeverityOverrideAt   sql.NullTime
	SeverityOverrideByID sql.NullInt64
}

// EffectiveSeverity returns the operator override if present, otherwise
// the agent's original severity.
func (f *Finding) EffectiveSeverity() string {
	if f.OperatorSeverity.Valid && f.OperatorSeverity.String != "" {
		return f.OperatorSeverity.String
	}
	return f.Severity.String
}

// IsValidSeverity rejects values outside the canonical CRITICAL/HIGH/MEDIUM/LOW/INFO
// vocabulary. Comparison is case-insensitive; the canonical form is upper-case.
func IsValidSeverity(s string) bool {
	switch strings.ToUpper(s) {
	case "CRITICAL", "HIGH", "MEDIUM", "LOW", "INFO":
		return true
	}
	return false
}

// CanonicalSeverity normalizes any acceptable severity string to upper-case.
func CanonicalSeverity(s string) string {
	return strings.ToUpper(strings.TrimSpace(s))
}

// FingerprintForFinding returns the same fingerprint string used by
// lookup_findings, known_issues, and finding_severity_rules. dedup_key
// wins when present; otherwise the title|severity|agent composite is
// used (matching the SQL COALESCE in those queries).
func FingerprintForFinding(dedupKey, title, severity, agent string) string {
	if dedupKey != "" {
		return dedupKey
	}
	return title + "|" + severity + "|" + agent
}

// Finding statuses. The daemon's known-issues fetcher uses these to decide
// whether to suppress a fingerprint locally before re-emitting.
const (
	FindingOpen           = "open"
	FindingAcknowledged   = "acknowledged"
	FindingInvestigating  = "investigating"
	FindingResolved       = "resolved"
	FindingFalsePositive  = "false_positive"
	FindingWontFix        = "wontfix"
)

// IsValidFindingStatus reports whether s is one of the allowed statuses.
func IsValidFindingStatus(s string) bool {
	switch s {
	case FindingOpen, FindingAcknowledged, FindingInvestigating,
		FindingResolved, FindingFalsePositive, FindingWontFix:
		return true
	}
	return false
}

// FindingFilter captures query-time filtering options for the list endpoint.
// Empty/zero fields mean "no filter on that dimension".
type FindingFilter struct {
	Severities []string // exact match, case-insensitive
	Agent      string   // exact match
	Host       string   // exact match
	Category   string   // exact match (process|file|network|cert|cloud|identity|config|other)
	Tag        string   // substring match within the comma-separated tags column
	SinceMs    int64    // ts >=
	UntilMs    int64    // ts <
	OnlyOpen   bool     // acknowledged = 0
	OnlyAcked  bool     // acknowledged = 1
	Limit      int      // 1..1000, default 100
	Offset     int
}

// FindingInsert is what the webhook handler hands to the store on a new
// finding event. Mirrors the wire shape but with explicit fields for clarity.
type FindingInsert struct {
	EventID  int64
	Ts       int64
	Agent    string
	Host     string
	Severity string
	Title    string
	Resource string
	Evidence string
	DedupKey string
	RawJSON  string

	// Phase 12 enrichment — optional structured fields.
	Category        string
	ProcessPID      int64
	ProcessName     string
	Path            string
	NetworkEndpoint string
	CVE             string
	Tags            string
	Attributes      string // expected to be valid JSON; not validated here
}

// InsertFinding stores a finding row tied to an event. If a per-fingerprint
// severity rule matches, operator_severity is set automatically (with
// severity_override_by = NULL to mark "rule-applied, not human"); the
// agent's original severity always lands in the `severity` column.
func (s *Store) InsertFinding(f *FindingInsert) (int64, error) {
	pid := sql.NullInt64{}
	if f.ProcessPID > 0 {
		pid = sql.NullInt64{Int64: f.ProcessPID, Valid: true}
	}

	// Look up a matching rule. The fingerprint computation is intentionally
	// identical to lookup_findings / known_issues so all three subsystems
	// agree on what counts as "the same kind of finding".
	fp := FingerprintForFinding(f.DedupKey, f.Title, f.Severity, f.Agent)
	var operatorSeverity sql.NullString
	if rule, err := s.GetSeverityRule(fp); err == nil && rule != nil {
		operatorSeverity = sql.NullString{String: rule.Severity, Valid: true}
	}

	overrideAtCol := "NULL"
	if operatorSeverity.Valid {
		overrideAtCol = "CURRENT_TIMESTAMP"
	}
	res, err := s.Exec(`
		INSERT INTO findings (
			event_id, ts, agent, host, severity, title,
			resource, evidence, dedup_key, raw_json,
			category, process_pid, process_name, path, network_endpoint, cve, tags, attributes,
			status, operator_severity, severity_override_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'open', ?, `+overrideAtCol+`)
	`,
		f.EventID, f.Ts,
		nullable(f.Agent), nullable(f.Host), nullable(f.Severity), nullable(f.Title),
		nullable(f.Resource), nullable(f.Evidence), nullable(f.DedupKey),
		f.RawJSON,
		nullable(f.Category), pid, nullable(f.ProcessName), nullable(f.Path),
		nullable(f.NetworkEndpoint), nullable(f.CVE), nullable(f.Tags), nullable(f.Attributes),
		operatorSeverity,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListFindings returns findings matching the filter.
func (s *Store) ListFindings(f FindingFilter) ([]*Finding, error) {
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 100
	}

	var (
		clauses []string
		args    []any
	)
	if len(f.Severities) > 0 {
		ph := make([]string, len(f.Severities))
		for i, s := range f.Severities {
			ph[i] = "?"
			args = append(args, strings.ToUpper(s))
		}
		clauses = append(clauses, "UPPER(COALESCE(NULLIF(operator_severity,''), severity)) IN ("+strings.Join(ph, ",")+")")
	}
	if f.Agent != "" {
		clauses = append(clauses, "agent = ?")
		args = append(args, f.Agent)
	}
	if f.Host != "" {
		clauses = append(clauses, "host = ?")
		args = append(args, f.Host)
	}
	if f.Category != "" {
		clauses = append(clauses, "category = ?")
		args = append(args, f.Category)
	}
	if f.Tag != "" {
		// tags is a comma-separated string. Wrap with "," so we don't get
		// false positives where a needle is a prefix of an unrelated tag.
		clauses = append(clauses, "(',' || tags || ',') LIKE ?")
		args = append(args, "%,"+f.Tag+",%")
	}
	if f.SinceMs > 0 {
		clauses = append(clauses, "ts >= ?")
		args = append(args, f.SinceMs)
	}
	if f.UntilMs > 0 {
		clauses = append(clauses, "ts < ?")
		args = append(args, f.UntilMs)
	}
	if f.OnlyOpen {
		clauses = append(clauses, "acknowledged = 0")
	} else if f.OnlyAcked {
		clauses = append(clauses, "acknowledged = 1")
	}

	q := `SELECT id, event_id, ts, agent, host, severity, title,
	             resource, evidence, dedup_key, raw_json,
	             acknowledged, acknowledged_at, acknowledged_by, ack_note,
	             created_at,
	             category, process_pid, process_name, path, network_endpoint, cve, tags, attributes,
	             status, triage_note, triaged_at, triaged_by_user_id, triaged_by_email,
	             operator_severity, severity_override_at, severity_override_by
	      FROM findings`
	if len(clauses) > 0 {
		q += " WHERE " + strings.Join(clauses, " AND ")
	}
	q += " ORDER BY ts DESC LIMIT ? OFFSET ?"
	args = append(args, f.Limit, f.Offset)

	rows, err := s.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Finding
	for rows.Next() {
		fr := &Finding{}
		var ack int64
		if err := rows.Scan(
			&fr.ID, &fr.EventID, &fr.Ts,
			&fr.Agent, &fr.Host, &fr.Severity, &fr.Title,
			&fr.Resource, &fr.Evidence, &fr.DedupKey, &fr.RawJSON,
			&ack, &fr.AckedAt, &fr.AckedBy, &fr.AckNote,
			&fr.CreatedAt,
			&fr.Category, &fr.ProcessPID, &fr.ProcessName, &fr.Path,
			&fr.NetworkEndpoint, &fr.CVE, &fr.Tags, &fr.Attributes,
			&fr.Status, &fr.TriageNote, &fr.TriagedAt, &fr.TriagedByUser, &fr.TriagedByEmail,
			&fr.OperatorSeverity, &fr.SeverityOverrideAt, &fr.SeverityOverrideByID,
		); err != nil {
			return nil, err
		}
		fr.Acknowledged = ack != 0
		out = append(out, fr)
	}
	return out, rows.Err()
}

// FindingByID returns a single finding or sql.ErrNoRows.
func (s *Store) FindingByID(id int64) (*Finding, error) {
	fr := &Finding{}
	var ack int64
	err := s.QueryRow(`
		SELECT id, event_id, ts, agent, host, severity, title,
		       resource, evidence, dedup_key, raw_json,
		       acknowledged, acknowledged_at, acknowledged_by, ack_note,
		       created_at,
		       category, process_pid, process_name, path, network_endpoint, cve, tags, attributes,
		       status, triage_note, triaged_at, triaged_by_user_id, triaged_by_email,
		       operator_severity, severity_override_at, severity_override_by
		FROM findings WHERE id = ?
	`, id).Scan(
		&fr.ID, &fr.EventID, &fr.Ts,
		&fr.Agent, &fr.Host, &fr.Severity, &fr.Title,
		&fr.Resource, &fr.Evidence, &fr.DedupKey, &fr.RawJSON,
		&ack, &fr.AckedAt, &fr.AckedBy, &fr.AckNote,
		&fr.CreatedAt,
		&fr.Category, &fr.ProcessPID, &fr.ProcessName, &fr.Path,
		&fr.NetworkEndpoint, &fr.CVE, &fr.Tags, &fr.Attributes,
		&fr.Status, &fr.TriageNote, &fr.TriagedAt, &fr.TriagedByUser, &fr.TriagedByEmail,
		&fr.OperatorSeverity, &fr.SeverityOverrideAt, &fr.SeverityOverrideByID,
	)
	if err != nil {
		return nil, err
	}
	fr.Acknowledged = ack != 0
	return fr, nil
}

// AcknowledgeGroup acknowledges every open finding sharing the given
// composite group key. Either dedupKey is non-empty (preferred) and we
// match on that alone, or we match on (title, severity, agent) — the same
// fallback the grouping query uses. Returns the number of rows updated.
func (s *Store) AcknowledgeGroup(dedupKey, title, severity, agent string, userID int64, note string) (int64, error) {
	if userID == 0 {
		return 0, sql.ErrNoRows
	}
	if dedupKey != "" {
		res, err := s.Exec(`
			UPDATE findings
			SET acknowledged = 1,
			    acknowledged_at = CURRENT_TIMESTAMP,
			    acknowledged_by = ?,
			    ack_note = ?
			WHERE dedup_key = ? AND acknowledged = 0
		`, userID, nullable(note), dedupKey)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected()
	}
	// No dedup_key — match the same composite the grouping query uses.
	res, err := s.Exec(`
		UPDATE findings
		SET acknowledged = 1,
		    acknowledged_at = CURRENT_TIMESTAMP,
		    acknowledged_by = ?,
		    ack_note = ?
		WHERE acknowledged = 0
		  AND (dedup_key IS NULL OR dedup_key = '')
		  AND COALESCE(title,    '') = ?
		  AND COALESCE(severity, '') = ?
		  AND COALESCE(agent,    '') = ?
	`, userID, nullable(note), title, severity, agent)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// SetFindingStatus is the canonical triage write. Sets status + note + actor
// and synchronizes the legacy acknowledged columns so old consumers
// (notification-routing rules, ack-only API callers) keep working.
//
// Setting status='open' clears the triage metadata.
func (s *Store) SetFindingStatus(findingID int64, status string, userID int64, userEmail, note string) error {
	if !IsValidFindingStatus(status) {
		return fmt.Errorf("invalid finding status %q", status)
	}
	if status == FindingOpen {
		_, err := s.Exec(`
			UPDATE findings
			   SET status = 'open',
			       triage_note = NULL,
			       triaged_at = NULL,
			       triaged_by_user_id = NULL,
			       triaged_by_email = NULL,
			       acknowledged = 0,
			       acknowledged_at = NULL,
			       acknowledged_by = NULL,
			       ack_note = NULL
			 WHERE id = ?
		`, findingID)
		return err
	}
	_, err := s.Exec(`
		UPDATE findings
		   SET status              = ?,
		       triage_note         = ?,
		       triaged_at          = CURRENT_TIMESTAMP,
		       triaged_by_user_id  = ?,
		       triaged_by_email    = ?,
		       -- legacy acknowledged mirror: any non-open status counts as acked
		       acknowledged        = 1,
		       acknowledged_at     = CURRENT_TIMESTAMP,
		       acknowledged_by     = ?,
		       ack_note            = ?
		 WHERE id = ?
	`, status, nullable(note), nullableInt64(userID), nullable(userEmail),
		nullableInt64(userID), nullable(note), findingID)
	return err
}

// SetGroupStatus applies the same status to every open finding sharing the
// given dedup_key (preferred) or (title, severity, agent). Returns the
// number of rows updated. Going to 'open' from a triaged status reopens
// the entire group.
func (s *Store) SetGroupStatus(dedupKey, title, severity, agent, status string,
	userID int64, userEmail, note string) (int64, error) {

	if !IsValidFindingStatus(status) {
		return 0, fmt.Errorf("invalid finding status %q", status)
	}

	var (
		setSQL   string
		args     []any
		whereSQL string
	)
	if status == FindingOpen {
		setSQL = `SET status = 'open',
		              triage_note = NULL, triaged_at = NULL,
		              triaged_by_user_id = NULL, triaged_by_email = NULL,
		              acknowledged = 0, acknowledged_at = NULL,
		              acknowledged_by = NULL, ack_note = NULL`
		whereSQL = "status != 'open'"
	} else {
		setSQL = `SET status = ?, triage_note = ?,
		              triaged_at = CURRENT_TIMESTAMP,
		              triaged_by_user_id = ?, triaged_by_email = ?,
		              acknowledged = 1, acknowledged_at = CURRENT_TIMESTAMP,
		              acknowledged_by = ?, ack_note = ?`
		args = []any{
			status, nullable(note), nullableInt64(userID), nullable(userEmail),
			nullableInt64(userID), nullable(note),
		}
		whereSQL = "status = 'open'"
	}

	if dedupKey != "" {
		args = append(args, dedupKey)
		res, err := s.Exec(`UPDATE findings `+setSQL+` WHERE `+whereSQL+` AND dedup_key = ?`, args...)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected()
	}
	args = append(args, title, severity, agent)
	res, err := s.Exec(`UPDATE findings `+setSQL+`
		WHERE `+whereSQL+`
		  AND (dedup_key IS NULL OR dedup_key = '')
		  AND COALESCE(title, '') = ?
		  AND COALESCE(severity, '') = ?
		  AND COALESCE(agent, '') = ?
	`, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// SetFindingSeverity overrides the severity on a single finding. The
// agent's original assignment stays in `severity`; this writes
// `operator_severity` plus the audit metadata. Pass severity="" to clear
// the override (revert to original).
func (s *Store) SetFindingSeverity(findingID int64, severity string, userID int64) error {
	if severity == "" {
		_, err := s.Exec(`
			UPDATE findings
			   SET operator_severity = NULL,
			       severity_override_at = NULL,
			       severity_override_by = NULL
			 WHERE id = ?
		`, findingID)
		return err
	}
	if !IsValidSeverity(severity) {
		return fmt.Errorf("invalid severity %q", severity)
	}
	_, err := s.Exec(`
		UPDATE findings
		   SET operator_severity     = ?,
		       severity_override_at  = CURRENT_TIMESTAMP,
		       severity_override_by  = ?
		 WHERE id = ?
	`, CanonicalSeverity(severity), nullableInt64(userID), findingID)
	return err
}

// SetGroupSeverity overrides operator_severity on every finding sharing a
// fingerprint, in one shot. dedupKey wins when present; otherwise the
// title|severity|agent composite is used. Returns rows affected.
func (s *Store) SetGroupSeverity(dedupKey, title, origSeverity, agent, severity string, userID int64) (int64, error) {
	if severity != "" && !IsValidSeverity(severity) {
		return 0, fmt.Errorf("invalid severity %q", severity)
	}
	canon := CanonicalSeverity(severity)
	var setSQL string
	var args []any
	if severity == "" {
		setSQL = `SET operator_severity = NULL,
		              severity_override_at = NULL,
		              severity_override_by = NULL`
	} else {
		setSQL = `SET operator_severity = ?,
		              severity_override_at = CURRENT_TIMESTAMP,
		              severity_override_by = ?`
		args = []any{canon, nullableInt64(userID)}
	}
	if dedupKey != "" {
		args = append(args, dedupKey)
		res, err := s.Exec(`UPDATE findings `+setSQL+` WHERE dedup_key = ?`, args...)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected()
	}
	args = append(args, title, origSeverity, agent)
	res, err := s.Exec(`UPDATE findings `+setSQL+`
		WHERE (dedup_key IS NULL OR dedup_key = '')
		  AND COALESCE(title, '')    = ?
		  AND COALESCE(severity, '') = ?
		  AND COALESCE(agent, '')    = ?
	`, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// SeverityRule is a per-fingerprint default that will be applied to every
// future finding sharing the fingerprint. Existing rows are not mutated
// when a rule is created — operators must run a backfill (or manually
// override) to retro-apply. The rationale: rules represent the operator's
// policy from now on; rewriting historical rows blurs the audit trail.
type SeverityRule struct {
	Fingerprint string
	Severity    string
	Note        sql.NullString
	CreatedAt   time.Time
	UpdatedAt   sql.NullTime
	CreatedBy   sql.NullInt64
}

// UpsertSeverityRule creates or updates a rule for the given fingerprint.
func (s *Store) UpsertSeverityRule(fingerprint, severity, note string, userID int64) error {
	if fingerprint == "" {
		return fmt.Errorf("fingerprint required")
	}
	if !IsValidSeverity(severity) {
		return fmt.Errorf("invalid severity %q", severity)
	}
	_, err := s.Exec(`
		INSERT INTO finding_severity_rules (fingerprint, severity, note, created_by)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(fingerprint) DO UPDATE SET
			severity   = excluded.severity,
			note       = excluded.note,
			updated_at = CURRENT_TIMESTAMP
	`, fingerprint, CanonicalSeverity(severity), nullable(note), nullableInt64(userID))
	return err
}

// DeleteSeverityRule removes the rule for a fingerprint. Existing finding
// rows that already received the override are NOT reverted — operators
// can clear those individually if they want. (Symmetric with the
// "creating a rule doesn't backfill" behavior.)
func (s *Store) DeleteSeverityRule(fingerprint string) error {
	_, err := s.Exec(`DELETE FROM finding_severity_rules WHERE fingerprint = ?`, fingerprint)
	return err
}

// GetSeverityRule returns the rule for a fingerprint, or sql.ErrNoRows.
func (s *Store) GetSeverityRule(fingerprint string) (*SeverityRule, error) {
	r := &SeverityRule{}
	err := s.QueryRow(`
		SELECT fingerprint, severity, note, created_at, updated_at, created_by
		FROM finding_severity_rules WHERE fingerprint = ?
	`, fingerprint).Scan(&r.Fingerprint, &r.Severity, &r.Note, &r.CreatedAt, &r.UpdatedAt, &r.CreatedBy)
	if err != nil {
		return nil, err
	}
	return r, nil
}

// ListSeverityRules returns every rule, newest first.
func (s *Store) ListSeverityRules() ([]*SeverityRule, error) {
	rows, err := s.Query(`
		SELECT fingerprint, severity, note, created_at, updated_at, created_by
		FROM finding_severity_rules
		ORDER BY COALESCE(updated_at, created_at) DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*SeverityRule
	for rows.Next() {
		r := &SeverityRule{}
		if err := rows.Scan(&r.Fingerprint, &r.Severity, &r.Note, &r.CreatedAt, &r.UpdatedAt, &r.CreatedBy); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// KnownIssue is one row in the daemon-facing feedback feed.
type KnownIssue struct {
	Fingerprint     string `json:"fingerprint"`
	Status          string `json:"status"`
	TriageNote      string `json:"triage_note,omitempty"`
	UpdatedAt       int64  `json:"updated_at"` // unix ms
	OccurrenceCount int64  `json:"occurrence_count"`
}

// KnownIssuesForAgent returns every NON-OPEN fingerprint the named agent has
// emitted across the fleet within the lookback window. Drives the daemon's
// pull-cache so triage decisions propagate to every host running this agent.
//
// The query uses portable SQL — `MAX(boolean_expr)` is SQLite-specific
// because SQLite treats booleans as ints; we use SUM(CASE...) > 0 which
// works on both backends. tsToMillisExpr handles the unix-ms conversion
// per dialect.
func (s *Store) KnownIssuesForAgent(agentName string, sinceMs int64, limit int) ([]KnownIssue, error) {
	if limit <= 0 || limit > 5000 {
		limit = 1000
	}
	tsExpr := tsToMillisExpr(s.Dialect, "triaged_at")
	rows, err := s.Query(`
		SELECT
		  COALESCE(NULLIF(dedup_key, ''),
		           COALESCE(title,'') || '|' || COALESCE(severity,'') || '|' || COALESCE(agent,'')) AS fp,
		  -- pick the most-severe status: false_positive / wontfix dominate ack/investigating
		  CASE
		    WHEN SUM(CASE WHEN status = 'false_positive' THEN 1 ELSE 0 END) > 0 THEN 'false_positive'
		    WHEN SUM(CASE WHEN status = 'wontfix'        THEN 1 ELSE 0 END) > 0 THEN 'wontfix'
		    WHEN SUM(CASE WHEN status = 'resolved'       THEN 1 ELSE 0 END) > 0 THEN 'resolved'
		    WHEN SUM(CASE WHEN status = 'acknowledged'   THEN 1 ELSE 0 END) > 0 THEN 'acknowledged'
		    WHEN SUM(CASE WHEN status = 'investigating'  THEN 1 ELSE 0 END) > 0 THEN 'investigating'
		    ELSE 'open'
		  END AS effective_status,
		  COALESCE(MAX(triage_note), '')                  AS note,
		  COALESCE(MAX(`+tsExpr+`), 0) AS updated_ms,
		  COUNT(*)                                        AS n
		FROM findings
		WHERE agent = ?
		  AND ts >= ?
		  AND status != 'open'
		GROUP BY fp
		HAVING (CASE
		    WHEN SUM(CASE WHEN status = 'false_positive' THEN 1 ELSE 0 END) > 0 THEN 'false_positive'
		    WHEN SUM(CASE WHEN status = 'wontfix'        THEN 1 ELSE 0 END) > 0 THEN 'wontfix'
		    WHEN SUM(CASE WHEN status = 'resolved'       THEN 1 ELSE 0 END) > 0 THEN 'resolved'
		    WHEN SUM(CASE WHEN status = 'acknowledged'   THEN 1 ELSE 0 END) > 0 THEN 'acknowledged'
		    WHEN SUM(CASE WHEN status = 'investigating'  THEN 1 ELSE 0 END) > 0 THEN 'investigating'
		    ELSE 'open'
		  END) != 'open'
		ORDER BY updated_ms DESC
		LIMIT ?
	`, agentName, sinceMs, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KnownIssue
	for rows.Next() {
		k := KnownIssue{}
		if err := rows.Scan(&k.Fingerprint, &k.Status, &k.TriageNote, &k.UpdatedAt, &k.OccurrenceCount); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// LookupFindingsResult is one row of the daemon's lookup_findings tool.
// Compact by design — token-budget aware (note + resource truncated to keep
// each result under ~150 tokens).
//
// Phase 14 calibration fields (Severity / OriginalSeverity / SuggestedSeverity):
//
//   - Severity: the EFFECTIVE severity of the most recent occurrence
//     (operator override if set, else what the LLM emitted). What you'd
//     see if you opened the finding in the dashboard right now.
//
//   - OriginalSeverity: what the LLM originally assigned, ignoring all
//     overrides. Lets the model see the gap between its past judgment
//     and the operator's correction.
//
//   - SuggestedSeverity: a server-computed recommendation. Set when EITHER
//     a per-fingerprint rule exists OR ≥3 prior occurrences carry an
//     operator override that all agree. Empty otherwise — the LLM should
//     stick with its own judgment when there's no signal.
type LookupFindingsResult struct {
	Fingerprint        string `json:"fingerprint"`
	Title              string `json:"title,omitempty"`
	Severity           string `json:"severity,omitempty"`
	OriginalSeverity   string `json:"original_severity,omitempty"`
	SuggestedSeverity  string `json:"suggested_severity,omitempty"`
	Status             string `json:"status,omitempty"`
	Host               string `json:"host,omitempty"`
	Resource           string `json:"resource,omitempty"`
	TriageNote         string `json:"triage_note,omitempty"`
	LastSeen           int64  `json:"last_seen,omitempty"` // unix ms
	OccurrenceCount    int64  `json:"occurrence_count"`
}

// LookupFindings runs a coarse LIKE search across the most-searched fields
// and returns at most limit results, scoped to the given agent across all
// hosts. Used by the daemon-side lookup_findings tool that the LLM can
// call when it wants to know whether something has been seen before.
//
// Triaged matches surface above untriaged so the LLM sees the operator's
// classification first.
func (s *Store) LookupFindings(agentName, query string, limit int) ([]LookupFindingsResult, error) {
	if limit <= 0 || limit > 15 {
		limit = 5
	}
	pattern := "%" + strings.ToLower(query) + "%"
	// One pass returns per-fingerprint aggregates AND a flat tally of
	// override severities (semicolon-joined "SEVERITY:N" pairs) so the
	// caller can compute the suggested-severity signal without a second
	// query. SQLite doesn't have a native histogram aggregate, so we
	// emulate it via a correlated sub-select per fingerprint.
	rows, err := s.Query(`
		WITH matches AS (
		  SELECT
		    COALESCE(NULLIF(dedup_key, ''),
		             COALESCE(title,'') || '|' || COALESCE(severity,'') || '|' || COALESCE(agent,'')) AS fp,
		    title, severity, operator_severity, status, host, resource, triage_note,
		    ts,
		    -- rank: triaged matches first, then by last-seen
		    CASE WHEN status != 'open' THEN 1 ELSE 0 END AS triaged
		  FROM findings
		  WHERE agent = ?
		    AND (
		      LOWER(COALESCE(title, ''))            LIKE ? OR
		      LOWER(COALESCE(resource, ''))         LIKE ? OR
		      LOWER(COALESCE(network_endpoint, '')) LIKE ? OR
		      LOWER(COALESCE(path, ''))             LIKE ? OR
		      LOWER(COALESCE(process_name, ''))     LIKE ? OR
		      LOWER(COALESCE(dedup_key, ''))        LIKE ?
		    )
		)
		SELECT m.fp,
		       COALESCE(MAX(m.title),'')                                                  AS title,
		       -- effective severity: operator override of the most-recent row wins,
		       -- falling back to the LLM's original.
		       COALESCE(
		         (SELECT COALESCE(NULLIF(operator_severity,''), severity)
		            FROM matches WHERE fp = m.fp ORDER BY ts DESC LIMIT 1),
		         ''
		       )                                                                          AS effective_severity,
		       COALESCE(
		         (SELECT severity
		            FROM matches WHERE fp = m.fp ORDER BY ts DESC LIMIT 1),
		         ''
		       )                                                                          AS original_severity,
		       -- per-fingerprint operator override majority: count overrides per value
		       -- and emit "SEV:N;SEV:N" so Go can pick the dominant one. Empty when no
		       -- overrides have been applied.
		       COALESCE(
		         (SELECT GROUP_CONCAT(operator_severity || ':' || c, ';')
		            FROM (
		              SELECT operator_severity, COUNT(*) AS c
		              FROM matches
		              WHERE fp = m.fp
		                AND operator_severity IS NOT NULL
		                AND operator_severity != ''
		              GROUP BY operator_severity
		            )
		         ),
		         ''
		       )                                                                          AS override_tally,
		       -- explicit per-fingerprint rule (separate from individual overrides)
		       COALESCE((SELECT severity FROM finding_severity_rules WHERE fingerprint = m.fp), '') AS rule_severity,
		       CASE
		         WHEN SUM(CASE WHEN m.status = 'false_positive' THEN 1 ELSE 0 END) > 0 THEN 'false_positive'
		         WHEN SUM(CASE WHEN m.status = 'wontfix'        THEN 1 ELSE 0 END) > 0 THEN 'wontfix'
		         WHEN SUM(CASE WHEN m.status = 'resolved'       THEN 1 ELSE 0 END) > 0 THEN 'resolved'
		         WHEN SUM(CASE WHEN m.status = 'acknowledged'   THEN 1 ELSE 0 END) > 0 THEN 'acknowledged'
		         WHEN SUM(CASE WHEN m.status = 'investigating'  THEN 1 ELSE 0 END) > 0 THEN 'investigating'
		         ELSE 'open'
		       END                                                                        AS status,
		       COALESCE(MAX(m.host),'')                                                    AS host,
		       COALESCE(MAX(m.resource),'')                                                AS resource,
		       COALESCE(MAX(m.triage_note),'')                                             AS triage_note,
		       MAX(m.ts)                                                                   AS last_seen,
		       COUNT(*)                                                                    AS occurrence_count,
		       MAX(m.triaged)                                                              AS any_triaged
		FROM matches m
		GROUP BY m.fp
		ORDER BY any_triaged DESC, MAX(m.ts) DESC
		LIMIT ?
	`, agentName, pattern, pattern, pattern, pattern, pattern, pattern, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LookupFindingsResult
	for rows.Next() {
		r := LookupFindingsResult{}
		var overrideTally, ruleSeverity string
		var anyTriaged int64
		if err := rows.Scan(
			&r.Fingerprint, &r.Title,
			&r.Severity, &r.OriginalSeverity,
			&overrideTally, &ruleSeverity,
			&r.Status, &r.Host, &r.Resource, &r.TriageNote,
			&r.LastSeen, &r.OccurrenceCount, &anyTriaged,
		); err != nil {
			return nil, err
		}
		r.SuggestedSeverity = computeSuggestedSeverity(ruleSeverity, overrideTally)
		// Token-budget truncation.
		if len(r.TriageNote) > 200 {
			r.TriageNote = r.TriageNote[:200] + "…"
		}
		if len(r.Resource) > 100 {
			r.Resource = r.Resource[:100] + "…"
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// computeSuggestedSeverity decides what severity hint to surface to the
// agent. Rule wins outright (operator policy is explicit). Otherwise we
// look at the operator-override tally — if ≥3 occurrences have been
// overridden AND a single severity has the strict majority, suggest it.
// Below that threshold we stay silent: a single drive-by reclassification
// shouldn't pivot future agents.
func computeSuggestedSeverity(ruleSeverity, overrideTally string) string {
	if ruleSeverity != "" {
		return ruleSeverity
	}
	if overrideTally == "" {
		return ""
	}
	type pair struct {
		sev   string
		count int64
	}
	var pairs []pair
	var total int64
	for _, part := range strings.Split(overrideTally, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		colon := strings.LastIndex(part, ":")
		if colon <= 0 {
			continue
		}
		sev := part[:colon]
		var c int64
		_, err := fmt.Sscanf(part[colon+1:], "%d", &c)
		if err != nil || c <= 0 {
			continue
		}
		pairs = append(pairs, pair{sev: sev, count: c})
		total += c
	}
	if total < 3 {
		return ""
	}
	// Pick the dominant severity; require a strict majority (>50%) so
	// split-decision overrides don't yield a misleading hint.
	var top pair
	for _, p := range pairs {
		if p.count > top.count {
			top = p
		}
	}
	if top.count*2 > total {
		return top.sev
	}
	return ""
}

// AcknowledgeFinding marks a finding as acknowledged by the given user.
// Pass userID=0 to clear acknowledgment.
func (s *Store) AcknowledgeFinding(findingID, userID int64, note string) error {
	if userID == 0 {
		_, err := s.Exec(`
			UPDATE findings
			SET acknowledged = 0, acknowledged_at = NULL,
			    acknowledged_by = NULL, ack_note = NULL
			WHERE id = ?
		`, findingID)
		return err
	}
	_, err := s.Exec(`
		UPDATE findings
		SET acknowledged = 1, acknowledged_at = CURRENT_TIMESTAMP,
		    acknowledged_by = ?, ack_note = ?
		WHERE id = ?
	`, userID, nullable(note), findingID)
	return err
}

// FindingsSummary is the aggregate counts shown on the dashboard.
//
// All severity counters are over OPEN findings only — acknowledged findings
// are excluded so the dashboard reflects what still needs operator attention.
// `Total` is unfiltered (open + acked) so the change in open vs total signals
// how much the team has triaged.
type FindingsSummary struct {
	Total    int64 `json:"total"`
	Open     int64 `json:"open"`
	Critical int64 `json:"critical"`
	High     int64 `json:"high"`
	Medium   int64 `json:"medium"`
	Low      int64 `json:"low"`
	Info     int64 `json:"info"`
	Last24h  int64 `json:"last_24h"`

	// ByAgent: open findings per agent name, ordered by count desc.
	ByAgent []FindingsByAgent `json:"by_agent"`

	// ByCategory: open findings per category (process/file/network/cert/...).
	// Findings without a category contribute to "(uncategorized)".
	ByCategory []FindingsByCategory `json:"by_category"`

	// Trend: 24 buckets, one per hour, ending at the current hour.
	// Each entry is the number of NEW findings (any severity) created in
	// that hour. Used to render the dashboard sparkline.
	Trend []FindingsTrendBucket `json:"trend"`
}

// FindingsByAgent is one row in the per-agent breakdown.
type FindingsByAgent struct {
	Agent    string `json:"agent"`
	Open     int64  `json:"open"`
	Critical int64  `json:"critical"`
	High     int64  `json:"high"`
}

// FindingsByCategory is one row in the per-category breakdown.
type FindingsByCategory struct {
	Category string `json:"category"`
	Open     int64  `json:"open"`
	Critical int64  `json:"critical"`
	High     int64  `json:"high"`
}

// FindingGroup is one consolidated issue: same dedup_key (or
// title+severity+agent fallback when dedup_key is empty) seen on one or
// more hosts. The dashboard collapses occurrences so an issue affecting
// 50 nodes shows as a single row instead of 50.
type FindingGroup struct {
	GroupKey    string   `json:"group_key"`    // composite key actually grouped on
	DedupKey    string   `json:"dedup_key,omitempty"`
	Severity    string   `json:"severity,omitempty"`
	Title       string   `json:"title,omitempty"`
	Agent       string   `json:"agent,omitempty"`
	Resource    string   `json:"resource,omitempty"`   // first occurrence's resource
	Count       int64    `json:"count"`               // how many open findings in this group
	Hosts       []string `json:"hosts"`               // distinct hosts reporting it
	FirstSeen   int64    `json:"first_seen"`          // unix ms of earliest occurrence
	LastSeen    int64    `json:"last_seen"`           // unix ms of most recent occurrence
	LatestID    int64    `json:"latest_id"`           // findings.id of the newest occurrence (for drill-in)
}

// FindingsTrendBucket is one hour of the trend sparkline.
type FindingsTrendBucket struct {
	HourTs int64 `json:"hour_ts"` // unix ms at the start of the hour (UTC)
	Count  int64 `json:"count"`
}

// FindingsSummary returns aggregate counts. Cheap thanks to the partial indexes.
func (s *Store) FindingsSummary() (*FindingsSummary, error) {
	out := &FindingsSummary{}
	if err := s.QueryRow(`SELECT COUNT(*) FROM findings`).Scan(&out.Total); err != nil {
		return nil, err
	}
	if err := s.QueryRow(`SELECT COUNT(*) FROM findings WHERE acknowledged = 0`).Scan(&out.Open); err != nil {
		return nil, err
	}
	type row struct {
		sev string
		ptr *int64
	}
	for _, r := range []row{
		{"CRITICAL", &out.Critical},
		{"HIGH", &out.High},
		{"MEDIUM", &out.Medium},
		{"LOW", &out.Low},
		{"INFO", &out.Info},
	} {
		if err := s.QueryRow(
			`SELECT COUNT(*) FROM findings WHERE UPPER(COALESCE(NULLIF(operator_severity,''), severity)) = ? AND acknowledged = 0`,
			r.sev,
		).Scan(r.ptr); err != nil {
			return nil, err
		}
	}
	dayAgo := time.Now().Add(-24 * time.Hour).UnixMilli()
	if err := s.QueryRow(
		`SELECT COUNT(*) FROM findings WHERE ts >= ?`, dayAgo,
	).Scan(&out.Last24h); err != nil {
		return nil, err
	}

	// Per-agent breakdown — open findings only, top 10 by count.
	rows, err := s.Query(`
		SELECT
		  COALESCE(agent, '(unset)') AS a,
		  COUNT(*) AS open_count,
		  SUM(CASE WHEN UPPER(COALESCE(NULLIF(operator_severity,''), severity)) = 'CRITICAL' THEN 1 ELSE 0 END) AS crit,
		  SUM(CASE WHEN UPPER(COALESCE(NULLIF(operator_severity,''), severity)) = 'HIGH'     THEN 1 ELSE 0 END) AS high
		FROM findings
		WHERE acknowledged = 0
		GROUP BY a
		ORDER BY open_count DESC
		LIMIT 10
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		ba := FindingsByAgent{}
		if err := rows.Scan(&ba.Agent, &ba.Open, &ba.Critical, &ba.High); err != nil {
			return nil, err
		}
		out.ByAgent = append(out.ByAgent, ba)
	}

	// Per-category breakdown — same shape, used for the category panel.
	cRows, err := s.Query(`
		SELECT
		  COALESCE(NULLIF(category, ''), '(uncategorized)') AS c,
		  COUNT(*) AS open_count,
		  SUM(CASE WHEN UPPER(COALESCE(NULLIF(operator_severity,''), severity)) = 'CRITICAL' THEN 1 ELSE 0 END) AS crit,
		  SUM(CASE WHEN UPPER(COALESCE(NULLIF(operator_severity,''), severity)) = 'HIGH'     THEN 1 ELSE 0 END) AS high
		FROM findings
		WHERE acknowledged = 0
		GROUP BY c
		ORDER BY open_count DESC
		LIMIT 12
	`)
	if err != nil {
		return nil, err
	}
	defer cRows.Close()
	for cRows.Next() {
		bc := FindingsByCategory{}
		if err := cRows.Scan(&bc.Category, &bc.Open, &bc.Critical, &bc.High); err != nil {
			return nil, err
		}
		out.ByCategory = append(out.ByCategory, bc)
	}

	// 24h-by-hour trend. Build the bucket grid in Go so empty hours show as 0.
	now := time.Now().UTC().Truncate(time.Hour)
	bucketByHour := make(map[int64]int64, 24)
	tRows, err := s.Query(`
		SELECT
		  ts,
		  COUNT(*) AS n
		FROM findings
		WHERE ts >= ?
		GROUP BY ts / 3600000
	`, now.Add(-24*time.Hour).UnixMilli())
	if err != nil {
		return nil, err
	}
	defer tRows.Close()
	for tRows.Next() {
		var ts, n int64
		if err := tRows.Scan(&ts, &n); err != nil {
			return nil, err
		}
		// Bucket by the hour boundary the row falls into.
		hourMs := (ts / 3600000) * 3600000
		bucketByHour[hourMs] += n
	}
	out.Trend = make([]FindingsTrendBucket, 0, 24)
	for i := 23; i >= 0; i-- {
		hourTs := now.Add(-time.Duration(i)*time.Hour).UnixMilli()
		out.Trend = append(out.Trend, FindingsTrendBucket{
			HourTs: hourTs,
			Count:  bucketByHour[hourTs],
		})
	}

	return out, nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// GroupedFindingsFilter captures the same filters as the list view but is
// consumed by the grouped query. State is implicitly "open" — the grouped
// dashboard view is for what still needs attention.
type GroupedFindingsFilter struct {
	Severities []string
	Agent      string
	Host       string
	Category   string
	Tag        string
	SinceMs    int64
	Limit      int
}

// GroupedFindings consolidates open findings by dedup_key (or
// title+severity+agent when dedup_key is empty) and returns one row per
// distinct issue with the host fan-out. Caps groups at filter.Limit (default
// 100). Caller is responsible for applying any RBAC restrictions.
func (s *Store) GroupedFindings(f GroupedFindingsFilter) ([]*FindingGroup, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}

	var (
		clauses = []string{"acknowledged = 0"}
		args    []any
	)
	if len(f.Severities) > 0 {
		ph := make([]string, len(f.Severities))
		for i, s := range f.Severities {
			ph[i] = "?"
			args = append(args, strings.ToUpper(s))
		}
		clauses = append(clauses, "UPPER(COALESCE(NULLIF(operator_severity,''), severity)) IN ("+strings.Join(ph, ",")+")")
	}
	if f.Agent != "" {
		clauses = append(clauses, "agent = ?")
		args = append(args, f.Agent)
	}
	if f.Host != "" {
		clauses = append(clauses, "host = ?")
		args = append(args, f.Host)
	}
	if f.Category != "" {
		clauses = append(clauses, "category = ?")
		args = append(args, f.Category)
	}
	if f.Tag != "" {
		clauses = append(clauses, "(',' || tags || ',') LIKE ?")
		args = append(args, "%,"+f.Tag+",%")
	}
	if f.SinceMs > 0 {
		clauses = append(clauses, "ts >= ?")
		args = append(args, f.SinceMs)
	}
	where := "WHERE " + strings.Join(clauses, " AND ")

	// COALESCE produces a single non-empty group key per finding:
	//   - prefer dedup_key (the LLM picked something stable)
	//   - else title|severity|agent (last-resort fallback that still merges
	//     the same recurring issue across hosts)
	// SQLite has GROUP_CONCAT but no DISTINCT-with-separator support pre-3.44,
	// so we run it once for hosts and accept duplicates if they sneak in.
	q := `
		SELECT
		  COALESCE(NULLIF(dedup_key, ''),
		           COALESCE(title,'') || '|' || COALESCE(severity,'') || '|' || COALESCE(agent,'')) AS gkey,
		  COALESCE(dedup_key, '')                          AS dk,
		  COALESCE(MAX(severity), '')                      AS sev,
		  COALESCE(MAX(title), '')                         AS title,
		  COALESCE(MAX(agent), '')                         AS agent,
		  COALESCE(MAX(resource), '')                      AS resource,
		  COUNT(*)                                         AS n,
		  GROUP_CONCAT(DISTINCT COALESCE(host,''))         AS hosts,
		  MIN(ts)                                          AS first_ts,
		  MAX(ts)                                          AS last_ts,
		  MAX(id)                                          AS latest_id
		FROM findings
		` + where + `
		GROUP BY gkey
		ORDER BY
		  CASE UPPER(sev)
		    WHEN 'CRITICAL' THEN 5
		    WHEN 'HIGH'     THEN 4
		    WHEN 'MEDIUM'   THEN 3
		    WHEN 'LOW'      THEN 2
		    WHEN 'INFO'     THEN 1
		    ELSE 0
		  END DESC,
		  n DESC,
		  last_ts DESC
		LIMIT ?
	`
	args = append(args, f.Limit)

	rows, err := s.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*FindingGroup
	for rows.Next() {
		g := &FindingGroup{}
		var hostsCsv string
		if err := rows.Scan(
			&g.GroupKey, &g.DedupKey, &g.Severity, &g.Title, &g.Agent, &g.Resource,
			&g.Count, &hostsCsv, &g.FirstSeen, &g.LastSeen, &g.LatestID,
		); err != nil {
			return nil, err
		}
		seen := map[string]struct{}{}
		for _, h := range strings.Split(hostsCsv, ",") {
			h = strings.TrimSpace(h)
			if h == "" {
				continue
			}
			if _, dup := seen[h]; dup {
				continue
			}
			seen[h] = struct{}{}
			g.Hosts = append(g.Hosts, h)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// FindingsTimeSeriesBucket is one (bucket_ts, series_name → count) row
// from FindingsTimeSeries. Used by the dashboard's multi-line chart.
type FindingsTimeSeriesBucket struct {
	Ts int64
	By map[string]int64
}

// FindingsTimeSeries buckets findings by (ts ÷ bucketMs) and aggregates
// counts per `dimCol` (severity | agent | host). Series are folded:
// only the top-`topN` distinct dimension values keep their own line;
// the rest collapse into "other". Empty dimension values render as
// "(unknown)".
//
// sinceMs lower-bounds ts; bucketMs sets the grid (3600000 for 1h,
// 86400000 for 1d). The caller fills empty buckets so a continuous
// line renders.
//
// Tested portably across SQLite + Postgres via the Store's placeholder
// rewriter — `?` works on both. The arithmetic is plain int math
// (ts / bucketMs * bucketMs), no dialect helpers needed.
func (s *Store) FindingsTimeSeries(sinceMs, bucketMs int64, dimCol string, topN int) ([]FindingsTimeSeriesBucket, []string, error) {
	switch dimCol {
	case "severity", "agent", "host":
		// allowed
	default:
		return nil, nil, fmt.Errorf("dimCol must be severity|agent|host, got %q", dimCol)
	}
	if bucketMs <= 0 {
		bucketMs = int64(time.Hour / time.Millisecond)
	}
	// Find the top-N dimension values by total count first; everything
	// else collapses to "other" to keep the chart readable.
	rows, err := s.Query(`
		SELECT COALESCE(NULLIF(`+dimCol+`, ''), '(unknown)') AS dim, COUNT(*) AS n
		FROM findings
		WHERE ts >= ?
		GROUP BY dim
		ORDER BY n DESC
		LIMIT ?
	`, sinceMs, topN)
	if err != nil {
		return nil, nil, err
	}
	top := map[string]bool{}
	var seriesOrdered []string
	for rows.Next() {
		var name string
		var n int64
		if err := rows.Scan(&name, &n); err != nil {
			rows.Close()
			return nil, nil, err
		}
		top[name] = true
		seriesOrdered = append(seriesOrdered, name)
	}
	rows.Close()

	// Now bucket every row, collapsing non-top series into "other".
	rows, err = s.Query(`
		SELECT (ts / ?) * ? AS bucket_ts,
		       COALESCE(NULLIF(`+dimCol+`, ''), '(unknown)') AS dim,
		       COUNT(*) AS n
		FROM findings
		WHERE ts >= ?
		GROUP BY bucket_ts, dim
		ORDER BY bucket_ts ASC
	`, bucketMs, bucketMs, sinceMs)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	hasOther := false
	bucketMap := map[int64]map[string]int64{}
	for rows.Next() {
		var bts, n int64
		var name string
		if err := rows.Scan(&bts, &name, &n); err != nil {
			return nil, nil, err
		}
		series := name
		if !top[series] {
			series = "other"
			hasOther = true
		}
		if bucketMap[bts] == nil {
			bucketMap[bts] = map[string]int64{}
		}
		bucketMap[bts][series] += n
	}
	if hasOther {
		seriesOrdered = append(seriesOrdered, "other")
	}

	out := make([]FindingsTimeSeriesBucket, 0, len(bucketMap))
	for ts, by := range bucketMap {
		out = append(out, FindingsTimeSeriesBucket{Ts: ts, By: by})
	}
	return out, seriesOrdered, nil
}

// HostFindingCount is one row of the dashboard's top-hosts bar chart:
// for a host, how many open findings + how many of those are CRITICAL.
type HostFindingCount struct {
	Host     string
	Open     int64
	Critical int64
}

// OpenFindingsByHost returns the top-`limit` hosts by open finding
// count, descending. Hosts with no findings are excluded. Empty
// host strings collapse into "(unknown)".
func (s *Store) OpenFindingsByHost(limit int) ([]HostFindingCount, error) {
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	rows, err := s.Query(`
		SELECT COALESCE(NULLIF(host, ''), '(unknown)') AS h,
		       COUNT(*) AS open_n,
		       SUM(CASE WHEN UPPER(severity) = 'CRITICAL' THEN 1 ELSE 0 END) AS crit_n
		FROM findings
		WHERE status = 'open'
		GROUP BY h
		ORDER BY open_n DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HostFindingCount
	for rows.Next() {
		var r HostFindingCount
		if err := rows.Scan(&r.Host, &r.Open, &r.Critical); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
