package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/section9labs/okesu/controlplane/db"
	"github.com/section9labs/okesu/controlplane/ioc/feeds"
)

// ── helpers ──────────────────────────────────────────────────────────────────

// newFakeScheduler creates a real Scheduler backed by st with a very slow tick
// so it never fires on its own; tests call RefreshNow directly.
func newFakeScheduler(st *db.Store) *feeds.Scheduler {
	return feeds.NewScheduler(st, &noopRefresher{}, 24*time.Hour)
}

type noopRefresher struct{}

func (n *noopRefresher) RefreshOne(_ context.Context, _ *db.FeedConfig) error { return nil }

// buildFeedsRouter wires all handlers on a chi.Router so path params work.
func buildFeedsRouter(st *db.Store, sch *feeds.Scheduler, wk *feeds.Worker) *chi.Mux {
	r := chi.NewRouter()
	r.Get("/api/feeds", ListFeedsHandler(st))
	r.Get("/api/feeds/registry", ListFeedsRegistryHandler())
	r.Post("/api/feeds", InstallFeedHandler(st))
	r.Post("/api/feeds/validate", ValidateFeedHandler(wk))
	r.Post("/api/feeds/consent", SetFeedsConsentHandler(st))
	r.Patch("/api/feeds/{id}", UpdateFeedHandler(st))
	r.Delete("/api/feeds/{id}", UninstallFeedHandler(st))
	r.Post("/api/feeds/{id}/refresh", RefreshFeedHandler(sch))
	return r
}

// ── tests ────────────────────────────────────────────────────────────────────

// 1. List empty — fresh store returns 200 + empty array.
func TestFeedsListEmpty(t *testing.T) {
	st := newTestStore(t)
	r := buildFeedsRouter(st, newFakeScheduler(st), nil)

	req := httptest.NewRequest(http.MethodGet, "/api/feeds", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var got []db.FeedConfig
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty array; got %d entries", len(got))
	}
}

// 2. List registry — returns at least one entry with slug "cisa-kev".
func TestFeedsListRegistry(t *testing.T) {
	r := chi.NewRouter()
	r.Get("/api/feeds/registry", ListFeedsRegistryHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/feeds/registry", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var got []feeds.FeedDef
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("expected at least one registry entry")
	}
	found := false
	for _, fd := range got {
		if fd.Slug == "cisa-kev" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected cisa-kev in registry")
	}
}

// 3. Install from registry — 201 then list shows entry.
func TestFeedsInstallFromRegistry(t *testing.T) {
	st := newTestStore(t)
	r := buildFeedsRouter(st, newFakeScheduler(st), nil)

	body, _ := json.Marshal(map[string]any{"from_registry_slug": "cisa-kev"})
	req := httptest.NewRequest(http.MethodPost, "/api/feeds", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("install: status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	var resp installFeedResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode install response: %v", err)
	}
	if resp.ID == 0 {
		t.Error("expected non-zero id")
	}
	if resp.Slug != "cisa-kev" {
		t.Errorf("slug = %q, want cisa-kev", resp.Slug)
	}

	// List should now show the installed feed.
	req2 := httptest.NewRequest(http.MethodGet, "/api/feeds", nil)
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, req2)
	var list []db.FeedConfig
	_ = json.Unmarshal(rec2.Body.Bytes(), &list)
	if len(list) != 1 {
		t.Fatalf("expected 1 feed after install; got %d", len(list))
	}
	if list[0].Slug != "cisa-kev" {
		t.Errorf("list[0].Slug = %q, want cisa-kev", list[0].Slug)
	}
}

