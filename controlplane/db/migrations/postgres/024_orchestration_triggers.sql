ALTER TABLE orchestrations ADD COLUMN IF NOT EXISTS last_fired_at TIMESTAMPTZ;
ALTER TABLE orchestrations ADD COLUMN IF NOT EXISTS last_fired_by TEXT;

CREATE INDEX IF NOT EXISTS idx_orchestrations_cron
  ON orchestrations(trigger_kind, enabled)
  WHERE trigger_kind = 'cron';
