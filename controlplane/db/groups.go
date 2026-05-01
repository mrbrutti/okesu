// Groups + scoped roles. Phase 22.8 PR α.
//
// The model is multi-membership: each user belongs to zero-or-more
// groups, each group carries zero-or-more (role, selector) tuples,
// and the user's effective roles are the union across their groups.
// Selectors are reserved for PR β; until that lands, all rows have
// selector NULL meaning "CP-wide".
//
// OIDC inheritance lives on the `external_id` column on `groups`.
// Manual groups have NULL there. The OIDC sync routine ensures one
// local group per claimed external_id, and rewrites the user's
// 'oidc'-source memberships on each login so revocations take
// effect by the next session boundary.

package db

import (
	"database/sql"
	"errors"
	"strings"
)

// Group is one row in `groups`.
type Group struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	ExternalID  string `json:"external_id"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// GroupRole is one (role, selector) pair attached to a group.
// Selector is the raw string PR β will parse against node labels;
// NULL/empty in v1 means "CP-wide".
type GroupRole struct {
	ID       int64  `json:"id"`
	GroupID  int64  `json:"group_id"`
	Role     string `json:"role"`
	Selector string `json:"selector"` // empty = CP-wide
}

// UserGroupRow is one row in `user_groups` joined to `groups` for
// display. Source distinguishes manual/oidc/auto memberships so the
// UI can lock the OIDC ones (the next login overwrites manual edits
// to those anyway).
type UserGroupRow struct {
	GroupID     int64  `json:"group_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`
	AddedAt     string `json:"added_at"`
}

// ── groups CRUD ────────────────────────────────────────────────────

