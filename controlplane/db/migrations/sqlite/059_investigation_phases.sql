-- Operator-defined phases of a case (e.g. "Initial detection 14:00–14:35",
-- "Containment 14:35–16:10"). Free-form name; no category column.
-- Color in the UI is derived from hash(name).
CREATE TABLE investigation_phases (
  id               INTEGER PRIMARY KEY AUTOINCREMENT,
  investigation_id INTEGER NOT NULL REFERENCES investigations(id) ON DELETE CASCADE,
  name             TEXT    NOT NULL,
  start_ts         INTEGER NOT NULL,
  end_ts           INTEGER NOT NULL,
  created_by       TEXT,
  created_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  CHECK (end_ts >= start_ts),
  CHECK (length(name) > 0)
);
CREATE INDEX investigation_phases_inv ON investigation_phases (investigation_id);
