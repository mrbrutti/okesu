// Secrets + selector-driven bindings. Phase 22.8 PR γ.
//
// Encryption mirrors cloud_credentials: AES-256-GCM under an HKDF-
// derived key from the same session HMAC master, with a different
// info string ('okesu-secrets-v1') for domain separation. The CP
// never logs plaintext; callers fetch via GetSecretValue and pass
// the master key explicitly so this module doesn't reach into the
// auth layer.
//
// Bindings are loose by design: a (selector, scope) tuple per row.
// The resolver `ListSecretsForNode(nodeID, scope)` evaluates each
// binding's selector against the node's labels and returns the
// matching secrets — same selector parser PR β shipped.

package db

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"golang.org/x/crypto/hkdf"
)

// SecretKind enumerates legitimate `kind` column values. Drives
// consumer-side rendering + validation. Anything else is rejected
// by the store.
const (
	SecretKindEnvVar = "env_var"
	SecretKindSSHKey = "ssh_key"
	SecretKindAPIKey = "api_key"
)

// SecretScope enumerates legitimate `scope` column values on
// secret_bindings. Drives the resolver's filter.
const (
	SecretScopeNode     = "node"
	SecretScopeDaimon   = "daimon"
	SecretScopeAgentRun = "agent_run"
	SecretScopeAny      = "any"
)

// Secret is the metadata-only view (plaintext lives in the
// encrypted columns and only comes out via GetSecretValue).
type Secret struct {
	ID             int64          `json:"id"`
	Name           string         `json:"name"`
	Kind           string         `json:"kind"`
	Description    string         `json:"description"`
	OwnerGroupID   sql.NullInt64  `json:"owner_group_id,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	CreatedByEmail sql.NullString `json:"created_by_email,omitempty"`
}

// SecretBinding is one (selector, scope) pair attached to a secret.
type SecretBinding struct {
	ID        int64     `json:"id"`
	SecretID  int64     `json:"secret_id"`
	Selector  string    `json:"selector"`
	Scope     string    `json:"scope"`
	CreatedAt time.Time `json:"created_at"`
}

// SecretInsert carries the create payload. Plaintext value is
// sealed before persisting.
type SecretInsert struct {
	Name           string
	Kind           string
	Description    string
	Value          string // plaintext at this layer; sealed before write
	OwnerGroupID   int64  // 0 = no owner
	CreatedByEmail string
}

// CreateSecret seals the plaintext + persists. masterKey is the
// caller-supplied bytes from store.MasterKeyFromMeta (mirrors the
// cloud_credentials pattern so the store doesn't reach into auth).
func (s *Store) CreateSecret(in SecretInsert, masterKey []byte) (*Secret, error) {
	if strings.TrimSpace(in.Name) == "" {
		return nil, errors.New("secret name required")
	}
	if !isValidSecretKind(in.Kind) {
		return nil, fmt.Errorf("invalid kind %q (allowed: %s, %s, %s)",
			in.Kind, SecretKindEnvVar, SecretKindSSHKey, SecretKindAPIKey)
	}
	if in.Value == "" {
		return nil, errors.New("secret value required")
	}
	ct, nonce, err := sealSecretPayload(masterKey, []byte(in.Value))
	if err != nil {
		return nil, fmt.Errorf("seal: %w", err)
	}
	res, err := s.Exec(`
		INSERT INTO secrets (name, kind, description, encrypted_payload, payload_nonce,
		                    owner_group_id, created_by_email)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		in.Name, in.Kind, in.Description, ct, nonce,
		nullableInt64(in.OwnerGroupID), nullableStr(in.CreatedByEmail))
	if err != nil {
		if isUniqueErr(err) {
			return nil, ErrSecretNameTaken
		}
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.GetSecret(id)
}

// GetSecret returns metadata only.
func (s *Store) GetSecret(id int64) (*Secret, error) {
	row := s.QueryRow(`
		SELECT id, name, kind, description, owner_group_id, created_at, created_by_email
		  FROM secrets WHERE id = ?`, id)
	out := &Secret{}
	if err := row.Scan(&out.ID, &out.Name, &out.Kind, &out.Description,
		&out.OwnerGroupID, &out.CreatedAt, &out.CreatedByEmail); err != nil {
		return nil, err
	}
	return out, nil
}

