-- Phase 22.4 postgres parity. See migrations/sqlite/037_ioc_enrichments_and_relationships.sql.

CREATE TABLE IF NOT EXISTS ioc_enrichments (
  id          BIGSERIAL PRIMARY KEY,
  ioc_id      BIGINT NOT NULL REFERENCES iocs(id) ON DELETE CASCADE,
  adapter     TEXT NOT NULL,
  verdict     TEXT,
  score       INTEGER,
  raw_json    TEXT NOT NULL,
  fetched_at  TIMESTAMPTZ DEFAULT NOW(),
  expires_at  TIMESTAMPTZ NOT NULL,
  UNIQUE (ioc_id, adapter)
);

CREATE INDEX IF NOT EXISTS idx_ioc_enrichments_expires ON ioc_enrichments(expires_at);
CREATE INDEX IF NOT EXISTS idx_ioc_enrichments_adapter ON ioc_enrichments(adapter);

CREATE TABLE IF NOT EXISTS ioc_relationships (
  id          BIGSERIAL PRIMARY KEY,
  subject_id  BIGINT NOT NULL REFERENCES iocs(id) ON DELETE CASCADE,
  predicate   TEXT NOT NULL,
  object_id   BIGINT NOT NULL REFERENCES iocs(id) ON DELETE CASCADE,
  source      TEXT NOT NULL DEFAULT 'agent',
  confidence  TEXT,
  created_at  TIMESTAMPTZ DEFAULT NOW(),
  UNIQUE (subject_id, predicate, object_id)
);

CREATE INDEX IF NOT EXISTS idx_ioc_relationships_subject  ON ioc_relationships(subject_id);
CREATE INDEX IF NOT EXISTS idx_ioc_relationships_object   ON ioc_relationships(object_id);
CREATE INDEX IF NOT EXISTS idx_ioc_relationships_predicate ON ioc_relationships(predicate);
