-- Phase 22.8 — see sqlite/048 for context.

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
