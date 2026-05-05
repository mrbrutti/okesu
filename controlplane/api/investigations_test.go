package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/section9labs/okesu/controlplane/db"
)

// withChiParams attaches a chi route context with the supplied
// key/value URL params to a test request — needed because the
// handlers now read params via chi.URLParam (so they work under both
// `/api/investigations/{id}` and `/api/v1/federation/investigations/{id}`)
// but httptest.NewRequest bypasses chi's router.
func withChiParams(req *http.Request, kv ...string) *http.Request {
	rctx := chi.NewRouteContext()
	for i := 0; i+1 < len(kv); i += 2 {
		rctx.URLParams.Add(kv[i], kv[i+1])
	}
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func TestCreateInvestigation_HTTP(t *testing.T) {
	st := newTestStore(t)
	body, _ := json.Marshal(map[string]any{
		"title":      "Test case",
		"summary":    "Initial sighting",
		"created_by": "alice@example.com",
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/investigations", bytes.NewReader(body))
	CreateInvestigationHandler(st)(rec, req)
	if rec.Code != 201 {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var got db.Investigation
	json.NewDecoder(rec.Body).Decode(&got)
	if got.Title != "Test case" || got.Status != "active" {
		t.Errorf("unexpected: %+v", got)
	}
}

func TestUpdateInvestigation_HTTP_ClosesWithResolution(t *testing.T) {
	st := newTestStore(t)
	id, _ := st.CreateInvestigation(&db.InvestigationInsert{Title: "x"})

	body, _ := json.Marshal(map[string]any{"status": "closed", "resolution": "resolved"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PATCH", "/api/investigations/"+strconv.FormatInt(id, 10), bytes.NewReader(body))
	req = withChiParams(req, "id", strconv.FormatInt(id, 10))
	UpdateInvestigationHandler(st)(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	got, _ := st.GetInvestigation(id)
	if got.Status != "closed" || got.Resolution != "resolved" {
		t.Errorf("not closed: %+v", got)
	}
}

func TestAddInvestigationNote_HTTP(t *testing.T) {
	st := newTestStore(t)
	id, _ := st.CreateInvestigation(&db.InvestigationInsert{Title: "x"})

	body, _ := json.Marshal(map[string]any{
		"author": "bob@example.com",
		"body":   "Worth checking other hosts",
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/investigations/"+strconv.FormatInt(id, 10)+"/notes", bytes.NewReader(body))
	req = withChiParams(req, "id", strconv.FormatInt(id, 10))
	AddInvestigationNoteHandler(st)(rec, req)
	if rec.Code != 201 {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	notes, _ := st.ListInvestigationNotes(id)
	if len(notes) != 1 || !strings.Contains(notes[0].Body, "other hosts") {
		t.Errorf("unexpected notes: %+v", notes)
	}
}

func TestListInvestigations_HTTP(t *testing.T) {
	st := newTestStore(t)
	st.CreateInvestigation(&db.InvestigationInsert{Title: "case 1"})
	st.CreateInvestigation(&db.InvestigationInsert{Title: "case 2"})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/investigations", nil)
	ListInvestigationsHandler(st)(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var got []db.Investigation
	json.NewDecoder(rec.Body).Decode(&got)
	if len(got) != 2 {
		t.Errorf("expected 2; got %d", len(got))
	}
}

func TestGetInvestigation_HTTP_ReturnsBundle(t *testing.T) {
	st := newTestStore(t)
	id, _ := st.CreateInvestigation(&db.InvestigationInsert{Title: "x"})
	st.AddInvestigationNote(id, "alice", "hello")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/investigations/"+strconv.FormatInt(id, 10), nil)
	req = withChiParams(req, "id", strconv.FormatInt(id, 10))
	GetInvestigationHandler(st)(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp struct {
		Investigation db.Investigation       `json:"investigation"`
		Findings      []int64                `json:"findings"`
		Runs          []int64                `json:"runs"`
		Notes         []db.InvestigationNote `json:"notes"`
	}
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Investigation.ID != id {
		t.Errorf("wrong investigation: %+v", resp.Investigation)
	}
	if len(resp.Notes) != 1 {
		t.Errorf("expected 1 note; got %d", len(resp.Notes))
	}
}

// TestLinkFindingToInvestigation_HTTP_Links exercises the only handler
// with non-trivial path parsing — three segments after the prefix,
// hardcoded `findings` middle, two ParseInts.
func TestLinkFindingToInvestigation_HTTP_Links(t *testing.T) {
	st := newTestStore(t)
	invID, _ := st.CreateInvestigation(&db.InvestigationInsert{Title: "case"})
	eventID, err := st.InsertEvent(&db.Event{
		Ts: 1, Type: "finding", Agent: sql.NullString{String: "t", Valid: true}, RawJSON: "{}",
	})
	if err != nil {
		t.Fatal(err)
	}
	findingID, _ := st.InsertFinding(&db.FindingInsert{
		EventID: eventID, Ts: 1, Title: "x", Severity: "MEDIUM",
	})

	rec := httptest.NewRecorder()
	path := "/api/investigations/" + strconv.FormatInt(invID, 10) + "/findings/" + strconv.FormatInt(findingID, 10)
	req := httptest.NewRequest("PUT", path, nil)
	req = withChiParams(req,
		"id", strconv.FormatInt(invID, 10),
		"finding_id", strconv.FormatInt(findingID, 10),
	)
	LinkFindingToInvestigationHandler(st)(rec, req)
	if rec.Code != 204 {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}

	got, _ := st.ListFindingsForInvestigation(invID)
	if len(got) != 1 || got[0] != findingID {
		t.Errorf("expected linked finding %d; got %v", findingID, got)
	}
}

// TestLinkFindingToInvestigation_HTTP_RejectsBadParams confirms the
// handler returns 400 when the chi-bound params can't parse to int64.
// Routing-shape rejections (wrong middle segment, missing segments)
// don't reach the handler now — chi's router screens them first.
func TestLinkFindingToInvestigation_HTTP_RejectsBadParams(t *testing.T) {
	st := newTestStore(t)
	cases := []struct {
		name      string
		invID     string
		findingID string
	}{
		{"bad investigation id", "notanumber", "5"},
		{"bad finding id", "1", "notanumber"},
		{"empty investigation id", "", "5"},
		{"empty finding id", "1", ""},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("PUT", "/api/investigations/x/findings/y", nil)
		req = withChiParams(req, "id", c.invID, "finding_id", c.findingID)
		LinkFindingToInvestigationHandler(st)(rec, req)
		if rec.Code != 400 {
			t.Errorf("%s: status = %d, want 400", c.name, rec.Code)
		}
	}
}

func TestGetInvestigation_SurfacesListErrors(t *testing.T) {
	store := newSeededTestStore(t)
	invID, err := store.CreateInvestigation(&db.InvestigationInsert{Title: "case"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Dropping a single list-source table is sufficient to exercise
	// the recordWarn path: the closure is structurally identical for
	// all six list calls (findings/runs/iocs/daimons/orchestrations/
	// notes), so a regression that re-introduces silent discards would
	// fail the warnings[0] prefix assertion below regardless of which
	// table we break.
	if _, err := store.Exec(`DROP TABLE investigation_findings`); err != nil {
		t.Fatalf("schema break: %v", err)
	}

	router := chi.NewRouter()
	router.Get("/api/investigations/{id}", GetInvestigationHandler(store))

	req := httptest.NewRequest("GET", "/api/investigations/"+strconv.FormatInt(invID, 10), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	warnings, ok := resp["bundle_warnings"].([]any)
	if !ok || len(warnings) == 0 {
		t.Fatalf("expected bundle_warnings entry; got %v", resp["bundle_warnings"])
	}
	first, _ := warnings[0].(string)
	if !strings.HasPrefix(first, "findings:") {
		t.Errorf("warning[0] = %q, want prefix \"findings:\"", first)
	}
	// Other arrays should still be empty slices, not missing.
	if _, ok := resp["findings"].([]any); !ok {
		t.Errorf("findings should be [] not nil")
	}
}

func TestGetInvestigation_NoWarningsOnSuccess(t *testing.T) {
	store := newSeededTestStore(t)
	invID, err := store.CreateInvestigation(&db.InvestigationInsert{Title: "case"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	router := chi.NewRouter()
	router.Get("/api/investigations/{id}", GetInvestigationHandler(store))

	req := httptest.NewRequest("GET", "/api/investigations/"+strconv.FormatInt(invID, 10), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if _, ok := resp["bundle_warnings"]; ok {
		t.Errorf("bundle_warnings should be omitted on success; got %v", resp["bundle_warnings"])
	}
}
