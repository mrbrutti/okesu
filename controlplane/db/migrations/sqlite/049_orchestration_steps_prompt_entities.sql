-- 047_orchestration_steps_prompt_entities.sql
-- Adds a typed side-channel listing entity refs that were templated
-- into a step's rendered_prompt. Populated by the orchestrator at
-- render time. Read by the UI (SmartPayload) to render entity chips
-- inline at the JSON-substitution sites.
--
-- Nullable: legacy steps (no capture path on insert) have NULL and
-- the UI falls back to client-side shape sniffing.
ALTER TABLE orchestration_steps
  ADD COLUMN prompt_entities TEXT;
