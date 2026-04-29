-- Phase 21.1 — CP bootstrap tokens.
--
-- One-time-use credentials handed out by a parent CP when it generates
-- a "spin up a child CP" bundle. The child includes the token in its
-- first /api/v1/cp/bootstrap call to the parent, which:
--   1. Verifies the token (prefix + bcrypt)
--   2. Creates a federation_peers row pointing at the child's URL
--   3. Marks the bootstrap token used (no further calls accepted)
--   4. Returns a fresh long-lived federation_token the child uses for
--      its own /api/v1/cp/introspect auth going forward
--
-- token_prefix is stored plaintext for O(1) lookup; the full token is
-- bcrypt-hashed under token_hash. Same scheme as api_tokens. Tokens
-- expire at expires_at — defaults to creation + 24h since they're
-- meant to be used immediately on a fresh deploy.

CREATE TABLE cp_bootstrap_tokens (
  id                  INTEGER PRIMARY KEY AUTOINCREMENT,
  token_prefix        TEXT    NOT NULL UNIQUE,
  token_hash          TEXT    NOT NULL,
  display_name        TEXT    NOT NULL,
  region              TEXT    NOT NULL,
  parent_url          TEXT    NOT NULL,
  created_by_user_id  INTEGER REFERENCES users(id) ON DELETE SET NULL,
  created_by_email    TEXT,
  created_at          TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at          TIMESTAMP NOT NULL,
  used_at             TIMESTAMP,
  used_peer_id        INTEGER REFERENCES federation_peers(id) ON DELETE SET NULL,
  used_from_url       TEXT
);

CREATE INDEX idx_cp_bootstrap_tokens_prefix ON cp_bootstrap_tokens(token_prefix);
CREATE INDEX idx_cp_bootstrap_tokens_unused ON cp_bootstrap_tokens(used_at) WHERE used_at IS NULL;
