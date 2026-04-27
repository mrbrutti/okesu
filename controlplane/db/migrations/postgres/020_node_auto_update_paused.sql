-- See sqlite/020_node_auto_update_paused.sql for rationale.

ALTER TABLE nodes ADD COLUMN IF NOT EXISTS auto_update_paused BOOLEAN NOT NULL DEFAULT FALSE;
