-- Phase 22.2: cluster_id + IOC metadata propagation on findings, plus
-- the agent_lessons KV.
--
-- cluster_id:        first finding to surface a given IOC mints the
--                    cluster (cluster_id = that finding's id, as TEXT);
--                    subsequent findings within the IOC's window get
--                    the same cluster_id. Operators see "cluster of N"
--                    in the UI rather than N disconnected findings.
--
-- ioc_confidence:    one of low | medium | high; copied from the matched
-- ioc_attribution:   IOC's metadata at ingest. Only catalog-source IOCs
-- ioc_classification: carry these fields, so this is also "did any
--                    catalog entry match?".
--
-- agent_lessons:     per-agent persistent notes that the daemon prepends
--                    to the system prompt on its next tick. Bounded to
--                    10 newest entries per agent_name (older are pruned
--                    on insert by RecordAgentLesson).

ALTER TABLE findings ADD COLUMN cluster_id TEXT;
ALTER TABLE findings ADD COLUMN ioc_confidence TEXT;
ALTER TABLE findings ADD COLUMN ioc_attribution TEXT;
ALTER TABLE findings ADD COLUMN ioc_classification TEXT;

CREATE INDEX IF NOT EXISTS idx_findings_cluster ON findings(cluster_id) WHERE cluster_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS agent_lessons (
  id                    INTEGER PRIMARY KEY AUTOINCREMENT,
  agent_name            TEXT NOT NULL,
  lesson_text           TEXT NOT NULL,
  orchestration_run_id  INTEGER,
  orchestration_step_id TEXT,
  created_at            TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_agent_lessons_name_created
  ON agent_lessons(agent_name, created_at DESC);
