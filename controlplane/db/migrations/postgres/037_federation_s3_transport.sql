-- Phase A — federation S3 transport. See sqlite/035 for context.

ALTER TABLE federation_peers
  ADD COLUMN transport TEXT NOT NULL DEFAULT 'https_pull';

ALTER TABLE federation_peers
  ADD COLUMN bucket_prefix TEXT;

ALTER TABLE federation_peers
  ADD COLUMN transport_config_id BIGINT REFERENCES transport_configs(id) ON DELETE SET NULL;
