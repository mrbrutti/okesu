-- Phase 22.7 — see sqlite/045 for context.

CREATE TABLE IF NOT EXISTS saved_searches (
  id          BIGSERIAL PRIMARY KEY,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name        TEXT    NOT NULL,
  scope       TEXT    NOT NULL DEFAULT 'findings',
  config_json TEXT    NOT NULL,
  is_default  INTEGER NOT NULL DEFAULT 0,
  created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  updated_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  UNIQUE (user_id, scope, name)
);

CREATE INDEX IF NOT EXISTS idx_saved_searches_user
  ON saved_searches(user_id, scope, updated_at DESC);
