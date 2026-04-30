-- Phase 22.5 postgres parity. See migrations/sqlite/040_iocs_name_tags.sql.

ALTER TABLE iocs ADD COLUMN IF NOT EXISTS name TEXT;
ALTER TABLE iocs ADD COLUMN IF NOT EXISTS tags TEXT;
