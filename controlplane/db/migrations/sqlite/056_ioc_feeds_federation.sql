-- Phase 23 (federation): mark each ioc_feeds row as either operator-
-- managed locally on this CP, or mirrored read-only from a parent CP
-- via the federation poller. The federation flow lives in
-- controlplane/federation/poller.go (fetchAndApplyFeeds) and writes
-- via Store.SetFeedConfigsFromFederation. Local rows are never touched
-- by federation; the operator can flip a federated row to 'local' via
-- the override-locally UI control.
--
-- See docs/superpowers/specs/2026-05-01-ioc-feeds-design.md.

ALTER TABLE ioc_feeds ADD COLUMN source TEXT NOT NULL DEFAULT 'local'
    CHECK (source IN ('local', 'federated_from_parent'));
ALTER TABLE ioc_feeds ADD COLUMN parent_cp_id TEXT;
CREATE INDEX IF NOT EXISTS idx_ioc_feeds_source ON ioc_feeds(source);
