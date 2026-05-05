// Worker-level tests for managed-node provisioning. Real OCI / S3
// calls are stubbed via the seams on NodeProvisionWorkerConfig
// (PackageBuilder + BlobUploader + a fake Provisioner). The test
// uses an in-memory DB store + the same fake provisioner pattern as
// node_provision_test.go.
//
// fakeNodeProvisioner is reused from node_provision_test.go — do
// NOT redeclare here.

package api

import (
	"context"
	"database/sql"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/section9labs/okesu/controlplane/cpprovision"
	"github.com/section9labs/okesu/controlplane/db"
	"github.com/section9labs/okesu/controlplane/packaging"
)

// recordingNodeProvisioner records the most recent LaunchRequest so
// the test can assert the cloud-init script + cloud_params shape.
type recordingNodeProvisioner struct {
	cloud      string
	lastReq    cpprovision.LaunchRequest
	calls      atomic.Int32
	resourceID string
	consoleURL string
	publicIP   string
}

func (f *recordingNodeProvisioner) Cloud() string { return f.cloud }
func (f *recordingNodeProvisioner) Launch(_ context.Context, req cpprovision.LaunchRequest, _ cpprovision.Logger) (*cpprovision.LaunchResult, error) {
	f.lastReq = req
	f.calls.Add(1)
	return &cpprovision.LaunchResult{
		ResourceID: f.resourceID,
		ConsoleURL: f.consoleURL,
		PublicIP:   f.publicIP,
	}, nil
}
func (f *recordingNodeProvisioner) Destroy(_ context.Context, _ string, _ string, _ []byte) error {
	return nil
}

