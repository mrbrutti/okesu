package db

import (
	"database/sql"
	"time"
)

// KnownHost is the trusted SSH host key for one Node.
type KnownHost struct {
	NodeID          int64
	KeyType         string
	Fingerprint     string
	PublicKey       string
	AcceptedAt      time.Time
	AcceptedByEmail sql.NullString
}

// GetKnownHost returns the pinned host key for a node, or sql.ErrNoRows
// if none has been recorded.
func (s *Store) GetKnownHost(nodeID int64) (*KnownHost, error) {
	kh := &KnownHost{}
	err := s.QueryRow(`
		SELECT node_id, key_type, fingerprint, public_key, accepted_at, accepted_by_email
		FROM known_hosts WHERE node_id = ?
	`, nodeID).Scan(
		&kh.NodeID, &kh.KeyType, &kh.Fingerprint, &kh.PublicKey,
		&kh.AcceptedAt, &kh.AcceptedByEmail,
	)
	if err != nil {
		return nil, err
	}
	return kh, nil
}

// UpsertKnownHost pins (or replaces) the trusted host key for a node.
func (s *Store) UpsertKnownHost(nodeID int64, keyType, fingerprint, publicKey, acceptedBy string) error {
	_, err := s.Exec(`
		INSERT INTO known_hosts (node_id, key_type, fingerprint, public_key, accepted_by_email)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(node_id) DO UPDATE SET
			key_type          = excluded.key_type,
			fingerprint       = excluded.fingerprint,
			public_key        = excluded.public_key,
			accepted_at       = CURRENT_TIMESTAMP,
			accepted_by_email = excluded.accepted_by_email
	`, nodeID, keyType, fingerprint, publicKey, nullable(acceptedBy))
	return err
}

// DeleteKnownHost clears the pinned host key for a node — the next deploy
// will re-establish trust on first connect.
func (s *Store) DeleteKnownHost(nodeID int64) error {
	_, err := s.Exec(`DELETE FROM known_hosts WHERE node_id = ?`, nodeID)
	return err
}

// ListKnownHosts returns every trusted entry joined with the node name for
// display in Settings → Deploy. Newest pinning first.
type KnownHostListItem struct {
	NodeID          int64
	NodeName        string
	Hostname        string
	SSHPort         int
	KeyType         string
	Fingerprint     string
	AcceptedAt      time.Time
	AcceptedByEmail sql.NullString
}

func (s *Store) ListKnownHosts() ([]*KnownHostListItem, error) {
	rows, err := s.Query(`
		SELECT n.id, n.name, n.hostname, n.ssh_port,
		       kh.key_type, kh.fingerprint, kh.accepted_at, kh.accepted_by_email
		FROM known_hosts kh
		JOIN nodes n ON n.id = kh.node_id
		ORDER BY kh.accepted_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*KnownHostListItem
	for rows.Next() {
		k := &KnownHostListItem{}
		if err := rows.Scan(
			&k.NodeID, &k.NodeName, &k.Hostname, &k.SSHPort,
			&k.KeyType, &k.Fingerprint, &k.AcceptedAt, &k.AcceptedByEmail,
		); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}
