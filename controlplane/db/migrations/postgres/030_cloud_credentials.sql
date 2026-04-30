-- Phase 21.2 — Cloud credentials. See sqlite/030 for context.

CREATE TABLE cloud_credentials (
  id                 BIGSERIAL PRIMARY KEY,
  cloud              TEXT NOT NULL,
  name               TEXT NOT NULL,
  region             TEXT,
  encrypted_payload  BYTEA NOT NULL,
  payload_nonce      BYTEA NOT NULL,
  created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
  created_by_email   TEXT,
  last_used_at       TIMESTAMPTZ,
  last_test_at       TIMESTAMPTZ,
  last_test_ok       BOOLEAN,
  last_test_error    TEXT,
  UNIQUE(cloud, name)
);

CREATE INDEX idx_cloud_credentials_cloud ON cloud_credentials(cloud);
