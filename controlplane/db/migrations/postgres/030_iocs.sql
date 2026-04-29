-- Phase 22.1 postgres parity. See migrations/sqlite/030_iocs.sql.

CREATE TABLE IF NOT EXISTS iocs (
  id                BIGSERIAL PRIMARY KEY,
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
  first_seen        TIMESTAMPTZ DEFAULT NOW(),
  last_seen         TIMESTAMPTZ DEFAULT NOW(),
  observation_count BIGINT NOT NULL DEFAULT 0,
  created_at        TIMESTAMPTZ DEFAULT NOW(),
  updated_at        TIMESTAMPTZ DEFAULT NOW(),
  UNIQUE (kind, normalized_value)
);

CREATE INDEX IF NOT EXISTS idx_iocs_kind          ON iocs(kind);
CREATE INDEX IF NOT EXISTS idx_iocs_source        ON iocs(source);
CREATE INDEX IF NOT EXISTS idx_iocs_last_seen     ON iocs(last_seen DESC);
CREATE INDEX IF NOT EXISTS idx_iocs_attribution   ON iocs(attribution) WHERE attribution IS NOT NULL;

CREATE TABLE IF NOT EXISTS ioc_observations (
  id                   BIGSERIAL PRIMARY KEY,
  ioc_id               BIGINT NOT NULL REFERENCES iocs(id) ON DELETE CASCADE,
  finding_id           BIGINT REFERENCES findings(id) ON DELETE CASCADE,
  orchestration_run_id BIGINT,
  host                 TEXT,
  observed_at          TIMESTAMPTZ DEFAULT NOW(),
  CHECK (finding_id IS NOT NULL OR orchestration_run_id IS NOT NULL)
);

CREATE INDEX IF NOT EXISTS idx_ioc_obs_ioc      ON ioc_observations(ioc_id, observed_at DESC);
CREATE INDEX IF NOT EXISTS idx_ioc_obs_finding  ON ioc_observations(finding_id) WHERE finding_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_ioc_obs_run      ON ioc_observations(orchestration_run_id) WHERE orchestration_run_id IS NOT NULL;
