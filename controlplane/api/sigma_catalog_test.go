package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/section9labs/okesu/controlplane/db"
)

func TestSigmaRulesYml_ConcatsWithHeaders(t *testing.T) {
	st := newTestStore(t)
	feedID, err := st.InsertFeedConfig(&db.FeedConfigInsert{
		Slug: "f1", Name: "F1", Kind: "single_file", URL: "x", Parser: "sigma",
		RefreshIntervalSeconds: 86400, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.UpsertIOC(&db.IOCUpsert{
		Kind:            "sigma_rule",
		Value:           "title: Rule One\nlevel: high\ndetection:\n  selection:\n    EventID: 4624\n  condition: selection",
		NormalizedValue: "norm-one",
		Source:          "feed:f1",
		Name:            "Rule One",
		Tags:            "attack.execution",
		SeverityFloor:   "HIGH",
		FeedID:          &feedID,
	}); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/catalog/sigma-rules.yml", nil)
	SigmaRulesYmlHandler(st)(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status: %d %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "# catalog name: Rule One") {
		t.Fatalf("missing comment header: %q", body)
	}
	if !strings.Contains(body, "# tags: attack.execution") {
		t.Fatalf("missing tags header: %q", body)
	}
	if !strings.Contains(body, "# severity_floor: HIGH") {
		t.Fatalf("missing severity header: %q", body)
	}
	if !strings.Contains(body, "title: Rule One") {
		t.Fatalf("missing rule body: %q", body)
	}
}

func TestSigmaRulesYml_TagFilter(t *testing.T) {
	st := newTestStore(t)
	feedID, _ := st.InsertFeedConfig(&db.FeedConfigInsert{
		Slug: "f1", Name: "F1", Kind: "single_file", URL: "x", Parser: "sigma",
		RefreshIntervalSeconds: 86400, Enabled: true,
	})
	// Two rules: one with tag "lateral", one without.
	if _, _, err := st.UpsertIOC(&db.IOCUpsert{
		Kind: "sigma_rule", Value: "title: A\n", NormalizedValue: "norm-a",
		Source: "feed:f1", Name: "A", Tags: "lateral,attack.t1021", SeverityFloor: "HIGH", FeedID: &feedID,
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.UpsertIOC(&db.IOCUpsert{
		Kind: "sigma_rule", Value: "title: B\n", NormalizedValue: "norm-b",
		Source: "feed:f1", Name: "B", Tags: "execution", SeverityFloor: "MEDIUM", FeedID: &feedID,
	}); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/catalog/sigma-rules.yml?tag=lateral", nil)
	SigmaRulesYmlHandler(st)(w, r)

	body := w.Body.String()
	if !strings.Contains(body, "# catalog name: A") {
		t.Fatalf("expected A in tag-filtered output: %q", body)
	}
	if strings.Contains(body, "# catalog name: B") {
		t.Fatalf("did not expect B in tag-filtered output: %q", body)
	}
}

func TestSigmaRulesYml_MultiDocSeparator(t *testing.T) {
	st := newTestStore(t)
	feedID, _ := st.InsertFeedConfig(&db.FeedConfigInsert{
		Slug: "f1", Name: "F1", Kind: "single_file", URL: "x", Parser: "sigma",
		RefreshIntervalSeconds: 86400, Enabled: true,
	})
	for i, name := range []string{"A", "B"} {
		if _, _, err := st.UpsertIOC(&db.IOCUpsert{
			Kind: "sigma_rule", Value: "title: " + name, NormalizedValue: "norm-" + name,
			Source: "feed:f1", Name: name, FeedID: &feedID,
		}); err != nil {
			t.Fatalf("rule %d: %v", i, err)
		}
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/catalog/sigma-rules.yml", nil)
	SigmaRulesYmlHandler(st)(w, r)

	body := w.Body.String()
	if !strings.Contains(body, "\n---\n") {
		t.Fatalf("expected multi-doc separator between rules: %q", body)
	}
}

func TestSigmaRulesYml_ContentType(t *testing.T) {
	st := newTestStore(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/catalog/sigma-rules.yml", nil)
	SigmaRulesYmlHandler(st)(w, r)
	if got := w.Header().Get("Content-Type"); got != "application/yaml" {
		t.Fatalf("content-type: %q", got)
	}
}
