package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/section9labs/okesu/controlplane/cpprovision"
	"github.com/section9labs/okesu/controlplane/db"
)

// fakeCompartmentProvisioner implements both BucketProvisioner and
// CompartmentLister. Used to exercise the OCI path in tests without
// real SDK calls.
type fakeCompartmentProvisioner struct {
	fakeBucketProvisioner                     // embed the existing stub
	compartments          []cpprovision.Compartment
}

func (f *fakeCompartmentProvisioner) ListCompartments(_ context.Context, _ []byte, _ string) ([]cpprovision.Compartment, error) {
	return f.compartments, nil
}

// routeCompartments builds a chi router, registers the handler, and
// serves a GET for /api/cloud-credentials/{credID}/compartments.
func routeCompartments(t *testing.T, store *db.Store, reg *cpprovision.BucketRegistry, credID, region string) *httptest.ResponseRecorder {
	t.Helper()
	url := "/api/cloud-credentials/" + credID + "/compartments"
	if region != "" {
		url += "?region=" + region
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	rec := httptest.NewRecorder()

	r := chi.NewRouter()
	r.Get("/api/cloud-credentials/{id}/compartments", ListCompartmentsForCredential(store, reg))
	r.ServeHTTP(rec, req)
	return rec
}

// TestListCompartments_AWSCloudReturnsEmptyArray verifies that a
// non-OCI cloud (AWS) with a provisioner that does not implement
// CompartmentLister gets a 200 with an empty JSON array.
func TestListCompartments_AWSCloudReturnsEmptyArray(t *testing.T) {
	st := newSeededTestStore(t)
	mk, _ := st.MasterKeyFromMeta()
	_, err := st.InsertCloudCredential(db.CloudCredentialInsert{
		Cloud: "aws", Name: "prod", Region: "us-east-1",
		Payload: []byte(`{"access_key_id":"AK","secret_access_key":"SK","region":"us-east-1"}`),
	}, mk)
	if err != nil {
		t.Fatalf("InsertCloudCredential: %v", err)
	}
	// The first credential gets ID 1.
	reg := cpprovision.NewBucketRegistry()
	reg.Register(&fakeBucketProvisioner{cloud: "aws"}) // does NOT implement CompartmentLister

	rec := routeCompartments(t, st, reg, "1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var got []cpprovision.Compartment
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty array; got %+v", got)
	}
}

// TestListCompartments_UnknownIDReturns404 verifies that requesting a
// credential that does not exist returns 404.
func TestListCompartments_UnknownIDReturns404(t *testing.T) {
	st := newSeededTestStore(t)
	reg := cpprovision.NewBucketRegistry()

	rec := routeCompartments(t, st, reg, "9999", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
}

// TestListCompartments_OCIReturnsCompartments verifies that an OCI
// credential whose provisioner implements CompartmentLister returns
// the compartment list with status 200.
func TestListCompartments_OCIReturnsCompartments(t *testing.T) {
	st := newSeededTestStore(t)
	mk, _ := st.MasterKeyFromMeta()
	_, err := st.InsertCloudCredential(db.CloudCredentialInsert{
		Cloud: "oci", Name: "oci-prod", Region: "us-ashburn-1",
		// Minimal valid-looking payload; the fake provisioner ignores it.
		Payload: []byte(`{"tenancy_ocid":"ocid1.tenancy.oc1..xxx","user_ocid":"ocid1.user.oc1..yyy","fingerprint":"aa:bb","private_key":"pk","region":"us-ashburn-1"}`),
	}, mk)
	if err != nil {
		t.Fatalf("InsertCloudCredential: %v", err)
	}

	want := []cpprovision.Compartment{
		{OCID: "ocid1.compartment.oc1..aaa", Name: "Alpha", ParentID: "ocid1.tenancy.oc1..xxx", LifecycleState: "ACTIVE"},
		{OCID: "ocid1.compartment.oc1..bbb", Name: "Beta", ParentID: "ocid1.tenancy.oc1..xxx", LifecycleState: "ACTIVE"},
	}
	reg := cpprovision.NewBucketRegistry()
	reg.Register(&fakeCompartmentProvisioner{
		fakeBucketProvisioner: fakeBucketProvisioner{cloud: "oci"},
		compartments:          want,
	})

	rec := routeCompartments(t, st, reg, "1", "us-ashburn-1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var got []cpprovision.Compartment
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("len(got) = %d, want %d; got = %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].OCID != want[i].OCID || got[i].Name != want[i].Name {
			t.Errorf("got[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}
