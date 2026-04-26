-- Extended node metadata. Captured by the "Refresh metadata" action
-- which probes a connected node over the existing tunnel — no SSH
-- credentials required, sub-second per node.
--
-- All fields nullable; a node with an offline tunnel keeps its prior
-- snapshot (or nulls when never refreshed).

ALTER TABLE nodes ADD COLUMN kernel_release  TEXT;
ALTER TABLE nodes ADD COLUMN os_release      TEXT;
ALTER TABLE nodes ADD COLUMN arch            TEXT;
ALTER TABLE nodes ADD COLUMN cpu_count       BIGINT;
ALTER TABLE nodes ADD COLUMN memory_mb       BIGINT;
ALTER TABLE nodes ADD COLUMN disk_free_mb    BIGINT;
ALTER TABLE nodes ADD COLUMN okesu_version   TEXT;
ALTER TABLE nodes ADD COLUMN metadata_at     TIMESTAMP;
