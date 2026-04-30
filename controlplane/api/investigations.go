package api

import (
	"encoding/json"
	"errors"
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
		findings, _ := store.ListFindingsForInvestigation(id)
		runs, _ := store.ListRunsForInvestigation(id)
		notes, _ := store.ListInvestigationNotes(id)
		// Empty slices instead of nil so JSON output is consistent.
		if findings == nil {
			findings = []int64{}
		}
		if runs == nil {
			runs = []int64{}
		}
		if notes == nil {
			notes = []db.InvestigationNote{}
		}
		resp := map[string]any{
			"investigation": inv,
			"findings":      findings,
			"runs":          runs,
			"notes":         notes,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
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
