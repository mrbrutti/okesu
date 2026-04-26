-- Phase 9 schema: API tokens for programmatic access.
--
-- Format: "okesu_<32-byte-hex>" — operator sees the full token exactly once
-- at creation. The CP stores only the bcrypt hash. A 16-char prefix is
-- kept in plaintext to enable O(1) row lookup at request time, after which
-- a single bcrypt-compare verifies the full secret.

CREATE TABLE IF NOT EXISTS api_tokens (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  name            TEXT    NOT NULL,
  -- First 16 chars of the random tail (post-"okesu_"). Indexed for fast lookup.
  prefix          TEXT    NOT NULL UNIQUE,
  hash            TEXT    NOT NULL,                       -- bcrypt of the full token
  -- CSV of scope strings, e.g. "findings:write".
  scopes          TEXT    NOT NULL DEFAULT '',
  created_by_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
  created_by_email TEXT,
  created_at      TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  expires_at      TIMESTAMP,                              -- NULL = no expiry
  last_used_at    TIMESTAMP,
  revoked_at      TIMESTAMP                               -- NULL = active
);

CREATE INDEX IF NOT EXISTS idx_api_tokens_prefix ON api_tokens(prefix);
CREATE INDEX IF NOT EXISTS idx_api_tokens_revoked ON api_tokens(revoked_at);
