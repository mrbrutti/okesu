// Groups + scoped roles API. Phase 22.8 PR α.
//
// Read endpoints are operator+ (so non-admins can see what groups
// they belong to + who else is a member). Mutation is admin-only —
// gated at the route layer in server.go.
//
// Selectors are accepted on the wire (group_role rows include them)
// but PR α doesn't enforce — they pass through verbatim and PR β's
// label evaluator will start consulting them.

package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/section9labs/okesu/controlplane/auth"
	"github.com/section9labs/okesu/controlplane/db"
)

// ── groups CRUD ─────────────────────────────────────────────────────

// ListGroupsHandler — every authenticated user; the Settings →
// Groups page is admin-gated at the route layer, but the user's own
// "what groups am I in?" view also reads from this list.
func ListGroupsHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		gs, err := store.ListGroups()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, gs)
	}
}

// GetGroupHandler returns a single group with its roles + members
// rolled in. Saves a round-trip on the detail page.
func GetGroupHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := groupIDFromChi(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		g, err := store.GetGroup(id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		roles, _ := store.ListGroupRoles(id)
		members, _ := store.ListGroupMembers(id)
		// Strip password hashes from members — they shouldn't leave
		// the auth package.
		mout := make([]map[string]any, 0, len(members))
		for _, m := range members {
			mout = append(mout, map[string]any{
				"id":    m.ID,
				"email": m.Email,
				"role":  m.Role,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"group":   g,
			"roles":   roles,
			"members": mout,
		})
	}
}

// CreateGroupHandler — admin only.
func CreateGroupHandler(store *db.Store) http.HandlerFunc {
	type req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		ExternalID  string `json:"external_id"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var body req
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(body.Name) == "" {
			http.Error(w, "name required", http.StatusBadRequest)
			return
		}
		id, err := store.CreateGroup(body.Name, body.Description, body.ExternalID)
		if err != nil {
			if errors.Is(err, db.ErrGroupNameTaken) {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		g, _ := store.GetGroup(id)
		writeJSON(w, http.StatusCreated, g)
	}
}

// UpdateGroupHandler — admin only. Renames and re-binds external_id.
func UpdateGroupHandler(store *db.Store) http.HandlerFunc {
	type req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		ExternalID  string `json:"external_id"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := groupIDFromChi(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var body req
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := store.UpdateGroup(id, body.Name, body.Description, body.ExternalID); err != nil {
			if errors.Is(err, db.ErrGroupNameTaken) {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// DeleteGroupHandler — admin only. Refuses if removing this group
// would orphan an admin.
func DeleteGroupHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := groupIDFromChi(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := store.DeleteGroup(id); err != nil {
			if errors.Is(err, db.ErrLastAdminGroup) {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// ── group roles ─────────────────────────────────────────────────────

// AddGroupRoleHandler — admin only.
//
// Body: {role: "admin"|"operator"|"viewer", selector: ""}
// Selector is accepted but the engine ignores it in PR α; PR β
// teaches the role evaluator to scope by it.
func AddGroupRoleHandler(store *db.Store) http.HandlerFunc {
	type req struct {
		Role     string `json:"role"`
		Selector string `json:"selector"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := groupIDFromChi(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var body req
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := store.AddGroupRole(id, body.Role, body.Selector); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// RemoveGroupRoleHandler — admin only. Path: .../roles/{roleID}.
// We accept the row id (from ListGroupRoles) so the UI's "delete
// this row" button doesn't have to know the natural key.
func RemoveGroupRoleHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := groupIDFromChi(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// We accept either the row id (clean delete by id) or the
		// natural key in the body (back-compat). Path id wins when
		// present.
		roleIDStr := chi.URLParam(r, "roleID")
		if roleIDStr != "" {
			// Look up the row to get its (role, selector) so the
			// existing RemoveGroupRole helper can do its idempotent
			// natural-key delete. Cheaper than another store method.
			roles, _ := store.ListGroupRoles(id)
			roleIDInt, _ := strconv.ParseInt(roleIDStr, 10, 64)
			for _, gr := range roles {
				if gr.ID == roleIDInt {
					_ = store.RemoveGroupRole(id, gr.Role, gr.Selector)
					w.WriteHeader(http.StatusNoContent)
					return
				}
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var body struct {
			Role     string `json:"role"`
			Selector string `json:"selector"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if err := store.RemoveGroupRole(id, body.Role, body.Selector); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// ── members ─────────────────────────────────────────────────────────

// AddGroupMemberHandler — admin only.
// Path: /api/groups/{id}/members/{userID}
// Always source='manual' from this endpoint; the OIDC sync routine
// uses the store directly.
func AddGroupMemberHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		gid, err := groupIDFromChi(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		uid, err := strconv.ParseInt(chi.URLParam(r, "userID"), 10, 64)
		if err != nil {
			http.Error(w, "bad user id", http.StatusBadRequest)
			return
		}
		if err := store.AddUserToGroup(uid, gid, "manual"); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// RemoveGroupMemberHandler — admin only. Only manual memberships
// are removable via this path; OIDC-derived ones come back on next
// login so the UI shouldn't pretend it can hide them.
func RemoveGroupMemberHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		gid, err := groupIDFromChi(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		uid, err := strconv.ParseInt(chi.URLParam(r, "userID"), 10, 64)
		if err != nil {
			http.Error(w, "bad user id", http.StatusBadRequest)
			return
		}
		if err := store.RemoveUserFromGroup(uid, gid, true); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// ── self-service ────────────────────────────────────────────────────

// MyGroupsHandler — every authenticated user's own group list. The
// Settings → Profile section uses this so a user can see (but not
// edit) their effective access.
func MyGroupsHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := auth.UserFromContext(r.Context())
		if u == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		groups, err := store.ListUserGroups(u.ID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		roles, _ := store.EffectiveRoles(u.ID)
		writeJSON(w, http.StatusOK, map[string]any{
			"groups":          groups,
			"effective_roles": roles,
		})
	}
}

// ── helpers ─────────────────────────────────────────────────────────

func groupIDFromChi(r *http.Request) (int64, error) {
	raw := chi.URLParam(r, "id")
	if raw == "" {
		return 0, errors.New("group id required")
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, errors.New("bad group id")
	}
	return id, nil
}

