package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/section9labs/okesu/controlplane/db"
)

func TestSTIXExport_EmitsValidBundle(t *testing.T) {
	st := newTestStore(t)
	iocID, _, _ := st.UpsertIOC(&db.IOCUpsert{
		Kind: "sha256", Value: "abc", NormalizedValue: "abc",
		Source: "catalog", SeverityFloor: "HIGH", Attribution: "apt-foo",
	})
	st.UpsertIOC(&db.IOCUpsert{Kind: "ipv4", Value: "1.2.3.4", NormalizedValue: "1.2.3.4"})
	other, _, _ := st.UpsertIOC(&db.IOCUpsert{Kind: "domain", Value: "evil.com", NormalizedValue: "evil.com"})
	st.AddIOCRelationship(&db.IOCRelationshipInsert{
		SubjectID: other, Predicate: "resolves-to", ObjectID: iocID, Source: "agent",
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/stix2/iocs", nil)
	STIX2ExportHandler(st)(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var bundle map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &bundle); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if bundle["type"] != "bundle" {
		t.Errorf("bundle type = %v, want 'bundle'", bundle["type"])
	}
	objs, _ := bundle["objects"].([]any)

	// Map from "<kind> <value>" -> expected substring in pattern.
	wantPattern := map[string]string{
		"sha256 abc":      "abc",
		"ipv4 1.2.3.4":    "1.2.3.4",
		"domain evil.com": "evil.com",
	}
	indicatorsSeen := 0
	hasRelationship := false
	for _, o := range objs {
		obj, _ := o.(map[string]any)
		switch obj["type"] {
		case "indicator":
			indicatorsSeen++
			name, _ := obj["name"].(string)
			pattern, _ := obj["pattern"].(string)
			want, ok := wantPattern[name]
			if !ok {
				t.Errorf("unexpected indicator name %q", name)
				continue
			}
			if !strings.Contains(pattern, want) {
				t.Errorf("indicator %q pattern %q missing %q", name, pattern, want)
			}
		case "relationship":
			hasRelationship = true
			if obj["relationship_type"] != "resolves-to" {
				t.Errorf("relationship_type = %v", obj["relationship_type"])
			}
		}
	}
	if indicatorsSeen != 3 {
		t.Errorf("expected 3 indicator objects, saw %d", indicatorsSeen)
	}
	if !hasRelationship {
		t.Errorf("expected at least one relationship object")
	}
}

func TestSTIXExport_RejectsInvalidSince(t *testing.T) {
	st := newTestStore(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/stix2/iocs?since=not-a-date", nil)
	STIX2ExportHandler(st)(rec, req)
	if rec.Code != 400 {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestSTIXExport_EscapesQuotesInPattern(t *testing.T) {
	st := newTestStore(t)
	st.UpsertIOC(&db.IOCUpsert{
		Kind:            "url",
		Value:           "https://evil.com/it's-bad",
		NormalizedValue: "https://evil.com/it's-bad",
		Source:          "catalog",
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/stix2/iocs", nil)
	STIX2ExportHandler(st)(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var bundle map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &bundle); err != nil {
		t.Fatalf("decode: %v", err)
	}
	objs, _ := bundle["objects"].([]any)
	found := false
	for _, o := range objs {
		obj, _ := o.(map[string]any)
		if obj["type"] != "indicator" {
			continue
		}
		pattern, _ := obj["pattern"].(string)
		if strings.Contains(pattern, "evil.com") {
			found = true
			// Bare unescaped single quote between the two delimiter quotes
			// would break the STIX pattern. Verify the value's quote is
			// backslash-escaped.
			if !strings.Contains(pattern, `it\'s-bad`) {
				t.Errorf("expected escaped single quote in pattern; got %q", pattern)
			}
		}
	}
	if !found {
		t.Errorf("no url indicator emitted")
	}
}
