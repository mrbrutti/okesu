package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

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
			_ = store.LinkFindingToInvestigation(id, req.FromFindingID)
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
		id, err := investigationIDFromPath(r.URL.Path, "/api/investigations/")
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
func AddInvestigationNoteHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := investigationIDFromPath(r.URL.Path, "/api/investigations/")
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
		noteID, err := store.AddInvestigationNote(id, req.Author, req.Body)
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
		id, err := investigationIDFromPath(r.URL.Path, "/api/investigations/")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		inv, err := store.GetInvestigation(id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		findings, _ := store.ListFindingsForInvestigationEnriched(id)
		runs, _ := store.ListRunsForInvestigationEnriched(id)
		iocs, _ := store.ListIOCsForInvestigation(id)
		daimons, _ := store.ListDaimonsForInvestigation(id)
		orchs, _ := store.ListOrchestrationsForInvestigation(id)
		notes, _ := store.ListInvestigationNotes(id)
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
			"investigation":   inv,
			"findings":        findings,
			"runs":            runs,
			"iocs":            iocs,
			"daimons":         daimons,
			"orchestrations":  orchs,
			"notes":           notes,
			"war_room":        warRoom,
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
		// Path: /api/findings/{id}/investigations — extract the {id}.
		segs := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/findings/"), "/")
		if len(segs) < 2 || segs[1] != "investigations" {
			http.Error(w, "path must be /api/findings/{id}/investigations", http.StatusBadRequest)
			return
		}
		findingID, err := strconv.ParseInt(segs[0], 10, 64)
		if err != nil {
			http.Error(w, "bad finding id", http.StatusBadRequest)
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
		invID, runID, err := invAndChildID(r.URL.Path, "/api/investigations/", "runs")
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
		invID, fid, err := invAndChildID(r.URL.Path, "/api/investigations/", "findings")
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
		invID, runID, err := invAndChildID(r.URL.Path, "/api/investigations/", "runs")
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

// invAndChildID parses /api/investigations/{id}/{kind}/{child} into
// (invID, childID). Returns an error on shape mismatch or
// non-integer ids. Tolerant of the kind keyword being either
// "findings" or "runs".
func invAndChildID(path, prefix, kind string) (int64, int64, error) {
	rest := strings.TrimPrefix(path, prefix)
	segs := strings.Split(rest, "/")
	if len(segs) < 3 || segs[1] != kind {
		return 0, 0, fmt.Errorf("path must match /api/investigations/{id}/%s/{child_id}", kind)
	}
	invID, err := strconv.ParseInt(segs[0], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("bad investigation id: %w", err)
	}
	childID, err := strconv.ParseInt(segs[2], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("bad %s id: %w", kind, err)
	}
	return invID, childID, nil
}

// LinkFindingToInvestigationHandler adds a finding to an investigation.
// Path: /api/investigations/{id}/findings/{finding_id}.
func LinkFindingToInvestigationHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		segs := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/investigations/"), "/")
		if len(segs) < 3 || segs[1] != "findings" {
			http.Error(w, "bad path", http.StatusBadRequest)
			return
		}
		invID, err := strconv.ParseInt(segs[0], 10, 64)
		if err != nil {
			http.Error(w, "bad investigation id", http.StatusBadRequest)
			return
		}
		findingID, err := strconv.ParseInt(segs[2], 10, 64)
		if err != nil {
			http.Error(w, "bad finding id", http.StatusBadRequest)
			return
		}
		if err := store.LinkFindingToInvestigation(invID, findingID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// investigationIDFromPath strips the prefix and parses the next segment
// as the investigation id. Tolerant of trailing path segments (e.g.
// "/api/investigations/42/notes" yields 42).
func investigationIDFromPath(path, prefix string) (int64, error) {
	rest := strings.TrimPrefix(path, prefix)
	if rest == "" {
		return 0, errors.New("investigation id required")
	}
	first := rest
	if i := strings.Index(rest, "/"); i >= 0 {
		first = rest[:i]
	}
	id, err := strconv.ParseInt(first, 10, 64)
	if err != nil {
		return 0, errors.New("bad investigation id")
	}
	return id, nil
}
