// Cloud credentials — encrypted storage for cloud API credentials
// the parent CP uses to provision child CPs (Phase 21.3+).
//
// The cloud-specific payload (tenancy OCIDs, access keys, service-
// account JSON, ...) is sealed with AES-256-GCM under a key derived
// from the same session HMAC seed the auth layer already keeps. The
// CP never logs the plaintext and only decrypts at the moment of an
// actual API call. The seal includes a per-row 12-byte nonce stored
// alongside the ciphertext.
//
// Key derivation:
//
//	masterKey = base64-decode(meta["session_hmac_key"])
//	cloudKey  = HKDF-SHA256(masterKey, "okesu-cloud-credentials-v1")
//
// HKDF gives us a domain-separated key for the cloud-creds use case
// without forcing the operator to manage a separate root secret. If
// the master is ever rotated, all rows must be re-sealed; the
// ListCloudCredentials method exposes a re-seal hook for that.

package db

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"golang.org/x/crypto/hkdf"
)

// CloudCredential is the metadata-only view of a stored credential —
// the encrypted payload stays in the DB; callers fetch it on demand
// via Decrypt and the Store passes them the master key.
type CloudCredential struct {
	ID               int64
	Cloud            string
	Name             string
	Region           sql.NullString
	MonthlyBudgetUSD sql.NullFloat64
	CreatedAt        time.Time
	CreatedByUser    sql.NullInt64
	CreatedByEmail   sql.NullString
	LastUsedAt       sql.NullTime
	LastTestAt       sql.NullTime
	LastTestOK       sql.NullBool
	LastTestError    sql.NullString
}

// CloudCredentialInsert is the input shape for InsertCloudCredential.
// Payload is the JSON-encoded cloud-specific credential body the
// store seals before persisting.
type CloudCredentialInsert struct {
	Cloud         string
	Name          string
	Region        string
	Payload       []byte // JSON, plaintext at this layer
	CreatedByUser int64
	CreatedByEmail string
}

// AllowedCloudKinds enumerates which `cloud` values the store will
// accept on insert. Adapters land cloud-by-cloud, but the
// allowlist itself lives in one place so a typo doesn't silently
// create an "amazon" row that no provisioner will pick up.
var AllowedCloudKinds = []string{"oci", "aws", "gcp", "azure", "digitalocean"}

