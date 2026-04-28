-- Phase 9: cp_meta — federation foundation.
-- See sqlite/021_cp_meta.sql for the design rationale.

CREATE TABLE IF NOT EXISTS cp_meta (
  id                    INTEGER PRIMARY KEY CHECK (id = 1),
  instance_id           TEXT    NOT NULL UNIQUE,
  region                TEXT    NOT NULL DEFAULT '',
  display_name          TEXT    NOT NULL DEFAULT '',
  role                  TEXT    NOT NULL DEFAULT 'standalone',
  federation_token_hash TEXT    NOT NULL DEFAULT '',
  created_at            TIMESTAMPTZ DEFAULT NOW(),
  updated_at            TIMESTAMPTZ DEFAULT NOW()
);
