-- Phase 2 schema: agents table tracks registered daemon agents.

CREATE TABLE IF NOT EXISTS agents (
  name              TEXT PRIMARY KEY,
  host              TEXT NOT NULL,
  provider          TEXT,
  model             TEXT,
  version           TEXT,

  registered_at     TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  last_heartbeat_at TIMESTAMP,
  last_tick_count   BIGINT NOT NULL DEFAULT 0,

  -- Desired config — pushed to the daemon on next config poll.
  -- NULL means "no override" (daemon keeps its agent-file value).
  desired_max_turns BIGINT,
  desired_effort    TEXT,
  desired_suspended BIGINT NOT NULL DEFAULT 0,
  config_updated_at TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_agents_host ON agents(host);
CREATE INDEX IF NOT EXISTS idx_agents_heartbeat ON agents(last_heartbeat_at DESC);
