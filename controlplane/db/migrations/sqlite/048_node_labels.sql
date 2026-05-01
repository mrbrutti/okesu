-- Phase 22.8 — node labels (PR β of the RBAC overhaul).
--
-- Kubernetes-style key/value labels on nodes. One value per key
-- per node — `(node_id, key)` is the primary key, so reassigning
-- env=staging on a node already labelled env=prod is a clean upsert.
--
-- Used by:
--   - PR β's selector parser (matches against label maps)
--   - PR α's group_roles.selector — once this lands, the
--     scoped-role gate evaluates selectors against a node's labels
--     to answer "can this user act on this node?"
--   - PR γ (next) — credential bindings to the same selectors so
--     deploys/agents pick the right SSH key / API key per scope.
--
-- (key, value) is indexed for the inverse query "which nodes match
-- env=prod?" — exact matches on a small column avoid full scans
-- when the fleet grows.

CREATE TABLE IF NOT EXISTS node_labels (
  node_id    INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  key        TEXT    NOT NULL,
  value      TEXT    NOT NULL DEFAULT '',
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (node_id, key)
);

CREATE INDEX IF NOT EXISTS idx_node_labels_kv
  ON node_labels(key, value);
