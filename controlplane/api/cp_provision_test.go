// Tests for CPProvisionDeleteHandler — focused on the cleanup-gap
// closures from PR #129 (Task 9):
//
//   - Deleting a cp_provisions row with a federation_peers link
//     also drops the peer (no orphan in the UI).
//   - The bootstrap-blob bucket-delete branch is exercised but its
//     success isn't asserted here — without a real bucket the
//     s3blob.New call returns an error, which the handler logs and
//     swallows. The smoke test in Task 10 covers the green path.

package api

import (
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/section9labs/okesu/controlplane/db"
)

// TestCPProvisionDelete_RemovesOrphanPeer is the regression test for
// the "orphan peer rows after destroy" bug observed in the PR #129
// demo session. Destroy with no ?destroy=true (no cloud-side teardown
// needed because we never set a cloud_resource_id) must still drop
// the federation_peers row the worker pre-registered.
func TestCPProvisionDelete_RemovesOrphanPeer(t *testing.T) {
	st := newSeededTestStore(t)
	tcID, err := st.CreateTransportConfig(db.TransportConfig{Name: "t", Kind: "s3", Bucket: "b", Endpoint: "h"})
	if err != nil {
		t.Fatal(err)
	}
	// Insert a cp_provisions row (transport=s3_dead_drop is what
	// pre-registers a peer in real flows; for the test a plain
	// row + manual link is fine).
	row, err := st.InsertCPProvision(db.CPProvisionInsert{
		DisplayName:       "child-1",
		Region:            "r",
		Cloud:             "oci",
		CredentialID:      0,
		CloudParamsJSON:   "{}",
		Transport:         "s3_dead_drop",
		TransportConfigID: tcID,
	})
	if err != nil {
		t.Fatal(err)
	}
	peer, err := st.AddS3FederationPeer("child-1", "cp/child-uuid/outbound/parent/", tcID, "tok")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetCPProvisionPeer(row.ID, peer.ID); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/federation/cp-provisions/"+strconv.FormatInt(row.ID, 10), nil)
	req = withChiParams(req, "id", strconv.FormatInt(row.ID, 10))
	CPProvisionDeleteHandler(st, fakeNodeProvisionerRegistry(t))(rec, req)
	if rec.Code != 204 {
		t.Fatalf("status=%d body=%s want 204", rec.Code, rec.Body.String())
	}

	// cp_provisions row gone:
	if _, err := st.GetCPProvision(row.ID); err == nil {
		t.Error("provision row still exists after delete")
	}
	// federation_peers row gone — this is the bug closure:
	if _, err := st.FederationPeer(peer.ID); err == nil {
		t.Error("federation_peers row still exists after cp_provision destroy; #129 cleanup gap not closed")
	}
}

// TestCPProvisionDelete_NoPeer is a regression guard for the common
// case where a provision row never linked a peer (e.g. failed during
// launch). Delete should still succeed; we just want to confirm the
// new cleanup branch doesn't NPE on a NULL peer_id.
func TestCPProvisionDelete_NoPeer(t *testing.T) {
	st := newSeededTestStore(t)
	row, err := st.InsertCPProvision(db.CPProvisionInsert{
		DisplayName:     "child-2",
		Region:          "r",
		Cloud:           "oci",
		CloudParamsJSON: "{}",
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/federation/cp-provisions/"+strconv.FormatInt(row.ID, 10), nil)
	req = withChiParams(req, "id", strconv.FormatInt(row.ID, 10))
	CPProvisionDeleteHandler(st, fakeNodeProvisionerRegistry(t))(rec, req)
	if rec.Code != 204 {
		t.Fatalf("status=%d body=%s want 204", rec.Code, rec.Body.String())
	}
	if _, err := st.GetCPProvision(row.ID); err == nil {
		t.Error("provision row still exists after delete")
	}
}

// TestCPProvisionDelete_BootstrapBlobBranchSafe exercises the new
// child_instance_id + transport_config_id cleanup branch. With no
// access keys on the transport_config the bucket-delete short-circuits
// with an error, which the handler must log + carry on (the row
// delete still succeeds, returning 204). This proves a bucket-cleanup
// failure can never block a destroy.
func TestCPProvisionDelete_BootstrapBlobBranchSafe(t *testing.T) {
	st := newSeededTestStore(t)
	// Note: no AccessKey/SecretKey set, so deleteCPProvisionBootstrapBlob's
	// "no access keys" guard fires before any network I/O.
	tcID, err := st.CreateTransportConfig(db.TransportConfig{
		Name: "t", Kind: "s3", Bucket: "b", Endpoint: "h",
	})
	if err != nil {
		t.Fatal(err)
	}
	row, err := st.InsertCPProvision(db.CPProvisionInsert{
		DisplayName:       "child-3",
		Region:            "r",
		Cloud:             "oci",
		CloudParamsJSON:   "{}",
		Transport:         "s3_dead_drop",
		TransportConfigID: tcID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetCPProvisionChildInstanceID(row.ID, "deadbeef-uuid"); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/federation/cp-provisions/"+strconv.FormatInt(row.ID, 10), nil)
	req = withChiParams(req, "id", strconv.FormatInt(row.ID, 10))
	CPProvisionDeleteHandler(st, fakeNodeProvisionerRegistry(t))(rec, req)
	if rec.Code != 204 {
		t.Fatalf("status=%d body=%s want 204 (bucket-cleanup failure must not block delete)", rec.Code, rec.Body.String())
	}
	if _, err := st.GetCPProvision(row.ID); err == nil {
		t.Error("provision row still exists after delete")
	}
}
