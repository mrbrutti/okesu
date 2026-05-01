-- Phase 22.8 — see sqlite/049 for context.

CREATE TABLE IF NOT EXISTS secrets (
  id                BIGSERIAL PRIMARY KEY,
  name              TEXT    NOT NULL UNIQUE,
  kind              TEXT    NOT NULL,
  description       TEXT    NOT NULL DEFAULT '',
  encrypted_payload BYTEA   NOT NULL,
  payload_nonce     BYTEA   NOT NULL,
  owner_group_id    INTEGER REFERENCES groups(id) ON DELETE SET NULL,
  created_at        TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  created_by_email  TEXT
);

CREATE INDEX IF NOT EXISTS idx_secrets_kind ON secrets(kind);
CREATE INDEX IF NOT EXISTS idx_secrets_owner ON secrets(owner_group_id);

CREATE TABLE IF NOT EXISTS secret_bindings (
  id          BIGSERIAL PRIMARY KEY,
  secret_id   INTEGER NOT NULL REFERENCES secrets(id) ON DELETE CASCADE,
  selector    TEXT    NOT NULL DEFAULT '',
  scope       TEXT    NOT NULL DEFAULT 'any',
  created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  UNIQUE (secret_id, selector, scope)
);

CREATE INDEX IF NOT EXISTS idx_secret_bindings_secret
  ON secret_bindings(secret_id);
