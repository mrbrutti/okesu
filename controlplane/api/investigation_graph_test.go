package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/section9labs/okesu/controlplane/db"
)

func TestInvestigationGraph_Returns404OnUnknown(t *testing.T) {
	store := newSeededTestStore(t)
	router := chi.NewRouter()
	router.Get("/api/investigations/{id}/graph", GetInvestigationGraphHandler(store))

	req := httptest.NewRequest("GET", "/api/investigations/99999/graph", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestInvestigationGraph_EmptyCase(t *testing.T) {
	store := newSeededTestStore(t)
	id, err := store.CreateInvestigation(&db.InvestigationInsert{Title: "empty"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	router := chi.NewRouter()
	router.Get("/api/investigations/{id}/graph", GetInvestigationGraphHandler(store))

	req := httptest.NewRequest("GET", "/api/investigations/"+strconv.FormatInt(id, 10)+"/graph", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp graphResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.TotalFindings != 0 {
		t.Errorf("TotalFindings = %d", resp.TotalFindings)
	}
	if len(resp.Nodes) != 0 {
		t.Errorf("Nodes = %d", len(resp.Nodes))
	}
	if len(resp.Edges) != 0 {
		t.Errorf("Edges = %d", len(resp.Edges))
	}
}

func TestInvestigationGraph_BuildResponse(t *testing.T) {
	// Pure-function test on buildGraphResponse with a synthetic db.GraphData.
	data := &db.GraphData{
		TotalFindings: 3,
		LimitApplied:  20,
		Findings: []db.GraphFinding{
			{ID: 1, Severity: "HIGH", Title: "Cron", Host: "h1", Agent: "edr-agent"},
			{ID: 2, Severity: "LOW", Title: "User", Host: "h1", Agent: "edr-agent"},
			{ID: 3, Severity: "INFO", Title: "Trivial", Host: "h2", Agent: ""},
		},
		IOCs: []db.GraphIOC{
			{ID: 100, Kind: "sha256", Value: "deadbeef", ObsCount: 2, HostCount: 1, FindingIDs: []int64{1, 2}},
		},
	}
	resp := buildGraphResponse(data)

	// 3 findings + 2 hosts + 1 daimon + 1 ioc = 7 nodes.
	if len(resp.Nodes) != 7 {
		t.Errorf("Nodes = %d, want 7", len(resp.Nodes))
	}
	// Edges: f1→h1, f1→edr, f1→i; f2→h1, f2→edr, f2→i; f3→h2 = 7 edges.
	if len(resp.Edges) != 7 {
		t.Errorf("Edges = %d, want 7", len(resp.Edges))
	}
	// daimon node finding_count is 2 (f1+f2; f3 has no agent).
	for _, n := range resp.Nodes {
		if n.Kind == "daimon" && n.ID == "d:edr-agent" {
			if n.FindingCount != 2 {
				t.Errorf("daimon finding_count = %d, want 2", n.FindingCount)
			}
		}
	}
}

func TestLastN(t *testing.T) {
	cases := map[string]string{
		"deadbeefcafe": "cafe",
		"abc":          "abc",
		"":             "",
		"1234":         "1234",
	}
	for in, want := range cases {
		if got := lastN(in, 4); got != want {
			t.Errorf("lastN(%q, 4) = %q, want %q", in, got, want)
		}
	}
}
