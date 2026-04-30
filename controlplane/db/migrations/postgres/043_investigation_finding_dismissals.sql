-- Phase 22.6 — see sqlite/043 for context.
--
-- Postgres variant. Identical schema; `TIMESTAMP DEFAULT
-- CURRENT_TIMESTAMP` works in both dialects.

CREATE TABLE IF NOT EXISTS investigation_finding_dismissals (
  investigation_id INTEGER NOT NULL REFERENCES investigations(id) ON DELETE CASCADE,
  finding_id       INTEGER NOT NULL REFERENCES findings(id) ON DELETE CASCADE,
  dismissed_by     TEXT,
  dismissed_at     TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (investigation_id, finding_id)
);

CREATE INDEX IF NOT EXISTS idx_inv_finding_dismiss_finding
  ON investigation_finding_dismissals(finding_id);
