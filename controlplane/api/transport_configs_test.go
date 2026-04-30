package api

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/section9labs/okesu/controlplane/db"
)

func TestTransportConfigPatch_NameAndScannerInterval(t *testing.T) {
	st := newTestStore(t)
	tcID, err := st.CreateTransportConfig(db.TransportConfig{
		Name: "old", Kind: "s3", Bucket: "b", Endpoint: "https://e", UseSSL: true,
	})
	if err != nil {
		t.Fatalf("CreateTransportConfig: %v", err)
	}

	r := chi.NewRouter()
	r.Patch("/api/transport-configs/{id}", TransportConfigPatch(st))

	body, _ := json.Marshal(map[string]any{
		"name":                "new-name",
		"scanner_interval_ms": 60000,
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PATCH", "/api/transport-configs/"+strconv.FormatInt(tcID, 10), bytes.NewReader(body))
	r.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	got, _ := st.GetTransportConfig(tcID)
	if got.Name != "new-name" {
		t.Errorf("Name = %q, want new-name", got.Name)
	}
	if got.ScannerIntervalMs != 60000 {
		t.Errorf("ScannerIntervalMs = %d, want 60000", got.ScannerIntervalMs)
	}
}

func TestTransportConfigPatch_IdentityFieldsIgnored(t *testing.T) {
	st := newTestStore(t)
	tcID, _ := st.CreateTransportConfig(db.TransportConfig{
		Name: "test", Kind: "s3", Bucket: "original", Endpoint: "https://original", UseSSL: true,
	})

	r := chi.NewRouter()
	r.Patch("/api/transport-configs/{id}", TransportConfigPatch(st))

	body, _ := json.Marshal(map[string]any{
		"bucket":   "should-be-ignored",
		"endpoint": "https://should-be-ignored",
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PATCH", "/api/transport-configs/"+strconv.FormatInt(tcID, 10), bytes.NewReader(body))
	r.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	got, _ := st.GetTransportConfig(tcID)
	if got.Bucket != "original" || got.Endpoint != "https://original" {
		t.Errorf("identity fields should not change; got bucket=%q endpoint=%q", got.Bucket, got.Endpoint)
	}
}

func TestTransportConfigDelete_409WhenReferenced(t *testing.T) {
	st := newTestStore(t)
	tcID, _ := st.CreateTransportConfig(db.TransportConfig{
		Name: "x", Kind: "s3", Bucket: "b", Endpoint: "https://e", UseSSL: true,
	})
	// CreateNode doesn't accept a transport_config_id, so insert the
	// node then link it via a direct UPDATE.
	nodeID, err := st.CreateNode("node-1", "host-1", "root", 22, "")
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	if _, err := st.Exec(`UPDATE nodes SET transport_config_id = ? WHERE id = ?`, tcID, nodeID); err != nil {
		t.Fatalf("link node to transport_config: %v", err)
	}

	r := chi.NewRouter()
	r.Delete("/api/transport-configs/{id}", TransportConfigDelete(st))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/transport-configs/"+strconv.FormatInt(tcID, 10), nil)
	r.ServeHTTP(rec, req)
	if rec.Code != 409 {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	var got transportConfigDeleteConflict
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.ReferencedBy.Nodes) != 1 {
		t.Errorf("expected 1 referencing node; got %+v", got.ReferencedBy)
	}
}

func TestTransportConfigDelete_204WhenClean(t *testing.T) {
	st := newTestStore(t)
	tcID, _ := st.CreateTransportConfig(db.TransportConfig{
		Name: "x", Kind: "s3", Bucket: "b", Endpoint: "https://e", UseSSL: true,
	})

	r := chi.NewRouter()
	r.Delete("/api/transport-configs/{id}", TransportConfigDelete(st))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/transport-configs/"+strconv.FormatInt(tcID, 10), nil)
	r.ServeHTTP(rec, req)
	if rec.Code != 204 {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
}
