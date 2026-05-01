-- Phase 22.9 — host_selector on notification rules.
--
-- Existing rules match by agent_substring + host_substring (free-text
-- contains), which works for "anything mentioning prod" but not for
-- the structured "every host with env=prod, role=db" routing the
-- labels system unlocks. host_selector parses as a K8s-style label
-- selector and the worker evaluates it against the finding's host
-- node labels at delivery time.
--
-- Empty (default) keeps the prior matching behaviour. Adding a
-- selector tightens the rule — old configs continue to fire.

ALTER TABLE notification_rules ADD COLUMN host_selector TEXT NOT NULL DEFAULT '';
