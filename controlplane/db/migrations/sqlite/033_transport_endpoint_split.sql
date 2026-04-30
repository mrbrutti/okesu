-- Phase 9.x — split transport_configs.endpoint into "what the scanner
-- sees" vs "what nodes embed in their bootstrap.json".
--
-- Production case: the CP scanner reaches the bucket over a private
-- VPC interface (e.g. an OCI Object Storage private endpoint, or an
-- S3 gateway endpoint inside the VPC), while remote nodes — out on
-- the open internet, behind home routers, in foreign clouds — must
-- use the public DNS name. Same bucket, two different hostnames.
--
-- Pre-Phase-9.x both sides used a single `endpoint` column, which
-- forced a choice: pick the public name and pay egress on every
-- scanner round-trip, or pick the private name and break enrollment
-- for any node that can't resolve it. Neither is right; this column
-- lets operators set both.
--
-- Behavior (enforced in the scanner + packaging code, NOT here):
--   - endpoint           = external (public) endpoint. Always
--                          embedded in package bootstrap.json so
--                          nodes can resolve it.
--   - endpoint_internal  = private endpoint the CP scanner uses.
--                          NULL (default) means "fall back to
--                          endpoint" — preserves existing rows.
--
-- The column is plain TEXT NULL so the existing INSERTs that don't
-- set it keep working; reads coerce NULL → endpoint at the
-- application layer.

ALTER TABLE transport_configs
  ADD COLUMN endpoint_internal TEXT;
