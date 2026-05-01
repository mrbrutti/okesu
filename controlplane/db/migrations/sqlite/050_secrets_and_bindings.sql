-- Phase 22.8 — credential bindings (PR γ of the RBAC overhaul).
--
-- Two tables: secrets carry the encrypted credential material;
-- secret_bindings glue a secret to a label selector + scope so a
-- daimon / deploy / agent run on a matching node picks the right
-- credential automatically.
--
-- Why generic instead of extending cloud_credentials and api_tokens
-- directly: those two tables are well-shaped for their existing
-- consumers (cloud provisioner, CP-API access). Pulling SSH keys
-- and arbitrary env-var rows into them would force polymorphic
-- columns. A new table for the bindable kinds keeps each role
-- clean.
--
-- Encryption: same AES-256-GCM + HKDF pattern cloud_credentials
-- uses, with a different domain-separated info string
-- ('okesu-secrets-v1'). Same master key from the meta table.

CREATE TABLE IF NOT EXISTS secrets (
  id                INTEGER PRIMARY KEY AUTOINCREMENT,
  name              TEXT    NOT NULL UNIQUE,
  -- Kind drives consumers + UI rendering.
  --   'env_var' — injected as ENV at agent_run / daimon spawn
  --   'ssh_key' — SSH private key for deploys to matching nodes
  --   'api_key' — generic API key, exposed to consumers by name
  kind              TEXT    NOT NULL,
  description       TEXT    NOT NULL DEFAULT '',
  encrypted_payload BLOB    NOT NULL,
  payload_nonce     BLOB    NOT NULL,
  -- Optional ownership: a group can own a secret so non-owner
  -- admins can be barred from reading the plaintext. NULL = open.
  owner_group_id    INTEGER REFERENCES groups(id) ON DELETE SET NULL,
  created_at        TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  created_by_email  TEXT
);

CREATE INDEX IF NOT EXISTS idx_secrets_kind ON secrets(kind);
CREATE INDEX IF NOT EXISTS idx_secrets_owner ON secrets(owner_group_id);

-- Bindings: a secret applies wherever the selector matches the
-- target's labels. v1 evaluates against node_labels (defined in
-- migration 048). Future scopes (daimon labels, run labels) plug
-- in by extending the resolver, not the schema.
CREATE TABLE IF NOT EXISTS secret_bindings (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  secret_id   INTEGER NOT NULL REFERENCES secrets(id) ON DELETE CASCADE,
  -- Selector grammar from migration 048's parser:
  --   "" = match-all (CP-wide; rarely useful but legal)
  --   "env=prod,team=infra" = AND-only K8s subset
  selector    TEXT    NOT NULL DEFAULT '',
  -- Scope narrows the binding to a particular consumer:
  --   'node'      — applies during deploy / direct-host operations
  --   'daimon'    — applies when a daimon runs on a matching node
  --   'agent_run' — applies for ad-hoc agent runs on a matching node
  --   'any'       — applies to all of the above
  scope       TEXT    NOT NULL DEFAULT 'any',
  created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  -- Same (secret, selector, scope) twice would be a no-op; PRIMARY
  -- KEY-style UNIQUE prevents accidental dupes.
  UNIQUE (secret_id, selector, scope)
);

CREATE INDEX IF NOT EXISTS idx_secret_bindings_secret
  ON secret_bindings(secret_id);
