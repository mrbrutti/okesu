package db

import (
	"database/sql"
	"strings"
	"time"
)

// Node lifecycle states.
const (
	NodeStatusPending   = "pending"
	NodeStatusDeploying = "deploying"
	NodeStatusReady     = "ready"
	NodeStatusFailed    = "failed"
)

// Node is a registered remote host the CP knows how to deploy to.
type Node struct {
	ID              int64
	Name            string
	Hostname        string
	SSHUser         string
	SSHPort         int
	Status          string
	StatusMessage   sql.NullString
	LastStatusAt    sql.NullTime
	LastDeployedAt  sql.NullTime
	AgentsInstalled sql.NullString
	Notes           sql.NullString
	// DaemonHostname is the hostname the agent daemon reports from inside
	// the guest (captured by the deploy job via SSH `hostname`). Often
	// differs from Hostname (the SSH target) when the node is a VM or
	// container with its own internal name. Used to filter NodeDetail's
	// Live Events feed.
	DaemonHostname  sql.NullString

	// Metadata captured by the tunnel-based "Refresh metadata" probe.
	// All fields nullable — a node with no tunnel never gets refreshed,
	// and partial probes (e.g. /proc/meminfo unreadable) leave individual
	// fields null without failing the rest.
	KernelRelease  sql.NullString
	OSRelease      sql.NullString
	Arch           sql.NullString
	CPUCount       sql.NullInt64
	MemoryMB       sql.NullInt64
	DiskFreeMB     sql.NullInt64
	OkesuVersion   sql.NullString
	MetadataAt     sql.NullTime

	CreatedAt       time.Time
}

