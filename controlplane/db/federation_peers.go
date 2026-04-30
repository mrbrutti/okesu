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
//
// Transport: 'https_pull' (default) means the parent's poller GETs
// https://<URL>/api/v1/cp/introspect. 's3_dead_drop' means the
// parent's s3reader reads {BucketPrefix}/introspect.json out of the
// transport_config_id-pointed bucket. Same row shape, different
// dataflow — last_seen_at + introspect_json are still the
// authoritative cache the rest of the federation aggregator reads
// from, so HealthyPeers / FetchJSON-like helpers don't change.
type FederationPeer struct {
	ID                int64
	URL               string
	DisplayName       string
	Token             string         // plaintext — sent outbound on each poll (https_pull)
	AddedAt           time.Time
	LastPolledAt      sql.NullTime
	LastSeenAt        sql.NullTime
	LastError         string
	IntrospectJSON    string
	Transport         string         // 'https_pull' | 's3_dead_drop'
	BucketPrefix      sql.NullString // s3_dead_drop only: 'cp/<child-id>/outbound/<this-cp-id>/'
	TransportConfigID sql.NullInt64  // s3_dead_drop only: -> transport_configs.id
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
		       last_polled_at, last_seen_at, last_error, introspect_json,
		       transport, bucket_prefix, transport_config_id
		  FROM federation_peers WHERE id = ?`, id)
	return scanFederationPeer(row)
}

// ListFederationPeers returns all peers ordered by addition time.
// No pagination — federation lists are small by construction (an
// operator manages at most dozens of children, not thousands).
func (s *Store) ListFederationPeers() ([]FederationPeer, error) {
	rows, err := s.Query(`
		SELECT id, url, display_name, token, added_at,
		       last_polled_at, last_seen_at, last_error, introspect_json,
		       transport, bucket_prefix, transport_config_id
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
		&p.Transport, &p.BucketPrefix, &p.TransportConfigID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, sql.ErrNoRows
	}
	if err != nil {
		return nil, fmt.Errorf("scan federation peer: %w", err)
	}
	return &p, nil
}

// AddS3FederationPeer registers a child CP that publishes via S3
// dead-drop instead of inbound HTTPS. URL is synthesized from the
// bucket prefix so the existing UNIQUE(url) constraint catches
// double-registrations without colliding with real https URLs.
//
// transportConfigID points at the transport_configs row whose
// bucket creds the parent's s3reader will use to GET
// {bucketPrefix}/introspect.json on a tick.
func (s *Store) AddS3FederationPeer(displayName, bucketPrefix string, transportConfigID int64, token string) (*FederationPeer, error) {
	bucketPrefix = strings.TrimRight(strings.TrimSpace(bucketPrefix), "/") + "/"
	if bucketPrefix == "/" {
		return nil, errors.New("federation peer: bucket_prefix required")
	}
	syntheticURL := "s3-deaddrop://" + strings.TrimSuffix(bucketPrefix, "/")
	res, err := s.Exec(`
		INSERT INTO federation_peers
			(url, display_name, token, transport, bucket_prefix, transport_config_id)
		VALUES (?, ?, ?, 's3_dead_drop', ?, ?)
	`, syntheticURL, displayName, token, bucketPrefix, transportConfigID)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrDuplicatePeer
		}
		return nil, fmt.Errorf("insert s3 federation peer: %w", err)
	}
	id, _ := res.LastInsertId()
	return s.FederationPeer(id)
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
