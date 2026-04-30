-- Phase 23.x postgres parity. See migrations/sqlite/038_step_node_dispatches.sql.

CREATE TABLE IF NOT EXISTS orchestration_step_node_dispatches (
  run_id          BIGINT      NOT NULL REFERENCES orchestration_runs(id) ON DELETE CASCADE,
  step_id         TEXT        NOT NULL,
  host            TEXT        NOT NULL,
  status          TEXT        NOT NULL,
  agent_run_id    TEXT,
  findings_count  INTEGER     NOT NULL DEFAULT 0,
  output_tail     TEXT,
  error           TEXT,
  started_at      TIMESTAMPTZ,
  ended_at        TIMESTAMPTZ,
  PRIMARY KEY (run_id, step_id, host)
);

CREATE INDEX IF NOT EXISTS idx_osnd_run_step
  ON orchestration_step_node_dispatches(run_id, step_id);
