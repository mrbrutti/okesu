-- Postgres mirror of 053_finding_recurrence.sql.
ALTER TABLE findings ADD COLUMN recurrence_count INTEGER NOT NULL DEFAULT 1;
ALTER TABLE findings ADD COLUMN last_seen_at    TIMESTAMPTZ;

UPDATE findings SET last_seen_at = created_at WHERE last_seen_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_findings_dedup_open
  ON findings(dedup_key, status)
  WHERE status = 'open';
