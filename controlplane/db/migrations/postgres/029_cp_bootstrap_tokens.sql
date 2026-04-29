-- Phase 21.1 — CP bootstrap tokens. See sqlite/029 for context.

CREATE TABLE cp_bootstrap_tokens (
  id                  BIGSERIAL PRIMARY KEY,
  token_prefix        TEXT    NOT NULL UNIQUE,
  token_hash          TEXT    NOT NULL,
  display_name        TEXT    NOT NULL,
  region              TEXT    NOT NULL,
  parent_url          TEXT    NOT NULL,
  created_by_user_id  BIGINT REFERENCES users(id) ON DELETE SET NULL,
  created_by_email    TEXT,
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at          TIMESTAMPTZ NOT NULL,
  used_at             TIMESTAMPTZ,
  used_peer_id        BIGINT REFERENCES federation_peers(id) ON DELETE SET NULL,
  used_from_url       TEXT
);

CREATE INDEX idx_cp_bootstrap_tokens_prefix ON cp_bootstrap_tokens(token_prefix);
CREATE INDEX idx_cp_bootstrap_tokens_unused ON cp_bootstrap_tokens(used_at) WHERE used_at IS NULL;
