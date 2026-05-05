-- 060_node_provisions.sql
-- Tracks a managed-deploy provisioning run: a CP-issued cloud VM
-- launch that auto-installs the okesu daemon and self-registers
-- via S3 against the picked transport_config. Mirrors cp_provisions
-- in shape; node_id (instead of peer_id) links to the nodes row
-- once registration completes.
CREATE TABLE node_provisions (
  id                      INTEGER PRIMARY KEY AUTOINCREMENT,
  display_name            TEXT NOT NULL,
  region                  TEXT NOT NULL,
  cloud                   TEXT NOT NULL,
  credential_id           INTEGER REFERENCES cloud_credentials(id) ON DELETE SET NULL,
  credential_name         TEXT,
  cloud_params_json       TEXT NOT NULL DEFAULT '{}',
  transport_config_id     INTEGER NOT NULL REFERENCES transport_configs(id) ON DELETE RESTRICT,
  status                  TEXT NOT NULL DEFAULT 'queued',
  cloud_resource_id       TEXT,
  cloud_resource_url      TEXT,
  node_id                 INTEGER REFERENCES nodes(id) ON DELETE SET NULL,
  log                     TEXT NOT NULL DEFAULT '',
  error                   TEXT,
  est_cost_per_hour_usd   REAL,
  instance_shape          TEXT,
  created_at              TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  started_at              TIMESTAMP,
  ended_at                TIMESTAMP,
  created_by_user_id      INTEGER REFERENCES users(id) ON DELETE SET NULL,
  created_by_email        TEXT
);
CREATE INDEX idx_node_provisions_status ON node_provisions(status, created_at DESC);
CREATE INDEX idx_node_provisions_cloud  ON node_provisions(cloud, created_at DESC);
