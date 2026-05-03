-- War-room note drafts. One row per case at most; ydoc_state is the
-- merged Yjs document bytes (encodeStateAsUpdate). Drafts older than
-- 7 days are deleted by the daily GC sweep (see
-- SweepStaleInvestigationNoteDrafts).
CREATE TABLE investigation_note_drafts (
  investigation_id INTEGER PRIMARY KEY REFERENCES investigations(id) ON DELETE CASCADE,
  ydoc_state       BLOB    NOT NULL,
  updated_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
