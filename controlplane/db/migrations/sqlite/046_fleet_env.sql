-- fleet_env stores the fleet-wide LLM API keys (Anthropic, OpenAI)
-- the operator manages from Settings → LLM Keys. Singleton (id=1)
-- per CP. Keys are AES-GCM sealed using the same master-key
-- pattern cloud_credentials uses (HKDF info "okesu-fleet-env-v1"
-- for domain separation).
--
-- source = 'local' when this CP's keys were set by the operator.
-- source = 'federated_from_parent' when the keys came from a parent
-- CP via the federation poller; the federation poller respects
-- this flag and only overwrites when source = 'federated_from_parent'.
--
-- version is monotonically incrementing — bumped on every successful
-- update so consumers (daemon poll, S3 readers, federation poller)
-- can cache and only react to changes.

CREATE TABLE IF NOT EXISTS fleet_env (
  id                          INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
  anthropic_api_key_sealed    BLOB,
  anthropic_api_key_nonce     BLOB,
  anthropic_api_key_last4     TEXT,
  openai_api_key_sealed       BLOB,
  openai_api_key_nonce        BLOB,
  openai_api_key_last4        TEXT,
  version                     INTEGER NOT NULL DEFAULT 0,
  source                      TEXT NOT NULL DEFAULT 'local'
                                CHECK (source IN ('local','federated_from_parent')),
  parent_cp_id                TEXT,
  updated_at                  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_by_user_email       TEXT
);

INSERT OR IGNORE INTO fleet_env (id) VALUES (1);