// ListGroups returns every group, alphabetical by name. Empty list
// is a valid first-boot answer (the auto-created default-* groups
// land in the migration backfill).
func (s *Store) ListGroups() ([]Group, error) {
	rows, err := s.Query(`
		SELECT id, name, description, COALESCE(external_id, ''),
		       CAST(created_at AS TEXT), CAST(updated_at AS TEXT)
		  FROM groups
		 ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Group{}
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.ID, &g.Name, &g.Description, &g.ExternalID, &g.CreatedAt, &g.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// GetGroup returns one group or sql.ErrNoRows.
func (s *Store) GetGroup(id int64) (*Group, error) {
	row := s.QueryRow(`
		SELECT id, name, description, COALESCE(external_id, ''),
		       CAST(created_at AS TEXT), CAST(updated_at AS TEXT)
		  FROM groups WHERE id = ?`, id)
	g := &Group{}
	if err := row.Scan(&g.ID, &g.Name, &g.Description, &g.ExternalID, &g.CreatedAt, &g.UpdatedAt); err != nil {
		return nil, err
	}
	return g, nil
}

// CreateGroup inserts a new group. Empty external_id stores NULL so
// the partial UNIQUE index doesn't collide across multiple manual
// groups. ErrGroupNameTaken is the typed signal for a UNIQUE crash
// on (name) — handler maps to 409.
func (s *Store) CreateGroup(name, description, externalID string) (int64, error) {
	if name == "" {
		return 0, errors.New("group name required")
	}
	res, err := s.Exec(`
		INSERT INTO groups (name, description, external_id)
		VALUES (?, ?, ?)`,
		name, description, nullableStr(externalID))
	if err != nil {
		if isUniqueErr(err) {
			return 0, ErrGroupNameTaken
		}
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateGroup patches name/description/external_id. Caller passes
// empty string to leave a field unchanged on description; name is
// always updated (callers that don't want to rename pass the
// existing value). external_id of "" clears (writes NULL).
//
// Renaming a default-<role> group is allowed — the migration only
// creates them on the FIRST run, so a rename won't be undone.
func (s *Store) UpdateGroup(id int64, name, description, externalID string) error {
	if _, err := s.Exec(`
		UPDATE groups
		   SET name = ?, description = ?, external_id = ?,
		       updated_at = CURRENT_TIMESTAMP
		 WHERE id = ?`,
		name, description, nullableStr(externalID), id); err != nil {
		if isUniqueErr(err) {
			return ErrGroupNameTaken
		}
		return err
	}
	return nil
}

// DeleteGroup removes a group + cascades user_groups + group_roles.
// Refuses to delete the last admin-bearing group on the CP — that
// would leave the tenancy unmanageable. Callers see this as
// ErrLastAdminGroup so the UI can show a clear message.
func (s *Store) DeleteGroup(id int64) error {
	// Find any user who has admin EFFECTIVELY through this group AND
	// not through any other. If one exists, refuse — this group is
	// load-bearing.
	row := s.QueryRow(`
		SELECT COUNT(*) FROM users u
		WHERE EXISTS (
			SELECT 1 FROM user_groups ug
			  JOIN group_roles gr ON gr.group_id = ug.group_id
			 WHERE ug.user_id = u.id AND ug.group_id = ? AND gr.role = 'admin'
		)
		  AND NOT EXISTS (
			SELECT 1 FROM user_groups ug
			  JOIN group_roles gr ON gr.group_id = ug.group_id
			 WHERE ug.user_id = u.id AND ug.group_id != ? AND gr.role = 'admin'
		)`, id, id)
	var orphaned int
	if err := row.Scan(&orphaned); err != nil {
		return err
	}
	if orphaned > 0 {
		return ErrLastAdminGroup
	}
	_, err := s.Exec(`DELETE FROM groups WHERE id = ?`, id)
	return err
}

// ── group roles ─────────────────────────────────────────────────────

// ListGroupRoles returns every role row attached to the group.
// Empty list is normal — a brand-new group has no role grants yet.
func (s *Store) ListGroupRoles(groupID int64) ([]GroupRole, error) {
	rows, err := s.Query(`
		SELECT id, group_id, role, COALESCE(selector, '')
		  FROM group_roles WHERE group_id = ?
		 ORDER BY role, COALESCE(selector, '')`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GroupRole{}
	for rows.Next() {
		var gr GroupRole
		if err := rows.Scan(&gr.ID, &gr.GroupID, &gr.Role, &gr.Selector); err != nil {
			return nil, err
		}
		out = append(out, gr)
	}
	return out, rows.Err()
}

// AddGroupRole grants a (role, selector) tuple to the group.
// Idempotent on the (group_id, role, selector) UNIQUE — re-adding
// the same row is a no-op so the UI's "save" button can fire
// without checking first.
func (s *Store) AddGroupRole(groupID int64, role, selector string) error {
	if !isValidRole(role) {
		return errors.New("invalid role: " + role)
	}
	_, err := s.Exec(`
		INSERT INTO group_roles (group_id, role, selector)
		VALUES (?, ?, ?)
		ON CONFLICT (group_id, role, selector) DO NOTHING`,
		groupID, role, nullableStr(selector))
	return err
}

// RemoveGroupRole drops a single (role, selector) tuple. Callers
// typically pass the row id from ListGroupRoles, but we accept the
// natural-key shape for symmetry with AddGroupRole. Idempotent.
func (s *Store) RemoveGroupRole(groupID int64, role, selector string) error {
	_, err := s.Exec(`
		DELETE FROM group_roles
		 WHERE group_id = ?
		   AND role = ?
		   AND COALESCE(selector, '') = COALESCE(?, '')`,
		groupID, role, nullableStr(selector))
	return err
}

// ── user ↔ group membership ─────────────────────────────────────────

// ListUserGroups returns every group the user belongs to, plus the
// source so the UI can mark OIDC-managed rows as read-only.
func (s *Store) ListUserGroups(userID int64) ([]UserGroupRow, error) {
	rows, err := s.Query(`
		SELECT g.id, g.name, g.description, ug.source,
		       CAST(ug.added_at AS TEXT)
		  FROM user_groups ug
		  JOIN groups g ON g.id = ug.group_id
		 WHERE ug.user_id = ?
		 ORDER BY g.name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UserGroupRow{}
	for rows.Next() {
		var u UserGroupRow
		if err := rows.Scan(&u.GroupID, &u.Name, &u.Description, &u.Source, &u.AddedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// ListGroupMembers returns the users in the group. Sibling of
// ListUserGroups for the Groups detail page.
func (s *Store) ListGroupMembers(groupID int64) ([]User, error) {
	rows, err := s.Query(`
		SELECT u.id, u.email, u.password_hash, u.role, u.created_at
		  FROM user_groups ug
		  JOIN users u ON u.id = ug.user_id
		 WHERE ug.group_id = ?
		 ORDER BY u.email`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		u := User{}
		if err := rows.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Role, &u.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// AddUserToGroup is idempotent. Source is one of 'manual' | 'oidc' |
// 'auto'; admins editing via the UI pass 'manual'. The OIDC sync
// routine passes 'oidc' and is the only writer that should touch
// 'oidc' rows.
func (s *Store) AddUserToGroup(userID, groupID int64, source string) error {
	if source == "" {
		source = "manual"
	}
	_, err := s.Exec(`
		INSERT INTO user_groups (user_id, group_id, source)
		VALUES (?, ?, ?)
		ON CONFLICT (user_id, group_id) DO NOTHING`,
		userID, groupID, source)
	return err
}

// RemoveUserFromGroup drops the membership. If `onlyManual` is true
// the row is removed only when source='manual' — admins shouldn't
// nuke an OIDC-derived membership through the manual UI (the next
// login would just re-create it). Returns nil when nothing matched.
func (s *Store) RemoveUserFromGroup(userID, groupID int64, onlyManual bool) error {
	q := `DELETE FROM user_groups WHERE user_id = ? AND group_id = ?`
	args := []any{userID, groupID}
	if onlyManual {
		q += ` AND source = 'manual'`
	}
	_, err := s.Exec(q, args...)
	return err
}

// ── effective roles ─────────────────────────────────────────────────

// EffectiveRoles returns the union of role names across every group
// the user belongs to. CP-wide rows (selector NULL) and any future
// scoped rows both contribute their role name; PR β will narrow this
// to scoped checks against a target resource. For PR α, the set is
// CP-wide.
//
// An admin who's been removed from every admin group sees their
// effective set drop to {operator} or {viewer} on the next request,
// no logout required.
func (s *Store) EffectiveRoles(userID int64) ([]string, error) {
	rows, err := s.Query(`
		SELECT DISTINCT gr.role
		  FROM user_groups ug
		  JOIN group_roles gr ON gr.group_id = ug.group_id
		 WHERE ug.user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// HasEffectiveRole is the shorthand used by RequireRole middleware.
// Mirrors the existing role-implication ladder: admin satisfies
// operator + viewer; operator satisfies viewer.
func (s *Store) HasEffectiveRole(userID int64, needed string) (bool, error) {
	roles, err := s.EffectiveRoles(userID)
	if err != nil {
		return false, err
	}
	for _, r := range roles {
		if roleSatisfies(r, needed) {
			return true, nil
		}
	}
	return false, nil
}

// ── OIDC sync ───────────────────────────────────────────────────────

// SyncOIDCGroupsForUser is called on each login by the OIDC handler.
// `claimedGroups` is the list of group identifiers from the IdP
// (whatever the operator configured in OIDCGroupsClaim). For each:
//   - ensure a local row exists in `groups` matched by external_id
//   - add a user_groups row with source='oidc'
// Then any 'oidc'-source memberships not in claimedGroups are
// removed — revocations take effect by the next session boundary.
//
// Local groups (external_id NULL) and 'manual' / 'auto' memberships
// are untouched.
func (s *Store) SyncOIDCGroupsForUser(userID int64, claimedGroups []string) error {
	// Empty claim is fine — the user keeps any manual + auto
	// memberships and just loses any prior 'oidc' ones.
	tx, err := s.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	// Ensure a local group exists per claimed external_id. We use
	// "oidc:<external_id>" as the local name to make the source
	// obvious in the Groups list; admins can rename later (the
	// external_id is the binding key, not the name).
	wantedIDs := make(map[int64]struct{}, len(claimedGroups))
	for _, ext := range claimedGroups {
		ext = strings.TrimSpace(ext)
		if ext == "" {
			continue
		}
		var gid int64
		// Probe first — saves an INSERT round-trip in the steady
		// state where the group already exists.
		if err := tx.QueryRow(
			`SELECT id FROM groups WHERE external_id = ?`, ext,
		).Scan(&gid); err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			res, err := tx.Exec(`
				INSERT INTO groups (name, description, external_id)
				VALUES (?, ?, ?)`,
				"oidc:"+ext, "Auto-created from OIDC group claim "+ext, ext)
			if err != nil {
				return err
			}
			gid, err = res.LastInsertId()
			if err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`
			INSERT INTO user_groups (user_id, group_id, source)
			VALUES (?, ?, 'oidc')
			ON CONFLICT (user_id, group_id) DO NOTHING`,
			userID, gid); err != nil {
			return err
		}
		wantedIDs[gid] = struct{}{}
	}

	// Drop 'oidc' memberships the IdP no longer asserts. Only
	// 'oidc'-source rows are affected; manual + auto rows survive.
	rows, err := tx.Query(`
		SELECT group_id FROM user_groups
		 WHERE user_id = ? AND source = 'oidc'`, userID)
	if err != nil {
		return err
	}
	var stale []int64
	for rows.Next() {
		var gid int64
		if err := rows.Scan(&gid); err != nil {
			rows.Close()
			return err
		}
		if _, kept := wantedIDs[gid]; !kept {
			stale = append(stale, gid)
		}
	}
	rows.Close()
	for _, gid := range stale {
		if _, err := tx.Exec(`
			DELETE FROM user_groups
			 WHERE user_id = ? AND group_id = ? AND source = 'oidc'`,
			userID, gid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ── helpers ─────────────────────────────────────────────────────────

func isValidRole(r string) bool {
	switch r {
	case "admin", "operator", "viewer":
		return true
	}
	return false
}

// roleSatisfies implements the role-implication ladder. Mirrors
// auth.RoleSatisfies — kept here so the store doesn't import auth
// (which imports db).
func roleSatisfies(have, needed string) bool {
	if have == needed {
		return true
	}
	switch needed {
	case "viewer":
		return have == "operator" || have == "admin"
	case "operator":
		return have == "admin"
	}
	return false
}

// ErrGroupNameTaken signals a UNIQUE collision on (name) so handlers
// can map to 409 Conflict.
var ErrGroupNameTaken = errors.New("group name already taken")

// ErrLastAdminGroup is returned by DeleteGroup when removing the
// group would orphan an admin (no other admin grants for them).
var ErrLastAdminGroup = errors.New("cannot delete: would orphan one or more admin users")
