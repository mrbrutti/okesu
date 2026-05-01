// Saved-searches API. CRUD over the operator's per-(user, scope)
// named filter sets. Findings is the only consumer in v1; the
// `scope` param keeps the API generic so future surfaces (cases,
// runs, IOCs) get the same primitive without a new endpoint shape.
//
// All endpoints derive the user from auth.UserFromContext — there's
// no `user_id` in the body or path, by design (callers can't operate
// on another user's rows).

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/section9labs/okesu/controlplane/audit"
	"github.com/section9labs/okesu/controlplane/auth"
	"github.com/section9labs/okesu/controlplane/db"
)

// userInGroup returns true when the user is a member of the named
// group. Used to gate access to group-shared saved searches.
func userInGroup(store *db.Store, userID, groupID int64) (bool, error) {
	groups, err := store.ListUserGroups(userID)
	if err != nil {
		return false, err
	}
	for _, g := range groups {
		if g.GroupID == groupID {
			return true, nil
		}
	}
	return false, nil
}

// ListSavedSearchesHandler returns the user's saved searches under
// the given scope (default: 'findings').
//
// Path: /api/saved-searches?scope=findings
func ListSavedSearchesHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := auth.UserFromContext(r.Context())
		if u == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		scope := r.URL.Query().Get("scope")
		if scope == "" {
			scope = "findings"
		}
		// Group-shared scopes require membership; otherwise we'd leak
		// other groups' saved views to anyone who guessed the scope
		// string.
		if gid, ok := db.ParseGroupScope(scope); ok {
			in, err := userInGroup(store, u.ID, gid)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if !in {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
		}
		out, err := store.ListSavedSearches(u.ID, scope)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if out == nil {
			out = []db.SavedSearch{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// CreateSavedSearchHandler. Returns 201 with the created row, or 409
// when the (user, scope, name) UNIQUE constraint trips so callers
// can route to "rename or replace existing?" UX.
//
// Path: /api/saved-searches
// Body: {name, scope, config_json, is_default?}
func CreateSavedSearchHandler(store *db.Store) http.HandlerFunc {
	type req struct {
		Name       string          `json:"name"`
		Scope      string          `json:"scope"`
		ConfigJSON json.RawMessage `json:"config_json"`
		IsDefault  bool            `json:"is_default"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		u := auth.UserFromContext(r.Context())
		if u == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var body req
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		if body.Name == "" {
			http.Error(w, "name required", http.StatusBadRequest)
			return
		}
		if body.Scope == "" {
			body.Scope = "findings"
		}
		// Default config_json to "{}" so the column-not-null constraint
		// holds for callers that just want to seed an empty filter set.
		cfg := string(body.ConfigJSON)
		if cfg == "" || cfg == "null" {
			cfg = "{}"
		}
		// Authorize group-shared creation up front.
		if gid, ok := db.ParseGroupScope(body.Scope); ok {
			in, err := userInGroup(store, u.ID, gid)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if !in {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
		}
		id, err := store.CreateSavedSearch(&db.SavedSearchInsert{
			UserID:     u.ID,
			Name:       body.Name,
			Scope:      body.Scope,
			ConfigJSON: cfg,
			IsDefault:  body.IsDefault,
		})
		if err != nil {
			if errors.Is(err, db.ErrSavedSearchNameTaken) {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if gid, ok := db.ParseGroupScope(body.Scope); ok {
			audit.Emit(r, store, db.AuditEntry{
				Action:   "saved_search.create",
				Target:   fmt.Sprintf("group:%d", gid),
				Metadata: map[string]any{"id": id, "name": body.Name, "scope": body.Scope},
			})
		}
		// Re-list to pick up the just-inserted row's timestamps.
		all, _ := store.ListSavedSearches(u.ID, body.Scope)
		var got *db.SavedSearch
		for i := range all {
			if all[i].ID == id {
				got = &all[i]
				break
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(got)
	}
}

// UpdateSavedSearchHandler patches a row. Body fields are pointers so
// the patch is partial-friendly — clearing is_default to false is
// distinguishable from "leave alone".
//
// Path: /api/saved-searches/{id}
// Body: {name?, config_json?, is_default?}
func UpdateSavedSearchHandler(store *db.Store) http.HandlerFunc {
	type req struct {
		Name       *string          `json:"name"`
		ConfigJSON *json.RawMessage `json:"config_json"`
		IsDefault  *bool            `json:"is_default"`
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
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		patch := &db.SavedSearchPatch{
			Name:      body.Name,
			IsDefault: body.IsDefault,
		}
		if body.ConfigJSON != nil {
			s := string(*body.ConfigJSON)
			if s == "" || s == "null" {
				s = "{}"
			}
			patch.ConfigJSON = &s
		}
		// Group-shared rows: load the row to peek at its scope, gate
		// on group membership, then delegate to UpdateSavedSearch
		// (which already drops the per-user owner check when the
		// scope is group-shared).
		row, err := store.GetSavedSearch(id)
		if err != nil {
			if errors.Is(err, db.ErrSavedSearchNotFound) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		gid, isGroup := db.ParseGroupScope(row.Scope)
		if isGroup {
			in, err := userInGroup(store, u.ID, gid)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if !in {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
		}
		if err := store.UpdateSavedSearch(u.ID, id, patch); err != nil {
			switch {
			case errors.Is(err, db.ErrSavedSearchNotFound):
				http.Error(w, "not found", http.StatusNotFound)
			case errors.Is(err, db.ErrSavedSearchNameTaken):
				http.Error(w, err.Error(), http.StatusConflict)
			default:
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
			return
		}
		if isGroup {
			audit.Emit(r, store, db.AuditEntry{
				Action:   "saved_search.update",
				Target:   fmt.Sprintf("group:%d", gid),
				Metadata: map[string]any{"id": id, "scope": row.Scope},
			})
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// DeleteSavedSearchHandler removes a row owned by the caller.
// Idempotent — non-existent ids return 204 just like successful
// deletes.
//
// Path: /api/saved-searches/{id}
func DeleteSavedSearchHandler(store *db.Store) http.HandlerFunc {
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
		// For group-shared rows, drop the per-user owner check after
		// confirming membership; otherwise fall through to the
		// existing user_id-filtered delete.
		row, gerr := store.GetSavedSearch(id)
		if gerr == nil {
			if gid, ok := db.ParseGroupScope(row.Scope); ok {
				in, err := userInGroup(store, u.ID, gid)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				if !in {
					http.Error(w, "not found", http.StatusNotFound)
					return
				}
				if err := store.DeleteSavedSearchUnchecked(id); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				audit.Emit(r, store, db.AuditEntry{
					Action:   "saved_search.delete",
					Target:   fmt.Sprintf("group:%d", gid),
					Metadata: map[string]any{"id": id, "scope": row.Scope, "name": row.Name},
				})
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		if err := store.DeleteSavedSearch(u.ID, id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
