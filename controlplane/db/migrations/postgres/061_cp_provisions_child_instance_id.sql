-- 061_cp_provisions_child_instance_id.sql
-- Records the child instance UUID minted at bundle-generation time
-- so the destroy path can compute the bootstrap-blob bucket key
-- (cp/<child-id>/bootstrap/bundle.tar.gz) without parsing the
-- federation_peers.bucket_prefix.
ALTER TABLE cp_provisions ADD COLUMN child_instance_id TEXT;
