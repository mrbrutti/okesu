-- See sqlite/053_ioc_feeds.sql for design notes.

CREATE TABLE IF NOT EXISTS ioc_feeds (
  id                          BIGSERIAL PRIMARY KEY,
  slug                        TEXT NOT NULL UNIQUE,
  name                        TEXT NOT NULL,
  kind                        TEXT NOT NULL CHECK (kind IN ('single_file','git')),
  url                         TEXT NOT NULL,
  subpath                     TEXT,
  parser                      TEXT NOT NULL
                                CHECK (parser IN ('yara','sigma','urlhaus_csv','threatfox_csv','cisa_kev_json')),
  auth_credential_id          BIGINT REFERENCES credentials(id) ON DELETE SET NULL,
  refresh_interval_seconds    INTEGER NOT NULL DEFAULT 86400,
  enabled                     BOOLEAN NOT NULL DEFAULT FALSE,
  installed_from_registry     BOOLEAN NOT NULL DEFAULT FALSE,
  last_refresh_at             TIMESTAMPTZ,
  last_refresh_status         TEXT,
  last_refresh_error          TEXT,
  last_refresh_entry_count    INTEGER,
  created_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_ioc_feeds_enabled ON ioc_feeds(enabled);

ALTER TABLE iocs ADD COLUMN IF NOT EXISTS feed_id BIGINT REFERENCES ioc_feeds(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_iocs_feed ON iocs(feed_id) WHERE feed_id IS NOT NULL;

ALTER TABLE ioc_observations
  ALTER COLUMN ioc_id DROP NOT NULL;

ALTER TABLE ioc_observations
  DROP CONSTRAINT IF EXISTS ioc_observations_ioc_id_fkey;
ALTER TABLE ioc_observations
  ADD CONSTRAINT ioc_observations_ioc_id_fkey
    FOREIGN KEY (ioc_id) REFERENCES iocs(id) ON DELETE SET NULL;

-- ioc_id is now nullable; rebuild the index as partial so orphaned
-- observations don't bloat it.
DROP INDEX IF EXISTS idx_ioc_obs_ioc;
CREATE INDEX idx_ioc_obs_ioc ON ioc_observations(ioc_id, observed_at DESC) WHERE ioc_id IS NOT NULL;

ALTER TABLE ioc_observations ADD COLUMN IF NOT EXISTS orphaned_rule_label TEXT;
CREATE INDEX IF NOT EXISTS idx_ioc_obs_orphan ON ioc_observations(orphaned_rule_label) WHERE orphaned_rule_label IS NOT NULL;

ALTER TABLE cp_meta ADD COLUMN IF NOT EXISTS feeds_consent_granted_at TIMESTAMPTZ;
