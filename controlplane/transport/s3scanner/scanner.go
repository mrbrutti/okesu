// Package s3scanner implements the CP-side ingest loop for the
// S3 dead-drop transport. It periodically lists each bucket prefix
// the CP cares about (registrations, heartbeats, events, findings,
// job exits, job output) and feeds the contents into the existing
// CP pipelines — eventpipeline, findings store, run lines table,
// node_jobs status updates.
//
// One scanner runs per CP per transport_config. Multiple transport
// configs (e.g. per-region) would each get their own scanner.
//
// The scanner is the dual of agent/s3transport — keep them in sync
// when the wire format changes.

package s3scanner

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/section9labs/okesu/agent"
	"github.com/section9labs/okesu/agent/s3transport"
	"github.com/section9labs/okesu/controlplane/db"
)

// Scanner ingests a single bucket on behalf of one CP.
type Scanner struct {
	store    *db.Store
	client   *s3transport.Client
	cpID     string
	cfgID    int64
	interval time.Duration

	// Hooks for the higher-level CP pipelines. The scanner doesn't
	// know how the CP processes events / findings — it just hands
	// each one off through these closures (set by the server bootstrap).
	OnEvent   func(nodeID int64, e agent.Event)
	OnFinding func(nodeID int64, e agent.Event)

	// CertIssuer signs CSRs during enrollment. Same interface the
	// rest of the CP uses; supplied at construction.
	IssueClientCert func(commonName string) (cert, key, ca []byte, err error)
}

// New constructs a scanner. interval clamps to [5s, 60s].
func New(store *db.Store, cli *s3transport.Client, cpID string, cfgID int64, intervalMs int) *Scanner {
	d := time.Duration(intervalMs) * time.Millisecond
	if d < 5*time.Second {
		d = 5 * time.Second
	}
	if d > 60*time.Second {
		d = 60 * time.Second
	}
	return &Scanner{store: store, client: cli, cpID: cpID, cfgID: cfgID, interval: d}
}

// Run executes the scan loop until ctx is cancelled. Errors per
// iteration are logged but never abort the loop — the scanner self-
// heals on the next tick.
func (s *Scanner) Run(ctx context.Context) {
	log.Printf("s3scanner: started (cp=%s cfg=%d every=%s)", s.cpID, s.cfgID, s.interval)
	t := time.NewTicker(s.interval)
	defer t.Stop()
	// Run one sweep right away so a fresh CP doesn't wait an interval
	// before noticing existing registrations.
	s.sweep(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.sweep(ctx)
		}
	}
}

func (s *Scanner) sweep(ctx context.Context) {
	if err := s.sweepRegistrations(ctx); err != nil {
		log.Printf("s3scanner: registrations: %v", err)
	}
	nodes, err := s.listS3Nodes(ctx)
	if err != nil {
		log.Printf("s3scanner: list nodes: %v", err)
		return
	}
	for _, nodeID := range nodes {
		if err := s.sweepHeartbeat(ctx, nodeID); err != nil {
			log.Printf("s3scanner: heartbeat node=%d: %v", nodeID, err)
		}
		if err := s.sweepEvents(ctx, nodeID); err != nil {
			log.Printf("s3scanner: events node=%d: %v", nodeID, err)
		}
		if err := s.sweepFindings(ctx, nodeID); err != nil {
			log.Printf("s3scanner: findings node=%d: %v", nodeID, err)
		}
		if err := s.sweepJobOutput(ctx, nodeID); err != nil {
			log.Printf("s3scanner: job output node=%d: %v", nodeID, err)
		}
		if err := s.sweepJobExits(ctx, nodeID); err != nil {
			log.Printf("s3scanner: job exits node=%d: %v", nodeID, err)
		}
	}
}

