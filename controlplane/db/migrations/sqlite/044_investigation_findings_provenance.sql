-- Phase 22.6.2 — link provenance on investigation_findings.
--
-- Every link row gains a `link_method` (how the link happened) and
-- `linked_by` (who/what initiated it). This lets the workspace's
-- Findings tab show provenance chips so operators can tell apart:
--
--   manual       — operator clicked "Link finding" or used the
--                  Investigate dialog from a Finding drawer
--   bulk         — workspace card "Add all ≥ N" button
--   auto-promote — finding promoted to a brand-new case via the
--                  CreateInvestigation `from_finding_id` shortcut
--   autolink     — engine auto-linked the finding when its top
--                  related-case score met the configured autolink
--                  threshold (Phase 22.6.2)
--   import       — backfilled rows from prior CP versions (this
--                  migration tags pre-existing rows; readers should
--                  treat NULL the same way for safety)
--
-- Existing rows: link_method = NULL, linked_by = NULL. The UI treats
-- NULL as 'unknown' and renders a neutral chip — historical links
-- never had provenance recorded, so we don't fabricate one.
--
-- Adding NULL columns to a table with FK references is the safe
-- shape across both sqlite and postgres: no row rewrite, no FK
-- recheck, idempotent if the migration partially ran (the IF NOT
-- EXISTS helpers below).

ALTER TABLE investigation_findings ADD COLUMN link_method TEXT;
ALTER TABLE investigation_findings ADD COLUMN linked_by   TEXT;

-- An index on link_method helps the workspace's "show only autolinked"
-- filter (operator review of engine decisions). Partial index keeps
-- the cost down since most reads still use (investigation_id, finding_id).
CREATE INDEX IF NOT EXISTS idx_inv_findings_method
  ON investigation_findings(link_method)
  WHERE link_method IS NOT NULL;
