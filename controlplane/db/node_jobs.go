// Pull-mode jobs queue store methods.
//
// The orchestrator's jobsDispatcher INSERTs a row, then waits for a
// terminal status update via the daemon's /exit POST. The daemon's
// jobs runtime polls ClaimPendingJobs to atomically take ownership of
// pending rows. AppendJobOutput streams stdout from the running agent
// into run_lines so the SSE feed the UI subscribes to picks it up.

package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// NodeJob mirrors a row in node_jobs. Status values:
//   pending   — created by the orchestrator; awaiting daemon claim
//   claimed   — daemon has it; agent_run is in flight, output flowing
//   succeeded — exit code 0 (or tunnel started ok)
//   failed    — exit code != 0 (or daemon reported error)
//   timeout   — engine cancelled while still pending or claimed
//   cancelled — operator cancelled the underlying Run / orchestration
type NodeJob struct {
	ID            int64
	RunID         sql.NullString
	NodeID        int64
	Kind          string
	Status        string
	PayloadJSON   string
	ClaimedAt     sql.NullTime
	FinishedAt    sql.NullTime
	ExitCode      sql.NullInt64
	Error         sql.NullString
	TunnelStarted bool
	CreatedAt     time.Time
}

// CreateNodeJob inserts a new pending job. The orchestrator dispatcher
// is the primary caller; ad-hoc Run dispatch can also use this when
// pull mode is selected.
func (s *Store) CreateNodeJob(runID string, nodeID int64, kind, payloadJSON string) (int64, error) {
	res, err := s.Exec(`
		INSERT INTO node_jobs (run_id, node_id, kind, status, payload_json)
		VALUES (?, ?, ?, 'pending', ?)
	`, nullable(runID), nodeID, kind, payloadJSON)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ClaimPendingJobs atomically transitions up to `max` of the oldest
// pending jobs for this node into 'claimed' state, returning them so
// the daemon can execute. The atomic-claim avoids a TOCTOU race when
// multiple polls overlap (a daemon restart, a partial network reply
// retry, etc.) — each row is claimed by exactly one poll.
func (s *Store) ClaimPendingJobs(nodeID int64, max int) ([]*NodeJob, error) {
	if max <= 0 || max > 50 {
		max = 10
	}
	tx, err := s.BeginTx(context.Background(), nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck

	rows, err := tx.Query(`
		SELECT id, run_id, node_id, kind, status, payload_json,
		       claimed_at, finished_at, exit_code, error, tunnel_started, created_at
		  FROM node_jobs
		 WHERE node_id = ? AND status = 'pending'
		 ORDER BY created_at
		 LIMIT ?
	`, nodeID, max)
	if err != nil {
		return nil, err
	}
	var out []*NodeJob
	for rows.Next() {
		j := &NodeJob{}
		var tunnelStarted int64
		if err := rows.Scan(
			&j.ID, &j.RunID, &j.NodeID, &j.Kind, &j.Status, &j.PayloadJSON,
			&j.ClaimedAt, &j.FinishedAt, &j.ExitCode, &j.Error, &tunnelStarted, &j.CreatedAt,
		); err != nil {
			rows.Close()
			return nil, err
		}
		j.TunnelStarted = tunnelStarted != 0
		out = append(out, j)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, j := range out {
		if _, err := tx.Exec(`
			UPDATE node_jobs SET status = 'claimed', claimed_at = CURRENT_TIMESTAMP
			 WHERE id = ? AND status = 'pending'
		`, j.ID); err != nil {
			return nil, err
		}
		j.Status = "claimed"
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

// FinishNodeJob marks a job terminal. Idempotent — repeated calls
// after the first one are no-ops, since the WHERE clause filters on
// status='claimed'. Used by the daemon's /exit POST.
func (s *Store) FinishNodeJob(jobID int64, status string, exitCode int, errMsg string, tunnelStarted bool) error {
	if status != "succeeded" && status != "failed" && status != "timeout" && status != "cancelled" {
		return errors.New("invalid terminal status: " + status)
	}
	ts := 0
	if tunnelStarted {
		ts = 1
	}
	_, err := s.Exec(`
		UPDATE node_jobs
		   SET status = ?, exit_code = ?, error = ?, tunnel_started = ?, finished_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND status IN ('pending', 'claimed')
	`, status, exitCode, nullable(errMsg), ts, jobID)
	return err
}

// GetNodeJob returns a single job by id. Used by the orchestrator's
// blocking-wait loop and by the /output / /exit handlers to validate
// the job exists + belongs to the polling daemon's node.
func (s *Store) GetNodeJob(id int64) (*NodeJob, error) {
	row := s.QueryRow(`
		SELECT id, run_id, node_id, kind, status, payload_json,
		       claimed_at, finished_at, exit_code, error, tunnel_started, created_at
		  FROM node_jobs WHERE id = ?
	`, id)
	j := &NodeJob{}
	var tunnelStarted int64
	if err := row.Scan(
		&j.ID, &j.RunID, &j.NodeID, &j.Kind, &j.Status, &j.PayloadJSON,
		&j.ClaimedAt, &j.FinishedAt, &j.ExitCode, &j.Error, &tunnelStarted, &j.CreatedAt,
	); err != nil {
		return nil, err
	}
	j.TunnelStarted = tunnelStarted != 0
	return j, nil
}

// CancelPendingNodeJobs is called when an orchestration run / Run is
// cancelled — flips any pending or claimed rows owned by it to
// 'cancelled'. The daemon will see status != 'claimed' on its next
// /output POST and stop streaming.
func (s *Store) CancelPendingNodeJobs(runID string) error {
	if runID == "" {
		return nil
	}
	_, err := s.Exec(`
		UPDATE node_jobs
		   SET status = 'cancelled', finished_at = CURRENT_TIMESTAMP, error = 'cancelled'
		 WHERE run_id = ? AND status IN ('pending', 'claimed')
	`, runID)
	return err
}

// MarkJobsRuntimeSeen bumps the node's liveness column. Called every
// time a daemon's heartbeat carries jobs_runtime_ready=true. The
// orchestrator's dispatch decision tree reads this column to decide
// whether the jobs runtime is "fresh enough" to use.
func (s *Store) MarkJobsRuntimeSeen(nodeID int64, tunnelRunning bool) error {
	tr := 0
	if tunnelRunning {
		tr = 1
	}
	_, err := s.Exec(`
		UPDATE nodes
		   SET jobs_runtime_seen_at = CURRENT_TIMESTAMP, tunnel_running = ?
		 WHERE id = ?
	`, tr, nodeID)
	return err
}

// SetNodePreferredDispatch configures a node's dispatch preference.
// "" means follow the engine's auto-pick; "tunnel" or "jobs" force
// that runtime. The orchestrator's per-step dispatch field still
// wins over this.
func (s *Store) SetNodePreferredDispatch(nodeID int64, pref string) error {
	if pref != "" && pref != "tunnel" && pref != "jobs" {
		return errors.New("preferred_dispatch must be empty | tunnel | jobs")
	}
	_, err := s.Exec(`UPDATE nodes SET preferred_dispatch = ? WHERE id = ?`, pref, nodeID)
	return err
}
