package db

import (
	"testing"
)

// seedNode inserts a minimal node row and returns its id.
func seedNode(t *testing.T, st *Store, name string) int64 {
	t.Helper()
	res, err := st.Exec(`
		INSERT INTO nodes (name, hostname, ssh_user, ssh_port, status)
		VALUES (?, ?, 'root', 22, 'ready')`, name, name)
	if err != nil {
		t.Fatalf("seed node %s: %v", name, err)
	}
	id, _ := res.LastInsertId()
	return id
}

// TestNodeLabels_CRUD — set / list / delete + idempotent set.
func TestNodeLabels_CRUD(t *testing.T) {
	st := openTempStore(t)
	n := seedNode(t, st, "n1")

	if err := st.SetNodeLabel(n, "env", "prod"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := st.SetNodeLabel(n, "team", "security"); err != nil {
		t.Fatalf("set 2: %v", err)
	}
	got, _ := st.ListNodeLabels(n)
	if got["env"] != "prod" || got["team"] != "security" {
		t.Errorf("got = %v, want env=prod team=security", got)
	}

	// Update — same key, new value.
	if err := st.SetNodeLabel(n, "env", "staging"); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ = st.ListNodeLabels(n)
	if got["env"] != "staging" {
		t.Errorf("after update: env = %q, want staging", got["env"])
	}

	// Delete.
	if err := st.DeleteNodeLabel(n, "team"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, _ = st.ListNodeLabels(n)
	if _, has := got["team"]; has {
		t.Errorf("team should be gone, got %v", got)
	}

	// Idempotent delete.
	if err := st.DeleteNodeLabel(n, "team"); err != nil {
		t.Errorf("idempotent delete: %v", err)
	}

	// Bad key rejected.
	if err := st.SetNodeLabel(n, "bad key", "x"); err == nil {
		t.Errorf("expected error on key with space")
	}
}

// TestNodeLabels_BatchListAndSelector — the matcher-driven flows
// the API uses to filter visible nodes.
func TestNodeLabels_BatchListAndSelector(t *testing.T) {
	st := openTempStore(t)
	a := seedNode(t, st, "prod-a")
	b := seedNode(t, st, "prod-b")
	c := seedNode(t, st, "stage-a")
	_ = st.SetNodeLabel(a, "env", "prod")
	_ = st.SetNodeLabel(a, "team", "infra")
	_ = st.SetNodeLabel(b, "env", "prod")
	_ = st.SetNodeLabel(b, "team", "security")
	_ = st.SetNodeLabel(c, "env", "staging")

	batch, _ := st.ListLabelsForNodes([]int64{a, b, c})
	if len(batch) != 3 {
		t.Errorf("batch missing nodes: %v", batch)
	}
	if batch[a]["env"] != "prod" {
		t.Errorf("batch[a].env = %q", batch[a]["env"])
	}

	// Selector matches.
	sel, _ := ParseSelector("env=prod,team=infra")
	if !sel.Matches(batch[a]) {
		t.Errorf("a should match env=prod,team=infra")
	}
	if sel.Matches(batch[b]) {
		t.Errorf("b should NOT match env=prod,team=infra (team=security)")
	}
	if sel.Matches(batch[c]) {
		t.Errorf("c should NOT match (env=staging)")
	}
}

// TestHasEffectiveRoleOnNode — the resource-scoped gate. CP-wide
// grants pass for any node; scoped grants narrow.
func TestHasEffectiveRoleOnNode(t *testing.T) {
	st := openTempStore(t)
	uid := seedUserWithRole(t, st, "alice@x", "viewer")
	a := seedNode(t, st, "prod-a")
	b := seedNode(t, st, "stage-b")
	_ = st.SetNodeLabel(a, "env", "prod")
	_ = st.SetNodeLabel(b, "env", "staging")

	// CP-wide operator grant (selector empty) → both nodes pass.
	g1, _ := st.CreateGroup("ops-anywhere", "", "")
	_ = st.AddGroupRole(g1, "operator", "")
	_ = st.AddUserToGroup(uid, g1, "manual")
	for _, nid := range []int64{a, b} {
		ok, _ := st.HasEffectiveRoleOnNode(uid, nid, "operator")
		if !ok {
			t.Errorf("CP-wide operator should pass on node %d", nid)
		}
	}

	// Detach CP-wide; add a env=prod-only operator grant.
	_ = st.RemoveUserFromGroup(uid, g1, false)
	g2, _ := st.CreateGroup("prod-ops", "", "")
	_ = st.AddGroupRole(g2, "operator", "env=prod")
	_ = st.AddUserToGroup(uid, g2, "manual")

	if ok, _ := st.HasEffectiveRoleOnNode(uid, a, "operator"); !ok {
		t.Errorf("env=prod operator should pass on prod-a")
	}
	if ok, _ := st.HasEffectiveRoleOnNode(uid, b, "operator"); ok {
		t.Errorf("env=prod operator should NOT pass on stage-b")
	}

	// Role-implication ladder: admin satisfies operator at the same
	// scope.
	g3, _ := st.CreateGroup("prod-admins", "", "")
	_ = st.AddGroupRole(g3, "admin", "env=prod")
	_ = st.AddUserToGroup(uid, g3, "manual")
	if ok, _ := st.HasEffectiveRoleOnNode(uid, a, "operator"); !ok {
		t.Errorf("env=prod admin should satisfy operator on prod-a")
	}
}

// TestFilterVisibleNodes — list-page scoping. CP-wide grants
// short-circuit to "see everything"; scoped grants narrow.
func TestFilterVisibleNodes(t *testing.T) {
	st := openTempStore(t)
	uid := seedUserWithRole(t, st, "bob@x", "viewer")
	a := seedNode(t, st, "prod-a")
	b := seedNode(t, st, "stage-b")
	c := seedNode(t, st, "prod-c")
	_ = st.SetNodeLabel(a, "env", "prod")
	_ = st.SetNodeLabel(b, "env", "staging")
	_ = st.SetNodeLabel(c, "env", "prod")

	// No grants → no visibility.
	got, _ := st.FilterVisibleNodes(uid, "viewer", []int64{a, b, c})
	if len(got) != 0 {
		t.Errorf("no grants → got %d, want 0", len(got))
	}

	// Scoped viewer on env=prod → see {a, c}.
	g, _ := st.CreateGroup("prod-viewers", "", "")
	_ = st.AddGroupRole(g, "viewer", "env=prod")
	_ = st.AddUserToGroup(uid, g, "manual")
	got, _ = st.FilterVisibleNodes(uid, "viewer", []int64{a, b, c})
	if len(got) != 2 {
		t.Errorf("scoped → got %d, want 2 (got %v)", len(got), got)
	}

	// Add a CP-wide viewer grant → see all three.
	g2, _ := st.CreateGroup("global-viewers", "", "")
	_ = st.AddGroupRole(g2, "viewer", "")
	_ = st.AddUserToGroup(uid, g2, "manual")
	got, _ = st.FilterVisibleNodes(uid, "viewer", []int64{a, b, c})
	if len(got) != 3 {
		t.Errorf("CP-wide → got %d, want 3", len(got))
	}
}
