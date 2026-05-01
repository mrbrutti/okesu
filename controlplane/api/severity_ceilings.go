// Severity-ceiling admin API. Phase 22.10 PR γ.
//
// CRUD over severity_ceilings. Read is admin-gated at the route
// layer (the table is small but exposes the operator's labelling
// strategy, so it stays admin-only). Mutations are admin-only too.

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/section9labs/okesu/controlplane/audit"
	"github.com/section9labs/okesu/controlplane/db"
)

// ListSeverityCeilingsHandler — GET /api/severity-ceilings
func ListSeverityCeilingsHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		out, err := store.ListSeverityCeilings()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// CreateSeverityCeilingHandler — POST /api/severity-ceilings
// Body: {selector, max_severity, reason?}
func CreateSeverityCeilingHandler(store *db.Store) http.HandlerFunc {
	type req struct {
		Selector    string `json:"selector"`
		MaxSeverity string `json:"max_severity"`
		Reason      string `json:"reason"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var body req
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		id, err := store.CreateSeverityCeiling(&db.SeverityCeiling{
			Selector:    body.Selector,
			MaxSeverity: body.MaxSeverity,
			Reason:      body.Reason,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		audit.Emit(r, store, db.AuditEntry{
			Action: "severity_ceiling.create",
			Target: fmt.Sprintf("severity_ceiling:%d", id),
			Metadata: map[string]any{
				"selector":     body.Selector,
				"max_severity": body.MaxSeverity,
			},
		})
		writeJSON(w, http.StatusCreated, map[string]any{"id": id})
	}
}

// UpdateSeverityCeilingHandler — PATCH /api/severity-ceilings/{id}
// Body: {max_severity?, reason?}
func UpdateSeverityCeilingHandler(store *db.Store) http.HandlerFunc {
	type req struct {
		MaxSeverity string `json:"max_severity"`
		Reason      string `json:"reason"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		var body req
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		if body.MaxSeverity == "" {
			http.Error(w, errors.New("max_severity required").Error(), http.StatusBadRequest)
			return
		}
		if err := store.UpdateSeverityCeiling(id, body.MaxSeverity, body.Reason); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		audit.Emit(r, store, db.AuditEntry{
			Action:   "severity_ceiling.update",
			Target:   fmt.Sprintf("severity_ceiling:%d", id),
			Metadata: map[string]any{"max_severity": body.MaxSeverity},
		})
		w.WriteHeader(http.StatusNoContent)
	}
}

// DeleteSeverityCeilingHandler — DELETE /api/severity-ceilings/{id}
func DeleteSeverityCeilingHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		if err := store.DeleteSeverityCeiling(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		audit.Emit(r, store, db.AuditEntry{
			Action: "severity_ceiling.delete",
			Target: fmt.Sprintf("severity_ceiling:%d", id),
		})
		w.WriteHeader(http.StatusNoContent)
	}
}
