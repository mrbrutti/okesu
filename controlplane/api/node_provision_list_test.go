// Tests for the node-provisions list/get/delete + estimate handlers.
// Mirrors the test pattern in node_provision_test.go (newSeededTestStore +
// the same fakeNodeProvisionerRegistry helper) so we don't drag the real
// OCI/AWS SDKs into a unit test. The chi.URLParam pattern follows the
// withChiParams helper used in investigations_test.go.

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

func TestNodeProvisionsList_Empty(t *testing.T) {
	st := newSeededTestStore(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/node-provisions", nil)
	NodeProvisionsListHandler(st)(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Errorf("body=%q want []", rec.Body.String())
	}
}

func TestNodeProvisionsList_HasRows(t *testing.T) {
	st := newSeededTestStore(t)
	tcID, err := st.CreateTransportConfig(db.TransportConfig{Name: "t", Kind: "s3", Bucket: "b", Endpoint: "h"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		_, err := st.InsertNodeProvision(db.NodeProvisionInsert{
			DisplayName:       "n",
			Region:            "r",
			Cloud:             "oci",
			TransportConfigID: tcID,
			CloudParamsJSON:   "{}",
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/node-provisions", nil)
	NodeProvisionsListHandler(st)(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("got %d rows, want 3", len(got))
	}
}

func TestNodeProvisionGet_NotFound(t *testing.T) {
	st := newSeededTestStore(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/node-provisions/9999", nil)
	req = withChiParams(req, "id", "9999")
	NodeProvisionGetHandler(st)(rec, req)
	if rec.Code != 404 {
		t.Errorf("status=%d want 404", rec.Code)
	}
}

func TestNodeProvisionGet_Found(t *testing.T) {
	st := newSeededTestStore(t)
	tcID, _ := st.CreateTransportConfig(db.TransportConfig{Name: "t", Kind: "s3", Bucket: "b", Endpoint: "h"})
	row, err := st.InsertNodeProvision(db.NodeProvisionInsert{
		DisplayName:       "edge-1",
		Region:            "us-phoenix-1",
		Cloud:             "oci",
		TransportConfigID: tcID,
		CloudParamsJSON:   `{"shape":"VM.Standard.E4.Flex"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/node-provisions/"+strconv.FormatInt(row.ID, 10), nil)
	req = withChiParams(req, "id", strconv.FormatInt(row.ID, 10))
	NodeProvisionGetHandler(st)(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["display_name"] != "edge-1" {
		t.Errorf("display_name=%v want edge-1", got["display_name"])
	}
}

// TestNodeProvisionDelete_Idempotent: non-destroying delete just removes
// the row. Cloud-side teardown is opt-in via ?destroy=true.
func TestNodeProvisionDelete_Idempotent(t *testing.T) {
	st := newSeededTestStore(t)
	tcID, _ := st.CreateTransportConfig(db.TransportConfig{Name: "t", Kind: "s3", Bucket: "b", Endpoint: "h"})
	row, err := st.InsertNodeProvision(db.NodeProvisionInsert{
		DisplayName:       "n",
		Region:            "r",
		Cloud:             "oci",
		TransportConfigID: tcID,
		CloudParamsJSON:   "{}",
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/node-provisions/"+strconv.FormatInt(row.ID, 10), nil)
	req = withChiParams(req, "id", strconv.FormatInt(row.ID, 10))
	NodeProvisionDeleteHandler(st, fakeNodeProvisionerRegistry(t))(rec, req)
	if rec.Code != 204 {
		t.Errorf("status=%d body=%s want 204", rec.Code, rec.Body.String())
	}
	if _, err := st.NodeProvision(row.ID); err == nil {
		t.Error("row still exists after delete")
	}
}

// TestNodeProvisionDelete_RemovesLinkedNode covers the cleanup-gap from
// PR #129: deleting a provision row also removes the nodes row it was
// linked to (set when the new node first registered via the bucket).
func TestNodeProvisionDelete_RemovesLinkedNode(t *testing.T) {
	st := newSeededTestStore(t)
	tcID, _ := st.CreateTransportConfig(db.TransportConfig{Name: "t", Kind: "s3", Bucket: "b", Endpoint: "h"})
	// Seed a nodes row + a provision linked to it.
	nodeID, err := st.CreateNode("n1", "n1.example", "root", 22, "")
	if err != nil {
		t.Fatal(err)
	}
	row, err := st.InsertNodeProvision(db.NodeProvisionInsert{
		DisplayName:       "n",
		Region:            "r",
		Cloud:             "oci",
		TransportConfigID: tcID,
		CloudParamsJSON:   "{}",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetNodeProvisionNode(row.ID, nodeID); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/node-provisions/"+strconv.FormatInt(row.ID, 10), nil)
	req = withChiParams(req, "id", strconv.FormatInt(row.ID, 10))
	NodeProvisionDeleteHandler(st, fakeNodeProvisionerRegistry(t))(rec, req)
	if rec.Code != 204 {
		t.Errorf("status=%d body=%s want 204", rec.Code, rec.Body.String())
	}
	// Provision row gone:
	if _, err := st.NodeProvision(row.ID); err == nil {
		t.Error("provision row still exists")
	}
	// Linked nodes row also gone:
	if _, err := st.NodeByID(nodeID); err == nil {
		t.Error("linked nodes row still exists; #129 cleanup gap not closed")
	}
}

// Confirms compile-time signature: ?destroy=true with a row that has a
// cloud_resource_id triggers Provisioner.Destroy. The fake provisioner
// returns nil so the row is deleted regardless. Coverage gate: the
// branch that decrypts + calls Destroy must execute without panicking
// on a real (encrypted) credential row.
func TestNodeProvisionDelete_DestroyTrue(t *testing.T) {
	st := newSeededTestStore(t)
	tcID, _ := st.CreateTransportConfig(db.TransportConfig{Name: "t", Kind: "s3", Bucket: "b", Endpoint: "h"})
	credID := seedCloudCredentialOCI(t, st)
	row, err := st.InsertNodeProvision(db.NodeProvisionInsert{
		DisplayName:       "n",
		Region:            "r",
		Cloud:             "oci",
		CredentialID:      sql.NullInt64{Int64: credID, Valid: true},
		TransportConfigID: tcID,
		CloudParamsJSON:   "{}",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetNodeProvisionCloudResource(row.ID, "fake-instance-id", "https://console.example/x"); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/node-provisions/"+strconv.FormatInt(row.ID, 10)+"?destroy=true", nil)
	req = withChiParams(req, "id", strconv.FormatInt(row.ID, 10))
	NodeProvisionDeleteHandler(st, fakeNodeProvisionerRegistry(t))(rec, req)
	if rec.Code != 204 {
		t.Errorf("status=%d body=%s want 204", rec.Code, rec.Body.String())
	}
	if _, err := st.NodeProvision(row.ID); err == nil {
		t.Error("row still exists after destroy=true delete")
	}
}

// TestNodeProvisionArchive verifies the archive offboarding path: both
// the linked nodes row AND the node_provisions row are flipped to
// status='archived' (with archived_at stamped) but neither row is
// deleted, so operators can still inspect them under the "Archived"
// filter for retrospective analysis.
func TestNodeProvisionArchive(t *testing.T) {
	st := newSeededTestStore(t)
	tcID, err := st.CreateTransportConfig(db.TransportConfig{
		Name: "t", Kind: "s3", Bucket: "b", Endpoint: "h",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Seed a managed-deploy provision row + a linked node row.
	row, err := st.InsertNodeProvision(db.NodeProvisionInsert{
		DisplayName:       "n",
		Region:            "r",
		Cloud:             "oci",
		TransportConfigID: tcID,
		CloudParamsJSON:   "{}",
	})
	if err != nil {
		t.Fatal(err)
	}
	nodeID, err := st.CreateNode("h-archive", "10.0.0.1", "root", 22, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetNodeProvisionNode(row.ID, nodeID); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/node-provisions/"+strconv.FormatInt(row.ID, 10)+"/archive", nil)
	req = withChiParams(req, "id", strconv.FormatInt(row.ID, 10))
	NodeProvisionArchiveHandler(st, fakeNodeProvisionerRegistry(t))(rec, req)

	if rec.Code != 204 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	// Both rows still exist.
	got, err := st.NodeProvision(row.ID)
	if err != nil {
		t.Fatalf("provision row gone after archive: %v", err)
	}
	if got.Status != db.NodeProvisionArchived {
		t.Errorf("provision status=%q want archived", got.Status)
	}
	if !got.ArchivedAt.Valid {
		t.Error("provision archived_at not set")
	}
	node, err := st.NodeByID(nodeID)
	if err != nil {
		t.Fatalf("node row gone after archive: %v", err)
	}
	if node.Status != db.NodeStatusArchived {
		t.Errorf("node status=%q want archived", node.Status)
	}
	if !node.ArchivedAt.Valid {
		t.Error("node archived_at not set")
	}
}

// TestNodeProvisionEstimate_OCI: the read-only preview returns hourly_usd
// for a known catalog entry. Mirrors cp_provision_estimate's wire shape.
func TestNodeProvisionEstimate_OCI(t *testing.T) {
	st := newSeededTestStore(t)
	body := map[string]any{
		"cloud": "oci",
		"cloud_params": map[string]any{
			"shape":         "VM.Standard.E4.Flex",
			"ocpus":         2,
			"memory_in_gbs": 16,
		},
	}
	buf, _ := json.Marshal(body)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/node-provision/estimate", bytes.NewReader(buf))
	NodeProvisionEstimateHandler(st)(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// VM.Standard.E4.Flex: 0.0250*2 + 0.00150*16 = 0.074
	v, ok := got["hourly_usd"].(float64)
	if !ok {
		t.Fatalf("hourly_usd missing or wrong type: %v", got)
	}
	if v <= 0 {
		t.Errorf("hourly_usd=%v want > 0", v)
	}
}
