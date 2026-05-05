package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/section9labs/okesu/controlplane/auth"
	"github.com/section9labs/okesu/controlplane/db"
)

// CreateInvestigationHandler creates a new investigation in the active
// state. Returns the inserted record (201). Optional payload field
// `from_finding_id` links the finding immediately after creation —
// reduces the 2-call dance for the most common create path (operator
// promotes a finding to a case).
func CreateInvestigationHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Title         string `json:"title"`
			Summary       string `json:"summary"`
			CreatedBy     string `json:"created_by"`
			FromFindingID int64  `json:"from_finding_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		if req.Title == "" {
			http.Error(w, "title is required", http.StatusBadRequest)
			return
		}
		id, err := store.CreateInvestigation(&db.InvestigationInsert{
			Title:     req.Title,
			Summary:   req.Summary,
			CreatedBy: req.CreatedBy,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if req.FromFindingID > 0 {
			// Soft-fail: investigation exists; if the link doesn't take
			// (e.g. the finding was deleted between calls) the operator
			// can still PUT it via /findings/{finding_id}.
			by := req.CreatedBy
			if by == "" {
				by = actorFromRequest(r)
			}
			_ = store.LinkFindingToInvestigationWithProvenance(
				id, req.FromFindingID, db.LinkMethodAutoPromote, by,
			)
		}
		got, err := store.GetInvestigation(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(got)
	}
}

// UpdateInvestigationHandler patches an investigation. Path:
// /api/investigations/{id}. Body: any subset of title, status,
// resolution, summary.
func UpdateInvestigationHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := investigationIDFromChi(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var req struct {
			Title      string `json:"title"`
			Status     string `json:"status"`
			Resolution string `json:"resolution"`
			Summary    string `json:"summary"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := store.UpdateInvestigation(id, &db.InvestigationUpdate{
			Title:      req.Title,
			Status:     req.Status,
			Resolution: req.Resolution,
			Summary:    req.Summary,
		}); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		got, _ := store.GetInvestigation(id)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(got)
	}
}

