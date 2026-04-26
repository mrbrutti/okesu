// Package auth implements session-based authentication for the Control Plane.
package auth

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/section9labs/okesu/controlplane/db"
	"golang.org/x/crypto/bcrypt"
)

// SeedAdmin ensures an admin user exists in the database.
//
// Behavior:
//   - If a user with the given email already exists, no action is taken
//     and the existing user is returned. The supplied password is ignored.
//   - If no user exists, password must be non-empty; a new admin user is
//     created with a bcrypt-hashed password.
func SeedAdmin(store *db.Store, email, password string) (*db.User, error) {
	existing, err := store.UserByEmail(email)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("lookup admin: %w", err)
	}

	if password == "" {
		return nil, fmt.Errorf("admin user %q does not exist and no password was provided", email)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	id, err := store.CreateUser(email, string(hash), "admin")
	if err != nil {
		return nil, fmt.Errorf("create admin: %w", err)
	}
	return store.UserByID(id)
}

// VerifyPassword returns the user if email+password match, or an error otherwise.
// Errors do not distinguish "no such user" from "wrong password" to avoid
// account enumeration.
func VerifyPassword(store *db.Store, email, password string) (*db.User, error) {
	u, err := store.UserByEmail(email)
	if err != nil {
		// Run a fake bcrypt comparison anyway to keep timing similar.
		_ = bcrypt.CompareHashAndPassword([]byte("$2a$10$abcdefghijklmnopqrstuv"), []byte(password))
		return nil, ErrInvalidCredentials
	}
	if !u.PasswordHash.Valid {
		return nil, ErrInvalidCredentials
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash.String), []byte(password)); err != nil {
		return nil, ErrInvalidCredentials
	}
	return u, nil
}

// ErrInvalidCredentials is returned by VerifyPassword on any auth failure.
var ErrInvalidCredentials = errors.New("invalid credentials")
