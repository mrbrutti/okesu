-- Phase 21.3 — managed CP provisioning job log.
--
-- One row per "+ Add CP → Managed deploy" submission. Carries the
-- state machine (queued → starting → cloud_init_running →
-- bootstrap_pending → ready / failed / cancelled) plus a free-form
-- log column the provisioner appends to as it makes API calls.
-- Operators see the job page on the Federation Page mid-deploy and
-- in the audit trail afterwards.
--
-- The cloud-specific instance handle (OCI instance OCID, AWS
-- instance id, etc.) lives in cloud_resource_id so a destroy /
-- retry can find it without re-deriving from name.
--
-- bundle_token_id links to the cp_bootstrap_tokens row issued for
-- this provision — when the new CP calls /api/v1/cp/bootstrap, that
-- token is consumed AND we observe the matching peer_id, which is
-- how the state machine learns "the new CP is alive".

CREATE TABLE cp_provisions (
  id                  INTEGER PRIMARY KEY AUTOINCREMENT,
  display_name        TEXT NOT NULL,
  region              TEXT NOT NULL,
  cloud               TEXT NOT NULL,
  credential_id       INTEGER REFERENCES cloud_credentials(id) ON DELETE SET NULL,
  credential_name     TEXT,                      -- snapshot for the audit log
  cloud_params_json   TEXT NOT NULL DEFAULT '{}', -- per-cloud knobs (subnet OCID, AMI id, ...)
  status              TEXT NOT NULL DEFAULT 'queued',
  cloud_resource_id   TEXT,                      -- e.g. ocid1.instance.oc1..xxx
  cloud_resource_url  TEXT,                      -- console URL for the operator
  bundle_token_id     INTEGER REFERENCES cp_bootstrap_tokens(id) ON DELETE SET NULL,
  peer_id             INTEGER REFERENCES federation_peers(id) ON DELETE SET NULL,
  log                 TEXT NOT NULL DEFAULT '',  -- newline-delimited operator-visible log
  error               TEXT,
  created_at          TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  started_at          TIMESTAMP,
  ended_at            TIMESTAMP,
  created_by_user_id  INTEGER REFERENCES users(id) ON DELETE SET NULL,
  created_by_email    TEXT
);

CREATE INDEX idx_cp_provisions_status ON cp_provisions(status, created_at DESC);
CREATE INDEX idx_cp_provisions_cloud  ON cp_provisions(cloud, created_at DESC);
