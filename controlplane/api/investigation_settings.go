// Investigation suggestion-engine settings API.
//
// GET  /api/investigations/settings              — any logged-in user
// PUT  /api/investigations/settings              — admin only
// POST /api/investigations/{id}/bulk-link-findings — link many at once
//
// Bulk-link is the operator ergonomics primitive that powers the
// workspace's "Add all ≥ N" / "Add all from same IOC" buttons. It
// loops the same store method as single linkFinding so the
// dismissal-tombstone-clear side effect is preserved per pair.

package api

import (
	"encoding/json"
	"net/http"

	"github.com/section9labs/okesu/controlplane/db"
)

// SuggestionSettingsHandler returns the persisted settings (or
// defaults). Read-only — any authenticated user can fetch since the
// workspace card uses the threshold value to render labels.
func SuggestionSettingsHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		settings, err := store.GetSuggestionSettings()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(settings)
	}
}

// UpdateSuggestionSettingsHandler persists a new settings blob.
// Admin-gated at the route layer (server.go RequireRole(RoleAdmin)).
//
// Server-side merge with defaults (in db.SetSuggestionSettings) means
// the UI doesn't have to send every field — partial updates land
// cleanly. A 200 + the rebuilt blob lets the UI sync state without a
// follow-up GET.
func UpdateSuggestionSettingsHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body db.SuggestionSettings
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := store.SetSuggestionSettings(body); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Read back for the response so the caller sees the merged-
		// with-defaults form (i.e., what the engine will actually use).
		got, _ := store.GetSuggestionSettings()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(got)
	}
}

// BulkLinkFindingsHandler links many findings to one investigation in
// a single round-trip. Used by the workspace card's "Add all ≥ N"
// and "Add all from same IOC" buttons.
//
// Body: {"finding_ids": [101, 102, 103]}
//
// Each link is processed via the same store method as the single-link
// path, so the tombstone-clear side effect runs per pair. We don't
// short-circuit on a single failure — return per-id results so the UI
// can show "linked 5 of 6 (1 failed)".
func BulkLinkFindingsHandler(store *db.Store) http.HandlerFunc {
	type item struct {
		FindingID int64  `json:"finding_id"`
		OK        bool   `json:"ok"`
		Error     string `json:"error,omitempty"`
	}
	type req struct {
		FindingIDs []int64 `json:"finding_ids"`
	}
	type resp struct {
		Linked  int    `json:"linked"`
		Failed  int    `json:"failed"`
		Results []item `json:"results"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		invID, err := investigationIDFromChi(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var body req
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		if len(body.FindingIDs) == 0 {
			http.Error(w, "finding_ids required", http.StatusBadRequest)
			return
		}
		out := resp{Results: make([]item, 0, len(body.FindingIDs))}
		for _, fid := range body.FindingIDs {
			it := item{FindingID: fid}
			if err := store.LinkFindingToInvestigation(invID, fid); err != nil {
				it.OK = false
				it.Error = err.Error()
				out.Failed++
			} else {
				it.OK = true
				out.Linked++
			}
			out.Results = append(out.Results, it)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}
