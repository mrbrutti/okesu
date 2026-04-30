-- Phase 22.5: human-readable name + comma-separated tags on iocs.
-- Both nullable; existing rows are unchanged. Used heavily by yara_rule
-- / sigma_rule kinds where tag-filtering is the primary operator UX,
-- but applies cleanly to every kind (e.g., a sha256 IOC named
-- "WannaCry binary" with tags "ransomware,wcry,2017").

ALTER TABLE iocs ADD COLUMN name TEXT;
ALTER TABLE iocs ADD COLUMN tags TEXT;
