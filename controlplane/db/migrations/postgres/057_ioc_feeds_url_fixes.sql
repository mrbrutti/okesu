-- See sqlite/057_ioc_feeds_url_fixes.sql for design notes.

UPDATE ioc_feeds
   SET url = 'https://threatfox.abuse.ch/export/csv/recent/'
 WHERE slug = 'abusech-threatfox'
   AND url = 'https://threatfox.abuse.ch/export/csv/full/'
   AND installed_from_registry = TRUE;

UPDATE ioc_feeds
   SET url = 'https://urlhaus.abuse.ch/downloads/csv_recent/'
 WHERE slug = 'abusech-urlhaus'
   AND url = 'https://urlhaus.abuse.ch/downloads/csv/'
   AND installed_from_registry = TRUE;

UPDATE ioc_feeds
   SET enabled = FALSE
 WHERE slug = 'yara-forge-core'
   AND installed_from_registry = TRUE;
