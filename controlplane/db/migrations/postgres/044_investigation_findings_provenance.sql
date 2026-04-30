-- Phase 22.6.2 — see sqlite/044 for context.
--
-- Postgres variant. Same shape; postgres's ALTER TABLE ADD COLUMN
-- is also non-rewriting when the new column is NULL.

ALTER TABLE investigation_findings ADD COLUMN IF NOT EXISTS link_method TEXT;
ALTER TABLE investigation_findings ADD COLUMN IF NOT EXISTS linked_by   TEXT;

CREATE INDEX IF NOT EXISTS idx_inv_findings_method
  ON investigation_findings(link_method)
  WHERE link_method IS NOT NULL;