// AddInvestigationNoteHandler appends a note. Path:
// /api/investigations/{id}/notes.
//
// Author is derived from the authenticated session — clients used
// to be required to send `author` in the body, but every UI surface
// that actually wires this endpoint already has the session, and
// asking the client to remember its own email both invites
// impersonation and broke notes posting in the case workspace
// (the body never sent `author`, so the request failed with
// "author is required" and notes silently never appeared).
//
// Optional `author` in the body is still respected as a back-compat
// hatch for scripted/automation callers, but the session always
// wins when both are present so an admin token can't claim to be a
// different operator.
func AddInvestigationNoteHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := investigationIDFromChi(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var req struct {
			Author string `json:"author"`
			Body   string `json:"body"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		author := ""
		if u := auth.UserFromContext(r.Context()); u != nil {
			author = u.Email
		}
		if author == "" {
			author = req.Author // automation back-compat
		}
		noteID, err := store.AddInvestigationNote(id, author, req.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": noteID})
	}
}

// ListInvestigationsHandler returns recent investigations, filtered by
// optional status query param.
func ListInvestigationsHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status := r.URL.Query().Get("status")
		limit := 100
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				limit = n
			}
		}
		items, err := store.ListInvestigations(status, limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(items)
	}
}

// GetInvestigationHandler returns an investigation with its linked
// findings, runs, and notes.
// GetInvestigationHandler returns the case bundle: the investigation
// row plus enriched lists for the workspace tabs (findings, runs,
// IOCs, daimons, orchestrations, notes) and a war_room flag derived
// from the linked findings' tags.
//
// Wire shape:
//
//	{ investigation, findings: [...], runs: [...],
//	  iocs: [...], daimons: [...], orchestrations: [...],
//	  notes: [...], war_room: bool }
//
// Each list contains *objects*, not just IDs, so the UI can render
// every tab without N+1 fetches. Truncations are applied at the DB
// layer (e.g. run prompts capped at 240 chars) to keep the payload
// small for cases that link hundreds of rows.
func GetInvestigationHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := investigationIDFromChi(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		inv, err := store.GetInvestigation(id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		var warnings []string
		recordWarn := func(name string, err error) {
			if err == nil {
				return
			}
			log.Printf("investigation bundle %d: %s: %v", id, name, err)
			warnings = append(warnings, fmt.Sprintf("%s: %s", name, err.Error()))
		}

		findings, fErr := store.ListFindingsForInvestigationEnriched(id)
		recordWarn("findings", fErr)
		runs, rErr := store.ListRunsForInvestigationEnriched(id)
		recordWarn("runs", rErr)
		iocs, iErr := store.ListIOCsForInvestigation(id)
		recordWarn("iocs", iErr)
		daimons, dErr := store.ListDaimonsForInvestigation(id)
		recordWarn("daimons", dErr)
		orchs, oErr := store.ListOrchestrationsForInvestigation(id)
		recordWarn("orchestrations", oErr)
		notes, nErr := store.ListInvestigationNotes(id)
		recordWarn("notes", nErr)
		warRoom, _ := store.IsInvestigationWarRoom(id)

		// Defensive nil → empty so JSON consumers see [], not null.
		if findings == nil {
			findings = []db.InvestigationFindingItem{}
		}
		if runs == nil {
			runs = []db.InvestigationRunItem{}
		}
		if iocs == nil {
			iocs = []db.InvestigationIOCItem{}
		}
		if daimons == nil {
			daimons = []db.InvestigationDaimonItem{}
		}
		if orchs == nil {
			orchs = []db.InvestigationOrchestrationItem{}
		}
		if notes == nil {
			notes = []db.InvestigationNote{}
		}

		resp := map[string]any{
			"investigation":  inv,
			"findings":       findings,
			"runs":           runs,
			"iocs":           iocs,
			"daimons":        daimons,
			"orchestrations": orchs,
			"notes":          notes,
			"war_room":       warRoom,
		}
		if len(warnings) > 0 {
			resp["bundle_warnings"] = warnings
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// ListInvestigationsForFindingHandler returns the cases a finding
// is currently linked to. Used by the t2-hypothesis-test
// orchestration to discover which cases need a note appended after
// the test runs, and by the UI's finding-detail panel to show case
// membership.
//
// Path: /api/findings/{id}/investigations
func ListInvestigationsForFindingHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		findingID, err := childIDFromChi(r, "id")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		invs, err := store.ListInvestigationsForFinding(findingID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(invs)
	}
}

// UpsertInvestigationByDedupHandler is the entry point for daimons
// that auto-open investigations. Idempotent — given the same
// `external_key`, callers always get back the same row, so a
// background scanner can safely re-emit on every tick.
//
// Body shape:
//
//	{
//	  "external_key": "cross-cp-pattern:42",
//	  "title":        "Cross-CP IOC pattern: sha256 abc…",
//	  "summary":      "Observed on N hosts across M CPs in last 1h.",
//	  "link_findings_by_ioc_id": 42,   // optional — if set, finds every
//	                                   //   finding whose observations
//	                                   //   reference this IOC and links
//	                                   //   them to the case.
//	  "created_by":   "cross-cp-pattern-investigator"
//	}
//
// Response: 200 with `{investigation: ..., created: bool, linked_findings: N}`.
// Status code is intentionally 200 even on first-create — the caller
// is asking "give me the investigation for this dedup key", not
// "create exactly one"; 200 keeps the contract uniform across both
// outcomes. A `created` flag in the body lets the caller
// differentiate when needed.
func UpsertInvestigationByDedupHandler(store *db.Store) http.HandlerFunc {
	type req struct {
		ExternalKey         string `json:"external_key"`
		Title               string `json:"title"`
		Summary             string `json:"summary,omitempty"`
		CreatedBy           string `json:"created_by,omitempty"`
		LinkFindingsByIOCID int64  `json:"link_findings_by_ioc_id,omitempty"`
	}
	type resp struct {
		Investigation  *db.Investigation `json:"investigation"`
		Created        bool              `json:"created"`
		LinkedFindings int               `json:"linked_findings"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var in req
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if in.ExternalKey == "" {
			http.Error(w, "external_key is required", http.StatusBadRequest)
			return
		}
		inv, created, err := store.UpsertInvestigationByExternalKey(
			in.ExternalKey, in.Title, in.Summary, in.CreatedBy)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		var linked int
		if in.LinkFindingsByIOCID > 0 {
			linked, _ = store.LinkFindingsByIOCObservations(inv.ID, in.LinkFindingsByIOCID)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp{
			Investigation:  inv,
			Created:        created,
			LinkedFindings: linked,
		})
	}
}

