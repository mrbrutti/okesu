-- Phase 23: orchestrations — chained agent runs (Postgres mirror).

CREATE TABLE IF NOT EXISTS orchestrations (
  id              BIGSERIAL PRIMARY KEY,
  name            TEXT NOT NULL UNIQUE,
  description     TEXT,
  spec_yaml       TEXT NOT NULL,
  trigger_kind    TEXT NOT NULL DEFAULT 'manual',
  trigger_filter  TEXT,
  trigger_cron    TEXT,
  enabled         BOOLEAN NOT NULL DEFAULT TRUE,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  created_by      BIGINT REFERENCES users(id),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_orchestrations_name         ON orchestrations(name);
CREATE INDEX IF NOT EXISTS idx_orchestrations_trigger_kind ON orchestrations(trigger_kind, enabled);

CREATE TABLE IF NOT EXISTS orchestration_runs (
  id                  BIGSERIAL PRIMARY KEY,
  orchestration_id    BIGINT NOT NULL REFERENCES orchestrations(id) ON DELETE CASCADE,
  status              TEXT NOT NULL DEFAULT 'pending',
  trigger_kind        TEXT NOT NULL,
  trigger_payload     JSONB,
  current_step_id     TEXT,
  started_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  ended_at            TIMESTAMPTZ,
  started_by          BIGINT REFERENCES users(id),
  error               TEXT
);

CREATE INDEX IF NOT EXISTS idx_orchestration_runs_orchestration ON orchestration_runs(orchestration_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_orchestration_runs_status        ON orchestration_runs(status, started_at DESC);

CREATE TABLE IF NOT EXISTS orchestration_steps (
  id                       BIGSERIAL PRIMARY KEY,
  orchestration_run_id     BIGINT NOT NULL REFERENCES orchestration_runs(id) ON DELETE CASCADE,
  step_id                  TEXT NOT NULL,
  step_idx                 INT NOT NULL,
  status                   TEXT NOT NULL DEFAULT 'pending',
  run_id                   BIGINT,
  cp_instance_id           TEXT,
  node_id                  BIGINT,
  rendered_prompt          TEXT,
  result_json              JSONB,
  output_summary           TEXT,
  started_at               TIMESTAMPTZ,
  ended_at                 TIMESTAMPTZ,
  error                    TEXT,
  approved_at              TIMESTAMPTZ,
  approved_by              BIGINT REFERENCES users(id),
  UNIQUE (orchestration_run_id, step_id)
);

CREATE INDEX IF NOT EXISTS idx_orchestration_steps_run    ON orchestration_steps(orchestration_run_id, step_idx);
CREATE INDEX IF NOT EXISTS idx_orchestration_steps_run_id ON orchestration_steps(run_id) WHERE run_id IS NOT NULL;

ALTER TABLE runs ADD COLUMN IF NOT EXISTS approval_required BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE runs ADD COLUMN IF NOT EXISTS approved_at       TIMESTAMPTZ;
ALTER TABLE runs ADD COLUMN IF NOT EXISTS approved_by       BIGINT REFERENCES users(id);
