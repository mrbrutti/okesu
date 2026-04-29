-- Phase 19.1 — orchestration step `data:` block snapshot.
-- See sqlite/028 for context.

ALTER TABLE orchestration_steps ADD COLUMN data_snapshot TEXT;
