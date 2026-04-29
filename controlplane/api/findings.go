package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/section9labs/okesu/controlplane/audit"
	"github.com/section9labs/okesu/controlplane/auth"
	"github.com/section9labs/okesu/controlplane/db"
)

type findingJSON struct {
	ID             int64           `json:"id"`
	EventID        int64           `json:"event_id"`
	Ts             int64           `json:"ts"`
	Agent          string          `json:"agent,omitempty"`
	Host           string          `json:"host,omitempty"`
	// Severity is the EFFECTIVE severity — operator override if present,
	// otherwise the agent's original assignment. Existing UI consumers
	// can keep treating this as "the severity" without code changes.
	Severity       string          `json:"severity,omitempty"`
	// OriginalSeverity is the agent's untouched assignment. Set even when
	// no override exists (equal to Severity in that case) so consumers
	// don't need to special-case.
	OriginalSeverity string        `json:"original_severity,omitempty"`
	// OperatorSeverity is set iff an override is in effect; the absence
	// of this field is the signal that severity = original.
	OperatorSeverity string        `json:"operator_severity,omitempty"`
	SeverityOverrideAt string      `json:"severity_override_at,omitempty"`
	Title          string          `json:"title,omitempty"`
	Resource       string          `json:"resource,omitempty"`
	Evidence       string          `json:"evidence,omitempty"`
	DedupKey       string          `json:"dedup_key,omitempty"`
	// Fingerprint is the canonical key (dedup_key when set, else
	// title|severity|agent). Used as the identifier for severity rules.
	Fingerprint    string          `json:"fingerprint,omitempty"`
	Acknowledged   bool            `json:"acknowledged"`
	AckedAt        string          `json:"acknowledged_at,omitempty"`
	AckedBy        int64           `json:"acknowledged_by,omitempty"`
	AckNote        string          `json:"ack_note,omitempty"`
	CreatedAt      string          `json:"created_at"`
	Raw            json.RawMessage `json:"raw,omitempty"`

	// Phase 13 — triage state.
	Status         string `json:"status,omitempty"` // open|acknowledged|investigating|resolved|false_positive|wontfix
	TriageNote     string `json:"triage_note,omitempty"`
	TriagedAt      string `json:"triaged_at,omitempty"`
	TriagedByEmail string `json:"triaged_by_email,omitempty"`

	// Phase 12 enrichment.
	Category        string          `json:"category,omitempty"`
	ProcessPID      int64           `json:"process_pid,omitempty"`
	ProcessName     string          `json:"process_name,omitempty"`
	Path            string          `json:"path,omitempty"`
	NetworkEndpoint string          `json:"network_endpoint,omitempty"`
	CVE             string          `json:"cve,omitempty"`
	Tags            []string        `json:"tags,omitempty"`
	Attributes      json.RawMessage `json:"attributes,omitempty"`

	// Phase 9.6 — federation source. Populated only when this row was
	// fetched from a federated child CP. Local rows leave this nil so
	// the UI can render a "from <CP>" chip iff non-null.
	CPSource *CPSourceRef `json:"cp_source,omitempty"`
}

// CPSourceRef tags a row with the child CP it came from. Stable across
// federated reads — InstanceID is the parent's only durable handle on
// a child (URL can change, display_name is cosmetic).
type CPSourceRef struct {
	InstanceID  string `json:"instance_id"`
	DisplayName string `json:"display_name,omitempty"`
	Region      string `json:"region,omitempty"`
}

