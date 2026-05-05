-- 062_archived_at.sql
-- Adds archived_at TIMESTAMP NULL and archived_by_email TEXT NULL to
-- both nodes and node_provisions so the operator's offboarding action
-- can flip them to a terminal non-deleting "archived" state. The
-- cloud VM is gone but the row + all history (events, findings,
-- runs, agents — keyed by host name not FK) survive intact for
-- retrospective analysis. The 'archived' status string is documented
-- in db/nodes.go (NodeStatusArchived).
ALTER TABLE nodes            ADD COLUMN archived_at TIMESTAMP;
ALTER TABLE nodes            ADD COLUMN archived_by_email TEXT;
ALTER TABLE node_provisions  ADD COLUMN archived_at TIMESTAMP;
ALTER TABLE node_provisions  ADD COLUMN archived_by_email TEXT;
