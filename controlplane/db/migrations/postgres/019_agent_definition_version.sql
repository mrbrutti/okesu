-- Operator-readable version label for the daimon definition currently
-- loaded by each agent. See sqlite/019_agent_definition_version.sql for
-- the full rationale.

ALTER TABLE agents ADD COLUMN IF NOT EXISTS definition_version TEXT;
