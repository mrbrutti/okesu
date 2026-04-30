-- Phase A — federation transport: S3 dead-drop alongside HTTPS pull.
--
-- The parent CP can now federate from a child CP that has no inbound
-- HTTPS path (NAT'd, air-gapped, or — like the dev lab — running on
-- a private host while a child boots in a public cloud). Same
-- bucket-based dataflow that nodes use for their dead-drop transport.
--
-- Bucket layout, by convention:
--
--   cp/<child-cp-instance-id>/outbound/<parent-cp-instance-id>/
--     introspect.json       — last full snapshot, overwritten per tick
--     heartbeat.json        — { "ts": "..." } updated per tick
--     findings/<chunk>.ndjson  (Phase A.2)
--     daimons.ndjson           (Phase A.2)
--     nodes.ndjson             (Phase A.2)
--     orchestrations.ndjson    (Phase A.2)
--
-- Existing federation_peers rows keep transport='https_pull' (the
-- default). New S3 peers carry:
--
--   transport            = 's3_dead_drop'
--   bucket_prefix        = 'cp/<child-id>/outbound/<this-cp-id>/'
--   transport_config_id  → transport_configs.id (which bucket creds
--                          to use; reuses the same table nodes use)
--
-- Reader (s3reader.Loop) updates last_seen_at + introspect_json on
-- the row exactly like the HTTPS poller does, so HealthyPeers() and
-- the rest of the aggregator path work unchanged.
--
-- url stays NOT NULL UNIQUE because the existing aggregator + UI
-- assume non-empty url. For S3 peers we synthesize a logical URL
-- like `s3-deaddrop://cp/<child-id>` so the constraint is satisfied
-- without colliding with real https URLs.

ALTER TABLE federation_peers
  ADD COLUMN transport TEXT NOT NULL DEFAULT 'https_pull';

ALTER TABLE federation_peers
  ADD COLUMN bucket_prefix TEXT;

ALTER TABLE federation_peers
  ADD COLUMN transport_config_id INTEGER REFERENCES transport_configs(id) ON DELETE SET NULL;
