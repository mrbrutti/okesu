-- Phase 8 schema: SSH host-key pinning per registered Node.
--
-- The CP captures the target's SSH host-key fingerprint on first connect
-- (TOFU) and verifies it on every subsequent connect. Tampering with the
-- target's sshd host key produces a mismatch and aborts the deploy.
--
-- One row per node — a node has at most one trusted fingerprint at a time.
-- To "re-pin" after legitimate host-key rotation, admins DELETE the row
-- (audit-logged) and the next deploy re-establishes trust.

CREATE TABLE IF NOT EXISTS known_hosts (
  node_id           INTEGER PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE,
  -- "ssh-ed25519" / "ssh-rsa" / etc. — the host key algorithm.
  key_type          TEXT NOT NULL,
  -- SHA-256 fingerprint of the public key, OpenSSH format
  -- (e.g. "SHA256:abcd...").
  fingerprint       TEXT NOT NULL,
  -- Full SSH public key in authorized_keys-line format, for forensics.
  public_key        TEXT NOT NULL,
  accepted_at       TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  accepted_by_email TEXT
);
