// CP bootstrap tokens — one-time-use credentials a parent CP hands
// out as part of a "spin up a child CP" bundle. Verified once on the
// child's first /api/v1/cp/bootstrap call, then permanently
// invalidated and replaced with a long-lived rotating peer token.
//
// Same prefix-index + bcrypt scheme as api_tokens.go: the first 16
// characters of the random tail are stored plaintext for O(1) DB
// lookup; the full token is bcrypt-hashed under token_hash. We do
// the prefix split inside this file so the surface presented to the
// rest of the CP is just IssueBootstrapToken / VerifyBootstrapToken
// / MarkBootstrapTokenUsed.

package db

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// bcryptInputForBootstrap pre-hashes the plaintext token with
// SHA-256 + hex-encode so the resulting 64-byte input always fits
// bcrypt's 72-byte limit. Our prefix ("okesu_cpboot_") + 32 random
// bytes hex (= 64 chars) total 77, which would otherwise be silently
// truncated. SHA-256 normalises the input length without losing
// any of the original entropy.
func bcryptInputForBootstrap(plaintext string) []byte {
	sum := sha256.Sum256([]byte(plaintext))
	hexed := hex.EncodeToString(sum[:])
	return []byte(hexed)
}

// CPBootstrapTokenPrefix is the literal prefix every bootstrap token
// starts with — easy to grep for in logs / leaked configs / shell
// history. Distinct from API tokens (TokenPrefix = "okesu_") so an
// operator never confuses one for the other.
const CPBootstrapTokenPrefix = "okesu_cpboot_"

// CPBootstrapTokenPrefixIdxLen is how many characters of the random
// tail are stored in plaintext for the index lookup.
const CPBootstrapTokenPrefixIdxLen = 16

// CPBootstrapTokenTTL is how long a freshly-issued bootstrap token
// stays valid. Short on purpose — the bundle is meant to be applied
// immediately; an old unused token is more risk than convenience.
const CPBootstrapTokenTTL = 24 * time.Hour

// CPBootstrapToken is the persisted row for an issued bootstrap
// token. The plaintext value is only available at issue time — once
// returned to the caller, this struct never holds it.
type CPBootstrapToken struct {
	ID            int64
	TokenPrefix   string
	DisplayName   string
	Region        string
	ParentURL     string
	CreatedByUser sql.NullInt64
	CreatedByEmail sql.NullString
	CreatedAt     time.Time
	ExpiresAt     time.Time
	UsedAt        sql.NullTime
	UsedPeerID    sql.NullInt64
	UsedFromURL   sql.NullString
}

