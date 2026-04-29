-- Phase 21.3 — managed CP provisioning job log. See sqlite/031.

CREATE TABLE cp_provisions (
  id                  BIGSERIAL PRIMARY KEY,
  display_name        TEXT NOT NULL,
  region              TEXT NOT NULL,
  cloud               TEXT NOT NULL,
  credential_id       BIGINT REFERENCES cloud_credentials(id) ON DELETE SET NULL,
  credential_name     TEXT,
  cloud_params_json   TEXT NOT NULL DEFAULT '{}',
  status              TEXT NOT NULL DEFAULT 'queued',
  cloud_resource_id   TEXT,
  cloud_resource_url  TEXT,
  bundle_token_id     BIGINT REFERENCES cp_bootstrap_tokens(id) ON DELETE SET NULL,
  peer_id             BIGINT REFERENCES federation_peers(id) ON DELETE SET NULL,
  log                 TEXT NOT NULL DEFAULT '',
  error               TEXT,
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  started_at          TIMESTAMPTZ,
  ended_at            TIMESTAMPTZ,
  created_by_user_id  BIGINT REFERENCES users(id) ON DELETE SET NULL,
  created_by_email    TEXT
);

CREATE INDEX idx_cp_provisions_status ON cp_provisions(status, created_at DESC);
CREATE INDEX idx_cp_provisions_cloud  ON cp_provisions(cloud, created_at DESC);
