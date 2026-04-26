-- Phase 10: durable ad-hoc runs.
--
-- Until now, ad-hoc agent runs (the "Run Agent" UI / POST /api/runs) lived
-- only in CP process memory. Restarting the CP wiped history, including any
-- in-flight runs. This migration moves runs to SQLite so:
--
--   1. Run history survives CP restarts.
--   2. In-flight runs at shutdown are reconciled to "cancelled" on next boot
--      (the child on the node either finished while we were down, or is
--      orphaned — either way the row should not stay "running").
--   3. The Cancel button has somewhere to record the operator action.
--
-- Live tail (SSE log streaming) still goes through an in-memory pub/sub —
-- the DB is the source of truth for status + replay, not for low-latency
-- fan-out. See controlplane/api/runs.go for the hybrid model.

CREATE TABLE IF NOT EXISTS runs (
  id            TEXT PRIMARY KEY,                           -- 16-hex id from randomID()
  node_name     TEXT NOT NULL,
  provider      TEXT,                                       -- claude | codex | auto
  model         TEXT,
  effort        TEXT,
  agent_name    TEXT,                                       -- agent-file basename, optional
  prompt        TEXT NOT NULL,
  status        TEXT NOT NULL DEFAULT 'running',            -- running | succeeded | failed | cancelled
  exit_code     BIGINT NOT NULL DEFAULT 0,
  error         TEXT,
  started_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  finished_at   TIMESTAMP,
  started_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
  started_by_email   TEXT
);

CREATE INDEX IF NOT EXISTS idx_runs_started_at ON runs(started_at DESC);
CREATE INDEX IF NOT EXISTS idx_runs_status     ON runs(status);
CREATE INDEX IF NOT EXISTS idx_runs_node       ON runs(node_name, started_at DESC);

CREATE TABLE IF NOT EXISTS run_lines (
  id        BIGSERIAL PRIMARY KEY,
  run_id    TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
  ts        TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  stream    TEXT NOT NULL DEFAULT 'stdout',                 -- stdout | stderr
  data      TEXT NOT NULL
);

-- One scan per run when streaming/replaying.
CREATE INDEX IF NOT EXISTS idx_run_lines_run_id_id ON run_lines(run_id, id);
