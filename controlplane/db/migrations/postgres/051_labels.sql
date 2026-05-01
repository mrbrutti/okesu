-- Postgres mirror of 051_labels.sql.
--
-- Same shape, idiomatic for postgres: SERIAL primary key, partial
-- UNIQUE indexes on (target_kind, target_id) / (target_kind,
-- target_key), and a (key, value) index for selector lookups.

CREATE TABLE IF NOT EXISTS labels (
  id          SERIAL PRIMARY KEY,
  target_kind TEXT    NOT NULL,
  target_id   BIGINT  NOT NULL DEFAULT 0,
  target_key  TEXT    NOT NULL DEFAULT '',
  key         TEXT    NOT NULL,
  value       TEXT    NOT NULL DEFAULT '',
  source      TEXT    NOT NULL DEFAULT 'manual',
  created_at  TIMESTAMPTZ DEFAULT NOW(),
  updated_at  TIMESTAMPTZ DEFAULT NOW(),
  UNIQUE (target_kind, target_id, target_key, key)
);

CREATE INDEX IF NOT EXISTS idx_labels_target_id  ON labels(target_kind, target_id)  WHERE target_id != 0;
CREATE INDEX IF NOT EXISTS idx_labels_target_key ON labels(target_kind, target_key) WHERE target_key != '';
CREATE INDEX IF NOT EXISTS idx_labels_kv         ON labels(key, value);

INSERT INTO labels (target_kind, target_id, target_key, key, value, source, created_at, updated_at)
SELECT 'node', node_id, '', key, value, 'manual', created_at, updated_at
  FROM node_labels
ON CONFLICT (target_kind, target_id, target_key, key) DO NOTHING;

DROP TABLE node_labels;
CREATE VIEW node_labels AS
  SELECT target_id AS node_id, key, value, created_at, updated_at
    FROM labels
   WHERE target_kind = 'node' AND target_id != 0;
