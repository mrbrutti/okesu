-- Phase 13: triage states beyond a binary "acknowledged".
--
-- Operators triage findings into one of several outcomes. The status drives
-- both the dashboard view (filter / pill) AND a feedback loop to the
-- daemon: a `false_positive` triage causes the daemon to silently suppress
-- future occurrences of the same fingerprint instead of re-emitting them.
--
-- Statuses (flat enum, no hierarchy):
--
--   open            New, untriaged.
--   acknowledged    "I've seen it, not handling now."
--   investigating   "Someone is digging in."
--   resolved        "Was real, fixed it."
--   false_positive  "LLM was wrong, ignore similar."
--   wontfix         "Real but accepted (risk_accepted alias)."
--
-- The legacy `acknowledged` boolean is kept as a generated column so old
-- API consumers and notification rules don't break. Anything other than
-- `open` counts as acknowledged.

ALTER TABLE findings ADD COLUMN status              TEXT;
ALTER TABLE findings ADD COLUMN triage_note         TEXT;
ALTER TABLE findings ADD COLUMN triaged_at          TIMESTAMP;
ALTER TABLE findings ADD COLUMN triaged_by_user_id  INTEGER REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE findings ADD COLUMN triaged_by_email    TEXT;

-- Backfill: existing acknowledged rows become 'acknowledged'; the rest 'open'.
UPDATE findings
   SET status              = CASE WHEN acknowledged = 1 THEN 'acknowledged' ELSE 'open' END,
       triage_note         = ack_note,
       triaged_at          = acknowledged_at,
       triaged_by_user_id  = acknowledged_by,
       triaged_by_email    = (SELECT email FROM users WHERE users.id = acknowledged_by)
 WHERE status IS NULL;

-- Make status NOT NULL going forward (SQLite doesn't support ALTER COLUMN, so
-- we enforce via app-side code; the column always carries a value because
-- InsertFinding writes 'open' explicitly and there's no path that doesn't).

CREATE INDEX IF NOT EXISTS idx_findings_status_ts ON findings(status, ts DESC);
