-- Link an ad-hoc Run back to the Finding that triggered it. Used by the
-- "Investigate this finding" flow on the FindingDrawer — operators
-- launch a one-shot run with the finding's context pre-filled, and the
-- resulting transcript stays attached to the finding so the
-- investigation history is reachable from the drawer.

ALTER TABLE runs ADD COLUMN finding_id BIGINT REFERENCES findings(id);

CREATE INDEX IF NOT EXISTS idx_runs_finding_id ON runs(finding_id) WHERE finding_id IS NOT NULL;
