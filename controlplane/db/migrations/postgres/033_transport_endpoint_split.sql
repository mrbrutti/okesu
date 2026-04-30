-- Phase 9.x — split transport_configs.endpoint. See sqlite/033 for context.

ALTER TABLE transport_configs
  ADD COLUMN endpoint_internal TEXT;
