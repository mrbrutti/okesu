-- Phase 3 schema: structured findings projected from finding events.
--
-- The events table is the source of truth for fidelity (raw_json preserved).
-- This findings table is a denormalized index optimized for the dashboard:
-- severity-based filtering, agent/host scopes, dedup, acknowledgment workflow.

CREATE TABLE IF NOT EXISTS findings (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  event_id        INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  ts              INTEGER NOT NULL,                       -- Unix ms from event.ts
  agent           TEXT,
  host            TEXT,
  severity        TEXT,                                   -- CRITICAL|HIGH|MEDIUM|LOW|INFO
  title           TEXT,
  resource        TEXT,                                   -- pid:N, path:/..., ip:1.2.3.4
  evidence        TEXT,                                   -- raw telemetry lines
  dedup_key       TEXT,
  raw_json        TEXT NOT NULL,

  -- Operator workflow.
  acknowledged    INTEGER NOT NULL DEFAULT 0,
  acknowledged_at TIMESTAMP,
  acknowledged_by INTEGER REFERENCES users(id),
  ack_note        TEXT,

  created_at      TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_findings_ts            ON findings(ts DESC);
CREATE INDEX IF NOT EXISTS idx_findings_severity_ts   ON findings(severity, ts DESC);
CREATE INDEX IF NOT EXISTS idx_findings_agent_ts      ON findings(agent, ts DESC);
CREATE INDEX IF NOT EXISTS idx_findings_host_ts       ON findings(host, ts DESC);
CREATE INDEX IF NOT EXISTS idx_findings_acknowledged  ON findings(acknowledged, ts DESC);
CREATE INDEX IF NOT EXISTS idx_findings_dedup         ON findings(dedup_key) WHERE dedup_key IS NOT NULL;
