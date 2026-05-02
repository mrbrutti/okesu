package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/section9labs/okesu/controlplane/db"
)

func TestInvestigationStructure_Unknown404(t *testing.T) {
	store := newSeededTestStore(t)
	router := chi.NewRouter()
	router.Get("/api/investigations/{id}/structure", GetInvestigationStructureHandler(store))

	req := httptest.NewRequest("GET", "/api/investigations/99999/structure", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestInvestigationStructure_EmptyCase(t *testing.T) {
	store := newSeededTestStore(t)
	id, err := store.CreateInvestigation(&db.InvestigationInsert{Title: "empty"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	router := chi.NewRouter()
	router.Get("/api/investigations/{id}/structure", GetInvestigationStructureHandler(store))

	req := httptest.NewRequest("GET", "/api/investigations/"+strconv.FormatInt(id, 10)+"/structure", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var got map[string]any
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, k := range []string{"hosts", "iocs", "daimons", "orchestrations"} {
		v, ok := got[k]
		if !ok {
			t.Errorf("missing key %q", k)
			continue
		}
		arr, ok := v.([]any)
		if !ok {
			t.Errorf("key %q is %T, want []any", k, v)
			continue
		}
		if len(arr) != 0 {
			t.Errorf("key %q has %d entries, want 0", k, len(arr))
		}
	}
}

func TestInvestigationStructure_Populated(t *testing.T) {
	store := newSeededTestStore(t)
	id, err := store.CreateInvestigation(&db.InvestigationInsert{Title: "case"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// 2 findings on host-a, 1 on host-b.
	for i, host := range []string{"host-a", "host-a", "host-b"} {
		fid := mustAPIInsertFinding(t, store, "HIGH", host, "edr-agent", int64(1000+i))
		if err := store.LinkFindingToInvestigation(id, fid); err != nil {
			t.Fatalf("link finding: %v", err)
		}
	}

	router := chi.NewRouter()
	router.Get("/api/investigations/{id}/structure", GetInvestigationStructureHandler(store))
	req := httptest.NewRequest("GET", "/api/investigations/"+strconv.FormatInt(id, 10)+"/structure", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var got struct {
		Hosts []struct {
			Host  string `json:"host"`
			Count int    `json:"count"`
		} `json:"hosts"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Hosts) != 2 {
		t.Fatalf("len(hosts) = %d, want 2", len(got.Hosts))
	}
	if got.Hosts[0].Host != "host-a" || got.Hosts[0].Count != 2 {
		t.Errorf("hosts[0] = %+v, want host-a/2", got.Hosts[0])
	}
}

// mustAPIInsertFinding inserts a parent event then a finding.
// FindingInsert uses plain strings; Event.Agent uses sql.NullString.
func mustAPIInsertFinding(t *testing.T, s *db.Store, severity, host, agent string, ts int64) int64 {
	t.Helper()
	eventID, err := s.InsertEvent(&db.Event{
		Ts:      ts,
		Type:    "finding",
		Agent:   sql.NullString{String: agent, Valid: agent != ""},
		RawJSON: "{}",
	})
	if err != nil {
		t.Fatalf("InsertEvent: %v", err)
	}
	fid, err := s.InsertFinding(&db.FindingInsert{
		EventID:  eventID,
		Ts:       ts,
		Agent:    agent,
		Host:     host,
		Severity: severity,
		Title:    "t",
	})
	if err != nil {
		t.Fatalf("InsertFinding: %v", err)
	}
	return fid
}
