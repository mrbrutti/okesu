CREATE TABLE investigation_note_drafts (
  investigation_id BIGINT PRIMARY KEY REFERENCES investigations(id) ON DELETE CASCADE,
  ydoc_state       BYTEA  NOT NULL,
  updated_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
