package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/section9labs/okesu/controlplane/db"
)

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
	LinkFindingToInvestigationHandler(st)(rec, req)
	if rec.Code != 204 {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}

	got, _ := st.ListFindingsForInvestigation(invID)
	if len(got) != 1 || got[0] != findingID {
		t.Errorf("expected linked finding %d; got %v", findingID, got)
	}
}

// TestLinkFindingToInvestigation_HTTP_RejectsMalformedPath confirms the
// path parser refuses misshapen URLs.
func TestLinkFindingToInvestigation_HTTP_RejectsMalformedPath(t *testing.T) {
	st := newTestStore(t)
	cases := []string{
		"/api/investigations/1/whatever/2",          // wrong middle segment
		"/api/investigations/notanumber/findings/5", // bad investigation id
		"/api/investigations/1/findings/notanumber", // bad finding id
		"/api/investigations/1",                     // too few segments
	}
	for _, p := range cases {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("PUT", p, nil)
		LinkFindingToInvestigationHandler(st)(rec, req)
		if rec.Code != 400 {
			t.Errorf("path %q: status = %d, want 400", p, rec.Code)
		}
	}
}
