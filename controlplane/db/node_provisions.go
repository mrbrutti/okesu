// Managed node provisioning job log (Phase 21.7).
//
// Node-side parallel of cp_provisions.go: one row per "+ Add Node →
// Managed deploy" submission. The state machine moves through:
//
//   queued                — created, not yet picked up by the worker
//   starting              — worker has the job, decrypted credentials
//   cloud_init_running    — VM is up, cloud-init pulling the bundle
//   bootstrap_pending     — install.sh running, waiting for the new
//                           node to register via the picked transport
//   ready                 — node row materialised, registration done
//   failed                — terminal error, see `error` column
//
// The provisioner appends to `log` as it makes API calls so the UI
// can stream a tail of "→ creating instance...", "✓ instance running
// at 10.0.x.x", "→ waiting for cloud-init...", etc. AdvanceNodeProvi-
// sionByNode is the equivalent of cp_provisions's AdvanceCPProvisi-
// onByPeer: the s3scanner flips bootstrap_pending → ready when it
// first sees the new node check in over the bucket pipe.

package db

import (
	"database/sql"
	"fmt"
	"time"
)

// NodeProvisionStatus enumerates the state machine. New states get
// added here so the UI can map them to a colored pill consistently.
type NodeProvisionStatus string

const (
	NodeProvisionQueued           NodeProvisionStatus = "queued"
	NodeProvisionStarting         NodeProvisionStatus = "starting"
	NodeProvisionCloudInitRunning NodeProvisionStatus = "cloud_init_running"
	NodeProvisionBootstrapPending NodeProvisionStatus = "bootstrap_pending"
	NodeProvisionReady            NodeProvisionStatus = "ready"
	NodeProvisionFailed           NodeProvisionStatus = "failed"
)

// NodeProvision is the full job row. Plaintext credentials never live
// here — the provisioner re-decrypts on demand from cloud_credentials
// using the linked credential_id.
type NodeProvision struct {
	ID                int64
	DisplayName       string
	Region            string
	Cloud             string
	CredentialID      sql.NullInt64
	CredentialName    sql.NullString
	CloudParamsJSON   string
	TransportConfigID int64
	Status            NodeProvisionStatus
	CloudResourceID   sql.NullString
	CloudResourceURL  sql.NullString
	NodeID            sql.NullInt64
	Log               string
	Error             sql.NullString
	EstCostPerHourUSD sql.NullFloat64
	InstanceShape     sql.NullString
	CreatedAt         time.Time
	StartedAt         sql.NullTime
	EndedAt           sql.NullTime
	CreatedByUserID   sql.NullInt64
	CreatedByEmail    sql.NullString
}

// NodeProvisionInsert is the input shape for InsertNodeProvision.
// Operator inputs come from the +Add Node modal; provisioner inputs
// are filled in as the job runs and use the *Update methods below.
type NodeProvisionInsert struct {
	DisplayName       string
	Region            string
	Cloud             string
	CredentialID      sql.NullInt64
	CredentialName    string
	CloudParamsJSON   string
	TransportConfigID int64
	// Cost-catalog snapshot at insert time. EstCostPerHourUSD
	// {Valid:false} = catalog had no entry for InstanceShape; the row
	// exists but contributes 0 to the budget rollup. InstanceShape is
	// preserved even when the rate is unknown so an operator can grep.
	EstCostPerHourUSD sql.NullFloat64
	InstanceShape     string
	CreatedByUserID   sql.NullInt64
	CreatedByEmail    string
}

