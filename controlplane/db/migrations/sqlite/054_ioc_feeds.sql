-- Phase X: IOC feeds — pull-on-CP threat-intel ingestion.
--
-- ioc_feeds is the per-feed config + last-refresh state. Each fetched
-- entry upserts into the existing iocs table tagged
-- source = 'feed:<slug>' and feed_id = <ioc_feeds.id>.
--
-- ioc_observations.ioc_id is rebuilt to be nullable + ON DELETE SET NULL
-- so reconcile can delete orphaned iocs rows while preserving the
-- observation history with an orphaned_rule_label breadcrumb.
--
-- See docs/superpowers/specs/2026-05-01-ioc-feeds-design.md.

CREATE TABLE IF NOT EXISTS ioc_feeds (
  id                          INTEGER PRIMARY KEY AUTOINCREMENT,
  slug                        TEXT NOT NULL UNIQUE,
  name                        TEXT NOT NULL,
  kind                        TEXT NOT NULL CHECK (kind IN ('single_file','git')),
  url                         TEXT NOT NULL,
  subpath                     TEXT,
  parser                      TEXT NOT NULL
                                CHECK (parser IN ('yara','sigma','urlhaus_csv','threatfox_csv','cisa_kev_json')),
  auth_credential_id          INTEGER REFERENCES cloud_credentials(id) ON DELETE SET NULL,
  refresh_interval_seconds    INTEGER NOT NULL DEFAULT 86400,
  enabled                     INTEGER NOT NULL DEFAULT 0,
  installed_from_registry     INTEGER NOT NULL DEFAULT 0,
  last_refresh_at             TIMESTAMP,
  last_refresh_status         TEXT,
  last_refresh_error          TEXT,
  last_refresh_entry_count    INTEGER,
  created_at                  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at                  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_ioc_feeds_enabled ON ioc_feeds(enabled);

ALTER TABLE iocs ADD COLUMN feed_id INTEGER REFERENCES ioc_feeds(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_iocs_feed ON iocs(feed_id) WHERE feed_id IS NOT NULL;

-- Rebuild ioc_observations: ioc_id NULLable + ON DELETE SET NULL,
-- new orphaned_rule_label column.
CREATE TABLE ioc_observations_new (
  id                   INTEGER PRIMARY KEY AUTOINCREMENT,
  ioc_id               INTEGER REFERENCES iocs(id) ON DELETE SET NULL,
  finding_id           INTEGER REFERENCES findings(id) ON DELETE SET NULL,
  orchestration_run_id INTEGER REFERENCES orchestration_runs(id) ON DELETE SET NULL,
  host                 TEXT,
  observed_at          TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  orphaned_rule_label  TEXT
);

INSERT INTO ioc_observations_new (id, ioc_id, finding_id, orchestration_run_id, host, observed_at)
SELECT id, ioc_id, finding_id, orchestration_run_id, host, observed_at FROM ioc_observations;

-- Safe to DROP: nothing currently holds a FK pointing at ioc_observations.
-- If a future migration adds such an FK, this rebuild pattern will need
-- adjustment (PRAGMA foreign_keys = ON would otherwise reject the drop).
DROP TABLE ioc_observations;
ALTER TABLE ioc_observations_new RENAME TO ioc_observations;

CREATE INDEX IF NOT EXISTS idx_ioc_obs_ioc      ON ioc_observations(ioc_id, observed_at DESC) WHERE ioc_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_ioc_obs_finding  ON ioc_observations(finding_id) WHERE finding_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_ioc_obs_run      ON ioc_observations(orchestration_run_id) WHERE orchestration_run_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_ioc_obs_orphan   ON ioc_observations(orphaned_rule_label) WHERE orphaned_rule_label IS NOT NULL;

-- Consent flag: scheduler refuses to run any refresh while NULL.
ALTER TABLE cp_meta ADD COLUMN feeds_consent_granted_at TIMESTAMP;
