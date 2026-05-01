-- Phase 22.8 — see sqlite/047 for context.
--
-- Postgres variant. Two notable differences:
--   - UNIQUE on external_id needs WHERE NOT NULL because postgres
--     treats NULL=NULL as not-distinct in UNIQUE indexes (sqlite is
--     more lenient).
--   - Backfill JOIN/INSERT shape is identical; postgres handles the
--     ON CONFLICT clauses the same way.

CREATE TABLE IF NOT EXISTS groups (
  id          BIGSERIAL PRIMARY KEY,
  name        TEXT    NOT NULL UNIQUE,
  description TEXT    NOT NULL DEFAULT '',
  external_id TEXT,
  created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  updated_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_groups_external_id
  ON groups(external_id)
  WHERE external_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS user_groups (
  user_id     INTEGER NOT NULL REFERENCES users(id)  ON DELETE CASCADE,
  group_id    INTEGER NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
  source      TEXT    NOT NULL DEFAULT 'manual',
  added_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (user_id, group_id)
);

CREATE INDEX IF NOT EXISTS idx_user_groups_group ON user_groups(group_id);

CREATE TABLE IF NOT EXISTS group_roles (
  id          BIGSERIAL PRIMARY KEY,
  group_id    INTEGER NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
  role        TEXT    NOT NULL,
  selector    TEXT,
  created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  UNIQUE (group_id, role, selector)
);

CREATE INDEX IF NOT EXISTS idx_group_roles_group ON group_roles(group_id);

INSERT INTO groups (name, description, external_id)
SELECT 'default-' || role,
       'Auto-created default group for users with role ' || role,
       NULL
  FROM users
 GROUP BY role
 ON CONFLICT (name) DO NOTHING;

INSERT INTO user_groups (user_id, group_id, source)
SELECT u.id, g.id, 'auto'
  FROM users u
  JOIN groups g ON g.name = 'default-' || u.role
 ON CONFLICT (user_id, group_id) DO NOTHING;

INSERT INTO group_roles (group_id, role, selector)
SELECT id, REPLACE(name, 'default-', ''), NULL
  FROM groups
 WHERE name LIKE 'default-%';
