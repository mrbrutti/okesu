-- Phase 19.1 — orchestration step `data:` block snapshot.
--
-- Replaces the curl-and-login pattern in cron orchestrations with a
-- declarative `data:` block resolved server-side. The engine fetches
-- the data and binds it into the prompt as {{data.<name>}}; this
-- column captures the JSON payload the agent actually saw, so a run
-- can be replayed against the same input even after the underlying
-- tables have moved on.
--
-- Empty / NULL when the step had no data: block — the column carries
-- no semantic load for the existing finding/host-scoped flows.

ALTER TABLE orchestration_steps ADD COLUMN data_snapshot TEXT;
