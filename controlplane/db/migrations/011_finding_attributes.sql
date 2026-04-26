-- Phase 12: enrich findings with structured indexed fields.
--
-- The original schema stored a free-form `resource` and `evidence`, which
-- works for display but offers no useful filtering surface. Operators want
-- to ask things like "show me everything that touched pid 1337", "what
-- findings reference /etc/cron.d", "every CVE-2024-* across the fleet" —
-- those queries need indexed columns, not LIKEs over evidence text.
--
-- Categories also let the dashboard group by *type of issue* (process /
-- file / network / cert / cloud / identity / config) rather than just by
-- severity, which is much more actionable in a fleet view.
--
-- All columns are nullable: agents that don't extract a particular field
-- simply leave it blank. The harvester (and webhook receiver) populate
-- whatever the LLM writes; missing columns degrade gracefully.

ALTER TABLE findings ADD COLUMN category         TEXT;   -- process|file|network|cert|cloud|identity|config|other
ALTER TABLE findings ADD COLUMN process_pid      INTEGER;
ALTER TABLE findings ADD COLUMN process_name     TEXT;
ALTER TABLE findings ADD COLUMN path             TEXT;
ALTER TABLE findings ADD COLUMN network_endpoint TEXT;   -- host:port, URL, or IP
ALTER TABLE findings ADD COLUMN cve              TEXT;   -- CVE-YYYY-NNNNN
ALTER TABLE findings ADD COLUMN tags             TEXT;   -- comma-separated; LIKE-searchable
ALTER TABLE findings ADD COLUMN attributes       TEXT;   -- JSON catch-all for everything else

-- Partial indexes — small because most findings only have a couple of
-- these populated. Operators still get fast equality+prefix queries.
CREATE INDEX IF NOT EXISTS idx_findings_category         ON findings(category)         WHERE category         IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_findings_process_pid      ON findings(process_pid)      WHERE process_pid      IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_findings_process_name     ON findings(process_name)     WHERE process_name     IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_findings_path             ON findings(path)             WHERE path             IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_findings_network_endpoint ON findings(network_endpoint) WHERE network_endpoint IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_findings_cve              ON findings(cve)              WHERE cve              IS NOT NULL;
