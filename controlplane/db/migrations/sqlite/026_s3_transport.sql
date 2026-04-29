-- Phase 9.2: S3-bucket "dead-drop" transport.
--
-- Agents can be configured to communicate with the CP via a shared
-- object-storage bucket instead of direct HTTPS. Used for nodes that
-- can't reach the CP (NAT'd, air-gapped, hostile networks) but can
-- reach S3 / OCI Object Storage / R2 / MinIO.
--
-- Two new pieces of state:
--   1. transport_configs — one row per CP describing the bucket the CP
--      uses for dead-drop transport. The CP holds the bucket creds and
--      the fleet enrollment keypair here.
--   2. nodes.transport / transport_config_id / poll_interval_ms — per-node
--      override of how the daemon talks to the CP.

CREATE TABLE IF NOT EXISTS transport_configs (
  id                       INTEGER PRIMARY KEY AUTOINCREMENT,
  -- Human-friendly name for the UI (operators may have multiple — one per
  -- CP, or one per region; future-proofed even though v1 binds one
  -- transport_config to one CP).
  name                     TEXT NOT NULL,
  kind                     TEXT NOT NULL,                  -- 's3' (covers AWS, OCI, R2, MinIO via S3 API)
  -- Bucket connection. Endpoint is host:port (no scheme); UseSSL is
  -- inferred from the use_ssl flag.
  bucket                   TEXT NOT NULL,
  endpoint                 TEXT NOT NULL,
  region                   TEXT,
  use_ssl                  INTEGER NOT NULL DEFAULT 1,
  -- Credentials — secret-grade. The access_key + secret_key fields are
  -- written via the secrets adapter when secrets-source is configured;
  -- otherwise stored at-rest (same trade-off as the existing webhook
  -- secret column on agents).
  access_key               TEXT,
  secret_key               TEXT,
  -- Fleet enrollment keypair. Public key is published in the bucket
  -- (cp/<cp-id>/enrollment/pubkey.pem); private key never leaves the CP
  -- and is used to verify package signatures during enrollment.
  fleet_pubkey_pem         TEXT,
  fleet_privkey_pem        TEXT,
  -- Default poll cadence the CP scanner uses for this bucket. Per-node
  -- override lives on the nodes row.
  scanner_interval_ms      INTEGER NOT NULL DEFAULT 10000,
  -- Logical CP this transport_config belongs to (matches cp_meta.id).
  -- v1 enforces one row per CP at the application layer; the column
  -- lets us pre-validate uniqueness without surfacing a UNIQUE
  -- constraint that future federation might want to relax.
  cp_id                    TEXT,
  created_at               TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  updated_at               TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_transport_configs_kind ON transport_configs(kind);
CREATE INDEX IF NOT EXISTS idx_transport_configs_cp_id ON transport_configs(cp_id);

-- Per-node transport selection.
ALTER TABLE nodes ADD COLUMN transport TEXT NOT NULL DEFAULT 'https';
ALTER TABLE nodes ADD COLUMN transport_config_id INTEGER REFERENCES transport_configs(id);
ALTER TABLE nodes ADD COLUMN poll_interval_ms INTEGER;
-- The UUID the node self-generated at enrollment. Useful so the CP can
-- correlate registration requests with the issued node row even before
-- the operator names the node. Empty for HTTPS-pull-mode nodes.
ALTER TABLE nodes ADD COLUMN node_uuid TEXT;
CREATE INDEX IF NOT EXISTS idx_nodes_transport ON nodes(transport);
CREATE INDEX IF NOT EXISTS idx_nodes_node_uuid ON nodes(node_uuid);

-- enrollment_packages — every "Generate package" click stores a row.
-- One package can register N machines. The fleet signing cert in the
-- package is bound to this row so the CP can revoke an individual
-- package without rotating the whole fleet keypair.
CREATE TABLE IF NOT EXISTS enrollment_packages (
  id                    INTEGER PRIMARY KEY AUTOINCREMENT,
  -- Operator-friendly identifier; printed in the install banner.
  display_name          TEXT NOT NULL,
  -- FK to transport_configs — which bucket the package will write to.
  transport_config_id   INTEGER NOT NULL REFERENCES transport_configs(id),
  cp_id                 TEXT NOT NULL,                  -- redundant w/ transport_configs.cp_id; cached for fast lookup
  -- Package-level signing cert + key. Cert is signed by the fleet key
  -- (transport_configs.fleet_privkey_pem) at generation time. The
  -- node embeds the cert and uses the matching private key to sign
  -- registration requests.
  package_cert_pem      TEXT NOT NULL,
  package_key_pem       TEXT NOT NULL,
  -- Per-package config defaults — the daimons each enrolled node
  -- starts with, override poll cadence, etc. JSON blob; schema is
  -- agent.PackageDefaults (in agent/s3transport/wire.go).
  defaults_json         TEXT,
  -- Revocation: setting revoked_at causes the CP scanner to reject any
  -- registration request signed with this package's cert.
  revoked_at            TIMESTAMP,
  created_at            TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  created_by            INTEGER REFERENCES users(id)
);

CREATE INDEX IF NOT EXISTS idx_enrollment_packages_transport_cfg ON enrollment_packages(transport_config_id);
CREATE INDEX IF NOT EXISTS idx_enrollment_packages_revoked ON enrollment_packages(revoked_at);