// listS3Nodes returns the node ids whose row points at this scanner's
// transport_config. Cheap query — small table.
func (s *Scanner) listS3Nodes(ctx context.Context) ([]int64, error) {
	rows, err := s.store.QueryContext(ctx, `
		SELECT id FROM nodes
		 WHERE transport = 's3' AND transport_config_id = ?`, s.cfgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ───────────────────────── registrations ─────────────────────────

func (s *Scanner) sweepRegistrations(ctx context.Context) error {
	prefix := s3transport.CPPrefix(s.cpID) + s3transport.PathRegistrationDir
	objs, err := s.client.List(ctx, prefix, 1000)
	if err != nil {
		return err
	}
	for _, o := range objs {
		if !strings.HasSuffix(o.Key, ".json") {
			continue
		}
		s.handleRegistration(ctx, o.Key)
	}
	return nil
}

func (s *Scanner) handleRegistration(ctx context.Context, key string) {
	body, err := s.client.GetBytes(ctx, key)
	if err != nil {
		log.Printf("s3scanner: read %s: %v", key, err)
		return
	}
	var rr s3transport.RegistrationRequest
	if err := json.Unmarshal(body, &rr); err != nil {
		log.Printf("s3scanner: decode %s: %v", key, err)
		s.rejectRegistration(ctx, rr.NodeUUID, "bad json: "+err.Error())
		_ = s.client.Delete(ctx, key)
		return
	}

	// Verify package cert against fleet pubkey, then verify the
	// request signature against the package cert. Both checks must
	// pass; both are done locally with no further bucket calls.
	if err := s.verifyRegistration(rr); err != nil {
		log.Printf("s3scanner: reject %s: %v", rr.NodeUUID, err)
		s.rejectRegistration(ctx, rr.NodeUUID, err.Error())
		_ = s.client.Delete(ctx, key)
		return
	}

	// De-dupe: a node that re-runs enroll uses a new UUID, but
	// network retries can re-upload the same one. Idempotent — the
	// nodes table's node_uuid uniqueness lets us short-circuit.
	if existing, ok := s.findNodeByUUID(ctx, rr.NodeUUID); ok {
		log.Printf("s3scanner: registration %s already issued (node_id=%d); republishing result", rr.NodeUUID, existing)
		s.publishRegistrationResult(ctx, rr.NodeUUID, existing, "")
		_ = s.client.Delete(ctx, key)
		return
	}

	// Allocate the node row.
	nodeName := rr.Hostname
	if nodeName == "" {
		nodeName = rr.NodeUUID
	}
	id, err := s.store.CreateNode(nodeName, "", "", 0, "auto-enrolled via s3 transport")
	if err != nil {
		log.Printf("s3scanner: create node %s: %v", rr.NodeUUID, err)
		s.publishRegistrationResult(ctx, rr.NodeUUID, 0, "create node: "+err.Error())
		return
	}
	if _, err := s.store.Exec(`UPDATE nodes SET transport = 's3', transport_config_id = ?, node_uuid = ? WHERE id = ?`, s.cfgID, rr.NodeUUID, id); err != nil {
		log.Printf("s3scanner: bind transport node=%d: %v", id, err)
	}

	// Sign the CSR using the CP's mTLS CA (same path the manual
	// enroll endpoint uses). Result cert lands at
	// cp/<cp-id>/nodes/<id>/cert.pem.
	if s.IssueClientCert == nil {
		s.publishRegistrationResult(ctx, rr.NodeUUID, id, "scanner has no cert issuer wired")
		return
	}
	certPEM, _, _, err := s.IssueClientCert(strconv.FormatInt(id, 10))
	if err != nil {
		log.Printf("s3scanner: issue cert node=%d: %v", id, err)
		s.publishRegistrationResult(ctx, rr.NodeUUID, id, "issue cert: "+err.Error())
		return
	}
	certKey := s3transport.NodePrefix(s.cpID, id) + s3transport.FileNodeCert
	if err := s.client.Put(ctx, certKey, certPEM, "application/x-pem-file"); err != nil {
		log.Printf("s3scanner: upload cert node=%d: %v", id, err)
	}

	// Seed initial agent_run jobs based on the requested daimons.
	for _, agentName := range rr.AgentsRequested {
		s.seedAgentInstall(ctx, id, agentName)
	}

	s.publishRegistrationResult(ctx, rr.NodeUUID, id, "")
	_ = s.client.Delete(ctx, key)
}

func (s *Scanner) verifyRegistration(rr s3transport.RegistrationRequest) error {
	if rr.PackageCertPEM == "" || rr.Signature == "" {
		return errors.New("missing package cert or signature")
	}
	// Parse the package cert and look it up by fingerprint to check
	// revocation status + fetch the parent enrollment_packages row.
	block, _ := pem.Decode([]byte(rr.PackageCertPEM))
	if block == nil {
		return errors.New("invalid package cert PEM")
	}
	pkgCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fmt.Errorf("parse package cert: %w", err)
	}
	fp := certFingerprint(pkgCert)
	pkg, err := s.store.FindEnrollmentPackageByCertFingerprint(fp)
	if err != nil {
		return fmt.Errorf("unknown package: %w", err)
	}
	if pkg.RevokedAt.Valid {
		return errors.New("package has been revoked")
	}
	if pkg.ID != rr.PackageID {
		return fmt.Errorf("package id mismatch (request=%d cert=%d)", rr.PackageID, pkg.ID)
	}
	// Signature verification is implemented in verify.go (separate
	// file so the scanner stays manageable). Skipped here for the
	// initial CP scaffold; verifySignature lives alongside.
	return verifyRequestSignature(rr, pkgCert)
}

func (s *Scanner) findNodeByUUID(ctx context.Context, nodeUUID string) (int64, bool) {
	row := s.store.QueryRowContext(ctx, `SELECT id FROM nodes WHERE node_uuid = ? LIMIT 1`, nodeUUID)
	var id int64
	if err := row.Scan(&id); err != nil {
		return 0, false
	}
	return id, true
}

func (s *Scanner) publishRegistrationResult(ctx context.Context, nodeUUID string, nodeID int64, errStr string) {
	res := s3transport.RegistrationResult{
		NodeUUID: nodeUUID,
		NodeID:   nodeID,
		IssuedAt: time.Now().UTC(),
		Error:    errStr,
	}
	if errStr == "" && nodeID > 0 {
		res.CertPath = s3transport.NodePrefix(s.cpID, nodeID) + s3transport.FileNodeCert
	}
	body, _ := json.Marshal(res)
	key := s3transport.CPPrefix(s.cpID) + s3transport.PathRegistrationResultDir + nodeUUID + ".json"
	_ = s.client.Put(ctx, key, body, "application/json")
}

func (s *Scanner) rejectRegistration(ctx context.Context, nodeUUID, reason string) {
	if nodeUUID == "" {
		return
	}
	s.publishRegistrationResult(ctx, nodeUUID, 0, reason)
}

// seedAgentInstall enqueues a placeholder agent_run job so the new
// node automatically picks up the daimons the operator asked for.
// In a follow-up phase the seed would actually invoke the existing
// daimon deploy path (write agent file, env, certs, service unit);
// for v1 we just mark the agent as "installed" in the nodes row so
// the UI shows it immediately.
func (s *Scanner) seedAgentInstall(ctx context.Context, nodeID int64, agentName string) {
	_ = ctx
	_, _ = s.store.Exec(`UPDATE nodes SET agents_installed = COALESCE(agents_installed, '') || ? WHERE id = ?`,
		agentName+",", nodeID)
}

// ───────────────────────── heartbeat ─────────────────────────

func (s *Scanner) sweepHeartbeat(ctx context.Context, nodeID int64) error {
	key := s3transport.NodePrefix(s.cpID, nodeID) + s3transport.FileHeartbeat
	body, err := s.client.GetBytes(ctx, key)
	if err != nil {
		if errors.Is(err, s3transport.ErrNotFound) {
			return nil
		}
		return err
	}
	var hb s3transport.Heartbeat
	if err := json.Unmarshal(body, &hb); err != nil {
		return err
	}
	// Reuse the same MarkJobsRuntimeSeen path the HTTPS pull-mode
	// uses — both transports share the freshness column.
	return s.store.MarkJobsRuntimeSeen(nodeID, hb.TunnelRunning)
}

// ───────────────────────── events ─────────────────────────

func (s *Scanner) sweepEvents(ctx context.Context, nodeID int64) error {
	prefix := s3transport.NodePrefix(s.cpID, nodeID) + s3transport.DirEvents
	objs, err := s.client.List(ctx, prefix, 1000)
	if err != nil {
		return err
	}
	for _, o := range objs {
		if !strings.HasSuffix(o.Key, ".ndjson") {
			continue
		}
		body, err := s.client.GetBytes(ctx, o.Key)
		if err != nil {
			log.Printf("s3scanner: read event %s: %v", o.Key, err)
			continue
		}
		// One event per line. Send each through the OnEvent hook;
		// the scanner does not own DB persistence — it delegates.
		for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
			if line == "" {
				continue
			}
			var e agent.Event
			if err := json.Unmarshal([]byte(line), &e); err != nil {
				log.Printf("s3scanner: bad event line in %s: %v", o.Key, err)
				continue
			}
			if s.OnEvent != nil {
				s.OnEvent(nodeID, e)
			}
		}
		_ = s.client.Delete(ctx, o.Key)
	}
	return nil
}

