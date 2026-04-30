-- Phase 21.5 — cost guardrails. See sqlite/032 for context.

ALTER TABLE cloud_credentials
  ADD COLUMN monthly_budget_usd DOUBLE PRECISION;

ALTER TABLE cp_provisions
  ADD COLUMN est_cost_per_hour_usd DOUBLE PRECISION;

ALTER TABLE cp_provisions
  ADD COLUMN instance_shape TEXT;
