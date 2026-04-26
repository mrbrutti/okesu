-- Phase 1 schema: users, sessions, events.

CREATE TABLE IF NOT EXISTS users (
  id            BIGSERIAL PRIMARY KEY,
  email         TEXT    NOT NULL UNIQUE,
  password_hash TEXT,                                       -- nullable for SSO-only users (Phase 4)
  role          TEXT    NOT NULL DEFAULT 'admin',           -- admin | operator | viewer
  created_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS sessions (
  id          TEXT PRIMARY KEY,                              -- 32 bytes hex
  user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at  TIMESTAMP NOT NULL,
  created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at);

CREATE TABLE IF NOT EXISTS events (
  id          BIGSERIAL PRIMARY KEY,
  ts          BIGINT NOT NULL,                              -- Unix ms from event.ts
  type        TEXT    NOT NULL,                              -- finding|action_taken|tick_done|...
  agent       TEXT,
  host        TEXT,
  severity    TEXT,
  title       TEXT,
  raw_json    TEXT    NOT NULL,                              -- full event JSON for fidelity
  received_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_events_ts ON events(ts DESC);
CREATE INDEX IF NOT EXISTS idx_events_agent_ts ON events(agent, ts DESC);
CREATE INDEX IF NOT EXISTS idx_events_severity ON events(severity) WHERE severity IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_events_type ON events(type);

-- meta is a single-row key/value table for things like the session HMAC key
-- that need to persist but aren't worth their own table.
CREATE TABLE IF NOT EXISTS meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
