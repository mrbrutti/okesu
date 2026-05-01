package db

import (
	"testing"
	"time"
)

func TestFeedConfig_InsertGetListUpdateDelete(t *testing.T) {
	st := openTempStore(t)

	in := FeedConfigInsert{
		Slug: "test-feed", Name: "Test Feed", Kind: "single_file",
		URL: "https://example.com/x.yar", Parser: "yara",
		RefreshIntervalSeconds: 86400, Enabled: true,
		InstalledFromRegistry: true,
	}
	id, err := st.InsertFeedConfig(&in)
	if err != nil || id == 0 {
		t.Fatalf("insert: id=%d err=%v", id, err)
	}

	got, err := st.GetFeedConfig(id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Slug != "test-feed" || got.Parser != "yara" || !got.Enabled {
		t.Fatalf("get round-trip: %+v", got)
	}

	bySlug, err := st.GetFeedConfigBySlug("test-feed")
	if err != nil {
		t.Fatalf("get-by-slug: %v", err)
	}
	if bySlug.ID != id {
		t.Fatalf("get-by-slug: got id=%d want %d", bySlug.ID, id)
	}

	if err := st.UpdateFeedConfig(id, &FeedConfigPatch{Enabled: ptrBool(false)}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got2, err := st.GetFeedConfig(id)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if got2.Enabled {
		t.Fatalf("expected disabled after update")
	}

	now := time.Now().UTC()
	if err := st.MarkFeedRefresh(id, "ok", "", 42, &now); err != nil {
		t.Fatalf("mark refresh: %v", err)
	}
	got3, err := st.GetFeedConfig(id)
	if err != nil {
		t.Fatalf("get after mark-refresh: %v", err)
	}
	if got3.LastRefreshStatus != "ok" || got3.LastRefreshEntryCount != 42 {
		t.Fatalf("post-mark: %+v", got3)
	}

	all, err := st.ListFeedConfigs()
	if err != nil || len(all) != 1 {
		t.Fatalf("list: n=%d err=%v", len(all), err)
	}

	if err := st.DeleteFeedConfig(id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := st.GetFeedConfig(id); err == nil {
		t.Fatalf("expected error after delete")
	}
}

func ptrBool(b bool) *bool { return &b }

func TestSetFeedConfigsFromFederation_InsertsNewFeeds(t *testing.T) {
	st := openTempStore(t)

	parent := []FederationFeedConfig{
		{Slug: "f1", Name: "F1", Kind: "single_file", URL: "https://x", Parser: "yara", RefreshIntervalSeconds: 86400, Enabled: true},
		{Slug: "f2", Name: "F2", Kind: "git", URL: "https://y", Parser: "sigma", RefreshIntervalSeconds: 604800, Enabled: true},
	}
	inserted, updated, toUninstall, err := st.SetFeedConfigsFromFederation("parent-1", parent)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if inserted != 2 || updated != 0 || len(toUninstall) != 0 {
		t.Fatalf("counts: ins=%d upd=%d unins=%d", inserted, updated, len(toUninstall))
	}

	all, _ := st.ListFeedConfigs()
	if len(all) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(all))
	}
	for _, fc := range all {
		if fc.Source != "federated_from_parent" {
			t.Errorf("%s: source=%q want federated_from_parent", fc.Slug, fc.Source)
		}
		if !fc.ParentCPID.Valid || fc.ParentCPID.String != "parent-1" {
			t.Errorf("%s: parent_cp_id=%v", fc.Slug, fc.ParentCPID)
		}
	}
}

func TestSetFeedConfigsFromFederation_UpdatesFederatedRows(t *testing.T) {
	st := openTempStore(t)

	first := []FederationFeedConfig{
		{Slug: "f1", Name: "F1 v1", Kind: "single_file", URL: "https://x/v1", Parser: "yara", RefreshIntervalSeconds: 86400, Enabled: true},
	}
	if _, _, _, err := st.SetFeedConfigsFromFederation("parent-1", first); err != nil {
		t.Fatal(err)
	}

	second := []FederationFeedConfig{
		{Slug: "f1", Name: "F1 v2", Kind: "single_file", URL: "https://x/v2", Parser: "yara", RefreshIntervalSeconds: 3600, Enabled: false},
	}
	inserted, updated, toUninstall, err := st.SetFeedConfigsFromFederation("parent-1", second)
	if err != nil {
		t.Fatal(err)
	}
	if inserted != 0 || updated != 1 || len(toUninstall) != 0 {
		t.Fatalf("counts: ins=%d upd=%d unins=%d", inserted, updated, len(toUninstall))
	}

	got, _ := st.GetFeedConfigBySlug("f1")
	if got.Name != "F1 v2" || got.URL != "https://x/v2" || got.RefreshIntervalSeconds != 3600 || got.Enabled {
		t.Fatalf("update did not stick: %+v", got)
	}
}

func TestSetFeedConfigsFromFederation_LocalRowsLeftAlone(t *testing.T) {
	st := openTempStore(t)

	// Operator-local row.
	if _, err := st.InsertFeedConfig(&FeedConfigInsert{
		Slug: "f-local", Name: "Local", Kind: "single_file", URL: "https://local",
		Parser: "yara", RefreshIntervalSeconds: 86400, Enabled: true, Source: "local",
	}); err != nil {
		t.Fatal(err)
	}

	// Parent has the SAME slug. Must not overwrite.
	parent := []FederationFeedConfig{
		{Slug: "f-local", Name: "Different Name", Kind: "single_file", URL: "https://upstream",
			Parser: "yara", RefreshIntervalSeconds: 3600, Enabled: false},
	}
	inserted, updated, toUninstall, err := st.SetFeedConfigsFromFederation("parent-1", parent)
	if err != nil {
		t.Fatal(err)
	}
	if inserted != 0 || updated != 0 || len(toUninstall) != 0 {
		t.Fatalf("counts: ins=%d upd=%d unins=%d (operator override should be untouched)", inserted, updated, len(toUninstall))
	}

	got, _ := st.GetFeedConfigBySlug("f-local")
	if got.Name != "Local" || got.URL != "https://local" {
		t.Fatalf("local row was modified: %+v", got)
	}
}

func TestSetFeedConfigsFromFederation_DeletedReturnsToUninstallList(t *testing.T) {
	st := openTempStore(t)

	first := []FederationFeedConfig{
		{Slug: "f1", Name: "F1", Kind: "single_file", URL: "https://x", Parser: "yara", RefreshIntervalSeconds: 86400, Enabled: true},
		{Slug: "f2", Name: "F2", Kind: "single_file", URL: "https://y", Parser: "yara", RefreshIntervalSeconds: 86400, Enabled: true},
	}
	if _, _, _, err := st.SetFeedConfigsFromFederation("parent-1", first); err != nil {
		t.Fatal(err)
	}

	// Parent now only has f2; f1 was uninstalled.
	second := []FederationFeedConfig{
		{Slug: "f2", Name: "F2", Kind: "single_file", URL: "https://y", Parser: "yara", RefreshIntervalSeconds: 86400, Enabled: true},
	}
	inserted, updated, toUninstall, err := st.SetFeedConfigsFromFederation("parent-1", second)
	if err != nil {
		t.Fatal(err)
	}
	if inserted != 0 || updated != 1 || len(toUninstall) != 1 {
		t.Fatalf("counts: ins=%d upd=%d unins=%d", inserted, updated, len(toUninstall))
	}

	// f2 still exists; f1's id is in toUninstall.
	gotF1, _ := st.GetFeedConfigBySlug("f1")
	if gotF1 == nil {
		t.Fatal("f1 should still be present until caller uninstalls; got nil")
	}
	gotF2, _ := st.GetFeedConfigBySlug("f2")
	if gotF2 == nil {
		t.Fatal("f2 should still be present")
	}
	if toUninstall[0] != gotF1.ID {
		t.Fatalf("toUninstall[0]=%d want f1.ID=%d", toUninstall[0], gotF1.ID)
	}
}
