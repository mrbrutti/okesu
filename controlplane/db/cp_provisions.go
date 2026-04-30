// Managed CP provisioning job log (Phase 21.3).
//
// One row per "+ Add CP → Managed deploy" submission. The state
// machine moves through:
//
//   queued                — created, not yet picked up by the worker
//   starting              — worker has the job, decrypted credentials
//   cloud_init_running    — VM is up, cloud-init pulling the bundle
//   bootstrap_pending     — bundle running, waiting for the new CP's
//                           /api/v1/cp/bootstrap call
//   ready                 — child registered, peer_id populated
//   failed                — terminal error, see `error` column
//   cancelled             — operator cancelled mid-flight
//
// The provisioner appends to `log` as it makes API calls so the UI
// can stream a tail of "→ creating instance...", "✓ instance running
// at 10.0.x.x", "→ waiting for cloud-init...", etc. Same shape as
// the node deploy job log on the Nodes page, just a different table.

package db

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// CPProvisionStatus enumerates the state machine. New states get
// added here so the UI can map them to a colored pill consistently.
type CPProvisionStatus string

const (
	CPProvisionQueued           CPProvisionStatus = "queued"
	CPProvisionStarting         CPProvisionStatus = "starting"
	CPProvisionCloudInitRunning CPProvisionStatus = "cloud_init_running"
	CPProvisionBootstrapPending CPProvisionStatus = "bootstrap_pending"
	CPProvisionReady            CPProvisionStatus = "ready"
	CPProvisionFailed           CPProvisionStatus = "failed"
	CPProvisionCancelled        CPProvisionStatus = "cancelled"
)

// CPProvision is the full job row. Plaintext credentials never live
// here — the provisioner re-decrypts on demand from cloud_credentials
// using the linked credential_id.
type CPProvision struct {
	ID                 int64
	DisplayName        string
	Region             string
	Cloud              string
	CredentialID       sql.NullInt64
	CredentialName     sql.NullString
	CloudParamsJSON    string
	Status             CPProvisionStatus
	CloudResourceID    sql.NullString
	CloudResourceURL   sql.NullString
	BundleTokenID      sql.NullInt64
	PeerID             sql.NullInt64
	Log                string
	Error              sql.NullString
	EstCostPerHourUSD  sql.NullFloat64
	InstanceShape      sql.NullString
	CreatedAt          time.Time
	StartedAt          sql.NullTime
	EndedAt            sql.NullTime
	CreatedByUserID    sql.NullInt64
	CreatedByEmail     sql.NullString
}

// CPProvisionInsert is the input shape for InsertCPProvision. Operator
// inputs come from the +Add CP modal; provisioner inputs are filled in
// as the job runs and use the *Update methods below.
type CPProvisionInsert struct {
	DisplayName     string
	Region          string
	Cloud           string
	CredentialID    int64
	CredentialName  string
	CloudParamsJSON string
	BundleTokenID   int64
	// Cost-catalog snapshot at insert time. EstCostPerHourUSD nil =
	// catalog had no entry for InstanceShape; the row exists but
	// contributes 0 to the budget rollup. InstanceShape is preserved
	// even when the rate is unknown so an operator can grep for it.
	EstCostPerHourUSD *float64
	InstanceShape     string
	CreatedByUserID   int64
	CreatedByEmail    string
}

