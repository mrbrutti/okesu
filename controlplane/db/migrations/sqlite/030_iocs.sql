-- Phase 22.1: IOCs as a first-class CP entity.
--
-- Two tables. `iocs` holds canonical indicators keyed by (kind,
-- normalized_value); rows come from two sources: catalog YAML (curated)
-- and finding-ingest extraction (observed). `ioc_observations` is the
-- many-to-many link from an IOC to the findings/runs that surfaced it.
--
-- See docs/superpowers/specs/2026-04-29-threatcaddy-borrows-phasing-design.md
-- for the design.

CREATE TABLE IF NOT EXISTS iocs (
  id                INTEGER PRIMARY KEY AUTOINCREMENT,
  kind              TEXT NOT NULL,
  value             TEXT NOT NULL,
  normalized_value  TEXT NOT NULL,
  source            TEXT NOT NULL DEFAULT 'observed',
  definition_path   TEXT,
  confidence        TEXT,
  attribution       TEXT,
  severity_floor    TEXT,
  classification    TEXT,
  notes             TEXT,
  first_seen        TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  last_seen         TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  observation_count INTEGER NOT NULL DEFAULT 0,
  created_at        TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  updated_at        TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  UNIQUE (kind, normalized_value)
);

CREATE INDEX IF NOT EXISTS idx_iocs_kind          ON iocs(kind);
CREATE INDEX IF NOT EXISTS idx_iocs_source        ON iocs(source);
CREATE INDEX IF NOT EXISTS idx_iocs_last_seen     ON iocs(last_seen DESC);
CREATE INDEX IF NOT EXISTS idx_iocs_attribution   ON iocs(attribution) WHERE attribution IS NOT NULL;

CREATE TABLE IF NOT EXISTS ioc_observations (
  id                   INTEGER PRIMARY KEY AUTOINCREMENT,
  ioc_id               INTEGER NOT NULL REFERENCES iocs(id) ON DELETE CASCADE,
  finding_id           INTEGER REFERENCES findings(id) ON DELETE CASCADE,
  orchestration_run_id INTEGER,
  host                 TEXT,
  observed_at          TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  CHECK (finding_id IS NOT NULL OR orchestration_run_id IS NOT NULL)
);

CREATE INDEX IF NOT EXISTS idx_ioc_obs_ioc      ON ioc_observations(ioc_id, observed_at DESC);
CREATE INDEX IF NOT EXISTS idx_ioc_obs_finding  ON ioc_observations(finding_id) WHERE finding_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_ioc_obs_run      ON ioc_observations(orchestration_run_id) WHERE orchestration_run_id IS NOT NULL;
