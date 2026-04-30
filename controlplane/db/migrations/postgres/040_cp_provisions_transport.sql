-- Phase 21.6 — managed CP deploys can target the s3-dead-drop
-- transport instead of HTTPS bootstrap. Two new columns on
-- cp_provisions:
--   • transport — 'https' (default, mTLS bootstrap) or 's3_dead_drop'
--   • transport_config_id — bucket the child publishes to + the
--     parent reads from (only meaningful when transport='s3_dead_drop')

ALTER TABLE cp_provisions ADD COLUMN transport TEXT NOT NULL DEFAULT 'https';
ALTER TABLE cp_provisions ADD COLUMN transport_config_id BIGINT NULL REFERENCES transport_configs(id) ON DELETE SET NULL;
