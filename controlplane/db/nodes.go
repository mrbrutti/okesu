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
	// NodeStatusArchived marks a node whose cloud-side resource is gone
	// but whose row + history (events, findings, runs, agents) survive
	// for retrospective analysis. Set by ArchiveNode; the corresponding
	// archived_at and archived_by_email columns are stamped at the same
	// moment (migration 062).
	NodeStatusArchived = "archived"
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

	// AutoUpdatePaused freezes daimon hot-reload for this node. The
	// mgmt-plane /config endpoint masks the canonical definition_hash
	// so the daemon's poll loop sees "no change" and stays put.
	// Operators flip it on for compliance windows or before manual
	// deploys; flip off to resume rollout.
	AutoUpdatePaused bool

	// Phase 4 — runtime liveness columns reported by the jobs runtime.
	// Populated by every poll against /api/v1/agents/jobs.
	JobsRuntimeSeenAt  sql.NullTime
	TunnelRunning      bool
	PreferredDispatch  string // "" | "tunnel" | "jobs"

	// Phase 9 — transport selection. 'https' (default) means the node
	// uses the existing pull-mode HTTPS endpoints; 's3' means the node
	// reads/writes a shared object-storage bucket. transport_config_id
	// points at the bucket configuration when transport != 'https'.
	Transport         string
	TransportConfigID sql.NullInt64
	PollIntervalMs    sql.NullInt64
	NodeUUID          sql.NullString

	// Archive state (migration 062). When status='archived', ArchivedAt
	// and ArchivedByEmail are populated by ArchiveNode. The row + all
	// history (events, findings, runs, agents — keyed by host name) are
	// retained for retrospective analysis.
	ArchivedAt      sql.NullTime
	ArchivedByEmail sql.NullString

	CreatedAt time.Time
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
	var tunnelRunning int64
	err := s.QueryRow(`
		SELECT id, name, hostname, ssh_user, ssh_port,
		       status, status_message, last_status_at, last_deployed_at,
		       agents_installed, notes, daemon_hostname,
		       kernel_release, os_release, arch, cpu_count, memory_mb,
		       disk_free_mb, okesu_version, metadata_at,
		       auto_update_paused,
		       jobs_runtime_seen_at, tunnel_running, preferred_dispatch,
		       transport, transport_config_id, poll_interval_ms, node_uuid,
		       archived_at, archived_by_email,
		       created_at
		FROM nodes WHERE id = ?
	`, id).Scan(
		&n.ID, &n.Name, &n.Hostname, &n.SSHUser, &n.SSHPort,
		&n.Status, &n.StatusMessage, &n.LastStatusAt, &n.LastDeployedAt,
		&n.AgentsInstalled, &n.Notes, &n.DaemonHostname,
			&n.KernelRelease, &n.OSRelease, &n.Arch, &n.CPUCount, &n.MemoryMB,
			&n.DiskFreeMB, &n.OkesuVersion, &n.MetadataAt,
			&n.AutoUpdatePaused,
			&n.JobsRuntimeSeenAt, &tunnelRunning, &n.PreferredDispatch,
			&n.Transport, &n.TransportConfigID, &n.PollIntervalMs, &n.NodeUUID,
			&n.ArchivedAt, &n.ArchivedByEmail,
			&n.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	n.TunnelRunning = tunnelRunning != 0
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
		       auto_update_paused,
		       jobs_runtime_seen_at, tunnel_running, preferred_dispatch,
		       transport, transport_config_id, poll_interval_ms, node_uuid,
		       archived_at, archived_by_email,
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
		var tunnelRunning int64
		if err := rows.Scan(
			&n.ID, &n.Name, &n.Hostname, &n.SSHUser, &n.SSHPort,
			&n.Status, &n.StatusMessage, &n.LastStatusAt, &n.LastDeployedAt,
			&n.AgentsInstalled, &n.Notes, &n.DaemonHostname,
			&n.KernelRelease, &n.OSRelease, &n.Arch, &n.CPUCount, &n.MemoryMB,
			&n.DiskFreeMB, &n.OkesuVersion, &n.MetadataAt,
			&n.AutoUpdatePaused,
			&n.JobsRuntimeSeenAt, &tunnelRunning, &n.PreferredDispatch,
			&n.Transport, &n.TransportConfigID, &n.PollIntervalMs, &n.NodeUUID,
			&n.ArchivedAt, &n.ArchivedByEmail,
			&n.CreatedAt,
		); err != nil {
			return nil, err
		}
		n.TunnelRunning = tunnelRunning != 0
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

// ArchiveNode marks a node as archived: sets status='archived',
// stamps archived_at = CURRENT_TIMESTAMP, records the operator's
// email. The node row + all history (events, findings, runs,
// agents — keyed by host name, not FK) survive intact for
// retrospective analysis. Idempotent: archiving an already-
// archived node refreshes the timestamp + email.
func (s *Store) ArchiveNode(id int64, byEmail string) error {
	_, err := s.Exec(`
		UPDATE nodes
		SET status = ?, archived_at = CURRENT_TIMESTAMP,
		    archived_by_email = ?, last_status_at = CURRENT_TIMESTAMP,
		    status_message = 'archived'
		WHERE id = ?
	`, NodeStatusArchived, nullable(byEmail), id)
	return err
}

// PurgeHostHistory removes all history rows that reference a node
// by host name (not FK): events, findings, runs, agents. Used by
// the node-delete-with-purge path so the operator can fully retire
// a host's footprint. Returns the total rows deleted across tables
// so the audit log can record it.
//
// Best-effort idempotent: running on an unknown host returns 0, no
// error. Caller is responsible for ordering this BEFORE any FK-
// cascading delete that would null the join column.
//
// Column conventions (verified against migrations 001/002/003/010):
//   events.host, findings.host, agents.host, runs.node_name.
func (s *Store) PurgeHostHistory(host string) (int64, error) {
	if host == "" {
		return 0, nil
	}
	var total int64
	for _, q := range []string{
		`DELETE FROM events     WHERE host = ?`,
		`DELETE FROM findings   WHERE host = ?`,
		`DELETE FROM runs       WHERE node_name = ?`,
		`DELETE FROM agents     WHERE host = ?`,
	} {
		res, err := s.Exec(q, host)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
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

// BackfillNodeDaemonHostname is the auto-recovery path that runs on
// every daemon heartbeat: when a node has agents_installed that
// includes `agentName` but its daemon_hostname is still blank, fill
// it in with the value the daemon just reported. Single UPDATE; the
// WHERE clause filters out the common no-op case (daemon_hostname
// already set), so it costs ~one row scan per heartbeat after the
// first match.
//
// Origin: nodes registered before Phase 7a's tunnel probe populated
// daemon_hostname were stuck at NULL, which broke NodeDetail's Live
// Events filter. This patch closes the gap going forward without
// requiring operators to run "Refresh metadata" by hand.
func (s *Store) BackfillNodeDaemonHostname(agentName, daemonHost string) error {
	if agentName == "" || daemonHost == "" {
		return nil
	}
	_, err := s.Exec(`
		UPDATE nodes
		SET daemon_hostname = ?
		WHERE (daemon_hostname IS NULL OR daemon_hostname = '')
		  AND agents_installed IS NOT NULL
		  AND ',' || agents_installed || ',' LIKE '%,' || ? || ',%'
	`, daemonHost, agentName)
	return err
}

// SetNodeAutoUpdatePaused flips the per-node freeze pin. When paused,
// the mgmt-plane /config endpoint masks definition_hash for all
// daimons running on this node so their hot-reload poll never fires.
func (s *Store) SetNodeAutoUpdatePaused(id int64, paused bool) error {
	_, err := s.Exec(`UPDATE nodes SET auto_update_paused = ? WHERE id = ?`, paused, id)
	return err
}

// NodeIsAutoUpdatePausedByDaemonHostname returns true when the daemon
// reporting `daemonHost` runs on a node flagged auto_update_paused.
// Used by the mgmt-plane /config handler to decide whether to mask
// the definition_hash for a poll. Returns false when no node matches
// (legacy daemons not deployed via the CP, or hostname mismatch).
func (s *Store) NodeIsAutoUpdatePausedByDaemonHostname(daemonHost string) (bool, error) {
	if daemonHost == "" {
		return false, nil
	}
	var paused bool
	err := s.QueryRow(`
		SELECT auto_update_paused FROM nodes
		WHERE daemon_hostname = ? OR hostname = ?
		LIMIT 1
	`, daemonHost, daemonHost).Scan(&paused)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return paused, nil
}
