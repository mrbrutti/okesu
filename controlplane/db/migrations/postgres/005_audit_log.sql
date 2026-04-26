-- Phase 7 schema: audit log of admin actions.
--
-- Captures who did what, when, and to what. Actor identity (email + role) is
-- copied at the time of the event so the audit row stays accurate even if the
-- user is later renamed or deleted.

CREATE TABLE IF NOT EXISTS audit_log (
  id           BIGSERIAL PRIMARY KEY,
  ts           TIMESTAMP DEFAULT CURRENT_TIMESTAMP,

  -- Actor — identity at the time of the action. NULL when triggered by a
  -- non-user actor (e.g. management plane, external API token).
  actor_id    BIGINT REFERENCES users(id) ON DELETE SET NULL,
  actor_email TEXT,
  actor_role  TEXT,
  actor_ip    TEXT,

  -- Action — short snake_case verb identifying what happened.
  -- Examples: "user.create", "user.role_change", "user.delete",
  --           "auth.login", "auth.logout", "auth.password_change",
  --           "session.revoke_all",
  --           "finding.acknowledge", "finding.unacknowledge",
  --           "agent.config_update",
  --           "node.create", "node.delete", "node.deploy",
  --           "run.start", "run.cancel",
  --           "system.vacuum"
  action      TEXT NOT NULL,

  -- Target — string identifier of the affected object.
  -- Examples: "user:42", "agent:edr", "node:7", "finding:12", "run:abc123"
  target      TEXT,

  -- Result — "ok" | "denied" | "error". Denials log the reason.
  result      TEXT NOT NULL DEFAULT 'ok',

  -- Optional metadata as a JSON object. Free-form per action — e.g. for
  -- user.role_change: {"from": "viewer", "to": "operator"}
  metadata    TEXT
);

CREATE INDEX IF NOT EXISTS idx_audit_ts          ON audit_log(ts DESC);
CREATE INDEX IF NOT EXISTS idx_audit_actor_ts    ON audit_log(actor_id, ts DESC);
CREATE INDEX IF NOT EXISTS idx_audit_action_ts   ON audit_log(action, ts DESC);
