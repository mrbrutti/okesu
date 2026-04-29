CREATE TABLE IF NOT EXISTS node_jobs (
  id             BIGSERIAL PRIMARY KEY,
  run_id         TEXT,
  node_id        BIGINT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  kind           TEXT NOT NULL,
  status         TEXT NOT NULL,
  payload_json   JSONB NOT NULL,
  claimed_at     TIMESTAMPTZ,
  finished_at    TIMESTAMPTZ,
  exit_code      INTEGER,
  error          TEXT,
  tunnel_started BOOLEAN NOT NULL DEFAULT FALSE,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_node_jobs_pickup ON node_jobs(node_id, status, created_at);
CREATE INDEX IF NOT EXISTS idx_node_jobs_run_id ON node_jobs(run_id) WHERE run_id IS NOT NULL;

ALTER TABLE nodes ADD COLUMN IF NOT EXISTS jobs_runtime_seen_at TIMESTAMPTZ;
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS tunnel_running       BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS preferred_dispatch   TEXT NOT NULL DEFAULT '';