func (s *Store) InsertNodeProvision(in NodeProvisionInsert) (*NodeProvision, error) {
	if in.CloudParamsJSON == "" {
		in.CloudParamsJSON = "{}"
	}
	res, err := s.Exec(`
		INSERT INTO node_provisions (
		    display_name, region, cloud, credential_id, credential_name,
		    cloud_params_json, transport_config_id, status,
		    est_cost_per_hour_usd, instance_shape,
		    created_by_user_id, created_by_email
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, in.DisplayName, in.Region, in.Cloud,
		in.CredentialID, nullable(in.CredentialName),
		in.CloudParamsJSON, in.TransportConfigID,
		string(NodeProvisionQueued),
		in.EstCostPerHourUSD, nullable(in.InstanceShape),
		in.CreatedByUserID, nullable(in.CreatedByEmail))
	if err != nil {
		return nil, fmt.Errorf("insert: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.NodeProvision(id)
}

func (s *Store) NodeProvision(id int64) (*NodeProvision, error) {
	row := s.QueryRow(nodeProvisionSelect+` WHERE id = ?`, id)
	return scanNodeProvision(row)
}

// ListNodeProvisions returns rows newest-first. limit caps result
// size; 0 / negative defaults to 100. Used by the Nodes page's
// managed-deploy job list.
func (s *Store) ListNodeProvisions(limit int) ([]NodeProvision, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.Query(nodeProvisionSelect+` ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NodeProvision
	for rows.Next() {
		r, err := scanNodeProvision(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// UpdateNodeProvisionStatus advances the state machine. Sets started_at
// the first time the row leaves "queued"; sets ended_at on terminal
// states (ready / failed).
func (s *Store) UpdateNodeProvisionStatus(id int64, status NodeProvisionStatus) error {
	_, err := s.Exec(`
		UPDATE node_provisions
		SET status = ?,
		    started_at = COALESCE(started_at,
		      CASE WHEN ? IN ('starting','cloud_init_running','bootstrap_pending','ready','failed') THEN CURRENT_TIMESTAMP END),
		    ended_at = CASE
		        WHEN ? IN ('ready','failed') THEN CURRENT_TIMESTAMP
		        ELSE ended_at
		    END
		WHERE id = ?
	`, string(status), string(status), string(status), id)
	return err
}

// AppendNodeProvisionLog appends one line to the provisioner log.
// Lines are newline-terminated by the caller (so the operator UI can
// simply `.split('\n')`). Idempotent w.r.t. truncation: the column is
// TEXT, no max-size enforced here — the worker self-limits via
// MaxLogBytes.
func (s *Store) AppendNodeProvisionLog(id int64, line string) error {
	_, err := s.Exec(`UPDATE node_provisions SET log = log || ? WHERE id = ?`, line, id)
	return err
}

// SetNodeProvisionCloudResource records the cloud-side instance
// handle once the provisioner gets one back. Links the row to the
// destroy/retry path.
func (s *Store) SetNodeProvisionCloudResource(id int64, resourceID, consoleURL string) error {
	_, err := s.Exec(`
		UPDATE node_provisions
		SET cloud_resource_id = ?, cloud_resource_url = ?
		WHERE id = ?
	`, nullable(resourceID), nullable(consoleURL), id)
	return err
}

// SetNodeProvisionNode ties the provision row to the nodes row that
// was created when the new node first registered via the picked
// transport. The state machine flips to ready when AdvanceNodeProvi-
// sionByNode subsequently observes this link.
func (s *Store) SetNodeProvisionNode(id, nodeID int64) error {
	_, err := s.Exec(`UPDATE node_provisions SET node_id = ? WHERE id = ?`, nodeID, id)
	return err
}

// AdvanceNodeProvisionByNode flips a bootstrap_pending row to ready
// when the s3scanner first observes the new node's check-in for the
// linked node_id. Node-side parallel of AdvanceCPProvisionByPeer.
// Idempotent: a no-op if no row matches or the row is already past
// bootstrap_pending. Returns whether a row was actually advanced so
// callers can append a log line just once.
func (s *Store) AdvanceNodeProvisionByNode(nodeID int64) (advanced bool, err error) {
	res, err := s.Exec(`
		UPDATE node_provisions
		SET status = 'ready', ended_at = CURRENT_TIMESTAMP
		WHERE node_id = ? AND status = 'bootstrap_pending'
	`, nodeID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// AppendNodeProvisionLogByNode is a convenience for the s3scanner
// path where the caller knows the node id but not the provision id —
// matches at most one row (node_id is unique per provision in
// practice). No-op if no row matches.
func (s *Store) AppendNodeProvisionLogByNode(nodeID int64, line string) error {
	_, err := s.Exec(`
		UPDATE node_provisions
		SET log = log || ?
		WHERE node_id = ?
	`, line, nodeID)
	return err
}

// FindPendingNodeProvisionByTransportConfig finds the oldest
// node_provisions row matching transport_config_id where
// status='bootstrap_pending' AND node_id IS NULL. Returns
// sql.ErrNoRows when none exists. Used by the s3scanner to link a
// freshly-registered node to its provision row at the moment of
// registration. If two provisions race for the same transport_config,
// the oldest one wins — this can misattribute under heavy concurrent
// launches but is acceptable for v1 (operator can retry / destroy +
// relaunch).
func (s *Store) FindPendingNodeProvisionByTransportConfig(tcID int64) (*NodeProvision, error) {
	// id ASC tiebreaker handles same-second inserts (CURRENT_TIMESTAMP
	// has second resolution in SQLite); auto-increment ids are
	// monotonic so this is equivalent to "oldest insert wins".
	row := s.QueryRow(nodeProvisionSelect+`
		WHERE transport_config_id = ? AND status = 'bootstrap_pending'
		      AND node_id IS NULL
		ORDER BY created_at ASC, id ASC LIMIT 1`, tcID)
	return scanNodeProvision(row)
}

// SetNodeProvisionError records a terminal error string and flips
// status=failed in one round-trip.
func (s *Store) SetNodeProvisionError(id int64, msg string) error {
	_, err := s.Exec(`
		UPDATE node_provisions
		SET status = 'failed', error = ?, ended_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, msg, id)
	return err
}

// DeleteNodeProvision removes a provision row by id. Idempotent.
// Cloud-side resources are NOT touched here — the api handler decides
// whether to call Provisioner.Destroy first based on operator intent.
func (s *Store) DeleteNodeProvision(id int64) error {
	_, err := s.Exec(`DELETE FROM node_provisions WHERE id = ?`, id)
	return err
}

const nodeProvisionSelect = `
	SELECT id, display_name, region, cloud, credential_id, credential_name,
	       cloud_params_json, transport_config_id, status,
	       cloud_resource_id, cloud_resource_url, node_id,
	       log, error, est_cost_per_hour_usd, instance_shape,
	       created_at, started_at, ended_at,
	       created_by_user_id, created_by_email
	  FROM node_provisions`

func scanNodeProvision(r rowScanner) (*NodeProvision, error) {
	p := &NodeProvision{}
	var status string
	if err := r.Scan(
		&p.ID, &p.DisplayName, &p.Region, &p.Cloud,
		&p.CredentialID, &p.CredentialName,
		&p.CloudParamsJSON, &p.TransportConfigID, &status,
		&p.CloudResourceID, &p.CloudResourceURL, &p.NodeID,
		&p.Log, &p.Error, &p.EstCostPerHourUSD, &p.InstanceShape,
		&p.CreatedAt, &p.StartedAt, &p.EndedAt,
		&p.CreatedByUserID, &p.CreatedByEmail,
	); err != nil {
		return nil, fmt.Errorf("scan node_provision: %w", err)
	}
	p.Status = NodeProvisionStatus(status)
	return p, nil
}
