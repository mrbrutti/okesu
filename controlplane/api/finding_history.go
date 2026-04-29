// HTTP handlers for finding edit history + run ↔ finding linkage.
//
//   GET /api/findings/{id}/history          — audit trail rows
//   GET /api/findings/{id}/runs              — orchestration runs linked to this finding
//   GET /api/orchestration-runs/{id}/findings — every finding this run touched
//
// Read-only. Writes happen exclusively through the orchestrator's
// action dispatcher (api/orchestration_actions.go) or the existing
// triage handlers — never via these routes.

package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/section9labs/okesu/controlplane/db"
)

// findingEditJSON is the wire shape of one audit row. Empty
// optionals collapse so the UI can render compact rows.
type findingEditJSON struct {
	ID                 int64  `json:"id"`
	FindingID          int64  `json:"finding_id"`
	Field              string `json:"field"`
	OldValue           string `json:"old_value,omitempty"`
	NewValue           string `json:"new_value,omitempty"`
	Reason             string `json:"reason,omitempty"`
	EditedByUserID     int64  `json:"edited_by_user_id,omitempty"`
	EditedByEmail      string `json:"edited_by_email,omitempty"`
	OrchestrationRunID int64  `json:"orchestration_run_id,omitempty"`
	OrchestrationStep  string `json:"orchestration_step_id,omitempty"`
	EditedAt           string `json:"edited_at"`
}

func toFindingEditJSON(e db.FindingEdit) findingEditJSON {
	out := findingEditJSON{
		ID:        e.ID,
		FindingID: e.FindingID,
		Field:     e.Field,
		EditedAt:  e.EditedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if e.OldValue.Valid {
		out.OldValue = e.OldValue.String
	}
	if e.NewValue.Valid {
		out.NewValue = e.NewValue.String
	}
	if e.Reason.Valid {
		out.Reason = e.Reason.String
	}
	if e.EditedByUserID.Valid {
		out.EditedByUserID = e.EditedByUserID.Int64
	}
	if e.EditedByEmail.Valid {
		out.EditedByEmail = e.EditedByEmail.String
	}
	if e.OrchestrationRunID.Valid {
		out.OrchestrationRunID = e.OrchestrationRunID.Int64
	}
	if e.OrchestrationStep.Valid {
		out.OrchestrationStep = e.OrchestrationStep.String
	}
	return out
}

type findingRunLinkJSON struct {
	FindingID          int64  `json:"finding_id"`
	OrchestrationRunID int64  `json:"orchestration_run_id"`
	StepID             string `json:"step_id,omitempty"`
	LinkedAt           string `json:"linked_at"`
}

func toFindingRunLinkJSON(l db.FindingRunLink) findingRunLinkJSON {
	out := findingRunLinkJSON{
		FindingID:          l.FindingID,
		OrchestrationRunID: l.OrchestrationRunID,
		LinkedAt:           l.LinkedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if l.StepID.Valid {
		out.StepID = l.StepID.String
	}
	return out
}

// FindingHistory — GET /api/findings/{id}/history
func FindingHistory(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		rows, err := store.ListFindingEdits(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := make([]findingEditJSON, 0, len(rows))
		for _, e := range rows {
			out = append(out, toFindingEditJSON(e))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// FindingLinkedRuns — GET /api/findings/{id}/runs
func FindingLinkedRuns(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		rows, err := store.ListFindingRunLinks(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := make([]findingRunLinkJSON, 0, len(rows))
		for _, l := range rows {
			out = append(out, toFindingRunLinkJSON(l))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// RunLinkedFindings — GET /api/orchestration-runs/{id}/findings
func RunLinkedFindings(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		rows, err := store.ListRunLinkedFindings(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := make([]findingRunLinkJSON, 0, len(rows))
		for _, l := range rows {
			out = append(out, toFindingRunLinkJSON(l))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}