// ListSecrets returns metadata for every row, alphabetical by name.
func (s *Store) ListSecrets() ([]Secret, error) {
	rows, err := s.Query(`
		SELECT id, name, kind, description, owner_group_id, created_at, created_by_email
		  FROM secrets ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Secret{}
	for rows.Next() {
		var sec Secret
		if err := rows.Scan(&sec.ID, &sec.Name, &sec.Kind, &sec.Description,
			&sec.OwnerGroupID, &sec.CreatedAt, &sec.CreatedByEmail); err != nil {
			return nil, err
		}
		out = append(out, sec)
	}
	return out, rows.Err()
}

// GetSecretValue decrypts and returns the plaintext. Callers should
// hold the result for the minimum lifetime needed and never log it.
// Owner-group enforcement is left to handlers — pass userID in via
// HasGroupMembership before this call.
func (s *Store) GetSecretValue(id int64, masterKey []byte) (string, error) {
	row := s.QueryRow(`SELECT encrypted_payload, payload_nonce FROM secrets WHERE id = ?`, id)
	var ct, nonce []byte
	if err := row.Scan(&ct, &nonce); err != nil {
		return "", err
	}
	pt, err := openSecretPayload(masterKey, ct, nonce)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

// UpdateSecretValue rotates the plaintext under a new nonce. Used
// by the rotation flow — never logged.
func (s *Store) UpdateSecretValue(id int64, value string, masterKey []byte) error {
	if value == "" {
		return errors.New("secret value required")
	}
	ct, nonce, err := sealSecretPayload(masterKey, []byte(value))
	if err != nil {
		return err
	}
	_, err = s.Exec(`UPDATE secrets SET encrypted_payload = ?, payload_nonce = ? WHERE id = ?`, ct, nonce, id)
	return err
}

// UpdateSecretMeta patches description + owner_group_id. Renaming
// is handled separately to avoid silent UNIQUE collisions.
func (s *Store) UpdateSecretMeta(id int64, description string, ownerGroupID int64) error {
	_, err := s.Exec(`
		UPDATE secrets SET description = ?, owner_group_id = ? WHERE id = ?`,
		description, nullableInt64(ownerGroupID), id)
	return err
}

// DeleteSecret cascades secret_bindings via the FK.
func (s *Store) DeleteSecret(id int64) error {
	_, err := s.Exec(`DELETE FROM secrets WHERE id = ?`, id)
	return err
}

// ── bindings ────────────────────────────────────────────────────────

// AddSecretBinding attaches a (selector, scope) tuple to a secret.
// Validates the selector via the parser so a typo lands as 400 at
// authoring time, not silently as "matches nothing" later.
func (s *Store) AddSecretBinding(secretID int64, selector, scope string) error {
	if scope == "" {
		scope = SecretScopeAny
	}
	if !isValidSecretScope(scope) {
		return fmt.Errorf("invalid scope %q", scope)
	}
	if _, err := ParseSelector(selector); err != nil {
		return fmt.Errorf("invalid selector: %w", err)
	}
	_, err := s.Exec(`
		INSERT INTO secret_bindings (secret_id, selector, scope)
		VALUES (?, ?, ?)
		ON CONFLICT (secret_id, selector, scope) DO NOTHING`,
		secretID, selector, scope)
	return err
}

// RemoveSecretBinding deletes one row by id (caller looks up the id
// from ListSecretBindings).
func (s *Store) RemoveSecretBinding(bindingID int64) error {
	_, err := s.Exec(`DELETE FROM secret_bindings WHERE id = ?`, bindingID)
	return err
}

// ListSecretBindings returns every binding attached to a secret.
func (s *Store) ListSecretBindings(secretID int64) ([]SecretBinding, error) {
	rows, err := s.Query(`
		SELECT id, secret_id, selector, scope, created_at
		  FROM secret_bindings WHERE secret_id = ?
		 ORDER BY scope, selector`, secretID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SecretBinding{}
	for rows.Next() {
		var b SecretBinding
		if err := rows.Scan(&b.ID, &b.SecretID, &b.Selector, &b.Scope, &b.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// ── resolver ────────────────────────────────────────────────────────

// ResolvedSecret pairs a Secret with the binding that picked it.
// Used by consumers that want both — e.g. a deploy-flow log line
// "matched binding env=prod for ssh_key 'prod-deploy'".
type ResolvedSecret struct {
	Secret  Secret
	Binding SecretBinding
}

// ListSecretsForNode evaluates every binding's selector against the
// node's labels and returns matching secrets. `scope` filters by
// binding scope: pass SecretScopeAny to ignore scope, or one of the
// specific values to narrow.
//
// 'any'-scoped bindings always match the scope filter — they apply
// everywhere — so callers asking for `node`-scope still see them.
//
// Optional `kind` filter further narrows to one secret kind. Empty
// kind matches everything.
//
// Returns secrets in deterministic order: by name (so two daemons
// reading the same scope see env vars in the same order on each
// poll).
func (s *Store) ListSecretsForNode(nodeID int64, scope, kind string) ([]ResolvedSecret, error) {
	labels, err := s.ListNodeLabels(nodeID)
	if err != nil {
		return nil, err
	}
	q := `
		SELECT s.id, s.name, s.kind, s.description, s.owner_group_id,
		       s.created_at, s.created_by_email,
		       b.id, b.secret_id, b.selector, b.scope, b.created_at
		  FROM secret_bindings b
		  JOIN secrets s ON s.id = b.secret_id
		 WHERE 1=1`
	args := []any{}
	if scope != "" && scope != SecretScopeAny {
		// Match the requested scope OR the wildcard 'any'.
		q += ` AND (b.scope = ? OR b.scope = ?)`
		args = append(args, scope, SecretScopeAny)
	}
	if kind != "" {
		q += ` AND s.kind = ?`
		args = append(args, kind)
	}
	q += ` ORDER BY s.name`
	rows, err := s.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ResolvedSecret
	for rows.Next() {
		var r ResolvedSecret
		if err := rows.Scan(
			&r.Secret.ID, &r.Secret.Name, &r.Secret.Kind, &r.Secret.Description,
			&r.Secret.OwnerGroupID, &r.Secret.CreatedAt, &r.Secret.CreatedByEmail,
			&r.Binding.ID, &r.Binding.SecretID, &r.Binding.Selector, &r.Binding.Scope, &r.Binding.CreatedAt,
		); err != nil {
			return nil, err
		}
		// Evaluate the selector. ParseSelector returns match-all on
		// empty input, so CP-wide bindings always match here.
		sel, perr := ParseSelector(r.Binding.Selector)
		if perr != nil {
			// Malformed bindings get skipped silently — the audit
			// logger could flag these; for now we err on the safe
			// side (don't expose a credential through a bad
			// selector that we can't reason about).
			continue
		}
		if sel.Matches(labels) {
			out = append(out, r)
		}
	}
	return out, rows.Err()
}

// ── encryption helpers ──────────────────────────────────────────────

func sealSecretPayload(masterKey, plaintext []byte) ([]byte, []byte, error) {
	key, err := deriveSecretKey(masterKey)
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
	return gcm.Seal(nil, nonce, plaintext, nil), nonce, nil
}

func openSecretPayload(masterKey, ciphertext, nonce []byte) ([]byte, error) {
	key, err := deriveSecretKey(masterKey)
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

func deriveSecretKey(masterKey []byte) ([]byte, error) {
	if len(masterKey) < 16 {
		return nil, errors.New("master key too short (need >= 16 bytes)")
	}
	r := hkdf.New(sha256.New, masterKey, nil, []byte("okesu-secrets-v1"))
	out := make([]byte, 32) // AES-256
	if _, err := io.ReadFull(r, out); err != nil {
		return nil, err
	}
	return out, nil
}

// ── validation ──────────────────────────────────────────────────────

func isValidSecretKind(k string) bool {
	switch k {
	case SecretKindEnvVar, SecretKindSSHKey, SecretKindAPIKey:
		return true
	}
	return false
}

func isValidSecretScope(s string) bool {
	switch s {
	case SecretScopeNode, SecretScopeDaimon, SecretScopeAgentRun, SecretScopeAny:
		return true
	}
	return false
}

// ErrSecretNameTaken signals a UNIQUE collision on (name).
var ErrSecretNameTaken = errors.New("secret name already taken")
