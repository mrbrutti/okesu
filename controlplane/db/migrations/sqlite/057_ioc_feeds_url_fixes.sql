-- Phase 23 hotfix: registry URL corrections for default-installed feeds.
--
-- The initial registry shipped three URLs that didn't work end-to-end:
--   * abusech-threatfox /export/csv/full/ → returns a zipped CSV; the
--     v1 single_file fetcher can't unpack archives. Switched to
--     /export/csv/recent/ which ships raw CSV.
--   * abusech-urlhaus /downloads/csv/ → same zipped-CSV issue.
--     Switched to /downloads/csv_recent/.
--   * yara-forge-core → only publishes zip releases. Demoted from
--     DefaultInstalled and disabled here so existing boots don't
--     keep retrying a refresh that can never succeed until issue
--     #101 (archive feed kind) lands.
--
-- These UPDATEs only touch rows that still have the original broken
-- URL AND were installed_from_registry — operators who customized
-- their copy (changed URL or interval) are left alone.

UPDATE ioc_feeds
   SET url = 'https://threatfox.abuse.ch/export/csv/recent/'
 WHERE slug = 'abusech-threatfox'
   AND url = 'https://threatfox.abuse.ch/export/csv/full/'
   AND installed_from_registry = 1;

UPDATE ioc_feeds
   SET url = 'https://urlhaus.abuse.ch/downloads/csv_recent/'
 WHERE slug = 'abusech-urlhaus'
   AND url = 'https://urlhaus.abuse.ch/downloads/csv/'
   AND installed_from_registry = 1;

UPDATE ioc_feeds
   SET enabled = 0
 WHERE slug = 'yara-forge-core'
   AND installed_from_registry = 1;
