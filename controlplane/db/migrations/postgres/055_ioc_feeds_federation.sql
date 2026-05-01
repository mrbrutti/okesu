-- See sqlite/055_ioc_feeds_federation.sql for design notes.

ALTER TABLE ioc_feeds
    ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT 'local'
        CHECK (source IN ('local', 'federated_from_parent'));
ALTER TABLE ioc_feeds ADD COLUMN IF NOT EXISTS parent_cp_id TEXT;
CREATE INDEX IF NOT EXISTS idx_ioc_feeds_source ON ioc_feeds(source);
