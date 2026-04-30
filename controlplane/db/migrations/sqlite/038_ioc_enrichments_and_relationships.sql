-- Phase 22.4: IOC enrichment cache + typed relationship graph.
--
-- ioc_enrichments:    Per-adapter cached vendor response for an IOC.
--                     Keyed (ioc_id, adapter). expires_at gates cache
--                     refresh; raw_json carries the full vendor body
--                     for forensic re-inspection. The indexed fields
--                     (verdict, score) are a coarse summary for fast
--                     UI/heatmap rendering.
--
-- ioc_relationships:  Typed directed edges over IOCs. Predicates from
--                     a fixed v1 vocabulary (resolves-to, exploits,
--                     hosted-at, belongs-to, signed-with, dropped-by).
--                     Source = "enrichment" | "agent". Confidence is
--                     low|medium|high.
--
-- See docs/superpowers/specs/2026-04-29-threatcaddy-borrows-phasing-design.md
-- §"Phase 22.4 — Cross-fleet pattern surfacing".

CREATE TABLE IF NOT EXISTS ioc_enrichments (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  ioc_id      INTEGER NOT NULL REFERENCES iocs(id) ON DELETE CASCADE,
  adapter     TEXT NOT NULL,
  verdict     TEXT,
  score       INTEGER,
  raw_json    TEXT NOT NULL,
  fetched_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  expires_at  TIMESTAMP NOT NULL,
  UNIQUE (ioc_id, adapter)
);

CREATE INDEX IF NOT EXISTS idx_ioc_enrichments_expires ON ioc_enrichments(expires_at);
CREATE INDEX IF NOT EXISTS idx_ioc_enrichments_adapter ON ioc_enrichments(adapter);

CREATE TABLE IF NOT EXISTS ioc_relationships (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  subject_id  INTEGER NOT NULL REFERENCES iocs(id) ON DELETE CASCADE,
  predicate   TEXT NOT NULL,
  object_id   INTEGER NOT NULL REFERENCES iocs(id) ON DELETE CASCADE,
  source      TEXT NOT NULL DEFAULT 'agent',
  confidence  TEXT,
  created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  UNIQUE (subject_id, predicate, object_id)
);

CREATE INDEX IF NOT EXISTS idx_ioc_relationships_subject  ON ioc_relationships(subject_id);
CREATE INDEX IF NOT EXISTS idx_ioc_relationships_object   ON ioc_relationships(object_id);
CREATE INDEX IF NOT EXISTS idx_ioc_relationships_predicate ON ioc_relationships(predicate);