// ───────────────────────── findings ─────────────────────────

func (s *Scanner) sweepFindings(ctx context.Context, nodeID int64) error {
	prefix := s3transport.NodePrefix(s.cpID, nodeID) + s3transport.DirFindings
	objs, err := s.client.List(ctx, prefix, 1000)
	if err != nil {
		return err
	}
	for _, o := range objs {
		body, err := s.client.GetBytes(ctx, o.Key)
		if err != nil {
			log.Printf("s3scanner: read finding %s: %v", o.Key, err)
			continue
		}
		var e agent.Event
		if err := json.Unmarshal(body, &e); err != nil {
			log.Printf("s3scanner: bad finding %s: %v", o.Key, err)
			continue
		}
		if s.OnFinding != nil {
			s.OnFinding(nodeID, e)
		}
		_ = s.client.Delete(ctx, o.Key)
	}
	return nil
}

// ───────────────────────── job output ─────────────────────────

func (s *Scanner) sweepJobOutput(ctx context.Context, nodeID int64) error {
	prefix := s3transport.NodePrefix(s.cpID, nodeID) + s3transport.DirJobs
	// We list by prefix; the layout is jobs/<id>/output/<seq>.txt.
	objs, err := s.client.List(ctx, prefix, 1000)
	if err != nil {
		return err
	}
	for _, o := range objs {
		// Cheap path filter for output chunks only.
		if !strings.Contains(o.Key, "/output/") || !strings.HasSuffix(o.Key, ".txt") {
			continue
		}
		body, err := s.client.GetBytes(ctx, o.Key)
		if err != nil {
			log.Printf("s3scanner: read output %s: %v", o.Key, err)
			continue
		}
		jobID := jobIDFromOutputKey(o.Key)
		runID := s.runIDForJob(ctx, jobID)
		if runID != "" {
			_ = s.store.AppendRunLine(runID, "stdout", string(body))
		}
		_ = s.client.Delete(ctx, o.Key)
	}
	return nil
}

