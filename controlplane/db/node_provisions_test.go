package db

import (
	"database/sql"
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
