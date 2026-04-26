-- Phase 9 schema: outbound notifications.
--
-- Channels are typed delivery destinations (Slack, email, generic webhook).
-- Rules match finding events against per-channel filters and enqueue
-- deliveries. Deliveries record every attempt so operators can debug
-- routing without losing the event itself (the source finding stays in
-- the findings table).

CREATE TABLE IF NOT EXISTS notification_channels (
  id              BIGSERIAL PRIMARY KEY,
  name            TEXT    NOT NULL UNIQUE,
  type            TEXT    NOT NULL,                              -- "slack" | "email" | "webhook"
  -- type-specific settings, JSON-encoded:
  --   slack:   {"webhook_url": "...", "channel_override": "#alerts"}
  --   email:   {"smtp_host":"smtp.example.com","smtp_port":587,"username":"u","password":"p","from":"alerts@","to":["ops@"],"tls":true}
  --   webhook: {"url":"https://...","secret":"hmac-key","extra_headers":{"X-Foo":"bar"}}
  config          TEXT    NOT NULL DEFAULT '{}',
  enabled         BIGINT NOT NULL DEFAULT 1,
  created_at      TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  created_by_email TEXT,
  updated_at      TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS notification_rules (
  id              BIGSERIAL PRIMARY KEY,
  name            TEXT    NOT NULL,
  channel_id      BIGINT NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
  -- Severity threshold — rule fires when finding severity >= this level.
  -- One of: "INFO" | "LOW" | "MEDIUM" | "HIGH" | "CRITICAL". Empty matches all.
  min_severity    TEXT    NOT NULL DEFAULT 'INFO',
  -- Optional substring filters. Empty = match everything for that field.
  agent_substring TEXT,
  host_substring  TEXT,
  enabled         BIGINT NOT NULL DEFAULT 1,
  created_at      TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_notification_rules_channel ON notification_rules(channel_id);

CREATE TABLE IF NOT EXISTS notification_deliveries (
  id              BIGSERIAL PRIMARY KEY,
  rule_id         BIGINT REFERENCES notification_rules(id) ON DELETE SET NULL,
  channel_id      BIGINT REFERENCES notification_channels(id) ON DELETE SET NULL,
  finding_id      BIGINT REFERENCES findings(id) ON DELETE SET NULL,
  -- Snapshot of finding bits — lets the deliveries log stay readable even
  -- after the source finding gets pruned.
  severity        TEXT,
  title           TEXT,
  status          TEXT NOT NULL DEFAULT 'pending',                -- pending | succeeded | failed
  attempt         BIGINT NOT NULL DEFAULT 0,
  error           TEXT,
  created_at      TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  finished_at     TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_deliveries_status_created ON notification_deliveries(status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_deliveries_channel_created ON notification_deliveries(channel_id, created_at DESC);