// CreateNode inserts a new node row.
func (s *Store) CreateNode(name, hostname, sshUser string, sshPort int, notes string) (int64, error) {
	if sshPort == 0 {
		sshPort = 22
	}
	if sshUser == "" {
		sshUser = "root"
	}
	res, err := s.Exec(`
		INSERT INTO nodes (name, hostname, ssh_user, ssh_port, notes)
		VALUES (?, ?, ?, ?, ?)
	`, name, hostname, sshUser, sshPort, nullable(notes))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// NodeByID returns a node by primary key.
func (s *Store) NodeByID(id int64) (*Node, error) {
	n := &Node{}
	err := s.QueryRow(`
		SELECT id, name, hostname, ssh_user, ssh_port,
		       status, status_message, last_status_at, last_deployed_at,
		       agents_installed, notes, daemon_hostname,
		       kernel_release, os_release, arch, cpu_count, memory_mb,
		       disk_free_mb, okesu_version, metadata_at,
		       created_at
		FROM nodes WHERE id = ?
	`, id).Scan(
		&n.ID, &n.Name, &n.Hostname, &n.SSHUser, &n.SSHPort,
		&n.Status, &n.StatusMessage, &n.LastStatusAt, &n.LastDeployedAt,
		&n.AgentsInstalled, &n.Notes, &n.DaemonHostname,
			&n.KernelRelease, &n.OSRelease, &n.Arch, &n.CPUCount, &n.MemoryMB,
			&n.DiskFreeMB, &n.OkesuVersion, &n.MetadataAt,
			&n.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return n, nil
}

// ListNodes returns nodes ordered by creation time descending.
// Pages via limit + offset; the UI's infinite-scroll calls it with
// limit=N, offset=loaded. limit=0 falls back to 1000.
func (s *Store) ListNodes(limit, offset int) ([]*Node, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.Query(`
		SELECT id, name, hostname, ssh_user, ssh_port,
		       status, status_message, last_status_at, last_deployed_at,
		       agents_installed, notes, daemon_hostname,
		       kernel_release, os_release, arch, cpu_count, memory_mb,
		       disk_free_mb, okesu_version, metadata_at,
		       created_at
		FROM nodes ORDER BY created_at DESC
		LIMIT ? OFFSET ?
	`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Node
	for rows.Next() {
		n := &Node{}
		if err := rows.Scan(
			&n.ID, &n.Name, &n.Hostname, &n.SSHUser, &n.SSHPort,
			&n.Status, &n.StatusMessage, &n.LastStatusAt, &n.LastDeployedAt,
			&n.AgentsInstalled, &n.Notes, &n.DaemonHostname,
			&n.KernelRelease, &n.OSRelease, &n.Arch, &n.CPUCount, &n.MemoryMB,
			&n.DiskFreeMB, &n.OkesuVersion, &n.MetadataAt,
			&n.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// UpdateNodeStatus sets status, status_message, and last_status_at.
func (s *Store) UpdateNodeStatus(id int64, status, message string) error {
	_, err := s.Exec(`
		UPDATE nodes
		SET status = ?, status_message = ?, last_status_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, status, nullable(message), id)
	return err
}

// MarkNodeDeployed records a successful deploy and the agent set installed.
// daemonHostname is the hostname the daemon reports from inside the guest;
// pass "" to leave the column unchanged (e.g. when the deploy didn't
// capture one).
func (s *Store) MarkNodeDeployed(id int64, agents []string, daemonHostname string) error {
	list := strings.Join(agents, ",")
	if daemonHostname == "" {
		_, err := s.Exec(`
			UPDATE nodes
			SET status = 'ready',
			    status_message = NULL,
			    last_status_at = CURRENT_TIMESTAMP,
			    last_deployed_at = CURRENT_TIMESTAMP,
			    agents_installed = ?
			WHERE id = ?
		`, list, id)
		return err
	}
	_, err := s.Exec(`
		UPDATE nodes
		SET status = 'ready',
		    status_message = NULL,
		    last_status_at = CURRENT_TIMESTAMP,
		    last_deployed_at = CURRENT_TIMESTAMP,
		    agents_installed = ?,
		    daemon_hostname = ?
		WHERE id = ?
	`, list, daemonHostname, id)
	return err
}

// DeleteNode removes a node row.
func (s *Store) DeleteNode(id int64) error {
	_, err := s.Exec(`DELETE FROM nodes WHERE id = ?`, id)
	return err
}

// NodeMetadataUpdate is the payload the API/tunnel layers hand to
// UpdateNodeMetadata. Empty/zero fields are skipped — partial probes
// don't clobber previously-known values.
type NodeMetadataUpdate struct {
	DaemonHostname string
	KernelRelease  string
	OSRelease      string
	Arch           string
	CPUCount       int
	MemoryMB       int64
	DiskFreeMB     int64
	OkesuVersion   string
}

// UpdateNodeMetadata writes a fresh metadata snapshot for a node. Updates
// metadata_at to CURRENT_TIMESTAMP regardless of which fields changed so
// the UI can show "last refreshed N minutes ago" reliably.
func (s *Store) UpdateNodeMetadata(id int64, m NodeMetadataUpdate) error {
	// Build an UPDATE that COALESCEs each new value onto the existing one
	// when the new value is empty, so a partial probe preserves what we
	// already had. SQLite doesn't have a clean "skip column when null"
	// SQL form so we encode the rule via NULLIF + COALESCE.
	_, err := s.Exec(`
		UPDATE nodes SET
			daemon_hostname  = COALESCE(NULLIF(?, ''), daemon_hostname),
			kernel_release   = COALESCE(NULLIF(?, ''), kernel_release),
			os_release       = COALESCE(NULLIF(?, ''), os_release),
			arch             = COALESCE(NULLIF(?, ''), arch),
			cpu_count        = CASE WHEN ? > 0 THEN ? ELSE cpu_count END,
			memory_mb        = CASE WHEN ? > 0 THEN ? ELSE memory_mb END,
			disk_free_mb     = CASE WHEN ? > 0 THEN ? ELSE disk_free_mb END,
			okesu_version    = COALESCE(NULLIF(?, ''), okesu_version),
			metadata_at      = CURRENT_TIMESTAMP
		WHERE id = ?
	`,
		m.DaemonHostname,
		m.KernelRelease,
		m.OSRelease,
		m.Arch,
		m.CPUCount, m.CPUCount,
		m.MemoryMB, m.MemoryMB,
		m.DiskFreeMB, m.DiskFreeMB,
		m.OkesuVersion,
		id,
	)
	return err
}
