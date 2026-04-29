-- Phase 23: orchestrations — chained agent runs.
--
-- An orchestration is a YAML spec stored on the CP. A run executes
-- the steps in order; each step dispatches a Run (existing table)
-- and links back to it. Step state is denormalised here for query
-- speed — the actual transcript / log lives in the Run.
--
-- Federation: orchestrations live on the CP that authored them.
-- Steps may dispatch to remote CPs via cp_instance_id; the parent
-- aggregates step state across CPs into one timeline.

CREATE TABLE IF NOT EXISTS orchestrations (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  name            TEXT NOT NULL UNIQUE,
  description     TEXT,
  spec_yaml       TEXT NOT NULL,
  -- Trigger metadata extracted from the spec for cheap filter lookup.
  -- The full filter expression stays in spec_yaml; these columns let
  -- the eventpipeline finding-trigger probe candidate orchestrations
  -- without parsing every spec on every finding.
  trigger_kind    TEXT NOT NULL DEFAULT 'manual',  -- manual | finding | cron
  trigger_filter  TEXT,
  trigger_cron    TEXT,
  enabled         INTEGER NOT NULL DEFAULT 1,
  created_at      TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  created_by      INTEGER REFERENCES users(id),
  updated_at      TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_orchestrations_name           ON orchestrations(name);
CREATE INDEX IF NOT EXISTS idx_orchestrations_trigger_kind   ON orchestrations(trigger_kind, enabled);

CREATE TABLE IF NOT EXISTS orchestration_runs (
  id                  INTEGER PRIMARY KEY AUTOINCREMENT,
  orchestration_id    INTEGER NOT NULL REFERENCES orchestrations(id) ON DELETE CASCADE,
  -- Status:
  --   pending           — created, not yet started (queued)
  --   running           — at least one step is in-flight
  --   approval_required — paused waiting on operator approval
  --   completed         — every step ended in completed or skipped
  --   failed            — at least one step failed without continue_on_error
  --   cancelled         — operator cancelled
  status              TEXT NOT NULL DEFAULT 'pending',
  trigger_kind        TEXT NOT NULL,
  trigger_payload     TEXT,                 -- JSON: input fields + trigger context (finding id, cron tick, etc.)
  current_step_id     TEXT,                 -- which step the engine is on (or last finished)
  started_at          TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  ended_at            TIMESTAMP,
  started_by          INTEGER REFERENCES users(id),
  error               TEXT
);

CREATE INDEX IF NOT EXISTS idx_orchestration_runs_orchestration ON orchestration_runs(orchestration_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_orchestration_runs_status        ON orchestration_runs(status, started_at DESC);

CREATE TABLE IF NOT EXISTS orchestration_steps (
  id                       INTEGER PRIMARY KEY AUTOINCREMENT,
  orchestration_run_id     INTEGER NOT NULL REFERENCES orchestration_runs(id) ON DELETE CASCADE,
  step_id                  TEXT NOT NULL,    -- spec-defined id (e.g. "triage")
  step_idx                 INTEGER NOT NULL, -- position in the steps list (0-based)
  -- Step status mirrors run status semantically but with extras:
  --   pending          — not yet scheduled
  --   waiting_approval — gated; awaiting operator click
  --   running          — Run dispatched, in-flight
  --   completed        — Run finished cleanly
  --   failed           — Run failed (and engine halted)
  --   skipped          — `when` evaluated false, or upstream step failed without continue_on_error
  status                   TEXT NOT NULL DEFAULT 'pending',
  run_id                   INTEGER,           -- FK runs(id) once dispatched (nullable for skipped/local steps)
  cp_instance_id           TEXT,              -- where the step ran ('local' or a federation child id)
  node_id                  INTEGER,           -- specific node, when dispatched to a tunnel
  rendered_prompt          TEXT,              -- what the engine actually sent (post-template)
  result_json              TEXT,              -- distilled result — the orchestration_result finding's attributes
  output_summary           TEXT,              -- last 4KB of stdout for audit / quick glance
  started_at               TIMESTAMP,
  ended_at                 TIMESTAMP,
  error                    TEXT,
  approved_at              TIMESTAMP,
  approved_by              INTEGER REFERENCES users(id),
  UNIQUE (orchestration_run_id, step_id)
);

CREATE INDEX IF NOT EXISTS idx_orchestration_steps_run ON orchestration_steps(orchestration_run_id, step_idx);
CREATE INDEX IF NOT EXISTS idx_orchestration_steps_run_id ON orchestration_steps(run_id) WHERE run_id IS NOT NULL;

-- Standalone-Run approval gating: same primitive the orchestrator
-- uses for `approval: required` steps, exposed on regular Runs so
-- operators can require approval before destructive single-agent
-- actions (containment, deploy rollback).
ALTER TABLE runs ADD COLUMN approval_required INTEGER NOT NULL DEFAULT 0;
ALTER TABLE runs ADD COLUMN approved_at       TIMESTAMP;
ALTER TABLE runs ADD COLUMN approved_by       INTEGER REFERENCES users(id);