func toFindingJSON(f *db.Finding, includeRaw bool) findingJSON {
	out := findingJSON{
		ID:               f.ID,
		EventID:          f.EventID,
		Ts:               f.Ts,
		Agent:            f.Agent.String,
		Host:             f.Host.String,
		Severity:         f.EffectiveSeverity(),
		OriginalSeverity: f.Severity.String,
		Title:            f.Title.String,
		Resource:         f.Resource.String,
		Evidence:         f.Evidence.String,
		DedupKey:         f.DedupKey.String,
		Fingerprint:      db.FingerprintForFinding(f.DedupKey.String, f.Title.String, f.Severity.String, f.Agent.String),
		Acknowledged:     f.Acknowledged,
		AckNote:          f.AckNote.String,
		CreatedAt:        f.CreatedAt.UTC().Format(time.RFC3339),

		Category:        f.Category.String,
		ProcessName:     f.ProcessName.String,
		Path:            f.Path.String,
		NetworkEndpoint: f.NetworkEndpoint.String,
		CVE:             f.CVE.String,
	}
	if f.OperatorSeverity.Valid && f.OperatorSeverity.String != "" {
		out.OperatorSeverity = f.OperatorSeverity.String
	}
	if f.SeverityOverrideAt.Valid {
		out.SeverityOverrideAt = f.SeverityOverrideAt.Time.UTC().Format(time.RFC3339)
	}
	if f.ProcessPID.Valid {
		out.ProcessPID = f.ProcessPID.Int64
	}
	if f.Tags.Valid && f.Tags.String != "" {
		for _, t := range strings.Split(f.Tags.String, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				out.Tags = append(out.Tags, t)
			}
		}
	}
	if f.Attributes.Valid && f.Attributes.String != "" {
		out.Attributes = json.RawMessage(f.Attributes.String)
	}
	if f.AckedAt.Valid {
		out.AckedAt = f.AckedAt.Time.UTC().Format(time.RFC3339)
	}
	if f.AckedBy.Valid {
		out.AckedBy = f.AckedBy.Int64
	}
	if f.Status.Valid {
		out.Status = f.Status.String
	}
	if out.Status == "" {
		out.Status = "open"
	}
	if f.TriageNote.Valid {
		out.TriageNote = f.TriageNote.String
	}
	if f.TriagedAt.Valid {
		out.TriagedAt = f.TriagedAt.Time.UTC().Format(time.RFC3339)
	}
	if f.TriagedByEmail.Valid {
		out.TriagedByEmail = f.TriagedByEmail.String
	}
	if includeRaw {
		out.Raw = json.RawMessage(f.RawJSON)
	}
	return out
}

// FindingsList handles GET /api/findings with filtering query params.
//
// Query params:
//
//	severity=CRITICAL,HIGH      comma-separated list
//	agent=edr
//	host=prod-web-01
//	since=<unix-ms>             ts >= since
//	until=<unix-ms>             ts <  until
//	state=open|acked|all        default open
//	limit=N (1..1000, default 100)
//	offset=N
func FindingsList(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		f := db.FindingFilter{
			Agent:    q.Get("agent"),
			Host:     q.Get("host"),
			Category: q.Get("category"),
			Tag:      q.Get("tag"),
		}
		if s := q.Get("severity"); s != "" {
			for _, p := range strings.Split(s, ",") {
				p = strings.TrimSpace(p)
				if p != "" {
					f.Severities = append(f.Severities, p)
				}
			}
		}
		f.SinceMs, _ = strconv.ParseInt(q.Get("since"), 10, 64)
		f.UntilMs, _ = strconv.ParseInt(q.Get("until"), 10, 64)
		f.Limit, _ = strconv.Atoi(q.Get("limit"))
		f.Offset, _ = strconv.Atoi(q.Get("offset"))

		switch q.Get("state") {
		case "", "open":
			f.OnlyOpen = true
		case "acked":
			f.OnlyAcked = true
		case "all":
			// no filter
		case "queue":
			f.OnlyQueue = true
		}

		findings, err := store.ListFindings(f)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := make([]findingJSON, 0, len(findings))
		for _, fr := range findings {
			out = append(out, toFindingJSON(fr, false))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// FindingDetail handles GET /api/findings/{id}.
func FindingDetail(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := chi.URLParam(r, "id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		fr, err := store.FindingByID(id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toFindingJSON(fr, true))
	}
}

// FindingsSummary handles GET /api/findings/summary.
func FindingsSummary(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		s, err := store.FindingsSummary()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(s)
	}
}

// FindingsGrouped handles GET /api/findings/grouped — open findings collapsed
// by dedup_key (or title+severity+agent fallback) so the dashboard can show
// one row per distinct issue regardless of how many hosts reported it.
//
// Same severity / agent / host / since query params as the list endpoint.
func FindingsGrouped(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		filter := db.GroupedFindingsFilter{
			Agent:    q.Get("agent"),
			Host:     q.Get("host"),
			Category: q.Get("category"),
			Tag:      q.Get("tag"),
		}
		if v := q.Get("severity"); v != "" {
			filter.Severities = strings.Split(v, ",")
		}
		if v := q.Get("since"); v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				filter.SinceMs = n
			}
		}
		if v := q.Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				filter.Limit = n
			}
		}
		groups, err := store.GroupedFindings(filter)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Coerce nil → empty array so the JSON shape is always consistent.
		if groups == nil {
			groups = []*db.FindingGroup{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(groups)
	}
}

