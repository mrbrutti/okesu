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
	hasIndicator := false
	hasRelationship := false
	for _, o := range objs {
		obj, _ := o.(map[string]any)
		switch obj["type"] {
		case "indicator":
			hasIndicator = true
			if !strings.Contains(obj["pattern"].(string), "abc") &&
				!strings.Contains(obj["pattern"].(string), "1.2.3.4") &&
				!strings.Contains(obj["pattern"].(string), "evil.com") {
				t.Errorf("indicator pattern doesn't reference any expected value: %q", obj["pattern"])
			}
		case "relationship":
			hasRelationship = true
			if obj["relationship_type"] != "resolves-to" {
				t.Errorf("relationship_type = %v", obj["relationship_type"])
			}
		}
	}
	if !hasIndicator {
		t.Errorf("expected at least one indicator object")
	}
	if !hasRelationship {
		t.Errorf("expected at least one relationship object")
	}
}
