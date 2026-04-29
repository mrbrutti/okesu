-- Phase 21.5 — cost guardrails on managed CP deploys.
--
-- Adds two layers of cost awareness to the managed-deploy flow:
--
--   1. cloud_credentials.monthly_budget_usd — per-credential cap.
--      Operators set it in Settings → Cloud. NULL = no cap (legacy
--      rows + ones the operator hasn't touched). Currency is fixed at
--      USD because every cloud SDK we use prices in USD natively;
--      operators in other markets can convert offline.
--
--   2. cp_provisions.est_cost_per_hour_usd + instance_shape — the
--      cost catalog's best-guess hourly rate at submit time, frozen
--      on the row so the totals don't shift retroactively when the
--      catalog changes. NULL when the catalog has no entry for that
--      (cloud, shape) — the row exists but contributes 0 to the
--      running monthly total. The accompanying instance_shape column
--      is the catalog key we looked up; preserved as-is so an
--      operator looking at a months-old row can grep for it.
--
-- The handler in api/cp_provision.go uses these to enforce the cap +
-- surface estimates in the +Add CP modal. Catalog values are pure-Go
-- in cpprovision/costcatalog.go — they never live in the DB so they
-- can ship with binary updates rather than requiring a migration.

ALTER TABLE cloud_credentials
  ADD COLUMN monthly_budget_usd REAL;

ALTER TABLE cp_provisions
  ADD COLUMN est_cost_per_hour_usd REAL;

ALTER TABLE cp_provisions
  ADD COLUMN instance_shape TEXT;
