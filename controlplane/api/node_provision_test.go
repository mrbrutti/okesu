// Tests for the POST /api/node-provision handler. Mirrors the patterns
// in buckets_test.go (newSeededTestStore + a small in-test fake
// provisioner registered against cpprovision.NewRegistry).
//
// The fake provisioner here is a no-op — Task 4 only exercises the
// HTTP-shape + persistence path. Task 5 will add worker-level tests
// once the real RunNodeProvisionWorker lands and replaces the stub.

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/section9labs/okesu/controlplane/cpprovision"
	"github.com/section9labs/okesu/controlplane/db"
)

// fakeNodeProvisioner is a minimal no-op Provisioner used by the
// node-provision handler tests so the registry lookup succeeds for
// "oci" without dragging the real OCI SDK into a unit test.
type fakeNodeProvisioner struct{ cloud string }

func (f *fakeNodeProvisioner) Cloud() string { return f.cloud }
func (f *fakeNodeProvisioner) Launch(_ context.Context, _ cpprovision.LaunchRequest, _ cpprovision.Logger) (*cpprovision.LaunchResult, error) {
	return &cpprovision.LaunchResult{ResourceID: "fake-resource", ConsoleURL: "https://console.example/fake"}, nil
}
func (f *fakeNodeProvisioner) Destroy(_ context.Context, _ string, _ string, _ []byte) error {
	return nil
}

// fakeNodeProvisionerRegistry returns a Registry pre-seeded with an
// "oci" provisioner so the handler's reg.Get("oci") call succeeds.
func fakeNodeProvisionerRegistry(t *testing.T) *cpprovision.Registry {
	t.Helper()
	reg := cpprovision.NewRegistry()
	reg.Register(&fakeNodeProvisioner{cloud: "oci"})
	return reg
}

// seedCloudCredentialOCI inserts an oci cloud_credentials row and
// returns its id. Mirrors the helper pattern used in
// buckets_test.go (InsertCloudCredential + MasterKeyFromMeta).
func seedCloudCredentialOCI(t *testing.T, st *db.Store) int64 {
	t.Helper()
	mk, err := st.MasterKeyFromMeta()
	if err != nil {
		t.Fatalf("MasterKeyFromMeta: %v", err)
	}
	cred, err := st.InsertCloudCredential(db.CloudCredentialInsert{
		Cloud:   "oci",
		Name:    "oci-test",
		Region:  "us-phoenix-1",
		Payload: []byte(`{}`),
	}, mk)
	if err != nil {
		t.Fatalf("InsertCloudCredential: %v", err)
	}
	return cred.ID
}

// TestNodeProvisionCreate_HappyPath verifies the round-trip:
// 202 + JSON row + status=queued.
func TestNodeProvisionCreate_HappyPath(t *testing.T) {
	st := newSeededTestStore(t)

	tcID, err := st.CreateTransportConfig(db.TransportConfig{Name: "t", Kind: "s3", Bucket: "b", Endpoint: "h"})
	if err != nil {
		t.Fatal(err)
	}
	credID := seedCloudCredentialOCI(t, st)

	body := map[string]any{
		"display_name":        "edge-1",
		"region":              "us-phoenix-1",
		"cloud":               "oci",
		"credential_id":       credID,
		"transport_config_id": tcID,
		"cloud_params": map[string]any{
			"shape":               "VM.Standard.E4.Flex",
			"subnet_id":           "ocid1.subnet.x",
			"image_id":            "ocid1.image.x",
			"availability_domain": "BzLN:PHX-AD-1",
			"ocpus":               1,
			"memory_in_gbs":       8,
		},
	}
	buf, _ := json.Marshal(body)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/node-provision", bytes.NewReader(buf))

	handler := NodeProvisionCreateHandler(st, fakeNodeProvisionerRegistry(t), NodeProvisionWorkerConfig{})
	handler(rec, req)

	if rec.Code != 202 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["status"] != "queued" {
		t.Errorf("status=%v want queued", got["status"])
	}
	if got["display_name"] != "edge-1" {
		t.Errorf("display_name=%v", got["display_name"])
	}
}

// TestNodeProvisionCreate_MissingFields rejects with 400.
func TestNodeProvisionCreate_MissingFields(t *testing.T) {
	st := newSeededTestStore(t)
	body := map[string]any{"display_name": "x"} // missing region, cloud, etc.
	buf, _ := json.Marshal(body)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/node-provision", bytes.NewReader(buf))
	NodeProvisionCreateHandler(st, fakeNodeProvisionerRegistry(t), NodeProvisionWorkerConfig{})(rec, req)
	if rec.Code != 400 {
		t.Errorf("status=%d, want 400", rec.Code)
	}
}

// TestNodeProvisionCreate_MissingCloudParams rejects with 400.
func TestNodeProvisionCreate_MissingCloudParams(t *testing.T) {
	st := newSeededTestStore(t)
	tcID, err := st.CreateTransportConfig(db.TransportConfig{Name: "t", Kind: "s3", Bucket: "b", Endpoint: "h"})
	if err != nil {
		t.Fatal(err)
	}
	credID := seedCloudCredentialOCI(t, st)
	body := map[string]any{
		"display_name":        "edge-1",
		"region":              "us-phoenix-1",
		"cloud":               "oci",
		"credential_id":       credID,
		"transport_config_id": tcID,
		"cloud_params":        map[string]any{}, // empty — missing oci required keys
	}
	buf, _ := json.Marshal(body)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/node-provision", bytes.NewReader(buf))
	NodeProvisionCreateHandler(st, fakeNodeProvisionerRegistry(t), NodeProvisionWorkerConfig{})(rec, req)
	if rec.Code != 400 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "shape") {
		t.Errorf("error should mention missing key 'shape', got: %s", rec.Body.String())
	}
}

// TestNodeProvisionCreate_BadTransportConfig rejects with 400 (FK validation).
func TestNodeProvisionCreate_BadTransportConfig(t *testing.T) {
	st := newSeededTestStore(t)
	credID := seedCloudCredentialOCI(t, st)
	body := map[string]any{
		"display_name":        "edge-1",
		"region":              "us-phoenix-1",
		"cloud":               "oci",
		"credential_id":       credID,
		"transport_config_id": 99999,
		"cloud_params": map[string]any{
			"shape":               "x",
			"subnet_id":           "x",
			"image_id":            "x",
			"availability_domain": "x",
			"ocpus":               1,
			"memory_in_gbs":       8,
		},
	}
	buf, _ := json.Marshal(body)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/node-provision", bytes.NewReader(buf))
	NodeProvisionCreateHandler(st, fakeNodeProvisionerRegistry(t), NodeProvisionWorkerConfig{})(rec, req)
	if rec.Code != 400 {
		t.Errorf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