// LinkRunToInvestigationHandler adds an orchestration_run to the
// case. PUT /api/investigations/{id}/runs/{run_id}.
func LinkRunToInvestigationHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		invID, err := investigationIDFromChi(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		runID, err := childIDFromChi(r, "run_id")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := store.LinkRunToInvestigation(invID, runID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// UnlinkFindingFromInvestigationHandler removes a finding ↔ case
// link. DELETE /api/investigations/{id}/findings/{finding_id}.
// Idempotent — 204 even when the row didn't exist.
func UnlinkFindingFromInvestigationHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		invID, err := investigationIDFromChi(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		fid, err := childIDFromChi(r, "finding_id")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := store.UnlinkFindingFromInvestigation(invID, fid); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// UnlinkRunFromInvestigationHandler removes a run ↔ case link.
// DELETE /api/investigations/{id}/runs/{run_id}. Idempotent.
func UnlinkRunFromInvestigationHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		invID, err := investigationIDFromChi(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		runID, err := childIDFromChi(r, "run_id")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := store.UnlinkRunFromInvestigation(invID, runID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// LinkFindingToInvestigationHandler adds a finding to an investigation.
// Path: /api/investigations/{id}/findings/{finding_id}. Mounted on
// the operator-facing route AND the federation route — chi.URLParam
// hides the prefix difference between the two.
//
// Records provenance: link_method=manual, linked_by=<operator email>.
// Federation requests (where the synthetic actor is "system:fed") are
// also captured — the audit trail shows that the link came in via a
// peer rather than from a local browser session.
func LinkFindingToInvestigationHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		invID, err := investigationIDFromChi(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		findingID, err := childIDFromChi(r, "finding_id")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		by := actorFromRequest(r)
		if err := store.LinkFindingToInvestigationWithProvenance(
			invID, findingID, db.LinkMethodManual, by,
		); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// actorFromRequest pulls the user email off the auth context. Empty
// string means an unauthenticated path (federation token or test);
// the caller should pass that through to nullableStr → NULL on the
// database row, not fabricate a user identity.
func actorFromRequest(r *http.Request) string {
	if u := auth.UserFromContext(r.Context()); u != nil {
		return u.Email
	}
	return ""
}

// InvestigationAuditHandler returns the case's chronological audit
// timeline — created/closed/notes/finding-linked/run-linked events
// merged from the existing tables (no new schema). Backs the
// workspace's Audit tab.
//
// Path: /api/investigations/{id}/audit
func InvestigationAuditHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		invID, err := investigationIDFromChi(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		events, err := store.ListInvestigationAudit(invID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if events == nil {
			events = []db.AuditEvent{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(events)
	}
}

// SuggestFindingsHandler returns scored finding candidates for a case's
// Suggested findings card. Filters out already-linked + per-case-
// dismissed candidates server-side.
//
// Path: /api/investigations/{id}/suggested-findings
// Query: threshold=<int> (default 30), limit=<int> (default 10)
//
// Response is a thin wrapper over the store result; signals are
// returned as a string slice so the UI can chip them. Empty list when
// the case has no linked findings (no signals to score against) or
// when nothing meets the threshold.
func SuggestFindingsHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		invID, err := investigationIDFromChi(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		threshold := 0
		if v := r.URL.Query().Get("threshold"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				threshold = n
			}
		}
		limit := 0
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				limit = n
			}
		}
		items, err := store.SuggestFindingsForInvestigation(invID, threshold, limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Defensive: ensure JSON [] not null.
		if items == nil {
			items = []db.SuggestedFinding{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(items)
	}
}

// RelatedCasesForFindingHandler returns active cases that score above
// threshold against this finding via the suggested-findings signals.
// Used by the Findings drawer's "looks related to N cases" banner.
//
// Path: /api/findings/{id}/related-cases
// Query: threshold=<int> (default 30), limit=<int> (default 5)
func RelatedCasesForFindingHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		findingID, err := childIDFromChi(r, "id")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		threshold := 0
		if v := r.URL.Query().Get("threshold"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				threshold = n
			}
		}
		limit := 0
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				limit = n
			}
		}
		items, err := store.ListRelatedCasesForFinding(findingID, threshold, limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if items == nil {
			items = []db.RelatedCase{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(items)
	}
}

// DismissSuggestedFindingHandler writes a per-case tombstone. The
// finding stops surfacing as a suggestion on this case (re-link via
// + Add lifts the tombstone — see LinkFindingToInvestigation's side
// effect).
//
// Path: /api/investigations/{id}/dismissed-findings/{finding_id}
// Body (optional): `{"dismissed_by": "user@example.com"}`
func DismissSuggestedFindingHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		invID, err := investigationIDFromChi(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		findingID, err := childIDFromChi(r, "finding_id")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var body struct {
			DismissedBy string `json:"dismissed_by"`
		}
		// Body is optional — bare DELETE with no body is fine; ignore
		// decode errors so an empty/missing body doesn't 400.
		_ = json.NewDecoder(r.Body).Decode(&body)
		if err := store.DismissSuggestedFinding(invID, findingID, body.DismissedBy); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// investigationIDFromChi reads the {id} URL parameter and parses it
// to int64. Replaces the older path-prefix parser so a single
// handler can be mounted under both `/api/investigations/{id}` and
// `/api/v1/federation/investigations/{id}` without prefix-aware
// dispatching — chi already binds the parameter the same way for
// both routes.
func investigationIDFromChi(r *http.Request) (int64, error) {
	raw := chi.URLParam(r, "id")
	if raw == "" {
		return 0, errors.New("investigation id required")
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("bad investigation id: %w", err)
	}
	return id, nil
}

// childIDFromChi reads a sibling URL parameter (`{finding_id}` or
// `{run_id}`) and parses it. Used by the link/unlink handlers on
// both the operator-facing route and the federation route.
func childIDFromChi(r *http.Request, paramName string) (int64, error) {
	raw := chi.URLParam(r, paramName)
	if raw == "" {
		return 0, fmt.Errorf("%s required", paramName)
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("bad %s: %w", paramName, err)
	}
	return id, nil
}

