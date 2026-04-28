package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// FederationPeer is one child CP this CP polls. The poller writes
// LastPolledAt + LastSeenAt + LastError + IntrospectJSON; the API
// /api/federation/peers POST handler writes the static fields.
type FederationPeer struct {
	ID             int64
	URL            string
	DisplayName    string
	Token          string         // plaintext — sent outbound on each poll
	AddedAt        time.Time
	LastPolledAt   sql.NullTime
	LastSeenAt     sql.NullTime
	LastError      string
	IntrospectJSON string
}

// AddFederationPeer registers a new child CP. Returns ErrDuplicatePeer
// if `url` is already registered (the unique constraint catches the
// race between two operators clicking Add at the same moment).
//
// We deliberately don't run an introspect probe here — the API layer
// does that before calling so the operator gets the auth-failure
// feedback immediately rather than via the poller's next tick. Doing
// it here too would be duplicate work.
func (s *Store) AddFederationPeer(url, displayName, token string) (*FederationPeer, error) {
	url = strings.TrimRight(strings.TrimSpace(url), "/")
	if url == "" || token == "" {
		return nil, errors.New("federation peer: url and token required")
	}
	res, err := s.Exec(
		`INSERT INTO federation_peers (url, display_name, token) VALUES (?, ?, ?)`,
		url, displayName, token,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrDuplicatePeer
		}
		return nil, fmt.Errorf("insert federation peer: %w", err)
	}
	id, _ := res.LastInsertId()
	return s.FederationPeer(id)
}

// ErrDuplicatePeer is returned when AddFederationPeer races against
// another insert with the same URL. Callers map this to a 409.
var ErrDuplicatePeer = errors.New("federation peer already registered")

// FederationPeer fetches one peer by id.
func (s *Store) FederationPeer(id int64) (*FederationPeer, error) {
	row := s.QueryRow(`
		SELECT id, url, display_name, token, added_at,
		       last_polled_at, last_seen_at, last_error, introspect_json
		  FROM federation_peers WHERE id = ?`, id)
	return scanFederationPeer(row)
}

// ListFederationPeers returns all peers ordered by addition time.
// No pagination — federation lists are small by construction (an
// operator manages at most dozens of children, not thousands).
func (s *Store) ListFederationPeers() ([]FederationPeer, error) {
	rows, err := s.Query(`
		SELECT id, url, display_name, token, added_at,
		       last_polled_at, last_seen_at, last_error, introspect_json
		  FROM federation_peers
		 ORDER BY added_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FederationPeer
	for rows.Next() {
		p, err := scanFederationPeer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// DeleteFederationPeer removes a peer by id. Idempotent — deleting
// a non-existent row is not an error (matches the REST DELETE
// idempotency convention used elsewhere in this codebase).
func (s *Store) DeleteFederationPeer(id int64) error {
	_, err := s.Exec(`DELETE FROM federation_peers WHERE id = ?`, id)
	return err
}

// RecordPeerSuccess updates the row after a successful introspect
// poll. Called from the poller. Always clears LastError so a
// previously-failing peer flips back to clean status.
func (s *Store) RecordPeerSuccess(id int64, introspectJSON string) error {
	_, err := s.Exec(`
		UPDATE federation_peers
		   SET last_polled_at = CURRENT_TIMESTAMP,
		       last_seen_at   = CURRENT_TIMESTAMP,
		       last_error     = '',
		       introspect_json = ?
		 WHERE id = ?`, introspectJSON, id)
	return err
}

// RecordPeerFailure updates last_polled_at and last_error but leaves
// last_seen_at and introspect_json alone — the UI keeps showing the
// most recent successful snapshot while flagging the peer as stale.
func (s *Store) RecordPeerFailure(id int64, errMsg string) error {
	_, err := s.Exec(`
		UPDATE federation_peers
		   SET last_polled_at = CURRENT_TIMESTAMP,
		       last_error     = ?
		 WHERE id = ?`, errMsg, id)
	return err
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanFederationPeer(r rowScanner) (*FederationPeer, error) {
	var p FederationPeer
	err := r.Scan(
		&p.ID, &p.URL, &p.DisplayName, &p.Token, &p.AddedAt,
		&p.LastPolledAt, &p.LastSeenAt, &p.LastError, &p.IntrospectJSON,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, sql.ErrNoRows
	}
	if err != nil {
		return nil, fmt.Errorf("scan federation peer: %w", err)
	}
	return &p, nil
}

// isUniqueViolation returns true for both SQLite ("UNIQUE constraint
// failed") and Postgres ("duplicate key value") unique violations.
// Used by Add* helpers so the API layer can map to a 409.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") ||
		strings.Contains(msg, "duplicate key value")
}
