-- Phase 9.2: S3-bucket "dead-drop" transport (postgres parity).
-- Mirrors the sqlite migration; see migrations/sqlite/028_s3_transport.sql
-- for design rationale.

CREATE TABLE IF NOT EXISTS transport_configs (
  id                       BIGSERIAL PRIMARY KEY,
  name                     TEXT NOT NULL,
  kind                     TEXT NOT NULL,
  bucket                   TEXT NOT NULL,
  endpoint                 TEXT NOT NULL,
  region                   TEXT,
  use_ssl                  BOOLEAN NOT NULL DEFAULT TRUE,
  access_key               TEXT,
  secret_key               TEXT,
  fleet_pubkey_pem         TEXT,
  fleet_privkey_pem        TEXT,
  scanner_interval_ms      INTEGER NOT NULL DEFAULT 10000,
  cp_id                    TEXT,
  created_at               TIMESTAMPTZ DEFAULT NOW(),
  updated_at               TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_transport_configs_kind ON transport_configs(kind);
CREATE INDEX IF NOT EXISTS idx_transport_configs_cp_id ON transport_configs(cp_id);

ALTER TABLE nodes ADD COLUMN IF NOT EXISTS transport TEXT NOT NULL DEFAULT 'https';
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS transport_config_id BIGINT REFERENCES transport_configs(id);
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS poll_interval_ms INTEGER;
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS node_uuid TEXT;
CREATE INDEX IF NOT EXISTS idx_nodes_transport ON nodes(transport);
CREATE INDEX IF NOT EXISTS idx_nodes_node_uuid ON nodes(node_uuid);

CREATE TABLE IF NOT EXISTS enrollment_packages (
  id                    BIGSERIAL PRIMARY KEY,
  display_name          TEXT NOT NULL,
  transport_config_id   BIGINT NOT NULL REFERENCES transport_configs(id),
  cp_id                 TEXT NOT NULL,
  package_cert_pem      TEXT NOT NULL,
  package_key_pem       TEXT NOT NULL,
  defaults_json         JSONB,
  revoked_at            TIMESTAMPTZ,
  created_at            TIMESTAMPTZ DEFAULT NOW(),
  created_by            BIGINT REFERENCES users(id)
);
CREATE INDEX IF NOT EXISTS idx_enrollment_packages_transport_cfg ON enrollment_packages(transport_config_id);
CREATE INDEX IF NOT EXISTS idx_enrollment_packages_revoked ON enrollment_packages(revoked_at);
