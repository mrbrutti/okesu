-- Phase 23.x: per-host fan-out dispatch state.
--
-- A fan-out step (StepSpec.Nodes non-empty) executes the same agent on
-- N hosts in parallel. Pre-23, the orchestration_steps row recorded
-- only the aggregate outcome — there was no place to surface per-host
-- progress on the run-detail canvas, no way to attribute a slow host,
-- and no queryable history of per-host fan-out failures.
--
-- This table holds one row per (run, step, host). The engine's fan-out
-- aggregator writes through a progress sink: Insert on dispatch start,
-- Update on return. The aggregated byNode map continues to land in
-- orchestration_steps.result_json at completion for template
-- back-compat ({{stepN.byNode["host"]}}).
--
-- See docs/superpowers/specs/2026-04-29-orchestration-fanout-visualization-design.md.

CREATE TABLE IF NOT EXISTS orchestration_step_node_dispatches (
  run_id          INTEGER NOT NULL REFERENCES orchestration_runs(id) ON DELETE CASCADE,
  step_id         TEXT    NOT NULL,
  host            TEXT    NOT NULL,
  status          TEXT    NOT NULL,         -- pending | running | completed | failed
  agent_run_id    TEXT,
  findings_count  INTEGER NOT NULL DEFAULT 0,
  output_tail     TEXT,
  error           TEXT,
  started_at      TIMESTAMP,
  ended_at        TIMESTAMP,
  PRIMARY KEY (run_id, step_id, host)
);

CREATE INDEX IF NOT EXISTS idx_osnd_run_step
  ON orchestration_step_node_dispatches(run_id, step_id);
