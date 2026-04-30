package api

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/section9labs/okesu/controlplane/db"
)

func TestYARARulesYar_ConcatenatesCatalogRules(t *testing.T) {
	st := newTestStore(t)
	st.UpsertIOC(&db.IOCUpsert{
		Kind:            "yara_rule",
		Value:           "rule One { condition: true }",
		NormalizedValue: "rule one { condition: true }",
		Source:          "catalog",
		Name:            "One",
		Tags:            "ransomware",
	})
	st.UpsertIOC(&db.IOCUpsert{
		Kind:            "yara_rule",
		Value:           "rule Two { condition: true }",
		NormalizedValue: "rule two { condition: true }",
		Source:          "catalog",
		Name:            "Two",
		Tags:            "phishing",
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/catalog/yara-rules.yar", nil)
	YARARulesYarHandler(st)(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/x-yara" {
		t.Errorf("Content-Type = %q, want text/x-yara", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "rule One") || !strings.Contains(body, "rule Two") {
		t.Errorf("body should contain both rules; got:\n%s", body)
	}
}

func TestYARARulesYar_FiltersByTag(t *testing.T) {
	st := newTestStore(t)
	st.UpsertIOC(&db.IOCUpsert{
		Kind: "yara_rule", Value: "rule R1 { condition: true }",
		NormalizedValue: "rule r1 { condition: true }",
		Source:          "catalog", Tags: "ransomware,emotet",
	})
	st.UpsertIOC(&db.IOCUpsert{
		Kind: "yara_rule", Value: "rule P1 { condition: true }",
		NormalizedValue: "rule p1 { condition: true }",
		Source:          "catalog", Tags: "phishing",
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/catalog/yara-rules.yar?tag=ransomware", nil)
	YARARulesYarHandler(st)(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, "rule R1") {
		t.Errorf("ransomware-tagged rule missing")
	}
	if strings.Contains(body, "rule P1") {
		t.Errorf("phishing rule should have been filtered out")
	}
}

func TestYARARulesYar_EmptyCatalog(t *testing.T) {
	st := newTestStore(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/catalog/yara-rules.yar", nil)
	YARARulesYarHandler(st)(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	// Body should be empty (or comment-only) — operators piping this
	// into `yara` get an empty rule set, not a 404.
	if rec.Body.Len() != 0 && !strings.HasPrefix(rec.Body.String(), "//") {
		t.Errorf("empty catalog should produce empty body (or comment-only); got: %q", rec.Body.String())
	}
}
