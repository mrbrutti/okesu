-- Phase 11.1: orchestration-driven finding mutations.
--
-- The orchestrator's action dispatcher (engine.applyActions) writes one
-- row here for every status change, severity override, tag mutation, or
-- run-link applied to a finding. Operators see this on the finding-
-- detail page as the audit trail; the run-detail page reads back the
-- subset where orchestration_run_id matches the run they're viewing.
--
-- The same table covers human edits too — when an operator triages a
-- finding through the UI, the API handler logs the change here. That
-- way the history view doesn't have to merge two sources.

CREATE TABLE IF NOT EXISTS finding_edits (
  id                    INTEGER PRIMARY KEY AUTOINCREMENT,
  finding_id            INTEGER NOT NULL REFERENCES findings(id) ON DELETE CASCADE,

  -- Field flag — one row per atomic change. Multi-field changes
  -- (e.g. status+tag in the same action) emit multiple rows.
  --   status            triage state changed
  --   severity_override severity_operator changed
  --   tag_add           a tag was added
  --   tag_remove        a tag was removed
  --   linked_run        an orchestration run was linked to this finding
  field                 TEXT NOT NULL,
  old_value             TEXT,
  new_value             TEXT,
  reason                TEXT,

  -- Origin. Exactly one of (user_id, orchestration_run_id) is non-null
  -- for an edit; both null means a system action (rare — currently
  -- only the dedup auto-suppress on ingest sets neither).
  edited_by_user_id     INTEGER REFERENCES users(id) ON DELETE SET NULL,
  edited_by_email       TEXT,
  orchestration_run_id  INTEGER,
  orchestration_step_id TEXT,

  edited_at             TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_finding_edits_finding ON finding_edits(finding_id, edited_at DESC);
CREATE INDEX IF NOT EXISTS idx_finding_edits_run     ON finding_edits(orchestration_run_id) WHERE orchestration_run_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_finding_edits_field   ON finding_edits(field);

-- finding_run_links — bidirectional join so the finding-detail page
-- can list "auto-handled by run #N" entries and the run-detail page
-- can list "this run touched findings X, Y, Z".
CREATE TABLE IF NOT EXISTS finding_run_links (
  finding_id            INTEGER NOT NULL REFERENCES findings(id) ON DELETE CASCADE,
  orchestration_run_id  INTEGER NOT NULL,
  step_id               TEXT,
  linked_at             TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (finding_id, orchestration_run_id, step_id)
);

CREATE INDEX IF NOT EXISTS idx_finding_run_links_run ON finding_run_links(orchestration_run_id);
