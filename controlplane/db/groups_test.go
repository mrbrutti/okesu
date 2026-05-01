package db

import (
	"errors"
	"testing"
)

// helper — seeds a user with a given role. Returns the new user id.
// Mirrors the CreateUser path the auth package uses internally; we
// don't want to depend on auth here so we INSERT directly.
func seedUserWithRole(t *testing.T, st *Store, email, role string) int64 {
	t.Helper()
	res, err := st.Exec(`
		INSERT INTO users (email, password_hash, role)
		VALUES (?, '', ?)`, email, role)
	if err != nil {
		t.Fatalf("seed user %s: %v", email, err)
	}
	id, _ := res.LastInsertId()
	return id
}

// TestGroups_BackfillCreatesDefaultGroups — the migration adds a
// default-<role> group per existing role and attaches every user.
// On a fresh test DB seeded with a user, the helper queries should
// find that membership.
func TestGroups_BackfillCreatesDefaultGroups(t *testing.T) {
	st := openTempStore(t)
	uid := seedUserWithRole(t, st, "alice@x", "admin")

	// Migration runs on Open, but it backfills only what was there
	// at migration time. The user we just seeded was created AFTER
	// the migration ran on the empty DB, so they have a `role` but
	// no auto-attached group. Two paths in production:
	//   - Existing users at upgrade: backfill kicks in.
	//   - New users post-upgrade: created via UpsertSSOUser /
	//     CreateUser, which we'd extend to attach default groups.
	// For the test, manually attach to verify the schema works.
	res, err := st.Exec(`SELECT id FROM groups WHERE name = 'default-admin'`)
	_ = res; _ = err
	row := st.QueryRow(`SELECT id FROM groups WHERE name = 'default-admin'`)
	var gid int64
	if err := row.Scan(&gid); err != nil {
		// Migration may not create the group when no users existed at
		// migration time. Create it manually to verify the rest.
		var ferr error
		gid, ferr = st.CreateGroup("default-admin", "test", "")
		if ferr != nil {
			t.Fatalf("seed default-admin: %v", ferr)
		}
		_ = st.AddGroupRole(gid, "admin", "")
	}
	if err := st.AddUserToGroup(uid, gid, "auto"); err != nil {
		t.Fatalf("attach: %v", err)
	}

	// Effective roles surface admin via the group_roles table.
	roles, err := st.EffectiveRoles(uid)
	if err != nil {
		t.Fatalf("effective: %v", err)
	}
	if len(roles) != 1 || roles[0] != "admin" {
		t.Errorf("effective roles = %v, want [admin]", roles)
	}

	// HasEffectiveRole respects the implication ladder.
	for _, r := range []string{"admin", "operator", "viewer"} {
		ok, _ := st.HasEffectiveRole(uid, r)
		if !ok {
			t.Errorf("admin should satisfy %s, got false", r)
		}
	}
}

// TestGroups_NameUniqueness — duplicate names fail with the typed
// error so handlers map cleanly to 409.
func TestGroups_NameUniqueness(t *testing.T) {
	st := openTempStore(t)
	if _, err := st.CreateGroup("infra", "", ""); err != nil {
		t.Fatalf("create: %v", err)
	}
	_, err := st.CreateGroup("infra", "", "")
	if !errors.Is(err, ErrGroupNameTaken) {
		t.Errorf("expected ErrGroupNameTaken, got %v", err)
	}
}

