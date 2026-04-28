-- Phase 9: cp_meta — federation foundation.
--
-- Single-row table holding this CP's identity. A parent CP discovers
-- children by polling each one's /api/v1/cp/introspect endpoint, which
-- reads from this table. The instance_id is generated on first boot
-- and stable for the life of the database — it's the only stable
-- handle a parent has on a child across restarts/upgrades.
--
-- The CHECK constraint enforces the singleton: there's exactly one row
-- per CP. Federation token is stored as a bcrypt hash so a leaked
-- backup of cp.db doesn't hand out the live token.

CREATE TABLE IF NOT EXISTS cp_meta (
  id                    INTEGER PRIMARY KEY CHECK (id = 1),
  instance_id           TEXT    NOT NULL UNIQUE,
  region                TEXT    NOT NULL DEFAULT '',
  display_name          TEXT    NOT NULL DEFAULT '',
  role                  TEXT    NOT NULL DEFAULT 'standalone',  -- standalone | parent | child
  federation_token_hash TEXT    NOT NULL DEFAULT '',            -- bcrypt; empty = federation disabled
  created_at            TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  updated_at            TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
