-- Postgres mirror of 054_severity_ceilings.sql.
CREATE TABLE IF NOT EXISTS severity_ceilings (
  id           SERIAL PRIMARY KEY,
  selector     TEXT    NOT NULL,
  max_severity TEXT    NOT NULL,
  reason       TEXT    NOT NULL DEFAULT '',
  created_at   TIMESTAMPTZ DEFAULT NOW(),
  updated_at   TIMESTAMPTZ DEFAULT NOW(),
  UNIQUE (selector)
);
