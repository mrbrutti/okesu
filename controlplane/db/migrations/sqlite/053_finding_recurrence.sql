-- Phase 22.10 — finding recurrence counter.
--
-- The dedup-fingerprint fix on the agent side stops the LLM
-- prose-drift fragmentation, but operators still want to see "how
-- many times has this fired?" The existing supersede path rolls
-- siblings into the new primary; we now sum those sibling counts
-- onto the survivor so the operator queue shows the recurrence
-- count next to the title.
--
-- last_seen_at tracks the most recent emission so a finding that
-- fires once in the morning and once at midnight is distinguishable
-- from one that's been firing every minute.

ALTER TABLE findings ADD COLUMN recurrence_count INTEGER NOT NULL DEFAULT 1;
ALTER TABLE findings ADD COLUMN last_seen_at    TIMESTAMP;

-- Backfill: every existing row counts as 1 (the column default
-- already provides this) and last_seen_at is just the row's
-- created_at since we don't have per-event last-seen data
-- pre-migration.
UPDATE findings SET last_seen_at = created_at WHERE last_seen_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_findings_dedup_open
  ON findings(dedup_key, status)
  WHERE status = 'open';
