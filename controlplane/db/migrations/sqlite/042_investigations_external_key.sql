-- Phase 22.5 — auto-opened investigations need a dedup key.
--
-- Background daimons (cross-CP IOC pattern investigator first;
-- enrichment-driven escalators next) want to upsert "open an
-- investigation for THIS specific IOC pattern" without creating a
-- duplicate every tick. external_key is the operator-opaque dedup
-- handle: callers compute it (e.g. "cross-cp-pattern:<ioc_id>") and
-- the upsert endpoint either returns the existing row or creates a
-- new one.
--
-- Manual investigations (operator → +New) leave external_key NULL.
-- The unique index permits multiple NULLs (sqlite allows multiple
-- NULLs in UNIQUE indexes; postgres needs WHERE NOT NULL — handled
-- in the postgres mig).

ALTER TABLE investigations ADD COLUMN external_key TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS idx_investigations_external_key
  ON investigations(external_key);
