// Store methods for transport_configs + enrollment_packages — the
// CP-side state behind the S3 dead-drop transport.
//
// One TransportConfig per CP describes the bucket the CP scanner
// reads/writes. EnrollmentPackage rows track each "Generate package"
// click; revoking the row prevents future registrations from that
// package without affecting already-enrolled nodes.

package db

import (
	"database/sql"
	"errors"
	"time"
)

// TransportConfig mirrors a transport_configs row.
//
// Endpoint vs EndpointInternal:
//
//   - Endpoint is the host:port that gets baked into the package
//     bootstrap.json, i.e. what remote nodes use to talk to the bucket.
//     Always set; this is the public/external name.
//   - EndpointInternal, when set, is the host:port the CP scanner
//     dials. Defaults to Endpoint when null. Useful for split-horizon
//     setups where the CP sits inside a VPC and reaches the bucket
//     over a private endpoint that nodes cannot resolve.
//
// Use ScannerEndpoint() to read the right value from scanner-side
// code so the fallback rule lives in one place.
type TransportConfig struct {
	ID                  int64
	Name                string
	Kind                string // 's3'
	Bucket              string
	Endpoint            string
	EndpointInternal    sql.NullString
	Region              sql.NullString
	UseSSL              bool
	AccessKey           sql.NullString
	SecretKey           sql.NullString
	FleetPubkeyPEM      sql.NullString
	FleetPrivkeyPEM     sql.NullString // never returned in API responses
	ScannerIntervalMs   int
	CPID                sql.NullString
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// ScannerEndpoint returns the host:port the CP scanner should dial.
// Falls back to the public Endpoint when EndpointInternal isn't set —
// matches the migration's "NULL = use public" behavior so legacy rows
// keep working without an operator touching them.
func (c TransportConfig) ScannerEndpoint() string {
	if c.EndpointInternal.Valid && c.EndpointInternal.String != "" {
		return c.EndpointInternal.String
	}
	return c.Endpoint
}

// CreateTransportConfig inserts a new config and returns its id.
func (s *Store) CreateTransportConfig(c TransportConfig) (int64, error) {
	res, err := s.Exec(`
		INSERT INTO transport_configs
			(name, kind, bucket, endpoint, endpoint_internal, region, use_ssl,
			 access_key, secret_key,
			 fleet_pubkey_pem, fleet_privkey_pem,
			 scanner_interval_ms, cp_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.Name, c.Kind, c.Bucket, c.Endpoint, c.EndpointInternal, c.Region, boolToInt(c.UseSSL),
		c.AccessKey, c.SecretKey,
		c.FleetPubkeyPEM, c.FleetPrivkeyPEM,
		c.ScannerIntervalMs, c.CPID)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetTransportConfig returns one row by id.
func (s *Store) GetTransportConfig(id int64) (TransportConfig, error) {
	row := s.QueryRow(`
		SELECT id, name, kind, bucket, endpoint, endpoint_internal, region, use_ssl,
		       access_key, secret_key, fleet_pubkey_pem, fleet_privkey_pem,
		       scanner_interval_ms, cp_id, created_at, updated_at
		  FROM transport_configs WHERE id = ?`, id)
	return scanTransportConfig(row)
}

// GetTransportConfigByCPID returns the (single, by convention) config
// bound to a given CP. Returns sql.ErrNoRows if absent.
func (s *Store) GetTransportConfigByCPID(cpID string) (TransportConfig, error) {
	row := s.QueryRow(`
		SELECT id, name, kind, bucket, endpoint, endpoint_internal, region, use_ssl,
		       access_key, secret_key, fleet_pubkey_pem, fleet_privkey_pem,
		       scanner_interval_ms, cp_id, created_at, updated_at
		  FROM transport_configs WHERE cp_id = ? LIMIT 1`, cpID)
	return scanTransportConfig(row)
}

// ListTransportConfigs returns all rows. Small table; no pagination.
func (s *Store) ListTransportConfigs() ([]TransportConfig, error) {
	rows, err := s.Query(`
		SELECT id, name, kind, bucket, endpoint, endpoint_internal, region, use_ssl,
		       access_key, secret_key, fleet_pubkey_pem, fleet_privkey_pem,
		       scanner_interval_ms, cp_id, created_at, updated_at
		  FROM transport_configs ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TransportConfig
	for rows.Next() {
		c, err := scanTransportConfig(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpdateTransportConfig overwrites mutable fields. Cred fields can be
// null'd by passing empty NullString — the API layer is responsible
// for preserving secret_key when the operator submits the form
// without a new value.
func (s *Store) UpdateTransportConfig(c TransportConfig) error {
	_, err := s.Exec(`
		UPDATE transport_configs SET
			name = ?, kind = ?, bucket = ?, endpoint = ?, endpoint_internal = ?, region = ?, use_ssl = ?,
			access_key = ?, secret_key = ?,
			fleet_pubkey_pem = ?, fleet_privkey_pem = ?,
			scanner_interval_ms = ?, cp_id = ?,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		c.Name, c.Kind, c.Bucket, c.Endpoint, c.EndpointInternal, c.Region, boolToInt(c.UseSSL),
		c.AccessKey, c.SecretKey,
		c.FleetPubkeyPEM, c.FleetPrivkeyPEM,
		c.ScannerIntervalMs, c.CPID,
		c.ID)
	return err
}

// DeleteTransportConfig removes a row. Foreign-keyed nodes with
// transport_config_id pointing here will have it set to NULL by SQLite
// (no ON DELETE CASCADE — we'd rather surface the orphan than lose
// node identities). The API layer should refuse to delete configs
// referenced by enabled enrollment_packages.
func (s *Store) DeleteTransportConfig(id int64) error {
	_, err := s.Exec(`DELETE FROM transport_configs WHERE id = ?`, id)
	return err
}

func scanTransportConfig(r rowScanner) (TransportConfig, error) {
	var c TransportConfig
	var useSSL int
	if err := r.Scan(
		&c.ID, &c.Name, &c.Kind, &c.Bucket, &c.Endpoint, &c.EndpointInternal, &c.Region, &useSSL,
		&c.AccessKey, &c.SecretKey, &c.FleetPubkeyPEM, &c.FleetPrivkeyPEM,
		&c.ScannerIntervalMs, &c.CPID, &c.CreatedAt, &c.UpdatedAt,
	); err != nil {
		return TransportConfig{}, err
	}
	c.UseSSL = useSSL != 0
	return c, nil
}

// ───────────────────────── enrollment_packages ─────────────────────────

// EnrollmentPackage mirrors an enrollment_packages row.
type EnrollmentPackage struct {
	ID                int64
	DisplayName       string
	TransportConfigID int64
	CPID              string
	PackageCertPEM    string
	PackageKeyPEM     string  // sensitive — only returned on first create
	DefaultsJSON      sql.NullString
	RevokedAt         sql.NullTime
	CreatedAt         time.Time
	CreatedBy         sql.NullInt64
}

// CreateEnrollmentPackage inserts and returns the id.
func (s *Store) CreateEnrollmentPackage(p EnrollmentPackage) (int64, error) {
	res, err := s.Exec(`
		INSERT INTO enrollment_packages
			(display_name, transport_config_id, cp_id,
			 package_cert_pem, package_key_pem,
			 defaults_json, created_by)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		p.DisplayName, p.TransportConfigID, p.CPID,
		p.PackageCertPEM, p.PackageKeyPEM,
		p.DefaultsJSON, p.CreatedBy)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetEnrollmentPackage returns one row by id, including secret fields.
// API layer must not expose package_key_pem after the package was
// originally generated.
func (s *Store) GetEnrollmentPackage(id int64) (EnrollmentPackage, error) {
	row := s.QueryRow(`
		SELECT id, display_name, transport_config_id, cp_id,
		       package_cert_pem, package_key_pem, defaults_json,
		       revoked_at, created_at, created_by
		  FROM enrollment_packages WHERE id = ?`, id)
	return scanEnrollmentPackage(row)
}

// ListEnrollmentPackages returns all rows newest-first.
func (s *Store) ListEnrollmentPackages() ([]EnrollmentPackage, error) {
	rows, err := s.Query(`
		SELECT id, display_name, transport_config_id, cp_id,
		       package_cert_pem, package_key_pem, defaults_json,
		       revoked_at, created_at, created_by
		  FROM enrollment_packages ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EnrollmentPackage
	for rows.Next() {
		p, err := scanEnrollmentPackage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// RevokeEnrollmentPackage stamps revoked_at so the scanner rejects
// future registrations carrying this package's signing cert. Idempotent.
func (s *Store) RevokeEnrollmentPackage(id int64) error {
	_, err := s.Exec(`UPDATE enrollment_packages SET revoked_at = CURRENT_TIMESTAMP WHERE id = ? AND revoked_at IS NULL`, id)
	return err
}

// FindEnrollmentPackageByCertFingerprint matches a registration request
// against the issuing package. The scanner uses this to verify the
// request's signature + check revocation status.
func (s *Store) FindEnrollmentPackageByCertFingerprint(fp string) (EnrollmentPackage, error) {
	// Stored cert PEM hashed at query time; we don't keep a separate
	// fingerprint column to avoid drift if the cert is ever rewritten.
	rows, err := s.Query(`SELECT id, display_name, transport_config_id, cp_id,
	       package_cert_pem, package_key_pem, defaults_json,
	       revoked_at, created_at, created_by
	  FROM enrollment_packages`)
	if err != nil {
		return EnrollmentPackage{}, err
	}
	defer rows.Close()
	for rows.Next() {
		p, err := scanEnrollmentPackage(rows)
		if err != nil {
			return EnrollmentPackage{}, err
		}
		if certPEMFingerprint(p.PackageCertPEM) == fp {
			return p, nil
		}
	}
	return EnrollmentPackage{}, errors.New("no enrollment package matches fingerprint")
}

func scanEnrollmentPackage(r rowScanner) (EnrollmentPackage, error) {
	var p EnrollmentPackage
	if err := r.Scan(
		&p.ID, &p.DisplayName, &p.TransportConfigID, &p.CPID,
		&p.PackageCertPEM, &p.PackageKeyPEM, &p.DefaultsJSON,
		&p.RevokedAt, &p.CreatedAt, &p.CreatedBy,
	); err != nil {
		return EnrollmentPackage{}, err
	}
	return p, nil
}

// certPEMFingerprint is a small helper rather than a real x509 parser
// — the package layer puts a real SHA-256 of the DER bytes in here and
// the scanner regenerates it the same way. Implemented in
// controlplane/packaging/fingerprint.go.
var certPEMFingerprint = func(pem string) string { return "" }

// SetCertFingerprintFn lets the packaging layer install its real
// fingerprint helper at boot. Avoids an import cycle between db and
// packaging.
func SetCertFingerprintFn(fn func(pem string) string) {
	if fn != nil {
		certPEMFingerprint = fn
	}
}
