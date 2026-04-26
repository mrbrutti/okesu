package db

import (
	"database/sql"
	"time"
)

// APIToken is a programmatic-access credential.
//
// Wire format presented to operators: "okesu_<32-hex>". The CP stores the
// bcrypt of the full token plus a 16-char prefix for fast lookup.
type APIToken struct {
	ID              int64
	Name            string
	Prefix          string
	Hash            string
	Scopes          string // CSV
	CreatedByUserID sql.NullInt64
	CreatedByEmail  sql.NullString
	CreatedAt       time.Time
	ExpiresAt       sql.NullTime
	LastUsedAt      sql.NullTime
	RevokedAt       sql.NullTime
}

// CreateAPIToken inserts a new token row.
func (s *Store) CreateAPIToken(name, prefix, hash, scopes string,
	createdByUserID int64, createdByEmail string,
	expiresAt sql.NullTime,
) (int64, error) {
	var userID sql.NullInt64
	if createdByUserID > 0 {
		userID = sql.NullInt64{Int64: createdByUserID, Valid: true}
	}
	res, err := s.Exec(`
		INSERT INTO api_tokens (name, prefix, hash, scopes, created_by_user_id, created_by_email, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, name, prefix, hash, scopes, userID, nullable(createdByEmail), expiresAt)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// APITokenByPrefix returns the token row matching prefix (active, not
// expired, not revoked) for verification.
func (s *Store) APITokenByPrefix(prefix string) (*APIToken, error) {
	t := &APIToken{}
	err := s.QueryRow(`
		SELECT id, name, prefix, hash, scopes, created_by_user_id, created_by_email,
		       created_at, expires_at, last_used_at, revoked_at
		FROM api_tokens
		WHERE prefix = ?
		  AND revoked_at IS NULL
		  AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)
	`, prefix).Scan(
		&t.ID, &t.Name, &t.Prefix, &t.Hash, &t.Scopes,
		&t.CreatedByUserID, &t.CreatedByEmail, &t.CreatedAt,
		&t.ExpiresAt, &t.LastUsedAt, &t.RevokedAt,
	)
	if err != nil {
		return nil, err
	}
	return t, nil
}

// MarkAPITokenUsed updates last_used_at. Best-effort.
func (s *Store) MarkAPITokenUsed(id int64) error {
	_, err := s.Exec(`UPDATE api_tokens SET last_used_at = CURRENT_TIMESTAMP WHERE id = ?`, id)
	return err
}

// ListAPITokens returns every token, newest first. The hash is included
// in the row but never sent to the UI by callers.
func (s *Store) ListAPITokens() ([]*APIToken, error) {
	rows, err := s.Query(`
		SELECT id, name, prefix, hash, scopes, created_by_user_id, created_by_email,
		       created_at, expires_at, last_used_at, revoked_at
		FROM api_tokens ORDER BY id DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*APIToken
	for rows.Next() {
		t := &APIToken{}
		if err := rows.Scan(
			&t.ID, &t.Name, &t.Prefix, &t.Hash, &t.Scopes,
			&t.CreatedByUserID, &t.CreatedByEmail, &t.CreatedAt,
			&t.ExpiresAt, &t.LastUsedAt, &t.RevokedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RevokeAPIToken marks a token revoked. Cannot be undone — issue a new one.
func (s *Store) RevokeAPIToken(id int64) error {
	_, err := s.Exec(`
		UPDATE api_tokens SET revoked_at = CURRENT_TIMESTAMP
		WHERE id = ? AND revoked_at IS NULL
	`, id)
	return err
}
