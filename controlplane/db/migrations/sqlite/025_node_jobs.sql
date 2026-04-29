-- Phase: pull-mode dispatch for orchestration steps + ad-hoc Runs.
--
-- The CP now supports two dispatch paths to a node:
--   - reverse-mTLS tunnel (existing): low-latency, persistent
--   - jobs queue (new):              poll-based, no persistent connection
--
-- The jobs runtime on each node polls /api/v1/agents/jobs and claims
-- pending rows. The orchestrator's jobsDispatcher INSERTs a row, then
-- waits on the row's status to flip to a terminal value.
--
-- One row per dispatched job. Terminal rows can be GC'd by an op cron
-- but we keep them around for audit + UI history; the table is small
-- because most dispatch is short-lived.

CREATE TABLE IF NOT EXISTS node_jobs (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  -- run_id mirrors runs.id when kind=agent_run so the orchestrator
  -- can correlate this dispatch envelope with the underlying Run
  -- record (which holds prompt, started_by, finding link, etc).
  -- NULL for control-plane jobs (start_tunnel, stop_tunnel).
  run_id        TEXT,
  node_id       INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  kind          TEXT NOT NULL,                       -- agent_run | start_tunnel | stop_tunnel
  status        TEXT NOT NULL,                       -- pending | claimed | succeeded | failed | timeout | cancelled
  payload_json  TEXT NOT NULL,                       -- agent.JobPayload as JSON
  claimed_at    TIMESTAMP,
  finished_at   TIMESTAMP,
  exit_code     INTEGER,
  error         TEXT,
  -- TunnelStarted is split out as a column rather than buried in
  -- payload because the orchestrator's tunnel-on-demand path watches
  -- it directly when waiting for a managed-child registration.
  tunnel_started INTEGER NOT NULL DEFAULT 0,
  created_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Pickup index — the daemon's poll uses (node_id, status='pending'
-- ORDER BY created_at) to get the oldest pending job first.
CREATE INDEX IF NOT EXISTS idx_node_jobs_pickup
  ON node_jobs(node_id, status, created_at);

-- Run linkage — orchestrator + UI lookups by run_id.
CREATE INDEX IF NOT EXISTS idx_node_jobs_run_id
  ON node_jobs(run_id) WHERE run_id IS NOT NULL;

-- Capability columns on nodes track the live state of each runtime
-- as reported by the daemon's heartbeat. The orchestrator's dispatch
-- decision tree reads these to pick tunnel vs jobs vs auto-deploy.
ALTER TABLE nodes ADD COLUMN jobs_runtime_seen_at  TIMESTAMP;
ALTER TABLE nodes ADD COLUMN tunnel_running        INTEGER NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN preferred_dispatch    TEXT NOT NULL DEFAULT '';
