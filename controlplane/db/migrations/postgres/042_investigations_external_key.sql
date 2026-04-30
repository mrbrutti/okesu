-- Phase 22.5 — see sqlite/042 for context.
--
-- Postgres version uses a partial UNIQUE index so multiple
-- NULL-key (manual) investigations don't collide. Without WHERE
-- external_key IS NOT NULL, postgres treats two NULLs as duplicates
-- (sqlite differs — multiple NULLs are allowed in UNIQUE).

ALTER TABLE investigations ADD COLUMN external_key TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS idx_investigations_external_key
  ON investigations(external_key)
  WHERE external_key IS NOT NULL;
