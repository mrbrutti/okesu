package db

import (
	"database/sql"
	"time"
)

// DaemonBinary is a registered per-arch daemon binary the CP can deploy.
type DaemonBinary struct {
	Name            string
	OS              string
	Arch            string
	Path            string
	SHA256          string
	SizeBytes       int64
	UploadedAt      time.Time
	UploadedByEmail sql.NullString
}

// UpsertDaemonBinary registers (or replaces) a binary by name. Caller is
// responsible for placing the file at Path before calling.
func (s *Store) UpsertDaemonBinary(b *DaemonBinary, uploadedBy string) error {
	_, err := s.Exec(`
		INSERT INTO daemon_binaries (name, os, arch, path, sha256, size_bytes, uploaded_by_email)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			os                = excluded.os,
			arch              = excluded.arch,
			path              = excluded.path,
			sha256            = excluded.sha256,
			size_bytes        = excluded.size_bytes,
			uploaded_at       = CURRENT_TIMESTAMP,
			uploaded_by_email = excluded.uploaded_by_email
	`, b.Name, b.OS, b.Arch, b.Path, b.SHA256, b.SizeBytes, nullable(uploadedBy))
	return err
}

// ListDaemonBinaries returns all registered binaries, newest upload first.
func (s *Store) ListDaemonBinaries() ([]*DaemonBinary, error) {
	rows, err := s.Query(`
		SELECT name, os, arch, path, sha256, size_bytes, uploaded_at, uploaded_by_email
		FROM daemon_binaries ORDER BY uploaded_at DESC, name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*DaemonBinary
	for rows.Next() {
		b := &DaemonBinary{}
		if err := rows.Scan(
			&b.Name, &b.OS, &b.Arch, &b.Path, &b.SHA256, &b.SizeBytes,
			&b.UploadedAt, &b.UploadedByEmail,
		); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// DaemonBinaryByOSArch returns the binary matching the given os+arch pair,
// or sql.ErrNoRows if none registered.
func (s *Store) DaemonBinaryByOSArch(osName, arch string) (*DaemonBinary, error) {
	b := &DaemonBinary{}
	err := s.QueryRow(`
		SELECT name, os, arch, path, sha256, size_bytes, uploaded_at, uploaded_by_email
		FROM daemon_binaries
		WHERE os = ? AND arch = ?
		ORDER BY uploaded_at DESC LIMIT 1
	`, osName, arch).Scan(
		&b.Name, &b.OS, &b.Arch, &b.Path, &b.SHA256, &b.SizeBytes,
		&b.UploadedAt, &b.UploadedByEmail,
	)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// DaemonBinaryByName returns the binary by canonical name (e.g. okesu-linux-amd64).
func (s *Store) DaemonBinaryByName(name string) (*DaemonBinary, error) {
	b := &DaemonBinary{}
	err := s.QueryRow(`
		SELECT name, os, arch, path, sha256, size_bytes, uploaded_at, uploaded_by_email
		FROM daemon_binaries WHERE name = ?
	`, name).Scan(
		&b.Name, &b.OS, &b.Arch, &b.Path, &b.SHA256, &b.SizeBytes,
		&b.UploadedAt, &b.UploadedByEmail,
	)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// DeleteDaemonBinary removes the row by name. Caller is responsible for
// removing the underlying file.
func (s *Store) DeleteDaemonBinary(name string) error {
	_, err := s.Exec(`DELETE FROM daemon_binaries WHERE name = ?`, name)
	return err
}