// IssueCPBootstrapToken mints a new bootstrap token and returns the
// plaintext (caller must show it once and forget) plus the row id.
// The plaintext format is "okesu_cpboot_<32-byte-hex>" — caller can
// log the prefix safely but never the full value.
func (s *Store) IssueCPBootstrapToken(displayName, region, parentURL string, createdByUserID int64, createdByEmail string) (plaintext string, id int64, err error) {
	tail := make([]byte, 32)
	if _, err := rand.Read(tail); err != nil {
		return "", 0, fmt.Errorf("rand: %w", err)
	}
	tailHex := hex.EncodeToString(tail)
	plaintext = CPBootstrapTokenPrefix + tailHex
	prefix := tailHex[:CPBootstrapTokenPrefixIdxLen]

	hash, err := bcrypt.GenerateFromPassword(bcryptInputForBootstrap(plaintext), bcrypt.DefaultCost)
	if err != nil {
		return "", 0, fmt.Errorf("bcrypt: %w", err)
	}

	expiresAt := time.Now().Add(CPBootstrapTokenTTL)
	res, err := s.Exec(`
		INSERT INTO cp_bootstrap_tokens (
		    token_prefix, token_hash, display_name, region, parent_url,
		    created_by_user_id, created_by_email, expires_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, prefix, string(hash), displayName, region, parentURL,
		nullableInt64(createdByUserID), nullable(createdByEmail), expiresAt)
	if err != nil {
		return "", 0, fmt.Errorf("insert: %w", err)
	}
	id, err = res.LastInsertId()
	if err != nil {
		return "", 0, fmt.Errorf("lastinsertid: %w", err)
	}
	return plaintext, id, nil
}

// VerifyCPBootstrapToken accepts a plaintext token, validates it
// exists, isn't expired, and isn't already used. Returns the row
// on success. Errors are deliberately non-specific to avoid leaking
// whether a particular prefix exists.
func (s *Store) VerifyCPBootstrapToken(presented string) (*CPBootstrapToken, error) {
	if !strings.HasPrefix(presented, CPBootstrapTokenPrefix) {
		return nil, errors.New("invalid bootstrap token")
	}
	tail := presented[len(CPBootstrapTokenPrefix):]
	if len(tail) < CPBootstrapTokenPrefixIdxLen {
		return nil, errors.New("invalid bootstrap token")
	}
	prefix := tail[:CPBootstrapTokenPrefixIdxLen]

	row := s.QueryRow(`
		SELECT id, token_prefix, token_hash, display_name, region, parent_url,
		       created_by_user_id, created_by_email, created_at, expires_at,
		       used_at, used_peer_id, used_from_url
		FROM cp_bootstrap_tokens
		WHERE token_prefix = ?
	`, prefix)
	var t CPBootstrapToken
	var hash string
	if err := row.Scan(&t.ID, &t.TokenPrefix, &hash, &t.DisplayName, &t.Region, &t.ParentURL,
		&t.CreatedByUser, &t.CreatedByEmail, &t.CreatedAt, &t.ExpiresAt,
		&t.UsedAt, &t.UsedPeerID, &t.UsedFromURL); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("invalid bootstrap token")
		}
		return nil, fmt.Errorf("query: %w", err)
	}
	if t.UsedAt.Valid {
		return nil, errors.New("bootstrap token already used")
	}
	if time.Now().After(t.ExpiresAt) {
		return nil, errors.New("bootstrap token expired")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), bcryptInputForBootstrap(presented)); err != nil {
		return nil, errors.New("invalid bootstrap token")
	}
	return &t, nil
}

// MarkCPBootstrapTokenUsed flips the row to used and links the
// federation_peers row that was created for the new child. Idempotent
// per row — calling twice errors with "already used" via the verify
// path so this only runs after a successful Verify.
func (s *Store) MarkCPBootstrapTokenUsed(id, peerID int64, fromURL string) error {
	_, err := s.Exec(`
		UPDATE cp_bootstrap_tokens
		   SET used_at = CURRENT_TIMESTAMP,
		       used_peer_id = ?,
		       used_from_url = ?
		 WHERE id = ?
		   AND used_at IS NULL
	`, peerID, fromURL, id)
	return err
}

// ListCPBootstrapTokens returns recent tokens (used + unused) for the
// admin Federation page so operators can see what's been issued.
// Plaintext is never returned — only the prefix + metadata.
func (s *Store) ListCPBootstrapTokens(limit int) ([]CPBootstrapToken, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.Query(`
		SELECT id, token_prefix, token_hash, display_name, region, parent_url,
		       created_by_user_id, created_by_email, created_at, expires_at,
		       used_at, used_peer_id, used_from_url
		FROM cp_bootstrap_tokens
		ORDER BY created_at DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CPBootstrapToken
	for rows.Next() {
		var t CPBootstrapToken
		var hash string
		if err := rows.Scan(&t.ID, &t.TokenPrefix, &hash, &t.DisplayName, &t.Region, &t.ParentURL,
			&t.CreatedByUser, &t.CreatedByEmail, &t.CreatedAt, &t.ExpiresAt,
			&t.UsedAt, &t.UsedPeerID, &t.UsedFromURL); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
