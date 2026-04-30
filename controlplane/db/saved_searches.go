// Saved searches — operator-named filter sets per (user, scope).
//
// Findings is the only consumer in v1, but the table is generic so
// future surfaces (cases, runs, IOCs) get the same primitive without
// a migration. Callers pass `scope` as a free-form string and stash
// whatever filter shape they want into `config_json` — the store
// is opaque to it.
//
// `is_default` per-(user, scope) tags one search to apply on page
// load. The setter clears any previous default for the same
// (user, scope) so the invariant holds — a stray non-zero flag from
// an earlier broken write would still be tolerated by the read path
// (we ORDER BY updated_at DESC and pick the first), but the writer
// makes it not happen.

package db

import (
	"database/sql"
	"errors"
	"strings"
)

// SavedSearch is one row.
type SavedSearch struct {
	ID         int64  `json:"id"`
	UserID     int64  `json:"user_id"`
	Name       string `json:"name"`
	Scope      string `json:"scope"`
	ConfigJSON string `json:"config_json"`
	IsDefault  bool   `json:"is_default"`
	CreatedAt  string `json:"created_at"` // ISO; emitted as-is from sqlite
	UpdatedAt  string `json:"updated_at"`
}

// SavedSearchInsert is the create payload.
type SavedSearchInsert struct {
	UserID     int64
	Name       string
	Scope      string
	ConfigJSON string
	IsDefault  bool
}

// SavedSearchPatch carries optional updates. Empty/nil fields are
// preserved; the IsDefault pointer disambiguates "set to false"
// from "leave unchanged".
type SavedSearchPatch struct {
	Name       *string
	ConfigJSON *string
	IsDefault  *bool
}

// CreateSavedSearch inserts a row. Returns ErrSavedSearchNameTaken
// when (user, scope, name) collides — callers that want upsert can
// catch and route to UpdateSavedSearch instead.
//
// When IsDefault=true, the prior default for the same (user, scope)
// is cleared in the same transaction so the invariant holds.
func (s *Store) CreateSavedSearch(in *SavedSearchInsert) (int64, error) {
	if in.UserID == 0 || in.Name == "" || in.Scope == "" {
		return 0, errors.New("user_id, name, scope required")
	}
	tx, err := s.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck

	if in.IsDefault {
		if _, err := tx.Exec(`
			UPDATE saved_searches SET is_default = 0
			 WHERE user_id = ? AND scope = ?`,
			in.UserID, in.Scope); err != nil {
			return 0, err
		}
	}
	def := 0
	if in.IsDefault {
		def = 1
	}
	res, err := tx.Exec(`
		INSERT INTO saved_searches (user_id, name, scope, config_json, is_default)
		VALUES (?, ?, ?, ?, ?)`,
		in.UserID, in.Name, in.Scope, in.ConfigJSON, def)
	if err != nil {
		if isUniqueErr(err) {
			return 0, ErrSavedSearchNameTaken
		}
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// ListSavedSearches returns saved searches for the user under the
// given scope, default-first, then alphabetical by name. Empty list
// (not nil) when nothing matches.
func (s *Store) ListSavedSearches(userID int64, scope string) ([]SavedSearch, error) {
	rows, err := s.Query(`
		SELECT id, user_id, name, scope, config_json, is_default,
		       CAST(created_at AS TEXT), CAST(updated_at AS TEXT)
		FROM saved_searches
		WHERE user_id = ? AND scope = ?
		ORDER BY is_default DESC, name ASC`,
		userID, scope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SavedSearch{}
	for rows.Next() {
		var ss SavedSearch
		var def int64
		if err := rows.Scan(&ss.ID, &ss.UserID, &ss.Name, &ss.Scope,
			&ss.ConfigJSON, &def, &ss.CreatedAt, &ss.UpdatedAt); err != nil {
			return nil, err
		}
		ss.IsDefault = def != 0
		out = append(out, ss)
	}
	return out, rows.Err()
}

// UpdateSavedSearch applies a patch. Pass nil pointers for fields you
// don't want to change. Setting IsDefault=true clears the prior
// default for the (user, scope) in the same transaction.
//
// Renames go through here too — UNIQUE collisions surface as
// ErrSavedSearchNameTaken.
func (s *Store) UpdateSavedSearch(userID, id int64, patch *SavedSearchPatch) error {
	if patch == nil {
		return nil
	}
	tx, err := s.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	// Verify ownership before any UPDATE so unauthorized callers
	// can't probe by id.
	var ownerID int64
	var scope string
	if err := tx.QueryRow(`SELECT user_id, scope FROM saved_searches WHERE id = ?`, id).Scan(&ownerID, &scope); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSavedSearchNotFound
		}
		return err
	}
	if ownerID != userID {
		return ErrSavedSearchNotFound // don't disclose existence
	}

	if patch.IsDefault != nil && *patch.IsDefault {
		if _, err := tx.Exec(`
			UPDATE saved_searches SET is_default = 0
			 WHERE user_id = ? AND scope = ? AND id != ?`,
			userID, scope, id); err != nil {
			return err
		}
	}

	// Build the SET clause from non-nil patch fields.
	sets := []string{"updated_at = CURRENT_TIMESTAMP"}
	args := []any{}
	if patch.Name != nil {
		sets = append(sets, "name = ?")
		args = append(args, *patch.Name)
	}
	if patch.ConfigJSON != nil {
		sets = append(sets, "config_json = ?")
		args = append(args, *patch.ConfigJSON)
	}
	if patch.IsDefault != nil {
		sets = append(sets, "is_default = ?")
		v := 0
		if *patch.IsDefault {
			v = 1
		}
		args = append(args, v)
	}
	args = append(args, id)
	q := "UPDATE saved_searches SET " + strings.Join(sets, ", ") + " WHERE id = ?"
	if _, err := tx.Exec(q, args...); err != nil {
		if isUniqueErr(err) {
			return ErrSavedSearchNameTaken
		}
		return err
	}
	return tx.Commit()
}

// DeleteSavedSearch removes a row. Owner check is implicit in the
// WHERE clause so unauthorized callers see no rows affected.
// Idempotent (returns nil even if the row doesn't exist).
func (s *Store) DeleteSavedSearch(userID, id int64) error {
	_, err := s.Exec(`DELETE FROM saved_searches WHERE id = ? AND user_id = ?`, id, userID)
	return err
}

// ErrSavedSearchNameTaken signals a UNIQUE collision on (user, scope,
// name). Callers can present "rename or replace existing?" UX rather
// than just an opaque 500.
var ErrSavedSearchNameTaken = errors.New("saved search name already taken in this scope")

// ErrSavedSearchNotFound is returned by Update when the id doesn't
// exist OR belongs to another user (we don't disclose the
// difference).
var ErrSavedSearchNotFound = errors.New("saved search not found")

// isUniqueErr is a dialect-tolerant check for UNIQUE constraint
// failures. SQLite's modernc driver wraps the error as text;
// postgres returns a typed code. The string match is brittle but
// good enough for the one constraint we care about — the alternative
// is dragging the postgres-specific error machinery into this file.
func isUniqueErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE") ||
		strings.Contains(msg, "duplicate key") ||
		strings.Contains(msg, "constraint")
}
