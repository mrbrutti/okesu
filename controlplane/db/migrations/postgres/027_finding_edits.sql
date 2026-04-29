-- Phase 11.1 postgres parity. See migrations/sqlite/027_finding_edits.sql.

CREATE TABLE IF NOT EXISTS finding_edits (
  id                    BIGSERIAL PRIMARY KEY,
  finding_id            BIGINT NOT NULL REFERENCES findings(id) ON DELETE CASCADE,
  field                 TEXT NOT NULL,
  old_value             TEXT,
  new_value             TEXT,
  reason                TEXT,
  edited_by_user_id     BIGINT REFERENCES users(id) ON DELETE SET NULL,
  edited_by_email       TEXT,
  orchestration_run_id  BIGINT,
  orchestration_step_id TEXT,
  edited_at             TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_finding_edits_finding ON finding_edits(finding_id, edited_at DESC);
CREATE INDEX IF NOT EXISTS idx_finding_edits_run     ON finding_edits(orchestration_run_id) WHERE orchestration_run_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_finding_edits_field   ON finding_edits(field);

CREATE TABLE IF NOT EXISTS finding_run_links (
  finding_id            BIGINT NOT NULL REFERENCES findings(id) ON DELETE CASCADE,
  orchestration_run_id  BIGINT NOT NULL,
  step_id               TEXT,
  linked_at             TIMESTAMPTZ DEFAULT NOW(),
  PRIMARY KEY (finding_id, orchestration_run_id, step_id)
);
CREATE INDEX IF NOT EXISTS idx_finding_run_links_run ON finding_run_links(orchestration_run_id);
