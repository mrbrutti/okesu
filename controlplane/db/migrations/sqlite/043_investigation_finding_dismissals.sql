-- Phase 22.6 — per-case dismiss tombstones for suggested findings.
--
-- The workspace's Suggested findings card scores candidate findings
-- (same dedup_key, same IOC, same host±60min, same daimon+severity in
-- 24h) and offers + Add / Dismiss. A dismiss isn't "I don't want to
-- see this finding" — it's "this finding does not belong on THIS case",
-- so the tombstone is keyed to the (case, finding) pair, not to the
-- finding itself.
--
-- Tombstones are NOT per-operator: dismissing a suggestion is a case-
-- level decision, not a personal-view filter. Once an operator says
-- "no, this isn't related to this case", the suggestion stays gone for
-- everyone working the case. If a different operator disagrees, they
-- can still + Add it from the Findings page or via the Investigate
-- dialog — Add wins (DELETE FROM dismissals WHERE …) so the tombstone
-- lifts on re-link.

CREATE TABLE IF NOT EXISTS investigation_finding_dismissals (
  investigation_id INTEGER NOT NULL REFERENCES investigations(id) ON DELETE CASCADE,
  finding_id       INTEGER NOT NULL REFERENCES findings(id) ON DELETE CASCADE,
  dismissed_by     TEXT,
  dismissed_at     TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (investigation_id, finding_id)
);

CREATE INDEX IF NOT EXISTS idx_inv_finding_dismiss_finding
  ON investigation_finding_dismissals(finding_id);