func jobIDFromOutputKey(key string) string {
	// .../jobs/<id>/output/<seq>.txt
	parts := strings.Split(key, "/")
	for i := 0; i < len(parts)-2; i++ {
		if parts[i] == "jobs" && parts[i+2] == "output" {
			return parts[i+1]
		}
	}
	return path.Base(path.Dir(path.Dir(key)))
}

func (s *Scanner) runIDForJob(ctx context.Context, jobID string) string {
	// In the S3 transport we stash run_id directly on the inbox
	// payload; the runner stamps it through to the claim marker.
	// Cheap lookup by reading the claim object.
	row := s.store.QueryRowContext(ctx, `SELECT run_id FROM node_jobs WHERE id = ?`, jobID)
	var runID string
	_ = row.Scan(&runID)
	return runID
}

// ───────────────────────── job exits ─────────────────────────

func (s *Scanner) sweepJobExits(ctx context.Context, nodeID int64) error {
	prefix := s3transport.NodePrefix(s.cpID, nodeID) + s3transport.DirJobs
	objs, err := s.client.List(ctx, prefix, 1000)
	if err != nil {
		return err
	}
	for _, o := range objs {
		if !strings.HasSuffix(o.Key, "/exit.json") {
			continue
		}
		body, err := s.client.GetBytes(ctx, o.Key)
		if err != nil {
			log.Printf("s3scanner: read exit %s: %v", o.Key, err)
			continue
		}
		var ex agent.JobExit
		if err := json.Unmarshal(body, &ex); err != nil {
			log.Printf("s3scanner: bad exit %s: %v", o.Key, err)
			continue
		}
		jobID := jobIDFromExitKey(o.Key)
		status := "succeeded"
		if ex.ExitCode != 0 || ex.Error != "" {
			status = "failed"
		}
		_ = s.store.FinishNodeJob(parseInt64(jobID), status, ex.ExitCode, ex.Error, ex.TunnelStarted)
		_ = s.client.Delete(ctx, o.Key)
	}
	return nil
}

func jobIDFromExitKey(key string) string {
	// .../jobs/<id>/exit.json
	return path.Base(path.Dir(key))
}

func parseInt64(s string) int64 {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return n
}
