-- 062_archived_at.sql (postgres)
ALTER TABLE nodes            ADD COLUMN archived_at TIMESTAMPTZ;
ALTER TABLE nodes            ADD COLUMN archived_by_email TEXT;
ALTER TABLE node_provisions  ADD COLUMN archived_at TIMESTAMPTZ;
ALTER TABLE node_provisions  ADD COLUMN archived_by_email TEXT;
