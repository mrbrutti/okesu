-- Phase 22.7 — operator-saved searches.
--
-- Each row captures a Findings-page filter set under a name. Callers
-- save the set when they find a useful triage view ("my queue",
-- "yesterday's HIGH on edr", "host=foo investigation") and re-apply
-- it via a single click — no more re-typing every filter.
--
-- The schema is intentionally generic on `scope` so the same table
-- can hold saved searches for other surfaces (cases, runs, IOCs)
-- without a migration. The Findings page is the only consumer in
-- v1; future surfaces just write rows with their own `scope` value.
--
-- `is_default` per-(user, scope) marks the search to load on page
-- open. We don't UNIQUE-constrain that flag because honoring it is a
-- read-side concern (the API picks the most-recently-updated default
-- when there's more than one, and clears the others on save) — the
-- migration stays simple.

CREATE TABLE IF NOT EXISTS saved_searches (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
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
