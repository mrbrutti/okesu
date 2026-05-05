-- 060_node_provisions.sql (postgres)
CREATE TABLE node_provisions (
  id                      BIGSERIAL PRIMARY KEY,
  display_name            TEXT NOT NULL,
  region                  TEXT NOT NULL,
  cloud                   TEXT NOT NULL,
  credential_id           BIGINT REFERENCES cloud_credentials(id) ON DELETE SET NULL,
  credential_name         TEXT,
  cloud_params_json       TEXT NOT NULL DEFAULT '{}',
  transport_config_id     BIGINT NOT NULL REFERENCES transport_configs(id) ON DELETE RESTRICT,
  status                  TEXT NOT NULL DEFAULT 'queued',
  cloud_resource_id       TEXT,
  cloud_resource_url      TEXT,
  node_id                 BIGINT REFERENCES nodes(id) ON DELETE SET NULL,
  log                     TEXT NOT NULL DEFAULT '',
  error                   TEXT,
  est_cost_per_hour_usd   DOUBLE PRECISION,
  instance_shape          TEXT,
  created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
  started_at              TIMESTAMPTZ,
  ended_at                TIMESTAMPTZ,
  created_by_user_id      BIGINT REFERENCES users(id) ON DELETE SET NULL,
  created_by_email        TEXT
);
CREATE INDEX idx_node_provisions_status ON node_provisions(status, created_at DESC);
CREATE INDEX idx_node_provisions_cloud  ON node_provisions(cloud, created_at DESC);