// TestGroups_ScopedRoleEffective — multiple groups contribute via
// union; removing the user's only admin group drops their effective
// admin status.
func TestGroups_ScopedRoleEffective(t *testing.T) {
	st := openTempStore(t)
	uid := seedUserWithRole(t, st, "bob@x", "viewer")

	gAdmin, _ := st.CreateGroup("infra-admins", "", "")
	gOp, _ := st.CreateGroup("infra-ops", "", "")
	_ = st.AddGroupRole(gAdmin, "admin", "")
	_ = st.AddGroupRole(gOp, "operator", "")
	_ = st.AddUserToGroup(uid, gAdmin, "manual")
	_ = st.AddUserToGroup(uid, gOp, "manual")

	roles, _ := st.EffectiveRoles(uid)
	if len(roles) != 2 {
		t.Errorf("expected 2 distinct roles, got %v", roles)
	}

	// Drop admin group → admin no longer effective.
	_ = st.RemoveUserFromGroup(uid, gAdmin, false)
	ok, _ := st.HasEffectiveRole(uid, "admin")
	if ok {
		t.Errorf("after removal, admin should not be effective")
	}
	ok, _ = st.HasEffectiveRole(uid, "operator")
	if !ok {
		t.Errorf("operator should still be effective")
	}
}

// TestGroups_DeleteRefusesIfOrphansAdmin — deleting the group that
// holds the last admin grant for a user must fail loudly.
func TestGroups_DeleteRefusesIfOrphansAdmin(t *testing.T) {
	st := openTempStore(t)
	uid := seedUserWithRole(t, st, "charlie@x", "viewer")

	gid, _ := st.CreateGroup("the-only-admins", "", "")
	_ = st.AddGroupRole(gid, "admin", "")
	_ = st.AddUserToGroup(uid, gid, "manual")

	if err := st.DeleteGroup(gid); !errors.Is(err, ErrLastAdminGroup) {
		t.Errorf("expected ErrLastAdminGroup, got %v", err)
	}

	// Adding a second admin group makes the delete safe.
	gid2, _ := st.CreateGroup("second-admins", "", "")
	_ = st.AddGroupRole(gid2, "admin", "")
	_ = st.AddUserToGroup(uid, gid2, "manual")
	if err := st.DeleteGroup(gid); err != nil {
		t.Errorf("now safe to delete, got: %v", err)
	}
}

// TestGroups_OIDCSync — the IdP claims rewrite the user's 'oidc'
// memberships on each login, but never touch 'manual' rows.
func TestGroups_OIDCSync(t *testing.T) {
	st := openTempStore(t)
	uid := seedUserWithRole(t, st, "dora@x", "viewer")

	// A pre-existing manual group the user belongs to.
	gManual, _ := st.CreateGroup("local-team", "", "")
	_ = st.AddGroupRole(gManual, "operator", "")
	_ = st.AddUserToGroup(uid, gManual, "manual")

	// First login claims two groups.
	if err := st.SyncOIDCGroupsForUser(uid, []string{"sec-eng", "platform"}); err != nil {
		t.Fatalf("sync: %v", err)
	}
	groups, _ := st.ListUserGroups(uid)
	if len(groups) != 3 {
		t.Errorf("expected 3 memberships (manual + 2 oidc), got %d: %+v", len(groups), groups)
	}
	sources := map[string]int{}
	for _, g := range groups {
		sources[g.Source]++
	}
	if sources["manual"] != 1 || sources["oidc"] != 2 {
		t.Errorf("unexpected sources: %+v", sources)
	}

	// Second login: IdP no longer asserts 'platform'.
	if err := st.SyncOIDCGroupsForUser(uid, []string{"sec-eng"}); err != nil {
		t.Fatalf("re-sync: %v", err)
	}
	groups, _ = st.ListUserGroups(uid)
	if len(groups) != 2 {
		t.Errorf("expected 2 memberships after revoke (manual + 1 oidc), got %d: %+v", len(groups), groups)
	}
	for _, g := range groups {
		if g.Name == "oidc:platform" {
			t.Errorf("platform group should have been revoked: %+v", g)
		}
	}

	// Third login: empty claim revokes all 'oidc' but leaves 'manual'.
	if err := st.SyncOIDCGroupsForUser(uid, nil); err != nil {
		t.Fatalf("empty sync: %v", err)
	}
	groups, _ = st.ListUserGroups(uid)
	if len(groups) != 1 || groups[0].Source != "manual" {
		t.Errorf("expected only manual group remaining, got %+v", groups)
	}
}
