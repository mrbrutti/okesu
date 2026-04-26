-- Phase 12: agents.primary_key → (name, host).
--
-- Real fleets routinely run the same agent (e.g. `edr`) on dozens of hosts.
-- The original schema used `name TEXT PRIMARY KEY`, which collapsed every
-- such daemon into a single row — only the most recent heartbeat survived,
-- and per-host visibility was lost.
--
-- SQLite doesn't allow altering an existing PRIMARY KEY in place, so we:
--   1. Build agents_new with the composite PK.
--   2. Copy existing rows. `host` is NOT NULL in the original, so the copy
--      is straightforward; if any legacy row somehow had an empty host we
--      coerce it to "(unknown)" so it satisfies the new key.
--   3. Drop the old table and rename.
--   4. Re-create indexes.
--
-- Foreign key references: as of migration 011 nothing references agents.
-- (Findings, runs, and audit log all carry an inline agent_name TEXT.)

CREATE TABLE IF NOT EXISTS agents_new (
  name              TEXT NOT NULL,
  host              TEXT NOT NULL,
  provider          TEXT,
  model             TEXT,
  version           TEXT,

  registered_at     TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  last_heartbeat_at TIMESTAMP,
  last_tick_count   INTEGER NOT NULL DEFAULT 0,

  desired_max_turns INTEGER,
  desired_effort    TEXT,
  desired_suspended INTEGER NOT NULL DEFAULT 0,
  config_updated_at TIMESTAMP,

  PRIMARY KEY (name, host)
);

INSERT INTO agents_new (
  name, host, provider, model, version,
  registered_at, last_heartbeat_at, last_tick_count,
  desired_max_turns, desired_effort, desired_suspended, config_updated_at
)
SELECT
  name,
  COALESCE(NULLIF(host, ''), '(unknown)') AS host,
  provider, model, version,
  registered_at, last_heartbeat_at, last_tick_count,
  desired_max_turns, desired_effort, desired_suspended, config_updated_at
FROM agents;

DROP TABLE agents;
ALTER TABLE agents_new RENAME TO agents;

CREATE INDEX IF NOT EXISTS idx_agents_host      ON agents(host);
CREATE INDEX IF NOT EXISTS idx_agents_heartbeat ON agents(last_heartbeat_at DESC);
CREATE INDEX IF NOT EXISTS idx_agents_name      ON agents(name);
