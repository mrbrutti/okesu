package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/section9labs/okesu/controlplane/cpprovision"
	"github.com/section9labs/okesu/controlplane/db"
)

// newSeededTestStore returns a test store with the session_hmac_key meta
// seeded so that MasterKeyFromMeta and InsertCloudCredential succeed.
func newSeededTestStore(t *testing.T) *db.Store {
	t.Helper()
	st := newTestStore(t)
	// 32 zero bytes base64-encoded — matches what MasterKeyFromMeta expects.
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	if err := st.MetaSet("session_hmac_key", key); err != nil {
		t.Fatalf("seed session_hmac_key: %v", err)
	}
	return st
}

// fakeBucketProvisioner is a minimal stub for tests so handlers
// can exercise the wire path without real SDK calls.
type fakeBucketProvisioner struct {
	cloud   string
	buckets []cpprovision.BucketInfo
}

func (f *fakeBucketProvisioner) Cloud() string { return f.cloud }
func (f *fakeBucketProvisioner) ListBuckets(_ context.Context, _ []byte, _, _ string) ([]cpprovision.BucketInfo, error) {
	return f.buckets, nil
}
func (f *fakeBucketProvisioner) EnsureBucket(_ context.Context, _ []byte, name, region, _ string) (*cpprovision.BucketInfo, error) {
	bi := cpprovision.BucketInfo{Name: name, Region: region, Endpoint: "https://fake.example"}
	return &bi, nil
}
func (f *fakeBucketProvisioner) BucketAccessKeys(_ context.Context, _ []byte) (string, string, error) {
	return "AK", "SK", nil
}

func TestBucketCloudProviders_FiltersByRegistry(t *testing.T) {
	st := newSeededTestStore(t)
	mk, err := st.MasterKeyFromMeta()
	if err != nil {
		t.Fatalf("MasterKeyFromMeta: %v", err)
	}
	if _, err := st.InsertCloudCredential(db.CloudCredentialInsert{
		Cloud: "aws", Name: "AWS-prod", Region: "us-east-1", Payload: []byte(`{}`),
	}, mk); err != nil {
		t.Fatalf("InsertCloudCredential aws: %v", err)
	}
	if _, err := st.InsertCloudCredential(db.CloudCredentialInsert{
		Cloud: "gcp", Name: "GCP-test", Region: "us-central1", Payload: []byte(`{}`),
	}, mk); err != nil {
		t.Fatalf("InsertCloudCredential gcp: %v", err)
	}

	reg := cpprovision.NewBucketRegistry()
	reg.Register(&fakeBucketProvisioner{cloud: "aws"})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/buckets/cloud-providers", nil)
	BucketCloudProviders(st, reg)(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var got []bucketProviderJSON
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].Cloud != "aws" {
		t.Errorf("expected one aws row; got %+v", got)
	}
}

func TestBucketProvision_CreateMode(t *testing.T) {
	st := newSeededTestStore(t)
	mk, _ := st.MasterKeyFromMeta()
	cred, err := st.InsertCloudCredential(db.CloudCredentialInsert{
		Cloud: "aws", Name: "aws-prod", Region: "us-east-1",
		Payload: []byte(`{"access_key_id":"AK","secret_access_key":"SK","region":"us-east-1"}`),
	}, mk)
	if err != nil {
		t.Fatalf("InsertCloudCredential: %v", err)
	}
	reg := cpprovision.NewBucketRegistry()
	reg.Register(&fakeBucketProvisioner{cloud: "aws"})

	body := bucketProvisionReq{
		CloudCredentialID: cred.ID,
		Region:            "us-east-1",
		BucketName:        "newbucket",
		Mode:              "create",
		DisplayName:       "Prod bucket",
	}
	buf, _ := json.Marshal(body)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/buckets/provision", bytes.NewReader(buf))
	BucketProvision(st, reg)(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got db.TransportConfig
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Bucket != "newbucket" || got.Endpoint != "https://fake.example" {
		t.Errorf("unexpected transport_config: %+v", got)
	}
}

func TestBucketProvision_DiscoverMode_NotFound(t *testing.T) {
	st := newSeededTestStore(t)
	mk, _ := st.MasterKeyFromMeta()
	cred, _ := st.InsertCloudCredential(db.CloudCredentialInsert{
		Cloud: "aws", Name: "x", Region: "us-east-1",
		Payload: []byte(`{}`),
	}, mk)
	reg := cpprovision.NewBucketRegistry()
	reg.Register(&fakeBucketProvisioner{cloud: "aws", buckets: []cpprovision.BucketInfo{
		{Name: "other", Region: "us-east-1", Endpoint: "https://fake.example"},
	}})

	body := bucketProvisionReq{
		CloudCredentialID: cred.ID,
		Region:            "us-east-1",
		BucketName:        "missing",
		Mode:              "discover",
		DisplayName:       "should not create",
	}
	buf, _ := json.Marshal(body)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/buckets/provision", bytes.NewReader(buf))
	BucketProvision(st, reg)(rec, req)
	if rec.Code != 404 {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestBucketDiscover_BadID(t *testing.T) {
	st := newSeededTestStore(t)
	reg := cpprovision.NewBucketRegistry()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/buckets/discover?cloud_credential_id=abc", nil)
	BucketDiscover(st, reg)(rec, req)
	if rec.Code != 400 {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
