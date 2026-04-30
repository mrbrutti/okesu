-- Phase 22.2 postgres parity. See migrations/sqlite/035_finding_clusters_and_lessons.sql.

ALTER TABLE findings ADD COLUMN IF NOT EXISTS cluster_id TEXT;
ALTER TABLE findings ADD COLUMN IF NOT EXISTS ioc_confidence TEXT;
ALTER TABLE findings ADD COLUMN IF NOT EXISTS ioc_attribution TEXT;
ALTER TABLE findings ADD COLUMN IF NOT EXISTS ioc_classification TEXT;

CREATE INDEX IF NOT EXISTS idx_findings_cluster ON findings(cluster_id) WHERE cluster_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS agent_lessons (
  id                    BIGSERIAL PRIMARY KEY,
  agent_name            TEXT NOT NULL,
  lesson_text           TEXT NOT NULL,
  orchestration_run_id  BIGINT REFERENCES orchestration_runs(id) ON DELETE SET NULL,
  orchestration_step_id TEXT,
  created_at            TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_agent_lessons_name_created
  ON agent_lessons(agent_name, created_at DESC);
