package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/section9labs/okesu/agent"
	"github.com/section9labs/okesu/controlplane/audit"
	"github.com/section9labs/okesu/controlplane/auth"
	"github.com/section9labs/okesu/controlplane/db"
	"github.com/section9labs/okesu/controlplane/ports"
)

// FindingIngestRequest is the body shape for /api/findings/ingest. Mirrors
// the agent finding event but accepts JSON (not JSONL) and lets the
// timestamp be omitted (defaults to now).
type FindingIngestRequest struct {
	Ts                int64  `json:"ts,omitempty"`
	Agent             string `json:"agent"`
	Host              string `json:"host"`
	Severity          string `json:"severity"`
	Title             string `json:"title"`
	Resource          string `json:"resource,omitempty"`
	Evidence          string `json:"evidence,omitempty"`
	RecommendedAction string `json:"recommended_action,omitempty"`
	DedupKey          string `json:"dedup_key,omitempty"`

	// Phase 12 enrichment — optional for external producers.
	Category        string                 `json:"category,omitempty"`
	ProcessPID      int64                  `json:"process_pid,omitempty"`
	ProcessName     string                 `json:"process_name,omitempty"`
	Path            string                 `json:"path,omitempty"`
	NetworkEndpoint string                 `json:"network_endpoint,omitempty"`
	CVE             string                 `json:"cve,omitempty"`
	Tags            []string               `json:"tags,omitempty"`
	Attributes      map[string]interface{} `json:"attributes,omitempty"`
}

// FindingIngest accepts a finding from a non-okesu source (CI, scanner, etc.)
// authenticated by an API token with scope "findings:write".
//
// The finding is normalized into the internal event format, persisted to
// both the events and findings tables, and re-broadcast on the SSE stream
// — so the rest of the system (notifications, dashboard, drill-down) treats
// it identically to a daemon-emitted finding.
func FindingIngest(store *db.Store, eventStore ports.EventStore, bcast Broadcaster) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 256<<10))
		if err != nil {
			http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
			return
		}
		var req FindingIngestRequest
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		if req.Title == "" {
			http.Error(w, "title is required", http.StatusBadRequest)
			return
		}
		req.Severity = strings.ToUpper(req.Severity)
		switch req.Severity {
		case "CRITICAL", "HIGH", "MEDIUM", "LOW", "INFO", "":
		default:
			http.Error(w, "severity must be CRITICAL|HIGH|MEDIUM|LOW|INFO", http.StatusBadRequest)
			return
		}
		if req.Severity == "" {
			req.Severity = "INFO"
		}
		if req.Ts == 0 {
			req.Ts = time.Now().UnixMilli()
		}
		if req.Agent == "" {
			// Default to the token's name so deliveries-log shows the source.
			if t := auth.TokenFromContext(r.Context()); t != nil {
				req.Agent = "token:" + t.Name
			} else {
				req.Agent = "external"
			}
		}

		// Build the canonical event JSON. Adding "type":"finding" lets the
		// existing webhook projection (api/webhook.go) treat this identically
		// to an agent-emitted finding.
		canonical := map[string]any{
			"type":               "finding",
			"ts":                 req.Ts,
			"agent":              req.Agent,
			"host":               req.Host,
			"severity":           req.Severity,
			"title":              req.Title,
			"resource":           req.Resource,
			"evidence":           req.Evidence,
			"recommended_action": req.RecommendedAction,
			"dedup_key":          req.DedupKey,
		}
		raw, err := json.Marshal(canonical)
		if err != nil {
			http.Error(w, "marshal: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Persist to events via the EventStore port (Phase 8c).
		eventID, err := eventStore.Insert(r.Context(), ports.EventRecord{
			Ts:       req.Ts,
			Type:     "finding",
			Agent:    req.Agent,
			Host:     req.Host,
			Severity: req.Severity,
			Title:    req.Title,
			RawJSON:  string(raw),
		})
		if err != nil {
			http.Error(w, "store event: "+err.Error(), http.StatusInternalServerError)
			return
		}
		// Project into findings. Re-normalize the title server-side so
		// external producers benefit from the same drift fix.
		var attrJSON string
		if len(req.Attributes) > 0 {
			if b, err := json.Marshal(req.Attributes); err == nil {
				attrJSON = string(b)
			}
		}
		findingID, err := store.InsertFinding(&db.FindingInsert{
			EventID:         eventID,
			Ts:              req.Ts,
			Agent:           req.Agent,
			Host:            req.Host,
			Severity:        req.Severity,
			Title:           agent.NormalizeFindingTitle(req.Title),
			Resource:        req.Resource,
			Evidence:        req.Evidence,
			DedupKey:        req.DedupKey,
			RawJSON:         string(raw),
			Category:        req.Category,
			ProcessPID:      req.ProcessPID,
			ProcessName:     req.ProcessName,
			Path:            req.Path,
			NetworkEndpoint: req.NetworkEndpoint,
			CVE:             req.CVE,
			Tags:            strings.ToLower(strings.Join(req.Tags, ",")),
			Attributes:      attrJSON,
		})
		if err != nil {
			http.Error(w, "store finding: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Broadcast — feeds notify.Worker + the live SSE stream.
		bcast.Publish(raw)

		// Audit so the source token shows up in the trail.
		actor := ""
		if t := auth.TokenFromContext(r.Context()); t != nil {
			actor = fmt.Sprintf("token:%s", t.Name)
		}
		_ = store.InsertAudit(db.AuditEntry{
			ActorEmail: actor,
			Action:     "finding.ingest",
			Target:     fmt.Sprintf("finding:%d", findingID),
			Metadata:   map[string]any{"severity": req.Severity, "title": req.Title},
		})
		_ = audit.Emit // keep the import if the caller is via cookie auth

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"event_id":   eventID,
			"finding_id": findingID,
		})
	}
}
