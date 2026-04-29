-- Phase D: track when each orchestration last fired its trigger so
-- the cron scheduler doesn't double-fire and the finding probe has
-- a place to dedupe per-finding runs.
--
-- Stored on the orchestration itself rather than a side table — the
-- cardinality is low (one orchestration → one last-fired) and the
-- writes are infrequent (once per cron tick / finding match).

ALTER TABLE orchestrations ADD COLUMN last_fired_at  TIMESTAMP;
ALTER TABLE orchestrations ADD COLUMN last_fired_by  TEXT;       -- "cron" | "finding:<id>" | "manual:<userid>"

CREATE INDEX IF NOT EXISTS idx_orchestrations_cron
  ON orchestrations(trigger_kind, enabled)
  WHERE trigger_kind = 'cron';
