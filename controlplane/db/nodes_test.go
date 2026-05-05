package db

import (
	"testing"
	"time"
)

// TestArchiveNode verifies ArchiveNode flips status to 'archived',
// stamps archived_at + archived_by_email, and is idempotent on
// re-archive.
func TestArchiveNode(t *testing.T) {
	st := openTempStore(t)
	id, err := st.CreateNode("h1", "10.0.0.1", "root", 22, "")
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}

	if err := st.ArchiveNode(id, "admin@example"); err != nil {
		t.Fatalf("ArchiveNode: %v", err)
	}

	row, err := st.NodeByID(id)
	if err != nil {
		t.Fatalf("NodeByID: %v", err)
	}
	if row.Status != NodeStatusArchived {
		t.Errorf("status=%q want %q", row.Status, NodeStatusArchived)
	}
	if !row.ArchivedAt.Valid {
		t.Error("archived_at should be non-NULL after archive")
	}
	if !row.ArchivedByEmail.Valid || row.ArchivedByEmail.String != "admin@example" {
		t.Errorf("archived_by_email=%+v want admin@example", row.ArchivedByEmail)
	}

	firstStamp := row.ArchivedAt.Time
	// Sleep enough that the second CURRENT_TIMESTAMP differs from the
	// first — SQLite's CURRENT_TIMESTAMP has second resolution. We
	// don't actually require the timestamp to advance for idempotency,
	// just that the call doesn't error.
	time.Sleep(1100 * time.Millisecond)

	// Idempotent re-archive: must not error, must keep status archived.
	if err := st.ArchiveNode(id, "admin@example"); err != nil {
		t.Fatalf("re-archive: %v", err)
	}
	row2, err := st.NodeByID(id)
	if err != nil {
		t.Fatalf("NodeByID after re-archive: %v", err)
	}
	if row2.Status != NodeStatusArchived {
		t.Errorf("re-archive status=%q want %q", row2.Status, NodeStatusArchived)
	}
	if !row2.ArchivedAt.Valid {
		t.Error("archived_at must remain non-NULL after re-archive")
	}
	if row2.ArchivedAt.Time.Before(firstStamp) {
		t.Errorf("archived_at went backwards: %v -> %v", firstStamp, row2.ArchivedAt.Time)
	}
}

// TestPurgeHostHistory seeds events / findings / runs / agents for
// host="h1" plus a sentinel "h2" row in each table, runs the purge,
// and asserts (a) all h1 rows are gone, (b) h2 rows are untouched,
// (c) the returned total reflects the deletes across all four
// tables.
func TestPurgeHostHistory(t *testing.T) {
	st := openTempStore(t)

	// --- Seed events: one for h1, one for h2 ---
	res, err := st.Exec(
		`INSERT INTO events (ts, type, agent, host, raw_json) VALUES (?, 'finding', 'a1', 'h1', '{}')`,
		time.Now().UnixMilli(),
	)
	if err != nil {
		t.Fatalf("seed event h1: %v", err)
	}
	h1EventID, _ := res.LastInsertId()
	if _, err := st.Exec(
		`INSERT INTO events (ts, type, agent, host, raw_json) VALUES (?, 'finding', 'a2', 'h2', '{}')`,
		time.Now().UnixMilli(),
	); err != nil {
		t.Fatalf("seed event h2: %v", err)
	}

	// --- Seed findings via the real InsertFinding path (uses the FK on
	// events). One for each host. ---
	if _, err := st.InsertFinding(&FindingInsert{
		EventID: h1EventID, Ts: time.Now().UnixMilli(),
		Agent: "a1", Host: "h1", Severity: "MEDIUM", Title: "h1-finding",
	}); err != nil {
		t.Fatalf("InsertFinding h1: %v", err)
	}
	// h2 finding needs its own event row.
	res2, err := st.Exec(
		`INSERT INTO events (ts, type, agent, host, raw_json) VALUES (?, 'finding', 'a2', 'h2', '{}')`,
		time.Now().UnixMilli(),
	)
	if err != nil {
		t.Fatalf("seed second event h2: %v", err)
	}
	h2EventID, _ := res2.LastInsertId()
	if _, err := st.InsertFinding(&FindingInsert{
		EventID: h2EventID, Ts: time.Now().UnixMilli(),
		Agent: "a2", Host: "h2", Severity: "LOW", Title: "h2-finding",
	}); err != nil {
		t.Fatalf("InsertFinding h2: %v", err)
	}

	// --- Seed runs: one per host (runs.node_name is the join column). ---
	if err := st.CreateRun(RunInsert{
		ID: "run-h1", NodeName: "h1", Prompt: "p", AgentName: "a1",
	}); err != nil {
		t.Fatalf("CreateRun h1: %v", err)
	}
	if err := st.CreateRun(RunInsert{
		ID: "run-h2", NodeName: "h2", Prompt: "p", AgentName: "a2",
	}); err != nil {
		t.Fatalf("CreateRun h2: %v", err)
	}

	// --- Seed agents: one per host. ---
	if err := st.UpsertAgentRegistration("a1", "h1", "claude", "model", "v1", ""); err != nil {
		t.Fatalf("UpsertAgentRegistration h1: %v", err)
	}
	if err := st.UpsertAgentRegistration("a2", "h2", "claude", "model", "v1", ""); err != nil {
		t.Fatalf("UpsertAgentRegistration h2: %v", err)
	}

	// --- Purge h1. ---
	n, err := st.PurgeHostHistory("h1")
	if err != nil {
		t.Fatalf("PurgeHostHistory: %v", err)
	}
	if n == 0 {
		t.Error("expected at least one row deleted")
	}

	// --- Assert all h1 rows gone. ---
	for _, c := range []struct {
		name  string
		query string
	}{
		{"events", `SELECT COUNT(*) FROM events WHERE host = 'h1'`},
		{"findings", `SELECT COUNT(*) FROM findings WHERE host = 'h1'`},
		{"runs", `SELECT COUNT(*) FROM runs WHERE node_name = 'h1'`},
		{"agents", `SELECT COUNT(*) FROM agents WHERE host = 'h1'`},
	} {
		var count int
		if err := st.QueryRow(c.query).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", c.name, err)
		}
		if count != 0 {
			t.Errorf("%s: %d h1 rows remain after purge", c.name, count)
		}
	}

	// --- Assert h2 rows untouched. ---
	for _, c := range []struct {
		name  string
		query string
	}{
		{"events", `SELECT COUNT(*) FROM events WHERE host = 'h2'`},
		{"findings", `SELECT COUNT(*) FROM findings WHERE host = 'h2'`},
		{"runs", `SELECT COUNT(*) FROM runs WHERE node_name = 'h2'`},
		{"agents", `SELECT COUNT(*) FROM agents WHERE host = 'h2'`},
	} {
		var count int
		if err := st.QueryRow(c.query).Scan(&count); err != nil {
			t.Fatalf("count %s h2: %v", c.name, err)
		}
		if count == 0 {
			t.Errorf("%s: h2 rows wrongly purged", c.name)
		}
	}

	// --- Idempotent re-purge: empty host = no-op. ---
	if got, err := st.PurgeHostHistory(""); err != nil || got != 0 {
		t.Errorf("PurgeHostHistory(\"\")=%d,%v want 0,nil", got, err)
	}
	// --- Re-purging same host returns 0 (no rows left). ---
	if got, err := st.PurgeHostHistory("h1"); err != nil || got != 0 {
		t.Errorf("re-purge h1 = %d,%v want 0,nil", got, err)
	}
}
