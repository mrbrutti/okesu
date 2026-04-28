-- Phase 9 (parent side): federation_peers — child CPs this CP federates from.
--
-- Each row is a child CP this parent CP polls /api/v1/cp/introspect on
-- every 30s. The cached introspect JSON powers the federation tab in
-- the UI without re-hitting the network on every page load.
--
-- The token is stored in plaintext because the parent needs to send
-- it outbound on each poll. That's a known tradeoff: a CP host
-- compromise already grants the attacker network access AND mTLS keys,
-- so encrypted-at-rest peer tokens add little here. A future phase
-- can re-route this through the existing secrets adapter
-- (file://, oci-vault://) if the operator's threat model needs it.
--
-- url is UNIQUE so an operator can't accidentally register the same
-- child twice. Lookups in the poller are by id (PK).

CREATE TABLE IF NOT EXISTS federation_peers (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  url             TEXT    NOT NULL UNIQUE,                -- absolute base URL, e.g. https://child:8443
  display_name    TEXT    NOT NULL DEFAULT '',            -- operator label; falls back to remote display_name
  token           TEXT    NOT NULL,                       -- plaintext federation token (sent outbound)
  added_at        TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  last_polled_at  TIMESTAMP,                              -- when the poller last attempted a call
  last_seen_at    TIMESTAMP,                              -- when the poller last got a 2xx
  last_error      TEXT    NOT NULL DEFAULT '',            -- most-recent failure message; cleared on success
  introspect_json TEXT    NOT NULL DEFAULT ''             -- last successful introspect response (JSON blob)
);

CREATE INDEX IF NOT EXISTS idx_federation_peers_last_seen ON federation_peers(last_seen_at DESC);
