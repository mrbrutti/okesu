package db

import (
	"testing"
)

// TestMarkInflightOrchestrationsCancelled reproduces the stuck-run
// pattern observed live (200+ orchestration_runs left at status=
// 'running' after a CP restart) and verifies the boot reconciler
// flips them, their step rows, and any per-host dispatches.
func TestMarkInflightOrchestrationsCancelled(t *testing.T) {
	st := openTempStore(t)

	// Seed an orchestration with three runs in three states:
	//   - r1: running (the bug case — should reconcile)
	//   - r2: pending (also wedged forever; reconciles)
	//   - r3: completed (must NOT be touched)
	if _, err := st.Exec(`
		INSERT INTO orchestrations (id, name, spec_yaml)
		VALUES (1, 'orch', 'name: orch')`); err != nil {
		t.Fatalf("seed orch: %v", err)
	}
	if _, err := st.Exec(`
		INSERT INTO orchestration_runs (id, orchestration_id, status, trigger_kind, current_step_id)
		VALUES (1, 1, 'running',   'finding', 'classify'),
		       (2, 1, 'pending',   'finding', ''),
		       (3, 1, 'completed', 'finding', '')`); err != nil {
		t.Fatalf("seed runs: %v", err)
	}
	// Step rows: r1 has classify=running + auto_suppress=pending;
	// r2 hasn't started any steps (no rows); r3 has all completed.
	if _, err := st.Exec(`
		INSERT INTO orchestration_steps (orchestration_run_id, step_id, step_idx, status, started_at)
		VALUES (1, 'classify',     0, 'running', CURRENT_TIMESTAMP),
		       (1, 'auto_suppress', 1, 'pending', NULL),
		       (3, 'classify',     0, 'completed', CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("seed steps: %v", err)
	}

	n, err := st.MarkInflightOrchestrationsCancelled()
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2 runs reconciled (r1+r2), got %d", n)
	}

	// Verify run states.
	want := map[int64]string{1: "cancelled", 2: "cancelled", 3: "completed"}
	for id, w := range want {
		var got string
		if err := st.QueryRow(`SELECT status FROM orchestration_runs WHERE id = ?`, id).Scan(&got); err != nil {
			t.Fatalf("read r%d: %v", id, err)
		}
		if got != w {
			t.Errorf("run %d status = %s, want %s", id, got, w)
		}
	}

	// Verify the running step on r1 was failed; the pending step on
	// r1 was also failed (it would never run); r3's completed step
	// must be untouched.
	rows, err := st.Query(`SELECT orchestration_run_id, step_id, status FROM orchestration_steps ORDER BY orchestration_run_id, step_idx`)
	if err != nil {
		t.Fatalf("read steps: %v", err)
	}
	defer rows.Close()
	type stepRow struct {
		runID  int64
		step   string
		status string
	}
	got := []stepRow{}
	for rows.Next() {
		var r stepRow
		if err := rows.Scan(&r.runID, &r.step, &r.status); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, r)
	}
	wantSteps := []stepRow{
		{1, "classify", "failed"},
		{1, "auto_suppress", "failed"},
		{3, "classify", "completed"},
	}
	if len(got) != len(wantSteps) {
		t.Fatalf("got %d step rows, want %d", len(got), len(wantSteps))
	}
	for i, w := range wantSteps {
		if got[i] != w {
			t.Errorf("step[%d] = %+v, want %+v", i, got[i], w)
		}
	}

	// Idempotency: a second call on a clean DB shouldn't error and
	// should report 0 reconciled.
	n2, err := st.MarkInflightOrchestrationsCancelled()
	if err != nil {
		t.Fatalf("idempotent reconcile: %v", err)
	}
	if n2 != 0 {
		t.Errorf("expected 0 on clean DB, got %d", n2)
	}
}
