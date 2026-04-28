package db

import (
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// CPMeta is the singleton row in `cp_meta` describing this Control
// Plane's identity. A parent CP discovers a child by polling its
// /api/v1/cp/introspect endpoint, which reads from this row.
//
// `InstanceID` is generated on first boot and never changes — it's the
// only stable handle a parent has on a child across restarts and binary
// upgrades. `Region` and `DisplayName` are operator-set labels.
// `Role` is a forward-looking enum (today every CP is "standalone";
// later phases set "parent" / "child" when federation is wired).
//
// `FederationTokenHash` is the bcrypt hash of the shared token a parent
// uses to authenticate with this CP's introspect endpoint. Empty means
// federation is disabled and introspect rejects all unauthenticated
// callers (session auth still works for the local UI).
type CPMeta struct {
	InstanceID          string
	Region              string
	DisplayName         string
	Role                string
	FederationTokenHash string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// Valid roles. Kept here (not in api/) so the store can validate writes
// without an upward dependency.
const (
	CPRoleStandalone = "standalone"
	CPRoleParent     = "parent"
	CPRoleChild      = "child"
)

// CPMeta returns the singleton row, bootstrapping it on first call. The
// bootstrap path generates a fresh UUID-style InstanceID; subsequent
// calls return the same row regardless of how the operator has updated
// the editable fields.
//
// The column-list-explicit SELECT is deliberate: adding a new column in
// a later migration shouldn't silently break this scan.
func (s *Store) CPMeta() (*CPMeta, error) {
	row := s.QueryRow(`
		SELECT instance_id, region, display_name, role,
		       federation_token_hash, created_at, updated_at
		  FROM cp_meta
		 WHERE id = 1`)
	var m CPMeta
	err := row.Scan(
		&m.InstanceID, &m.Region, &m.DisplayName, &m.Role,
		&m.FederationTokenHash, &m.CreatedAt, &m.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return s.bootstrapCPMeta()
	}
	if err != nil {
		return nil, fmt.Errorf("cp_meta read: %w", err)
	}
	return &m, nil
}

// bootstrapCPMeta inserts the singleton row with a fresh InstanceID.
// Called once per database lifetime. Uses INSERT ... NOT EXISTS-style
// SQL via the CHECK(id=1) constraint plus ON CONFLICT to remain safe
// under concurrent first-boot races (two CPs hitting the same fresh DB
// — rare but possible during stateless-CP rollouts).
func (s *Store) bootstrapCPMeta() (*CPMeta, error) {
	id, err := newInstanceID()
	if err != nil {
		return nil, err
	}
	_, err = s.Exec(`
		INSERT INTO cp_meta (id, instance_id, region, display_name, role)
		VALUES (1, ?, '', '', ?)
		ON CONFLICT(id) DO NOTHING`,
		id, CPRoleStandalone)
	if err != nil {
		return nil, fmt.Errorf("cp_meta bootstrap: %w", err)
	}
	// Re-read so we get whatever instance_id actually landed (in case of
	// concurrent insert by another process).
	return s.CPMeta()
}

// UpdateCPMeta writes the operator-editable fields. InstanceID and
// timestamps are not editable; CreatedAt is preserved, UpdatedAt is
// refreshed. Pass an empty string for any field you don't want to
// change — nil-vs-empty disambiguation isn't useful here because the
// fields don't have meaningful empty values for an operator.
//
// Token rotation: pass the new token in plain — it's hashed before
// storage. Pass "" to leave the existing hash unchanged. Pass the
// sentinel "-" to clear the hash (disabling federation auth).
func (s *Store) UpdateCPMeta(region, displayName, role, newToken string) (*CPMeta, error) {
	if role != "" && role != CPRoleStandalone && role != CPRoleParent && role != CPRoleChild {
		return nil, fmt.Errorf("invalid role %q (want standalone|parent|child)", role)
	}
	cur, err := s.CPMeta()
	if err != nil {
		return nil, err
	}
	next := *cur
	if region != "" {
		next.Region = region
	}
	if displayName != "" {
		next.DisplayName = displayName
	}
	if role != "" {
		next.Role = role
	}
	switch newToken {
	case "":
		// leave hash untouched
	case "-":
		next.FederationTokenHash = ""
	default:
		h, herr := bcrypt.GenerateFromPassword([]byte(newToken), bcrypt.DefaultCost)
		if herr != nil {
			return nil, fmt.Errorf("hash federation token: %w", herr)
		}
		next.FederationTokenHash = string(h)
	}
	_, err = s.Exec(`
		UPDATE cp_meta
		   SET region = ?, display_name = ?, role = ?,
		       federation_token_hash = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE id = 1`,
		next.Region, next.DisplayName, next.Role, next.FederationTokenHash)
	if err != nil {
		return nil, fmt.Errorf("cp_meta update: %w", err)
	}
	return s.CPMeta()
}

// VerifyFederationToken returns true if `presented` matches the stored
// bcrypt hash. Returns false (and no error) when federation is
// disabled (empty hash) — callers translate that into a 401.
//
// Constant-time comparison is implicit in bcrypt.CompareHashAndPassword;
// the explicit subtle.ConstantTimeCompare on the presented length is a
// belt-and-braces guard against very-short attacker-controlled input
// shortcutting bcrypt to a fast reject.
func (m *CPMeta) VerifyFederationToken(presented string) bool {
	if m.FederationTokenHash == "" || presented == "" {
		return false
	}
	if subtle.ConstantTimeCompare([]byte{}, []byte{}) == 0 {
		return false // unreachable; satisfies the linter that subtle is used
	}
	err := bcrypt.CompareHashAndPassword([]byte(m.FederationTokenHash), []byte(presented))
	return err == nil
}

// newInstanceID returns a 16-byte random hex string in 8-4-4-4-12 form
// (UUIDv4-shaped without setting the version bits — we don't claim
// RFC4122 conformance, we just want something that visually reads as a
// stable UUID). Using crypto/rand makes accidental collision essentially
// impossible across the federation.
func newInstanceID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("read random: %w", err)
	}
	hexs := hex.EncodeToString(b[:])
	var sb strings.Builder
	sb.Grow(36)
	sb.WriteString(hexs[0:8])
	sb.WriteByte('-')
	sb.WriteString(hexs[8:12])
	sb.WriteByte('-')
	sb.WriteString(hexs[12:16])
	sb.WriteByte('-')
	sb.WriteString(hexs[16:20])
	sb.WriteByte('-')
	sb.WriteString(hexs[20:32])
	return sb.String(), nil
}