// FindingAcknowledge handles POST /api/findings/{id}/acknowledge with body
// {"note": "..."} | {"clear": true}.
func FindingAcknowledge(store *db.Store) http.HandlerFunc {
	type req struct {
		Note  string `json:"note,omitempty"`
		Clear bool   `json:"clear,omitempty"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		u := auth.UserFromContext(r.Context())
		if u == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		idStr := chi.URLParam(r, "id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		var body req
		_ = json.NewDecoder(r.Body).Decode(&body) // empty body OK

		userID := u.ID
		if body.Clear {
			userID = 0
		}
		if err := store.AcknowledgeFinding(id, userID, body.Note); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		fr, err := store.FindingByID(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		action := "finding.acknowledge"
		if body.Clear {
			action = "finding.unacknowledge"
		}
		audit.Emit(r, store, db.AuditEntry{
			Action:   action,
			Target:   fmt.Sprintf("finding:%d", id),
			Metadata: map[string]any{"severity": fr.Severity.String, "title": fr.Title.String, "note": body.Note},
		})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toFindingJSON(fr, true))
	}
}

// FindingsGroupAcknowledge handles POST /api/findings/group/acknowledge.
// Acks every open finding sharing the same composite group key. Body:
//
//	{
//	  "dedup_key": "..."        // preferred — single field is enough
//	  // OR (when dedup_key is empty):
//	  "title":    "...",
//	  "severity": "...",
//	  "agent":    "...",
//	  "note":     "operator note"
//	}
//
// Audit-logs the count.
func FindingsGroupAcknowledge(store *db.Store) http.HandlerFunc {
	type req struct {
		DedupKey string `json:"dedup_key"`
		Title    string `json:"title"`
		Severity string `json:"severity"`
		Agent    string `json:"agent"`
		Note     string `json:"note"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		u := auth.UserFromContext(r.Context())
		if u == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var body req
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if body.DedupKey == "" && (body.Title == "" || body.Severity == "" || body.Agent == "") {
			http.Error(w, "either dedup_key or (title, severity, agent) required", http.StatusBadRequest)
			return
		}
		n, err := store.AcknowledgeGroup(body.DedupKey, body.Title, body.Severity, body.Agent, u.ID, body.Note)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		audit.Emit(r, store, db.AuditEntry{
			Action: "finding.group_acknowledge",
			Target: fmt.Sprintf("group:%s", coalesce(body.DedupKey, body.Title)),
			Metadata: map[string]any{
				"dedup_key":   body.DedupKey,
				"title":       body.Title,
				"severity":    body.Severity,
				"agent":       body.Agent,
				"acked_count": n,
				"note":        body.Note,
			},
		})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"acked": n})
	}
}

func coalesce(a ...string) string {
	for _, v := range a {
		if v != "" {
			return v
		}
	}
	return ""
}

// FindingSetStatus handles POST /api/findings/{id}/status.
//
//	{ "status": "false_positive", "note": "internal mining proxy, expected" }
//
// Setting status='open' clears triage state. Audit-logged. Other writers
// (FindingAcknowledge below) remain as thin shims for backward compat.
func FindingSetStatus(store *db.Store) http.HandlerFunc {
	type req struct {
		Status string `json:"status"`
		Note   string `json:"note"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		u := auth.UserFromContext(r.Context())
		if u == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		var body req
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if !db.IsValidFindingStatus(body.Status) {
			http.Error(w, "status must be one of open|acknowledged|investigating|resolved|false_positive|wontfix", http.StatusBadRequest)
			return
		}
		if err := store.SetFindingStatus(id, body.Status, u.ID, u.Email, body.Note); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		fr, err := store.FindingByID(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		audit.Emit(r, store, db.AuditEntry{
			Action: "finding.status_change",
			Target: fmt.Sprintf("finding:%d", id),
			Metadata: map[string]any{
				"status":   body.Status,
				"severity": fr.Severity.String,
				"title":    fr.Title.String,
				"note":     body.Note,
			},
		})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toFindingJSON(fr, true))
	}
}

// FindingsGroupSetStatus handles POST /api/findings/group/status.
// Same body as the individual endpoint plus group identifier:
//
//	{ "dedup_key": "...", "status": "false_positive", "note": "..." }
//	OR
//	{ "title": "...", "severity": "...", "agent": "...",
//	  "status": "...", "note": "..." }
func FindingsGroupSetStatus(store *db.Store) http.HandlerFunc {
	type req struct {
		DedupKey string `json:"dedup_key"`
		Title    string `json:"title"`
		Severity string `json:"severity"`
		Agent    string `json:"agent"`
		Status   string `json:"status"`
		Note     string `json:"note"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		u := auth.UserFromContext(r.Context())
		if u == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var body req
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if !db.IsValidFindingStatus(body.Status) {
			http.Error(w, "status must be one of open|acknowledged|investigating|resolved|false_positive|wontfix", http.StatusBadRequest)
			return
		}
		if body.DedupKey == "" && (body.Title == "" || body.Severity == "" || body.Agent == "") {
			http.Error(w, "either dedup_key or (title, severity, agent) required", http.StatusBadRequest)
			return
		}
		n, err := store.SetGroupStatus(body.DedupKey, body.Title, body.Severity, body.Agent,
			body.Status, u.ID, u.Email, body.Note)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		audit.Emit(r, store, db.AuditEntry{
			Action: "finding.group_status_change",
			Target: fmt.Sprintf("group:%s", coalesce(body.DedupKey, body.Title)),
			Metadata: map[string]any{
				"dedup_key":   body.DedupKey,
				"title":       body.Title,
				"severity":    body.Severity,
				"agent":       body.Agent,
				"status":      body.Status,
				"changed":     n,
				"note":        body.Note,
			},
		})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"changed": n, "status": body.Status})
	}
}

