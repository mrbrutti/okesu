-- Phase 22.9 — generic labels.
--
-- Extends the K8s-style label/selector machinery from PR β (which
-- shipped only on nodes) to every operator-visible entity: nodes,
-- daimons (agents), findings, investigations, agent runs,
-- orchestrations, federation peers (CPs), secrets, and groups. One
-- table; per-kind discriminator on `target_kind`.
--
-- Targets carry either a numeric id (most entities) or a composite
-- key (daimons = name@host, federation peers = instance UUID, etc.).
-- target_id stays 0 when target_key is set; target_key stays '' when
-- target_id is set. The UNIQUE index covers both forms cleanly.
--
-- Backfills the existing node_labels rows so the visibility filter
-- and orchestration selector keep matching every node we already
-- knew about. node_labels is then dropped — store helpers in
-- node_labels.go re-target the new table at the same call sites.

CREATE TABLE IF NOT EXISTS labels (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  target_kind TEXT    NOT NULL,
  target_id   INTEGER NOT NULL DEFAULT 0,
  target_key  TEXT    NOT NULL DEFAULT '',
  key         TEXT    NOT NULL,
  value       TEXT    NOT NULL DEFAULT '',
  source      TEXT    NOT NULL DEFAULT 'manual', -- manual | oidc | auto | inherited
  created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  updated_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  UNIQUE (target_kind, target_id, target_key, key)
);

CREATE INDEX IF NOT EXISTS idx_labels_target_id  ON labels(target_kind, target_id)  WHERE target_id != 0;
CREATE INDEX IF NOT EXISTS idx_labels_target_key ON labels(target_kind, target_key) WHERE target_key != '';
CREATE INDEX IF NOT EXISTS idx_labels_kv         ON labels(key, value);

-- Backfill node labels into the unified table. INSERT OR IGNORE
-- handles re-runs where the row already exists (e.g. the heal-probe
-- fired and someone manually re-applied this migration).
INSERT OR IGNORE INTO labels (target_kind, target_id, target_key, key, value, source, created_at, updated_at)
SELECT 'node', node_id, '', key, value, 'manual', created_at, updated_at
  FROM node_labels;

-- node_labels is now a thin compatibility view. The Go helpers stop
-- writing to it on this deploy; reads still work for any out-of-tree
-- consumers that might still reach for the old name. A future
-- migration can drop the view entirely once nothing references it.
DROP TABLE node_labels;
CREATE VIEW node_labels AS
  SELECT target_id AS node_id, key, value, created_at, updated_at
    FROM labels
   WHERE target_kind = 'node' AND target_id != 0;
