-- Postgres mirror of 052_notification_host_selector.sql.
ALTER TABLE notification_rules ADD COLUMN host_selector TEXT NOT NULL DEFAULT '';
