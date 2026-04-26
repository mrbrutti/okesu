-- Phase 8 schema: daemon binary inventory.
--
-- Used by the multi-arch deploy. The CP looks up the binary matching the
-- target host's `uname -m` output and uploads it. Binaries are uploaded by
-- admins through the UI (multipart POST) and stored on the CP filesystem
-- under <DaemonBinariesDir>/. The DB tracks metadata so the UI can list
-- what's available without scanning the filesystem on every request.

CREATE TABLE IF NOT EXISTS daemon_binaries (
  -- Stable name = "okesu-<os>-<arch>" by convention. UNIQUE so an upload
  -- with the same os/arch overwrites the previous entry.
  name              TEXT PRIMARY KEY,
  os                TEXT NOT NULL,         -- "linux", "darwin", "windows"
  arch              TEXT NOT NULL,         -- "amd64", "arm64", "386", ...
  path              TEXT NOT NULL,         -- absolute path on the CP host
  sha256            TEXT NOT NULL,
  size_bytes        INTEGER NOT NULL,
  uploaded_at       TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  uploaded_by_email TEXT
);

CREATE INDEX IF NOT EXISTS idx_daemon_binaries_os_arch ON daemon_binaries(os, arch);