// FindingSetSeverity handles POST /api/findings/{id}/severity.
//
//	{ "severity": "LOW", "apply_to_fingerprint": false }
//
// `apply_to_fingerprint=true` also writes a finding_severity_rules row
// keyed on the finding's fingerprint, so future occurrences inherit the
// override automatically. Pass severity="" to clear the override.
func FindingSetSeverity(store *db.Store) http.HandlerFunc {
	type req struct {
		Severity           string `json:"severity"`
		ApplyToFingerprint bool   `json:"apply_to_fingerprint,omitempty"`
		ApplyToGroup       bool   `json:"apply_to_group,omitempty"`
		Note               string `json:"note,omitempty"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		u := auth.UserFromContext(r.Context())
		if u == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		var body req
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if body.Severity != "" && !db.IsValidSeverity(body.Severity) {
			http.Error(w, "severity must be one of CRITICAL|HIGH|MEDIUM|LOW|INFO", http.StatusBadRequest)
			return
		}
		fr, err := store.FindingByID(id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		canon := db.CanonicalSeverity(body.Severity)

		var groupAffected int64
		if body.ApplyToGroup {
			n, err := store.SetGroupSeverity(
				fr.DedupKey.String, fr.Title.String, fr.Severity.String, fr.Agent.String,
				canon, u.ID,
			)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			groupAffected = n
		} else if err := store.SetFindingSeverity(id, canon, u.ID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		fp := db.FingerprintForFinding(
			fr.DedupKey.String, fr.Title.String, fr.Severity.String, fr.Agent.String,
		)
		if body.ApplyToFingerprint {
			if canon == "" {
				if err := store.DeleteSeverityRule(fp); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
			} else if err := store.UpsertSeverityRule(fp, canon, body.Note, u.ID); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}

		// Re-fetch so the returned shape reflects the post-update state.
		updated, err := store.FindingByID(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		audit.Emit(r, store, db.AuditEntry{
			Action: "finding.severity_override",
			Target: fmt.Sprintf("finding:%d", id),
			Metadata: map[string]any{
				"fingerprint":          fp,
				"original_severity":    fr.Severity.String,
				"new_severity":         canon,
				"apply_to_fingerprint": body.ApplyToFingerprint,
				"apply_to_group":       body.ApplyToGroup,
				"group_affected":       groupAffected,
				"note":                 body.Note,
			},
		})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toFindingJSON(updated, true))
	}
}

// SeverityRulesList handles GET /api/findings/severity-rules.
func SeverityRulesList(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		rules, err := store.ListSeverityRules()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := make([]map[string]any, 0, len(rules))
		for _, r := range rules {
			row := map[string]any{
				"fingerprint": r.Fingerprint,
				"severity":    r.Severity,
				"created_at":  r.CreatedAt.UTC().Format(time.RFC3339),
			}
			if r.Note.Valid {
				row["note"] = r.Note.String
			}
			if r.UpdatedAt.Valid {
				row["updated_at"] = r.UpdatedAt.Time.UTC().Format(time.RFC3339)
			}
			if r.CreatedBy.Valid {
				row["created_by"] = r.CreatedBy.Int64
			}
			out = append(out, row)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// SeverityRuleDelete handles DELETE /api/findings/severity-rules — body
// `{"fingerprint": "..."}`. Path-encoding fingerprints with `|`/`/` in
// them is messy, so we accept the fingerprint in the JSON body instead.
func SeverityRuleDelete(store *db.Store) http.HandlerFunc {
	type req struct {
		Fingerprint string `json:"fingerprint"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		u := auth.UserFromContext(r.Context())
		if u == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var body req
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if body.Fingerprint == "" {
			http.Error(w, "fingerprint required", http.StatusBadRequest)
			return
		}
		if err := store.DeleteSeverityRule(body.Fingerprint); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		audit.Emit(r, store, db.AuditEntry{
			Action: "finding.severity_rule_delete",
			Target: "rule:" + body.Fingerprint,
		})
		w.WriteHeader(http.StatusNoContent)
	}
}
