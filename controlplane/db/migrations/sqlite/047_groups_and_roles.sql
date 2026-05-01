-- Phase 22.8 — groups + scoped roles (PR α of the RBAC overhaul).
--
-- Replaces the single `users.role` enum with multi-membership groups
-- that each carry zero-or-more scoped roles. Existing CPs keep
-- working: every user is auto-attached to a "default-<role>" group
-- on migration so RequireRole(...) checks don't break overnight.
--
-- The selector column on group_roles is reserved for PR β (node
-- labels). NULL means "CP-wide", which is the only thing PR α
-- evaluates — selectors land as a no-op for now and the parser
-- ships in PR β.
--
-- OIDC inheritance: the `external_id` column on groups marks rows
-- whose membership is mirrored from the IdP claims on each login.
-- Manual groups have external_id NULL. The OIDC sync routine ensures
-- a local group exists per claimed group (matched by external_id),
-- syncs user_groups with source='oidc', and leaves manual rows
-- alone.

CREATE TABLE IF NOT EXISTS groups (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  name        TEXT    NOT NULL UNIQUE,
  description TEXT    NOT NULL DEFAULT '',
  -- IdP-side identifier (e.g. group DN, claim value). NULL = local-only.
  external_id TEXT,
  created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  updated_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  -- Composite uniqueness so the same external_id can't bind to two
  -- local groups; multiple NULL external_ids are allowed (sqlite
  -- treats NULLs as distinct in UNIQUE indexes by default).
  UNIQUE (external_id)
);

CREATE TABLE IF NOT EXISTS user_groups (
  user_id     INTEGER NOT NULL REFERENCES users(id)  ON DELETE CASCADE,
  group_id    INTEGER NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
  -- 'manual' = admin attached via UI; 'oidc' = mirrored from claim;
  -- 'auto'   = backfill from migration (default-<role> groups).
  source      TEXT    NOT NULL DEFAULT 'manual',
  added_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (user_id, group_id)
);

CREATE INDEX IF NOT EXISTS idx_user_groups_group ON user_groups(group_id);

CREATE TABLE IF NOT EXISTS group_roles (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  group_id    INTEGER NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
  role        TEXT    NOT NULL,                   -- 'admin' | 'operator' | 'viewer'
  -- Selector grammar lands in PR β. NULL = CP-wide; non-NULL holds
  -- the raw selector string ("env=prod,team=security") that PR β's
  -- parser will evaluate against node labels.
  selector    TEXT,
  created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  -- One (group, role, selector) tuple per row. We don't enforce
  -- uniqueness across rows because two different selectors with the
  -- same role are both meaningful (e.g. operator on env=prod AND
  -- operator on env=staging). Duplicate-prevention happens UI-side.
  UNIQUE (group_id, role, selector)
);

CREATE INDEX IF NOT EXISTS idx_group_roles_group ON group_roles(group_id);

-- Backfill: every existing user gets attached to a default group
-- carrying their current role. Naming convention "default-<role>"
-- so the migration is idempotent (a re-run won't make duplicates).
-- After this, RequireRole(operator) keeps working unchanged because
-- everyone with role='operator' is now in default-operator which
-- has a CP-wide operator group_role.
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