// 4. Install custom — 201.
func TestFeedsInstallCustom(t *testing.T) {
	st := newTestStore(t)
	r := buildFeedsRouter(st, newFakeScheduler(st), nil)

	custom := db.FeedConfigInsert{
		Slug:                   "my-feed",
		Name:                   "My",
		Kind:                   "single_file",
		URL:                    "https://x",
		Parser:                 "yara",
		RefreshIntervalSeconds: 3600,
		Enabled:                true,
	}
	body, _ := json.Marshal(map[string]any{"custom": custom})
	req := httptest.NewRequest(http.MethodPost, "/api/feeds", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("install custom: status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
}

// 5. Install with both fields set — 400.
func TestFeedsInstallBothFields(t *testing.T) {
	st := newTestStore(t)
	r := buildFeedsRouter(st, newFakeScheduler(st), nil)

	body, _ := json.Marshal(map[string]any{
		"from_registry_slug": "cisa-kev",
		"custom": db.FeedConfigInsert{Slug: "x", Kind: "single_file", URL: "https://x", Parser: "yara"},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/feeds", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("both fields: status = %d, want 400", rec.Code)
	}
}

// 6. Install with neither set — 400.
func TestFeedsInstallNeitherField(t *testing.T) {
	st := newTestStore(t)
	r := buildFeedsRouter(st, newFakeScheduler(st), nil)

	body, _ := json.Marshal(map[string]any{})
	req := httptest.NewRequest(http.MethodPost, "/api/feeds", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("neither field: status = %d, want 400", rec.Code)
	}
}

// 7. Update — install, patch Enabled=false, verify via GetFeedConfig.
func TestFeedsUpdate(t *testing.T) {
	st := newTestStore(t)
	r := buildFeedsRouter(st, newFakeScheduler(st), nil)

	// Install first.
	body, _ := json.Marshal(map[string]any{"from_registry_slug": "cisa-kev"})
	req := httptest.NewRequest(http.MethodPost, "/api/feeds", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("install: %d", rec.Code)
	}
	var installResp installFeedResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &installResp)

	// Patch Enabled=false.
	enabled := false
	patch := db.FeedConfigPatch{Enabled: &enabled}
	patchBody, _ := json.Marshal(patch)
	req2 := httptest.NewRequest(http.MethodPatch, "/api/feeds/"+strconv.FormatInt(installResp.ID, 10), bytes.NewReader(patchBody))
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusNoContent {
		t.Fatalf("patch: status = %d, want 204; body = %s", rec2.Code, rec2.Body.String())
	}

	// Verify via store.
	fc, err := st.GetFeedConfig(installResp.ID)
	if err != nil {
		t.Fatalf("GetFeedConfig: %v", err)
	}
	if fc.Enabled {
		t.Error("expected feed to be disabled after patch")
	}
}

// 8. Uninstall reconciles to empty.
func TestFeedsUninstall(t *testing.T) {
	st := newTestStore(t)
	r := buildFeedsRouter(st, newFakeScheduler(st), nil)

	// Install a feed.
	feedID, err := st.InsertFeedConfig(&db.FeedConfigInsert{
		Slug: "test-feed", Name: "Test", Kind: "single_file",
		URL: "https://x", Parser: "yara", RefreshIntervalSeconds: 86400,
	})
	if err != nil {
		t.Fatalf("InsertFeedConfig: %v", err)
	}

	// Manually upsert one IOC scoped to the feed.
	_, _, err = st.UpsertIOC(&db.IOCUpsert{
		Kind: "sha256", Value: "abc123", NormalizedValue: "abc123",
		Source: "feed:test-feed", FeedID: &feedID,
	})
	if err != nil {
		t.Fatalf("UpsertIOC: %v", err)
	}

	// Verify IOC is present.
	iocs, _ := st.ListIOCsByFeed(feedID)
	if len(iocs) != 1 {
		t.Fatalf("expected 1 ioc before uninstall; got %d", len(iocs))
	}

	// DELETE /api/feeds/{id}.
	req := httptest.NewRequest(http.MethodDelete, "/api/feeds/"+strconv.FormatInt(feedID, 10), nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("uninstall: status = %d, want 204; body = %s", rec.Code, rec.Body.String())
	}

	// Feed config should be gone.
	_, err = st.GetFeedConfig(feedID)
	if err == nil {
		t.Error("expected GetFeedConfig to error after uninstall")
	}

	// IOCs should be gone.
	iocs2, _ := st.ListIOCsByFeed(feedID)
	if len(iocs2) != 0 {
		t.Errorf("expected 0 iocs after uninstall; got %d", len(iocs2))
	}
}

// 9. Refresh respects consent gate.
func TestFeedsRefreshConsentGate(t *testing.T) {
	st := newTestStore(t)
	sch := newFakeScheduler(st)
	r := buildFeedsRouter(st, sch, nil)

	// Install a feed.
	feedID, err := st.InsertFeedConfig(&db.FeedConfigInsert{
		Slug: "f1", Name: "F1", Kind: "single_file",
		URL: "https://x", Parser: "yara", RefreshIntervalSeconds: 86400, Enabled: true,
	})
	if err != nil {
		t.Fatalf("InsertFeedConfig: %v", err)
	}

	// Refresh before consent — should 400.
	req := httptest.NewRequest(http.MethodPost, "/api/feeds/"+strconv.FormatInt(feedID, 10)+"/refresh", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("before consent: status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}

	// Grant consent.
	if err := st.SetFeedsConsentGrantedAt(time.Now().UTC()); err != nil {
		t.Fatalf("SetFeedsConsentGrantedAt: %v", err)
	}

	// Refresh after consent — should 202.
	req2 := httptest.NewRequest(http.MethodPost, "/api/feeds/"+strconv.FormatInt(feedID, 10)+"/refresh", nil)
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusAccepted {
		t.Fatalf("after consent: status = %d, want 202; body = %s", rec2.Code, rec2.Body.String())
	}
}

// 10. Validate with httptest server.
func TestFeedsValidate(t *testing.T) {
	// Small YARA bundle served by a local httptest server.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`rule A { strings: $a = "x" condition: $a }` + "\n\n" + `rule B { strings: $b = "y" condition: $b }`))
	}))
	defer srv.Close()

	st := newTestStore(t)
	wk := feeds.NewWorker(st, feeds.NewFetcher(t.TempDir()))
	r := buildFeedsRouter(st, newFakeScheduler(st), wk)

	spec := db.FeedConfigInsert{
		Slug:                   "validate-test",
		Name:                   "Validate Test",
		Kind:                   "single_file",
		URL:                    srv.URL,
		Parser:                 "yara",
		RefreshIntervalSeconds: 3600,
	}
	body, _ := json.Marshal(spec)
	req := httptest.NewRequest(http.MethodPost, "/api/feeds/validate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("validate: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var resp validateFeedResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.EntryCount <= 0 {
		t.Errorf("entry_count = %d, want > 0", resp.EntryCount)
	}
}

// 11. SetConsent flips enabled=true on registry-sourced feeds; leaves custom alone.
func TestFeedsConsentFlipsRegistryFeeds(t *testing.T) {
	st := newTestStore(t)
	r := buildFeedsRouter(st, newFakeScheduler(st), nil)

	// Install a registry-sourced feed via the Install handler (Enabled=true
	// because operator intent), then manually reset it to Enabled=false to
	// simulate what SeedRegistryDefaults does (inserts with Enabled=false).
	// We re-insert directly so we control the starting state.
	regFeedID, err := st.InsertFeedConfig(&db.FeedConfigInsert{
		Slug:                  "cisa-kev",
		Name:                  "CISA KEV",
		Kind:                  "single_file",
		URL:                   "https://example.com",
		Parser:                "cisa_kev_json",
		RefreshIntervalSeconds: 86400,
		Enabled:               false, // seed state: waiting for consent
		InstalledFromRegistry: true,
	})
	if err != nil {
		t.Fatalf("insert registry feed: %v", err)
	}

	// Install a custom feed (Enabled=true; consent endpoint should leave it alone).
	customFeedID, err := st.InsertFeedConfig(&db.FeedConfigInsert{
		Slug:                   "my-custom",
		Name:                   "My Custom",
		Kind:                   "single_file",
		URL:                    "https://example.com/custom",
		Parser:                 "yara",
		RefreshIntervalSeconds: 3600,
		Enabled:                false,
		InstalledFromRegistry:  false,
	})
	if err != nil {
		t.Fatalf("insert custom feed: %v", err)
	}

	// POST /api/feeds/consent.
	req := httptest.NewRequest(http.MethodPost, "/api/feeds/consent", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("consent: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var resp setConsentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode consent response: %v", err)
	}
	if resp.GrantedAt == "" {
		t.Error("expected non-empty granted_at")
	}

	// Registry-sourced feed should now be enabled.
	regFC, err := st.GetFeedConfig(regFeedID)
	if err != nil {
		t.Fatalf("GetFeedConfig(reg): %v", err)
	}
	if !regFC.Enabled {
		t.Error("expected registry feed to be enabled after consent")
	}

	// Custom feed should remain disabled (we don't touch it).
	customFC, err := st.GetFeedConfig(customFeedID)
	if err != nil {
		t.Fatalf("GetFeedConfig(custom): %v", err)
	}
	if customFC.Enabled {
		t.Error("expected custom feed to remain disabled; consent handler should not flip it")
	}
}