// seedTransportConfigWithFleetKeys creates a transport_config row
// with a real fleet keypair so the cert-mint path actually exercises
// packaging.SignPackageCert.
func seedTransportConfigWithFleetKeys(t *testing.T, st *db.Store) (id int64) {
	t.Helper()
	pubPEM, privPEM, err := generateFleetKeypair()
	if err != nil {
		t.Fatalf("generateFleetKeypair: %v", err)
	}
	id, err = st.CreateTransportConfig(db.TransportConfig{
		Name:            "tc-test",
		Kind:            "s3",
		Bucket:          "okesu-test",
		Endpoint:        "minio.example:9000",
		Region:          sql.NullString{String: "us-phoenix-1", Valid: true},
		UseSSL:          false,
		AccessKey:       sql.NullString{String: "AK", Valid: true},
		SecretKey:       sql.NullString{String: "SK", Valid: true},
		FleetPubkeyPEM:  sql.NullString{String: pubPEM, Valid: true},
		FleetPrivkeyPEM: sql.NullString{String: privPEM, Valid: true},
		CPID:            sql.NullString{String: "cp-test", Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateTransportConfig: %v", err)
	}
	return id
}

// TestNodeProvisionWorker_HappyPath drives a row from queued to
// bootstrap_pending using the recording fake Provisioner + stubbed
// PackageBuilder + BlobUploader. Verifies:
//   - row ends in bootstrap_pending
//   - enrollment_packages row was minted with non-empty cert + key
//   - Provisioner.Launch was called exactly once
//   - cloud_resource_id + cloud_resource_url were stamped
//   - the rendered cloud-init contains the presigned URL stub
func TestNodeProvisionWorker_HappyPath(t *testing.T) {
	st := newSeededTestStore(t)
	tcID := seedTransportConfigWithFleetKeys(t, st)
	credID := seedCloudCredentialOCI(t, st)

	row, err := st.InsertNodeProvision(db.NodeProvisionInsert{
		DisplayName:       "edge-1",
		Region:            "us-phoenix-1",
		Cloud:             "oci",
		CredentialID:      sql.NullInt64{Int64: credID, Valid: true},
		CredentialName:    "oci-test",
		CloudParamsJSON:   `{"shape":"VM.Standard.E4.Flex","subnet_id":"x"}`,
		TransportConfigID: tcID,
	})
	if err != nil {
		t.Fatalf("InsertNodeProvision: %v", err)
	}

	rec := &recordingNodeProvisioner{
		cloud:      "oci",
		resourceID: "ocid1.instance.fake",
		consoleURL: "https://console.example/fake",
		publicIP:   "10.0.0.42",
	}
	reg := cpprovision.NewRegistry()
	reg.Register(rec)

	// Stub PackageBuilder: skip the real packaging path so the test
	// doesn't need a registered formatter at this scope. We assert
	// the BuildRequest carries the freshly-minted cert + key + the
	// transport_config's bucket details.
	var observedReq packaging.BuildRequest
	stubBuilder := func(req packaging.BuildRequest) (packaging.BuildResult, error) {
		observedReq = req
		return packaging.BuildResult{
			Bytes:       []byte("fake-tarball-bytes"),
			Filename:    "edge-1.tar.gz",
			ContentType: "application/gzip",
		}, nil
	}

	var uploadedKey string
	var uploadedBytes []byte
	stubUploader := func(_ context.Context, tc db.TransportConfig, key string, body []byte) (string, error) {
		uploadedKey = key
		uploadedBytes = append([]byte(nil), body...)
		return "https://minio.example/okesu-test/" + key + "?X-Amz-Signature=stub", nil
	}

	cfg := NodeProvisionWorkerConfig{
		Store:    st,
		Registry: reg,
		BinaryResolver: func(target string) ([]byte, error) {
			if target == "linux-amd64" {
				return []byte("fake-okesu-binary"), nil
			}
			return nil, nil
		},
		PackageBuilder: stubBuilder,
		BlobUploader:   stubUploader,
	}

	RunNodeProvisionWorker(context.Background(), cfg, row.ID)

	got, err := st.NodeProvision(row.ID)
	if err != nil {
		t.Fatalf("re-read row: %v", err)
	}
	if got.Status != db.NodeProvisionBootstrapPending {
		t.Errorf("status=%q want bootstrap_pending; log:\n%s", got.Status, got.Log)
	}
	if !got.CloudResourceID.Valid || got.CloudResourceID.String != "ocid1.instance.fake" {
		t.Errorf("cloud_resource_id=%v", got.CloudResourceID)
	}
	if !got.CloudResourceURL.Valid || got.CloudResourceURL.String != "https://console.example/fake" {
		t.Errorf("cloud_resource_url=%v", got.CloudResourceURL)
	}
	if rec.calls.Load() != 1 {
		t.Errorf("Launch calls=%d want 1", rec.calls.Load())
	}
	if rec.lastReq.DisplayName != "edge-1" {
		t.Errorf("Launch req display_name=%q", rec.lastReq.DisplayName)
	}
	if !strings.Contains(rec.lastReq.CloudInitScript, "X-Amz-Signature=stub") {
		t.Errorf("cloud-init missing presigned URL marker; got:\n%s", rec.lastReq.CloudInitScript)
	}
	if !strings.Contains(rec.lastReq.CloudInitScript, "edge-1.tar.gz") {
		t.Errorf("cloud-init missing filename marker; got:\n%s", rec.lastReq.CloudInitScript)
	}
	// CloudParams should round-trip through the worker.
	if shape, _ := rec.lastReq.CloudParams["shape"].(string); shape != "VM.Standard.E4.Flex" {
		t.Errorf("CloudParams.shape=%v", rec.lastReq.CloudParams["shape"])
	}

	// BuildRequest carried the right transport_config fields + the
	// minted cert/key.
	if observedReq.Bucket != "okesu-test" || observedReq.Endpoint != "minio.example:9000" {
		t.Errorf("BuildRequest tc fields: %+v", observedReq)
	}
	if observedReq.PackageCertPEM == "" || observedReq.PackageKeyPEM == "" {
		t.Errorf("BuildRequest cert/key empty — mintProvisionEnrollmentPackage didn't run")
	}
	if !strings.Contains(observedReq.PackageCertPEM, "BEGIN CERTIFICATE") {
		t.Errorf("PackageCertPEM not a PEM cert: %s", observedReq.PackageCertPEM)
	}
	if !strings.Contains(observedReq.PackageKeyPEM, "EC PRIVATE KEY") {
		t.Errorf("PackageKeyPEM not an EC PEM key: %s", observedReq.PackageKeyPEM)
	}

	// The enrollment_package row was persisted.
	pkgs, err := st.ListEnrollmentPackages()
	if err != nil {
		t.Fatalf("ListEnrollmentPackages: %v", err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("expected 1 enrollment_package, got %d", len(pkgs))
	}
	if pkgs[0].PackageCertPEM == "" || pkgs[0].PackageKeyPEM == "" {
		t.Errorf("persisted package has empty cert/key")
	}
	if !strings.Contains(pkgs[0].DisplayName, "edge-1") {
		t.Errorf("package display_name=%q want it to contain edge-1", pkgs[0].DisplayName)
	}

	// Bundle ended up at provisions/nodes/<id>/package.tar.gz.
	wantKey := "provisions/nodes/" + itoa(row.ID) + "/package.tar.gz"
	if uploadedKey != wantKey {
		t.Errorf("uploaded key=%q want %q", uploadedKey, wantKey)
	}
	if string(uploadedBytes) != "fake-tarball-bytes" {
		t.Errorf("uploaded bytes mismatch: %q", string(uploadedBytes))
	}
}

// TestNodeProvisionWorker_NoBinaries fails fast when the resolver
// has no binaries — operator gets a clean "upload binaries first"
// error rather than a confused packaging error.
func TestNodeProvisionWorker_NoBinaries(t *testing.T) {
	st := newSeededTestStore(t)
	tcID := seedTransportConfigWithFleetKeys(t, st)
	credID := seedCloudCredentialOCI(t, st)

	row, _ := st.InsertNodeProvision(db.NodeProvisionInsert{
		DisplayName:       "edge-1",
		Region:            "us-phoenix-1",
		Cloud:             "oci",
		CredentialID:      sql.NullInt64{Int64: credID, Valid: true},
		TransportConfigID: tcID,
	})

	reg := cpprovision.NewRegistry()
	reg.Register(&fakeNodeProvisioner{cloud: "oci"})

	cfg := NodeProvisionWorkerConfig{
		Store:          st,
		Registry:       reg,
		BinaryResolver: func(string) ([]byte, error) { return nil, nil },
	}
	RunNodeProvisionWorker(context.Background(), cfg, row.ID)

	got, _ := st.NodeProvision(row.ID)
	if got.Status != db.NodeProvisionFailed {
		t.Errorf("status=%q want failed; log:\n%s", got.Status, got.Log)
	}
	if !got.Error.Valid || !strings.Contains(got.Error.String, "no daemon binaries") {
		t.Errorf("error=%v want mention of missing daemon binaries", got.Error)
	}
}

// TestNodeProvisionWorker_NoFleetKeys fails fast when the
// transport_config row has no fleet keypair — same actionable error
// EnrollmentPackageCreate gives the manual S3 tab.
func TestNodeProvisionWorker_NoFleetKeys(t *testing.T) {
	st := newSeededTestStore(t)
	credID := seedCloudCredentialOCI(t, st)
	// Transport config without fleet keys.
	tcID, err := st.CreateTransportConfig(db.TransportConfig{
		Name: "tc", Kind: "s3", Bucket: "b", Endpoint: "h",
		AccessKey: sql.NullString{String: "AK", Valid: true},
		SecretKey: sql.NullString{String: "SK", Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	row, _ := st.InsertNodeProvision(db.NodeProvisionInsert{
		DisplayName:       "edge-1",
		Region:            "us-phoenix-1",
		Cloud:             "oci",
		CredentialID:      sql.NullInt64{Int64: credID, Valid: true},
		TransportConfigID: tcID,
	})

	reg := cpprovision.NewRegistry()
	reg.Register(&fakeNodeProvisioner{cloud: "oci"})

	cfg := NodeProvisionWorkerConfig{
		Store:    st,
		Registry: reg,
		BinaryResolver: func(string) ([]byte, error) {
			return []byte("x"), nil
		},
	}
	RunNodeProvisionWorker(context.Background(), cfg, row.ID)

	got, _ := st.NodeProvision(row.ID)
	if got.Status != db.NodeProvisionFailed {
		t.Errorf("status=%q want failed; log:\n%s", got.Status, got.Log)
	}
	if !got.Error.Valid || !strings.Contains(got.Error.String, "fleet keypair") {
		t.Errorf("error=%v want mention of fleet keypair", got.Error)
	}
}
