-- Per-node "do not auto-update" pin. When set, the CP's mgmt-plane
-- /config endpoint omits the canonical definition_hash for daimons on
-- this node, so the daemon's hot-reload poll never fires and the node
-- stays on whatever it has loaded. Useful for compliance ("freeze
-- this host until I explicitly redeploy") and for manual rollouts.
--
-- Default 0 (auto-update enabled) preserves existing behaviour.

ALTER TABLE nodes ADD COLUMN auto_update_paused INTEGER NOT NULL DEFAULT 0;
