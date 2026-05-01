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