func (s *Store) InsertCPProvision(in CPProvisionInsert) (*CPProvision, error) {
	if in.CloudParamsJSON == "" {
		in.CloudParamsJSON = "{}"
	}
	var costArg sql.NullFloat64
	if in.EstCostPerHourUSD != nil {
		costArg = sql.NullFloat64{Float64: *in.EstCostPerHourUSD, Valid: true}
	}
	res, err := s.Exec(`
		INSERT INTO cp_provisions (
		    display_name, region, cloud, credential_id, credential_name,
		    cloud_params_json, bundle_token_id, status,
		    est_cost_per_hour_usd, instance_shape,
		    created_by_user_id, created_by_email
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, in.DisplayName, in.Region, in.Cloud,
		nullableInt64(in.CredentialID), nullable(in.CredentialName),
		in.CloudParamsJSON, nullableInt64(in.BundleTokenID),
		string(CPProvisionQueued),
		costArg, nullable(in.InstanceShape),
		nullableInt64(in.CreatedByUserID), nullable(in.CreatedByEmail))
	if err != nil {
		return nil, fmt.Errorf("insert: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.GetCPProvision(id)
}

func (s *Store) GetCPProvision(id int64) (*CPProvision, error) {
	row := s.QueryRow(`
		SELECT id, display_name, region, cloud, credential_id, credential_name,
		       cloud_params_json, status, cloud_resource_id, cloud_resource_url,
		       bundle_token_id, peer_id, log, error,
		       est_cost_per_hour_usd, instance_shape,
		       created_at, started_at, ended_at,
		       created_by_user_id, created_by_email
		FROM cp_provisions WHERE id = ?
	`, id)
	return scanCPProvision(row)
}

// ListCPProvisions returns rows newest-first. limit caps result size;
// 0 / negative defaults to 100. Used by the Federation page's
// managed-deploy job list.
func (s *Store) ListCPProvisions(limit int) ([]CPProvision, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.Query(`
		SELECT id, display_name, region, cloud, credential_id, credential_name,
		       cloud_params_json, status, cloud_resource_id, cloud_resource_url,
		       bundle_token_id, peer_id, log, error,
		       est_cost_per_hour_usd, instance_shape,
		       created_at, started_at, ended_at,
		       created_by_user_id, created_by_email
		FROM cp_provisions ORDER BY created_at DESC LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CPProvision
	for rows.Next() {
		c, err := scanCPProvision(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// UpdateCPProvisionStatus advances the state machine. Sets started_at
// the first time the row leaves "queued"; sets ended_at on terminal
// states (ready / failed / cancelled).
func (s *Store) UpdateCPProvisionStatus(id int64, status CPProvisionStatus) error {
	_, err := s.Exec(`
		UPDATE cp_provisions
		SET status = ?,
		    started_at = COALESCE(started_at,
		      CASE WHEN ? IN ('starting','cloud_init_running','bootstrap_pending','ready','failed','cancelled') THEN CURRENT_TIMESTAMP END),
		    ended_at = CASE
		        WHEN ? IN ('ready','failed','cancelled') THEN CURRENT_TIMESTAMP
		        ELSE ended_at
		    END
		WHERE id = ?
	`, string(status), string(status), string(status), id)
	return err
}

// AppendCPProvisionLog appends one line to the provisioner log. Lines
// are newline-terminated by the caller (so the operator UI can simply
// `.split('\n')`). Idempotent w.r.t. truncation: the column is TEXT,
// no max-size enforced here — the worker self-limits via MaxLogBytes.
func (s *Store) AppendCPProvisionLog(id int64, line string) error {
	_, err := s.Exec(`UPDATE cp_provisions SET log = log || ? WHERE id = ?`, line, id)
	return err
}

// SetCPProvisionCloudResource records the cloud-side instance handle
// once the provisioner gets one back. Links the row to the
// destroy/retry path.
func (s *Store) SetCPProvisionCloudResource(id int64, resourceID, consoleURL string) error {
	_, err := s.Exec(`
		UPDATE cp_provisions
		SET cloud_resource_id = ?, cloud_resource_url = ?
		WHERE id = ?
	`, nullable(resourceID), nullable(consoleURL), id)
	return err
}

// SetCPProvisionPeer ties the provision row to the federation_peers
// row that was created when the new child CP called /bootstrap. The
// state machine flips to ready when this is set.
func (s *Store) SetCPProvisionPeer(id, peerID int64) error {
	_, err := s.Exec(`UPDATE cp_provisions SET peer_id = ? WHERE id = ?`, peerID, id)
	return err
}

// SetCPProvisionError records a terminal error string and flips
// status=failed in one round-trip.
func (s *Store) SetCPProvisionError(id int64, msg string) error {
	_, err := s.Exec(`
		UPDATE cp_provisions
		SET status = 'failed', error = ?, ended_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, msg, id)
	return err
}

// FindCPProvisionByBundleToken looks up the provision row a given
// bootstrap token belongs to — called from the bootstrap handler so
// it can advance the matching provision row to "ready" + record the
// peer_id atomically with the bootstrap exchange.
func (s *Store) FindCPProvisionByBundleToken(tokenID int64) (*CPProvision, error) {
	row := s.QueryRow(`
		SELECT id, display_name, region, cloud, credential_id, credential_name,
		       cloud_params_json, status, cloud_resource_id, cloud_resource_url,
		       bundle_token_id, peer_id, log, error,
		       est_cost_per_hour_usd, instance_shape,
		       created_at, started_at, ended_at,
		       created_by_user_id, created_by_email
		FROM cp_provisions WHERE bundle_token_id = ?
	`, tokenID)
	c, err := scanCPProvision(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, sql.ErrNoRows
		}
		return nil, err
	}
	return c, nil
}

func scanCPProvision(s rowScanner) (*CPProvision, error) {
	c := &CPProvision{}
	var status string
	if err := s.Scan(
		&c.ID, &c.DisplayName, &c.Region, &c.Cloud,
		&c.CredentialID, &c.CredentialName,
		&c.CloudParamsJSON, &status,
		&c.CloudResourceID, &c.CloudResourceURL,
		&c.BundleTokenID, &c.PeerID, &c.Log, &c.Error,
		&c.EstCostPerHourUSD, &c.InstanceShape,
		&c.CreatedAt, &c.StartedAt, &c.EndedAt,
		&c.CreatedByUserID, &c.CreatedByEmail,
	); err != nil {
		return nil, err
	}
	c.Status = CPProvisionStatus(status)
	return c, nil
}
