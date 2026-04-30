package db

import (
	"errors"
	"testing"
)

func seedUser(t *testing.T, st *Store, email string) int64 {
	t.Helper()
	res, err := st.Exec(`
		INSERT INTO users (email, password_hash, role)
		VALUES (?, '', 'admin')`, email)
	if err != nil {
		t.Fatalf("seed user %s: %v", email, err)
	}
	id, _ := res.LastInsertId()
	return id
}

// TestSavedSearches_CRUD covers create, list, update, delete plus
// the (user, scope, name) UNIQUE constraint and the is_default
// invariant (only one default per scope).
func TestSavedSearches_CRUD(t *testing.T) {
	st := openTempStore(t)
	alice := seedUser(t, st, "alice@x")
	bob := seedUser(t, st, "bob@x")

	id1, err := st.CreateSavedSearch(&SavedSearchInsert{
		UserID: alice, Name: "my queue", Scope: "findings",
		ConfigJSON: `{"state":"queue","severity":["HIGH"]}`,
		IsDefault:  true,
	})
	if err != nil {
		t.Fatalf("create #1: %v", err)
	}
	if id1 == 0 {
		t.Fatal("expected nonzero id")
	}

	// Same name for the same user/scope → conflict.
	if _, err := st.CreateSavedSearch(&SavedSearchInsert{
		UserID: alice, Name: "my queue", Scope: "findings",
		ConfigJSON: `{}`,
	}); !errors.Is(err, ErrSavedSearchNameTaken) {
		t.Fatalf("expected ErrSavedSearchNameTaken, got %v", err)
	}

	// Different user can reuse the name.
	if _, err := st.CreateSavedSearch(&SavedSearchInsert{
		UserID: bob, Name: "my queue", Scope: "findings",
		ConfigJSON: `{}`,
	}); err != nil {
		t.Errorf("bob should be able to reuse name: %v", err)
	}

	// Add a second alice search; mark it default → first one should
	// drop is_default in the same transaction.
	id2, err := st.CreateSavedSearch(&SavedSearchInsert{
		UserID: alice, Name: "edr fleet", Scope: "findings",
		ConfigJSON: `{"agent":"edr"}`,
		IsDefault:  true,
	})
	if err != nil {
		t.Fatalf("create #2: %v", err)
	}
	got, err := st.ListSavedSearches(alice, "findings")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("alice should have 2 searches, got %d", len(got))
	}
	defaults := 0
	for _, s := range got {
		if s.IsDefault {
			defaults++
			if s.ID != id2 {
				t.Errorf("expected default = id2 (%d), got %d", id2, s.ID)
			}
		}
	}
	if defaults != 1 {
		t.Errorf("expected exactly one default, got %d", defaults)
	}

	// Patch — rename and clear default.
	newName := "queue (edited)"
	falseVal := false
	if err := st.UpdateSavedSearch(alice, id1, &SavedSearchPatch{
		Name:      &newName,
		IsDefault: &falseVal,
	}); err != nil {
		t.Fatalf("patch: %v", err)
	}
	got, _ = st.ListSavedSearches(alice, "findings")
	for _, s := range got {
		if s.ID == id1 && s.Name != newName {
			t.Errorf("rename didn't apply: %s", s.Name)
		}
	}

	// Update a row that belongs to bob from alice → ErrSavedSearchNotFound.
	bobID, _ := st.CreateSavedSearch(&SavedSearchInsert{
		UserID: bob, Name: "bob-only", Scope: "findings", ConfigJSON: `{}`,
	})
	bobName := "stolen"
	if err := st.UpdateSavedSearch(alice, bobID, &SavedSearchPatch{Name: &bobName}); !errors.Is(err, ErrSavedSearchNotFound) {
		t.Errorf("alice modifying bob's row should be NotFound, got %v", err)
	}

	// Delete is owner-scoped: alice deleting bob's row is a no-op.
	if err := st.DeleteSavedSearch(alice, bobID); err != nil {
		t.Errorf("delete: %v", err)
	}
	gotBob, _ := st.ListSavedSearches(bob, "findings")
	if len(gotBob) != 2 {
		t.Errorf("bob's rows should be intact (2), got %d", len(gotBob))
	}

	// Owner delete works.
	if err := st.DeleteSavedSearch(alice, id1); err != nil {
		t.Errorf("delete owned: %v", err)
	}
	got, _ = st.ListSavedSearches(alice, "findings")
	if len(got) != 1 {
		t.Errorf("expected 1 after delete, got %d", len(got))
	}
}
