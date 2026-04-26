-- Phase 5 schema: registered remote hosts the CP can SSH-deploy to.

CREATE TABLE IF NOT EXISTS nodes (
  id              BIGSERIAL PRIMARY KEY,
  name            TEXT NOT NULL UNIQUE,                    -- short label, e.g. "prod-web-01"
  hostname        TEXT NOT NULL,                           -- IP or DNS name
  ssh_user        TEXT NOT NULL DEFAULT 'root',
  ssh_port        BIGINT NOT NULL DEFAULT 22,

  -- pending: registered but never deployed
  -- deploying: a deploy job is in-flight
  -- ready: at least one successful deploy; daemon should be running
  -- failed: last deploy failed
  status          TEXT NOT NULL DEFAULT 'pending',
  status_message  TEXT,                                    -- human-readable detail
  last_status_at  TIMESTAMP,
  last_deployed_at TIMESTAMP,

  -- Comma-separated agent names installed on this node, e.g. "edr,instance-integrity".
  agents_installed TEXT,

  notes           TEXT,
  created_at      TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_nodes_status ON nodes(status);
