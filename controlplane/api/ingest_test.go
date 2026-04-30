package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/section9labs/okesu/controlplane/adapters/sqliteevents"
	"github.com/section9labs/okesu/controlplane/db"
)

// noopBroadcaster satisfies api.Broadcaster for tests that don't care
// about the SSE side-effect.
type noopBroadcaster struct{}

func (noopBroadcaster) Publish(_ []byte) {}

// TestFindingIngest_ExtractsAndLinksIOCs POSTs a finding through the
// real handler and asserts that IOCs appearing in the title/evidence
// are upserted to the iocs table and observation-linked to the finding.
//
// The extraction call inside FindingIngest is synchronous (Task D2),
// so this test does not need any sleeps or eventually-style polling.
func TestFindingIngest_ExtractsAndLinksIOCs(t *testing.T) {
	store := newTestStore(t)
	es := sqliteevents.New(store)

	body, err := json.Marshal(FindingIngestRequest{
		Agent:    "test",
		Host:     "host-1",
		Severity: "HIGH",
		Title:    "Saw 1.2.3.4 talk to evil[.]example.com",
		Evidence: "hash 0000000000000000000000000000000000000000000000000000000000000000 referenced CVE-2024-1234",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	h := FindingIngest(store, es, noopBroadcaster{})
	req := httptest.NewRequest(http.MethodPost, "/api/findings/ingest", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("ingest: got %d, want 202; body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		FindingID int64 `json:"finding_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.FindingID == 0 {
		t.Fatalf("expected non-zero finding_id; body=%s", rec.Body.String())
	}

	// Each IOC the extractor finds should now be in the iocs table and
	// linked to the finding via ioc_observations.
	cases := []struct {
		kind, normalized string
	}{
		{"ipv4", "1.2.3.4"},
		{"domain", "evil.example.com"},
		{"sha256", "0000000000000000000000000000000000000000000000000000000000000000"},
		{"cve", "CVE-2024-1234"},
	}
	for _, c := range cases {
		ioc, err := store.LookupIOC(c.kind, c.normalized)
		if err != nil {
			t.Errorf("LookupIOC(%s, %s): %v", c.kind, c.normalized, err)
			continue
		}
		if ioc.Source != "observed" {
			t.Errorf("ioc %s/%s source = %q, want observed", c.kind, c.normalized, ioc.Source)
		}
		if ioc.ObservationCount < 1 {
			t.Errorf("ioc %s/%s observation_count = %d, want >= 1", c.kind, c.normalized, ioc.ObservationCount)
		}
		obs, err := store.ListIOCObservations(ioc.ID)
		if err != nil {
			t.Fatalf("ListIOCObservations: %v", err)
		}
		linked := false
		for _, o := range obs {
			if o.FindingID == resp.FindingID {
				linked = true
				break
			}
		}
		if !linked {
			t.Errorf("ioc %s/%s not linked to finding %d; observations=%+v", c.kind, c.normalized, resp.FindingID, obs)
		}
	}
}

// TestFindingIngest_RaisesSeverityFromCatalogFloor exercises the
// Phase 22.2 propagation path end-to-end: a catalog IOC with
// severity_floor=HIGH should raise an incoming MEDIUM finding, and
// since this is the first surface of that IOC the finding should
// receive a freshly-minted cluster_id (its own row id, stringified).
func TestFindingIngest_RaisesSeverityFromCatalogFloor(t *testing.T) {
	store := newTestStore(t)
	es := sqliteevents.New(store)

	store.UpsertIOC(&db.IOCUpsert{
		Kind:            "sha256",
		Value:           "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		NormalizedValue: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		Source:          "catalog",
		SeverityFloor:   "HIGH",
	})

	body, err := json.Marshal(FindingIngestRequest{
		Agent:    "test",
		Host:     "h1",
		Severity: "MEDIUM",
		Title:    "saw hash deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	h := FindingIngest(store, es, noopBroadcaster{})
	req := httptest.NewRequest(http.MethodPost, "/api/findings/ingest", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		FindingID int64 `json:"finding_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	f, err := store.FindingByID(resp.FindingID)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.Severity.String; got != "HIGH" {
		t.Errorf("finding severity = %q, want HIGH (raised from MEDIUM)", got)
	}
	if f.ClusterID == "" {
		t.Error("cluster_id should be minted on first finding")
	}
}

// TestFindingIngest_StoresSubtype verifies that the Phase 22.3 subtype
// field on FindingIngestRequest is threaded through to the persisted
// finding row, so external producers can flag hypotheses, meeting
// minutes, etc. for special rendering downstream.
func TestFindingIngest_StoresSubtype(t *testing.T) {
	store := newTestStore(t)
	es := sqliteevents.New(store)

	body, err := json.Marshal(map[string]any{
		"agent":    "hypothesis-writer",
		"host":     "h1",
		"severity": "MEDIUM",
		"title":    "Lateral movement via SMB",
		"subtype":  "hypothesis",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	h := FindingIngest(store, es, noopBroadcaster{})
	req := httptest.NewRequest(http.MethodPost, "/api/findings/ingest", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		FindingID int64 `json:"finding_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	f, err := store.FindingByID(resp.FindingID)
	if err != nil {
		t.Fatal(err)
	}
	if f.Subtype != "hypothesis" {
		t.Errorf("Subtype = %q, want hypothesis", f.Subtype)
	}
}
