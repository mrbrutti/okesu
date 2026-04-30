-- Phase 22.3 postgres parity. See migrations/sqlite/036_investigations_and_subtype.sql.

CREATE TABLE IF NOT EXISTS investigations (
  id          BIGSERIAL PRIMARY KEY,
  title       TEXT NOT NULL,
  status      TEXT NOT NULL DEFAULT 'active',
  resolution  TEXT,
  summary     TEXT,
  created_by  TEXT,
  created_at  TIMESTAMPTZ DEFAULT NOW(),
  closed_at   TIMESTAMPTZ,
  updated_at  TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_investigations_status ON investigations(status, updated_at DESC);

CREATE TABLE IF NOT EXISTS investigation_findings (
  investigation_id BIGINT NOT NULL REFERENCES investigations(id) ON DELETE CASCADE,
  finding_id       BIGINT NOT NULL REFERENCES findings(id) ON DELETE CASCADE,
  linked_at        TIMESTAMPTZ DEFAULT NOW(),
  PRIMARY KEY (investigation_id, finding_id)
);

CREATE INDEX IF NOT EXISTS idx_inv_findings_finding ON investigation_findings(finding_id);

CREATE TABLE IF NOT EXISTS investigation_runs (
  investigation_id     BIGINT NOT NULL REFERENCES investigations(id) ON DELETE CASCADE,
  orchestration_run_id BIGINT NOT NULL REFERENCES orchestration_runs(id) ON DELETE CASCADE,
  linked_at            TIMESTAMPTZ DEFAULT NOW(),
  PRIMARY KEY (investigation_id, orchestration_run_id)
);

CREATE INDEX IF NOT EXISTS idx_inv_runs_run ON investigation_runs(orchestration_run_id);

CREATE TABLE IF NOT EXISTS investigation_notes (
  id               BIGSERIAL PRIMARY KEY,
  investigation_id BIGINT NOT NULL REFERENCES investigations(id) ON DELETE CASCADE,
  author           TEXT NOT NULL,
  body             TEXT NOT NULL,
  created_at       TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_inv_notes_investigation
  ON investigation_notes(investigation_id, created_at DESC);

ALTER TABLE findings ADD COLUMN IF NOT EXISTS subtype TEXT;
CREATE INDEX IF NOT EXISTS idx_findings_subtype ON findings(subtype) WHERE subtype IS NOT NULL;