// InsertCloudCredential seals the payload + persists. masterKey is
// the same base64-decoded session HMAC bytes the auth Manager uses;
// pass it through the store call so this file doesn't have to know
// where it comes from.
func (s *Store) InsertCloudCredential(in CloudCredentialInsert, masterKey []byte) (*CloudCredential, error) {
	if !isAllowedCloud(in.Cloud) {
		return nil, fmt.Errorf("cloud %q not supported (allowed: %v)", in.Cloud, AllowedCloudKinds)
	}
	if strings.TrimSpace(in.Name) == "" {
		return nil, errors.New("name is required")
	}
	if len(in.Payload) == 0 {
		return nil, errors.New("payload is required")
	}
	// Validate that payload is JSON so a Decrypt later reliably
	// produces a structured object rather than opaque bytes.
	var probe map[string]any
	if err := json.Unmarshal(in.Payload, &probe); err != nil {
		return nil, fmt.Errorf("payload must be JSON: %w", err)
	}

	ciphertext, nonce, err := sealCloudPayload(masterKey, in.Payload)
	if err != nil {
		return nil, fmt.Errorf("seal: %w", err)
	}

	res, err := s.Exec(`
		INSERT INTO cloud_credentials (
		    cloud, name, region, encrypted_payload, payload_nonce,
		    created_by_user_id, created_by_email
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`, in.Cloud, in.Name, nullable(in.Region), ciphertext, nonce,
		nullableInt64(in.CreatedByUser), nullable(in.CreatedByEmail))
	if err != nil {
		return nil, fmt.Errorf("insert: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.GetCloudCredential(id)
}

// GetCloudCredential returns the metadata for one row. Plaintext is
// only available via DecryptCloudCredential.
func (s *Store) GetCloudCredential(id int64) (*CloudCredential, error) {
	row := s.QueryRow(`
		SELECT id, cloud, name, region, monthly_budget_usd,
		       created_at, created_by_user_id, created_by_email,
		       last_used_at, last_test_at, last_test_ok, last_test_error
		FROM cloud_credentials WHERE id = ?
	`, id)
	return scanCloudCredential(row)
}

// ListCloudCredentials returns metadata for all rows, newest first.
// Filters by cloud when set; pass empty to get every cloud.
func (s *Store) ListCloudCredentials(cloud string) ([]CloudCredential, error) {
	q := `SELECT id, cloud, name, region, monthly_budget_usd,
	             created_at, created_by_user_id, created_by_email,
	             last_used_at, last_test_at, last_test_ok, last_test_error
	      FROM cloud_credentials`
	args := []any{}
	if cloud != "" {
		q += ` WHERE cloud = ?`
		args = append(args, cloud)
	}
	q += ` ORDER BY created_at DESC`
	rows, err := s.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CloudCredential
	for rows.Next() {
		c, err := scanCloudCredential(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// DecryptCloudCredential returns the stored JSON plaintext for the
// given row. Caller passes the master key — same source as
// InsertCloudCredential. last_used_at is bumped on success so the UI
// can show "this credential was used N minutes ago".
func (s *Store) DecryptCloudCredential(id int64, masterKey []byte) ([]byte, error) {
	row := s.QueryRow(`
		SELECT encrypted_payload, payload_nonce
		FROM cloud_credentials WHERE id = ?
	`, id)
	var ct, nonce []byte
	if err := row.Scan(&ct, &nonce); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, sql.ErrNoRows
		}
		return nil, err
	}
	pt, err := openCloudPayload(masterKey, ct, nonce)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	if _, err := s.Exec(`UPDATE cloud_credentials SET last_used_at = CURRENT_TIMESTAMP WHERE id = ?`, id); err != nil {
		// soft fail — caller already has the plaintext
		_ = err
	}
	return pt, nil
}

// DeleteCloudCredential removes a row by id. Idempotent.
func (s *Store) DeleteCloudCredential(id int64) error {
	_, err := s.Exec(`DELETE FROM cloud_credentials WHERE id = ?`, id)
	return err
}

// RecordCloudCredentialTest stamps the test result on the row so the
// UI can surface "tested 5m ago — OK" / "test failed: <reason>".
func (s *Store) RecordCloudCredentialTest(id int64, ok bool, errMsg string) error {
	_, err := s.Exec(`
		UPDATE cloud_credentials
		   SET last_test_at = CURRENT_TIMESTAMP,
		       last_test_ok = ?,
		       last_test_error = ?
		 WHERE id = ?
	`, ok, nullable(errMsg), id)
	return err
}

// ── helpers ────────────────────────────────────────────────────────

func scanCloudCredential(s rowScanner) (*CloudCredential, error) {
	c := &CloudCredential{}
	if err := s.Scan(
		&c.ID, &c.Cloud, &c.Name, &c.Region, &c.MonthlyBudgetUSD,
		&c.CreatedAt,
		&c.CreatedByUser, &c.CreatedByEmail,
		&c.LastUsedAt, &c.LastTestAt, &c.LastTestOK, &c.LastTestError,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, sql.ErrNoRows
		}
		return nil, err
	}
	return c, nil
}

// SetCloudCredentialBudget updates the per-credential monthly USD
// budget. Pass nil to clear (no cap).
func (s *Store) SetCloudCredentialBudget(id int64, budget *float64) error {
	var arg sql.NullFloat64
	if budget != nil {
		arg = sql.NullFloat64{Float64: *budget, Valid: true}
	}
	_, err := s.Exec(`UPDATE cloud_credentials SET monthly_budget_usd = ? WHERE id = ?`, arg, id)
	return err
}

// SumActiveMonthlyCostUSDByCredential adds up est_cost_per_hour_usd
// (× 730 hours/mo) across non-terminal cp_provisions rows linked to
// the credential. Used by the budget check + the Federation page's
// "current spend" tile. Rows with NULL est_cost_per_hour_usd
// contribute zero — the catalog had no entry for that shape, so we
// can't truthfully account for them. The handler surfaces those
// counts separately so the operator isn't fooled by a too-low total.
//
// Active = anything NOT in (failed, cancelled). A "ready" CP keeps
// charging until the operator destroys the underlying VM, so we
// count it.
func (s *Store) SumActiveMonthlyCostUSDByCredential(credentialID int64) (totalUSD float64, unknownCount int, err error) {
	row := s.QueryRow(`
		SELECT
		    COALESCE(SUM(est_cost_per_hour_usd), 0) * 730.0 AS monthly_usd,
		    COUNT(CASE WHEN est_cost_per_hour_usd IS NULL THEN 1 END) AS unknowns
		FROM cp_provisions
		WHERE credential_id = ?
		  AND status NOT IN ('failed', 'cancelled')
	`, credentialID)
	if err := row.Scan(&totalUSD, &unknownCount); err != nil {
		return 0, 0, err
	}
	return totalUSD, unknownCount, nil
}

func isAllowedCloud(c string) bool {
	for _, k := range AllowedCloudKinds {
		if k == c {
			return true
		}
	}
	return false
}

// MasterKeyFromMeta derives the seal master key from the session
// HMAC seed. Centralised here so the api layer doesn't have to know
// the meta key name or the base64 decoding rule.
func (s *Store) MasterKeyFromMeta() ([]byte, error) {
	stored, err := s.MetaGet("session_hmac_key")
	if err != nil {
		return nil, fmt.Errorf("read session hmac key: %w", err)
	}
	if stored == "" {
		return nil, errors.New("session hmac key not yet seeded — start the CP at least once before using cloud credentials")
	}
	raw, err := base64.StdEncoding.DecodeString(stored)
	if err != nil {
		return nil, fmt.Errorf("decode session hmac key: %w", err)
	}
	return raw, nil
}

// sealCloudPayload returns ciphertext + nonce. Domain-separates from
// other uses of the same master via HKDF info string.
func sealCloudPayload(masterKey, plaintext []byte) ([]byte, []byte, error) {
	key, err := deriveCloudKey(masterKey)
	if err != nil {
		return nil, nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	ct := gcm.Seal(nil, nonce, plaintext, nil)
	return ct, nonce, nil
}

func openCloudPayload(masterKey, ciphertext, nonce []byte) ([]byte, error) {
	key, err := deriveCloudKey(masterKey)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, nonce, ciphertext, nil)
}

func deriveCloudKey(masterKey []byte) ([]byte, error) {
	if len(masterKey) < 16 {
		return nil, errors.New("master key too short (need >= 16 bytes)")
	}
	// hkdf.New requires an explicit hash constructor — passing nil
	// here previously nil-panicked on the first Read because the
	// "default sha256" the comment claimed doesn't exist in the API.
	r := hkdf.New(sha256.New, masterKey, nil, []byte("okesu-cloud-credentials-v1"))
	out := make([]byte, 32) // AES-256
	if _, err := io.ReadFull(r, out); err != nil {
		return nil, err
	}
	return out, nil
}
