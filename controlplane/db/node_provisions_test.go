package db

import (
	"database/sql"
	"errors"
	"testing"
)

func TestNodeProvisionCRUD(t *testing.T) {
	st := openTempStore(t)
	tcID, err := st.CreateTransportConfig(TransportConfig{
		Name: "t", Kind: "s3", Bucket: "b", Endpoint: "h",
	})
	if err != nil {
		t.Fatalf("CreateTransportConfig: %v", err)
	}
	r, err := st.InsertNodeProvision(NodeProvisionInsert{
		DisplayName:       "n1",
		Region:            "us-phoenix-1",
		Cloud:             "oci",
		CloudParamsJSON:   `{"shape":"VM.Standard.E4.Flex"}`,
		TransportConfigID: tcID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != NodeProvisionQueued {
		t.Errorf("status=%q want queued", r.Status)
	}

	// status flip via node id — create a real node row to satisfy the FK
	nodeID, err := st.CreateNode("n1", "10.0.0.1", "root", 22, "")
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	if err := st.SetNodeProvisionNode(r.ID, nodeID); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateNodeProvisionStatus(r.ID, NodeProvisionBootstrapPending); err != nil {
		t.Fatal(err)
	}
	advanced, err := st.AdvanceNodeProvisionByNode(nodeID)
	if err != nil || !advanced {
		t.Errorf("advanced=%v err=%v", advanced, err)
	}
	row, _ := st.NodeProvision(r.ID)
	if row.Status != NodeProvisionReady {
		t.Errorf("status=%q want ready", row.Status)
	}
	// idempotent
	advanced, _ = st.AdvanceNodeProvisionByNode(nodeID)
	if advanced {
		t.Error("second call advanced")
	}
}

var _ = sql.NullString{}

func TestFindPendingNodeProvisionByTransportConfig(t *testing.T) {
	st := openTempStore(t)
	tcID, err := st.CreateTransportConfig(TransportConfig{
		Name: "t", Kind: "s3", Bucket: "b", Endpoint: "h",
	})
	if err != nil {
		t.Fatalf("CreateTransportConfig: %v", err)
	}
	otherTCID, err := st.CreateTransportConfig(TransportConfig{
		Name: "other", Kind: "s3", Bucket: "ob", Endpoint: "oh",
	})
	if err != nil {
		t.Fatalf("CreateTransportConfig other: %v", err)
	}

	// No matching row → ErrNoRows.
	if _, err := st.FindPendingNodeProvisionByTransportConfig(tcID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expected ErrNoRows for empty table, got %v", err)
	}

	// Insert two rows for tcID + one for the other.
	r1, err := st.InsertNodeProvision(NodeProvisionInsert{
		DisplayName: "a", Region: "r", Cloud: "oci",
		TransportConfigID: tcID, CloudParamsJSON: "{}",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateNodeProvisionStatus(r1.ID, NodeProvisionBootstrapPending); err != nil {
		t.Fatal(err)
	}
	r2, err := st.InsertNodeProvision(NodeProvisionInsert{
		DisplayName: "b", Region: "r", Cloud: "oci",
		TransportConfigID: tcID, CloudParamsJSON: "{}",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateNodeProvisionStatus(r2.ID, NodeProvisionBootstrapPending); err != nil {
		t.Fatal(err)
	}
	r3, err := st.InsertNodeProvision(NodeProvisionInsert{
		DisplayName: "c", Region: "r", Cloud: "oci",
		TransportConfigID: otherTCID, CloudParamsJSON: "{}",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateNodeProvisionStatus(r3.ID, NodeProvisionBootstrapPending); err != nil {
		t.Fatal(err)
	}

	// Oldest matching one wins.
	got, err := st.FindPendingNodeProvisionByTransportConfig(tcID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != r1.ID {
		t.Errorf("got id=%d want oldest=%d", got.ID, r1.ID)
	}

	// Status guard: an already-ready provision is NOT returned.
	// FK enforcement requires real node rows for the link.
	nodeA, err := st.CreateNode("nA", "10.0.0.1", "root", 22, "")
	if err != nil {
		t.Fatalf("CreateNode A: %v", err)
	}
	if err := st.SetNodeProvisionNode(r1.ID, nodeA); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AdvanceNodeProvisionByNode(nodeA); err != nil {
		t.Fatal(err)
	}
	got, err = st.FindPendingNodeProvisionByTransportConfig(tcID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != r2.ID {
		t.Errorf("after advance r1, expected r2=%d, got %d", r2.ID, got.ID)
	}

	// node_id-set guard: an already-linked provision is NOT returned.
	nodeB, err := st.CreateNode("nB", "10.0.0.2", "root", 22, "")
	if err != nil {
		t.Fatalf("CreateNode B: %v", err)
	}
	if err := st.SetNodeProvisionNode(r2.ID, nodeB); err != nil {
		t.Fatal(err)
	}
	if _, err := st.FindPendingNodeProvisionByTransportConfig(tcID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expected ErrNoRows once all matching rows are linked, got %v", err)
	}
}
