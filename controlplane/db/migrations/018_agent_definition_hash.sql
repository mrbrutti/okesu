-- Each registered daimon (per name+host) reports the sha256 of the
-- definition it currently has loaded via the heartbeat. The CP compares
-- against the canonical hash from --daimon-files-dir to detect drift,
-- and the Daimon Library page can show "X of Y on current version"
-- without scraping the daemon process.

ALTER TABLE agents ADD COLUMN current_definition_hash TEXT;
