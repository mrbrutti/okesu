-- Phase: postgres parity. See migrations/sqlite/045_fleet_env.sql.

CREATE TABLE IF NOT EXISTS fleet_env (
  id                          INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
  anthropic_api_key_sealed    BYTEA,
  anthropic_api_key_nonce     BYTEA,
  anthropic_api_key_last4     TEXT,
  openai_api_key_sealed       BYTEA,
  openai_api_key_nonce        BYTEA,
  openai_api_key_last4        TEXT,
  version                     BIGINT NOT NULL DEFAULT 0,
  source                      TEXT NOT NULL DEFAULT 'local'
                                CHECK (source IN ('local','federated_from_parent')),
  parent_cp_id                TEXT,
  updated_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_by_user_email       TEXT
);

INSERT INTO fleet_env (id) VALUES (1) ON CONFLICT (id) DO NOTHING;
