-- Phase 9 (parent side): federation_peers. See sqlite/022_federation_peers.sql
-- for the design rationale.

CREATE TABLE IF NOT EXISTS federation_peers (
  id              SERIAL PRIMARY KEY,
  url             TEXT    NOT NULL UNIQUE,
  display_name    TEXT    NOT NULL DEFAULT '',
  token           TEXT    NOT NULL,
  added_at        TIMESTAMPTZ DEFAULT NOW(),
  last_polled_at  TIMESTAMPTZ,
  last_seen_at    TIMESTAMPTZ,
  last_error      TEXT    NOT NULL DEFAULT '',
  introspect_json TEXT    NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_federation_peers_last_seen ON federation_peers(last_seen_at DESC);
