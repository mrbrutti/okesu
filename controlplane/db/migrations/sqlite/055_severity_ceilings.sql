-- Phase 22.10 PR γ — per-label severity ceiling.
--
-- Operators want lab/staging noise to stop bubbling into the
-- dashboards as if it were prod. A label selector + a max severity
-- expresses "any finding on a node matching this selector is capped
-- at MAX." Most natural use: env=staging → MEDIUM, env=dev → LOW.
--
-- Evaluated at finding-ingest time alongside the per-fingerprint
-- severity rule (Phase 14): if either fires, the finding's
-- operator_severity gets set to the cap. Ceilings only LOWER —
-- a finding the agent assigned LOW won't be promoted to MEDIUM
-- because some ceiling allowed MEDIUM.

CREATE TABLE IF NOT EXISTS severity_ceilings (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  selector     TEXT    NOT NULL,
  max_severity TEXT    NOT NULL,
  reason       TEXT    NOT NULL DEFAULT '',
  created_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  updated_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  UNIQUE (selector)
);
