-- Phase 21.2 — Cloud credentials.
--
-- Per-cloud credential records that the parent CP uses (Phase 21.3+)
-- to provision child CPs via cloud APIs. The cloud-specific payload
-- (tenancy OCIDs, access keys, service-account JSON, etc.) lives in
-- encrypted_payload — AES-GCM-sealed against a key derived from
-- cp_meta.session_hmac_key. The CP never logs the plaintext and
-- only decrypts at the moment of an actual API call.
--
-- Why a DB table and not the Secrets ports adapter:
--   - One row per credential lets operators have multiple accounts
--     per cloud (e.g. "prod-tenancy", "staging-tenancy") and the
--     UI lists them. The Secrets adapter is keyed by static names
--     and isn't a good fit for a dynamic list.
--   - Tests + provisioner code already work against *db.Store; this
--     keeps the surface uniform.
--   - Encrypted-at-rest is provided by the AES-GCM seal; for stronger
--     guarantees an operator can layer the existing OCI Vault
--     Secrets adapter under db.* (the encryption key itself comes
--     from the Secrets-managed session_hmac_key).

CREATE TABLE cloud_credentials (
  id                 INTEGER PRIMARY KEY AUTOINCREMENT,
  cloud              TEXT NOT NULL,
  name               TEXT NOT NULL,
  region             TEXT,
  encrypted_payload  BLOB NOT NULL,
  payload_nonce      BLOB NOT NULL,
  created_at         TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  created_by_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
  created_by_email   TEXT,
  last_used_at       TIMESTAMP,
  last_test_at       TIMESTAMP,
  last_test_ok       INTEGER,
  last_test_error    TEXT,
  UNIQUE(cloud, name)
);

CREATE INDEX idx_cloud_credentials_cloud ON cloud_credentials(cloud);
