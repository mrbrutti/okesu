-- Operator severity overrides. The LLM assigns severity at finding-creation
-- time and is sometimes wrong (over-classifying noise as CRITICAL is the
-- common failure). Operators can override on a per-row basis, and that
-- override is exposed back to agents through the lookup_findings tool so
-- the model learns from the corrections.
--
-- Two scopes:
--   1. Per-finding (operator_severity columns on findings) — one click
--      changes one row.
--   2. Per-fingerprint rule (finding_severity_rules table) — applies to
--      every NEW finding sharing the fingerprint going forward.
--
-- The original LLM-assigned severity stays in `findings.severity` (never
-- mutated). The JSON wire shape exposes `severity` as the EFFECTIVE
-- severity (operator override if present, else original) and adds an
-- `original_severity` field for transparency.

ALTER TABLE findings ADD COLUMN operator_severity TEXT;
ALTER TABLE findings ADD COLUMN severity_override_at TIMESTAMP;
ALTER TABLE findings ADD COLUMN severity_override_by INTEGER REFERENCES users(id);

CREATE TABLE IF NOT EXISTS finding_severity_rules (
  -- Fingerprint follows the same convention used by lookup_findings /
  -- known_issues: dedup_key when the agent emitted one, otherwise
  -- "title|severity|agent". Stored verbatim — comparisons are exact.
  fingerprint  TEXT PRIMARY KEY,
  severity     TEXT NOT NULL,
  note         TEXT,
  created_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  updated_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  created_by   INTEGER REFERENCES users(id)
);

-- We don't index the rules table — it's expected to stay small (operators
-- only add rules deliberately) and PK lookup is sufficient.
