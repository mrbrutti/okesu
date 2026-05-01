package api

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/section9labs/okesu/controlplane/db"
)

func TestFeedsFederation_ReturnsLocalOnly(t *testing.T) {
	st := newTestStore(t)

	// Local feed (should be exposed).
	if _, err := st.InsertFeedConfig(&db.FeedConfigInsert{
		Slug: "f-local", Name: "Local", Kind: "single_file",
		URL: "https://x", Parser: "yara",
		RefreshIntervalSeconds: 86400, Enabled: true, Source: "local",
	}); err != nil {
		t.Fatal(err)
	}
	// Mirrored-from-some-other-parent feed (should NOT be exposed; one-hop only).
	if _, err := st.InsertFeedConfig(&db.FeedConfigInsert{
		Slug: "f-mirrored", Name: "Mirrored", Kind: "single_file",
		URL: "https://y", Parser: "yara",
		RefreshIntervalSeconds: 86400, Enabled: true, Source: "federated_from_parent",
		ParentCPID: "some-other-parent",
	}); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/v1/federation/feeds", nil)
	FeedsFederation(st)(w, r)

	if w.Code != 200 {
		t.Fatalf("status: %d body=%s", w.Code, w.Body.String())
	}
	var got []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0]["slug"] != "f-local" {
		t.Fatalf("expected only the local feed: %+v", got)
	}
}
