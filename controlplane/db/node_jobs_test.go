package db

import (
	"testing"
)

// TestListPendingJobsForS3Config exercises the writer-side query the
// s3scanner uses to publish queued node_jobs into bucket inboxes.
// Three rows are seeded with different transports / cfg ids so the
// filter conditions are individually pinned.
func TestListPendingJobsForS3Config(t *testing.T) {
	st := openTempStore(t)

	// Two transport configs so we can prove the cfg filter works.
	if _, err := st.Exec(`
		INSERT INTO transport_configs (id, name, kind, bucket, region, endpoint, access_key, secret_key, use_ssl, scanner_interval_ms, cp_id)
		VALUES (1, 'cfg-A', 's3', 'bA', 'us-east', '', '', '', 1, 5000, 'cp-A'),
		       (2, 'cfg-B', 's3', 'bB', 'us-east', '', '', '', 1, 5000, 'cp-B')`); err != nil {
		t.Fatalf("seed transport_configs: %v", err)
	}

	// Three nodes:
	//   id=10 → s3 cfg=1 (target)
	//   id=11 → s3 cfg=2 (different cfg — must NOT surface for cfg=1)
	//   id=12 → https    (must NOT surface — wrong transport)
	if _, err := st.Exec(`
		INSERT INTO nodes (id, name, hostname, transport, transport_config_id, status)
		VALUES (10, 'n10', 'h10', 's3',    1,    'ready'),
		       (11, 'n11', 'h11', 's3',    2,    'ready'),
		       (12, 'n12', 'h12', 'https', NULL, 'ready')`); err != nil {
		t.Fatalf("seed nodes: %v", err)
	}

	// Each node gets a pending agent_run job.
	if _, err := st.Exec(`
		INSERT INTO node_jobs (id, run_id, node_id, kind, status, payload_json)
		VALUES (1001, 'r1', 10, 'agent_run', 'pending',   '{"prompt":"a"}'),
		       (1002, 'r2', 11, 'agent_run', 'pending',   '{"prompt":"b"}'),
		       (1003, 'r3', 12, 'agent_run', 'pending',   '{"prompt":"c"}'),
		       (1004, 'r4', 10, 'agent_run', 'claimed',   '{"prompt":"d"}'),
		       (1005, 'r5', 10, 'agent_run', 'succeeded', '{"prompt":"e"}')`); err != nil {
		t.Fatalf("seed node_jobs: %v", err)
	}

	got, err := st.ListPendingJobsForS3Config(1, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 pending row for cfg=1 (id=1001), got %d: %+v", len(got), got)
	}
	if got[0].ID != 1001 {
		t.Errorf("expected job 1001, got %d", got[0].ID)
	}
	if got[0].NodeID != 10 {
		t.Errorf("expected node_id=10, got %d", got[0].NodeID)
	}
	if got[0].Status != "pending" {
		t.Errorf("expected pending, got %s", got[0].Status)
	}

	// cfg=2 returns row 1002 only — proves cfg filter discriminates.
	got2, _ := st.ListPendingJobsForS3Config(2, 0)
	if len(got2) != 1 || got2[0].ID != 1002 {
		t.Errorf("expected [1002] for cfg=2, got %+v", got2)
	}

	// Limit clamps to the requested cap.
	if _, err := st.Exec(`
		INSERT INTO node_jobs (id, run_id, node_id, kind, status, payload_json)
		VALUES (1006, 'r6', 10, 'agent_run', 'pending', '{"prompt":"f"}'),
		       (1007, 'r7', 10, 'agent_run', 'pending', '{"prompt":"g"}')`); err != nil {
		t.Fatalf("seed extra: %v", err)
	}
	gotLim, _ := st.ListPendingJobsForS3Config(1, 2)
	if len(gotLim) != 2 {
		t.Errorf("expected 2 rows under limit, got %d", len(gotLim))
	}
}
