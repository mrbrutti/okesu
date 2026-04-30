-- Phase 22.3: Investigations entity + finding subtype column.
--
-- investigations:        T2 case workspace. Groups findings + runs +
--                        notes through an active→closed lifecycle so
--                        operators have something to *open*, *work*, and
--                        *close* (Phase 22.1's findings + runs are the
--                        atomic events; this entity is the case file).
-- investigation_findings: m2m link from investigation → findings. A
--                        finding can belong to multiple investigations
--                        (e.g. cross-incident IOC reuse).
-- investigation_runs:    m2m link from investigation → orchestration_runs.
-- investigation_notes:   per-investigation analyst notes; markdown body
--                        rendered in UI.
-- findings.subtype:      orthogonal to category; identifies the
--                        STRUCTURED SHAPE of attributes ("hypothesis",
--                        "meeting_minutes", future: "advisory", etc.).
--                        Enables UI rendering hooks per subtype.
--
-- See docs/superpowers/specs/2026-04-29-threatcaddy-borrows-phasing-design.md
-- §"Phase 22.3 — Hypothesis-driven T2".

CREATE TABLE IF NOT EXISTS investigations (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  title       TEXT NOT NULL,
  status      TEXT NOT NULL DEFAULT 'active',  -- active | closed | archived
  resolution  TEXT,                             -- resolved | false_positive | duplicate | wont_fix; NULL when not closed
  summary     TEXT,
  created_by  TEXT,                             -- user email or system actor
  created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  closed_at   TIMESTAMP,
  updated_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_investigations_status ON investigations(status, updated_at DESC);

CREATE TABLE IF NOT EXISTS investigation_findings (
  investigation_id INTEGER NOT NULL REFERENCES investigations(id) ON DELETE CASCADE,
  finding_id       INTEGER NOT NULL REFERENCES findings(id) ON DELETE CASCADE,
  linked_at        TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (investigation_id, finding_id)
);

CREATE INDEX IF NOT EXISTS idx_inv_findings_finding ON investigation_findings(finding_id);

CREATE TABLE IF NOT EXISTS investigation_runs (
  investigation_id     INTEGER NOT NULL REFERENCES investigations(id) ON DELETE CASCADE,
  orchestration_run_id INTEGER NOT NULL REFERENCES orchestration_runs(id) ON DELETE CASCADE,
  linked_at            TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (investigation_id, orchestration_run_id)
);

CREATE INDEX IF NOT EXISTS idx_inv_runs_run ON investigation_runs(orchestration_run_id);

CREATE TABLE IF NOT EXISTS investigation_notes (
  id               INTEGER PRIMARY KEY AUTOINCREMENT,
  investigation_id INTEGER NOT NULL REFERENCES investigations(id) ON DELETE CASCADE,
  author           TEXT NOT NULL,
  body             TEXT NOT NULL,
  created_at       TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_inv_notes_investigation
  ON investigation_notes(investigation_id, created_at DESC);

-- Finding subtype column. Orthogonal to `category`: category is the
-- operational classification (process/file/network/cert), subtype is
-- the structured shape of attributes. Most findings have NULL subtype.
ALTER TABLE findings ADD COLUMN subtype TEXT;
CREATE INDEX IF NOT EXISTS idx_findings_subtype ON findings(subtype) WHERE subtype IS NOT NULL;
