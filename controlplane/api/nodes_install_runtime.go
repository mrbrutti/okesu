// Operator + engine entry points for installing the host-side jobs
// runtime on a node.
//
// Two callers:
//   - Operator-triggered (UI): POST /api/nodes/{id}/install-jobs-runtime
//     with the operator's SSH credential in the body. The handler
//     issues a fresh node-cert from the CP CA and runs the SSH
//     install in a background job, streaming logs.
//   - Engine-triggered (auto-deploy): the orchestrator calls
//     InstallJobsRuntimeForNode directly with a fleet-level SSH
//     credential. Same logic, no HTTP shell.

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/section9labs/okesu/controlplane/audit"
	"github.com/section9labs/okesu/controlplane/auth"
	"github.com/section9labs/okesu/controlplane/db"
	"github.com/section9labs/okesu/controlplane/jobs"
	"github.com/section9labs/okesu/controlplane/sshdeploy"
)

// CertIssuer is what the install path needs from the CP — issuance
// of a node-level mTLS cert from the CP CA. The Server type
// satisfies it via its existing IssueClientCert method (added for
// the daimon deploy flow), so we don't need a new wiring path.
type CertIssuer interface {
	IssueClientCert(name string) (cert, key, ca []byte, err error)
}

// InstallJobsRuntimeRequest is the request body for the operator-
// triggered install endpoint. SSH cred fields mirror the existing
// deploy flow's input shape, so a UI that already has the deploy
// dialog can reuse the same fields.
type InstallJobsRuntimeRequest struct {
	PrivateKey   string `json:"private_key"`
	Passphrase   string `json:"passphrase,omitempty"`
	SudoPassword string `json:"sudo_password,omitempty"`
	// SSHUser / SSHPort fall back to the values stored on the
	// nodes row when omitted, matching how Deploy / UpdateBinary
	// behave today.
	SSHUser string `json:"ssh_user,omitempty"`
	SSHPort int    `json:"ssh_port,omitempty"`

	// Optional API keys forwarded to /etc/okesu/jobs.env so spawned
	// agent_run jobs can authenticate. Operators can omit these and
	// supply them later by editing the env file directly.
	AnthropicKey string `json:"anthropic_api_key,omitempty"`
	OpenAIKey    string `json:"openai_api_key,omitempty"`
}

// InstallJobsRuntime handles POST /api/nodes/{id}/install-jobs-runtime.
// Returns 202 with a job id; the install runs in the background and
// the operator polls the existing /api/jobs/{id} endpoint for log
// output.
func InstallJobsRuntime(
	store *db.Store,
	reg *jobs.Registry,
	issuer CertIssuer,
	cfgFn func() (mgmtURL, daemonBinaryPath string, daemonResolver sshdeploy.DaemonBinaryResolver),
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		n, err := store.NodeByID(id)
		if err != nil {
			http.Error(w, "node not found", http.StatusNotFound)
			return
		}
		var req InstallJobsRuntimeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if req.PrivateKey == "" {
			http.Error(w, "private_key is required", http.StatusBadRequest)
			return
		}

		mgmtURL, daemonBinPath, daemonResolver := cfgFn()
		if mgmtURL == "" {
			http.Error(w, "CP has no mgmt-plane URL configured; cannot install jobs runtime", http.StatusInternalServerError)
			return
		}

		// Issue a fresh node-cert. CN matches the node name so the
		// jobs runtime authenticates as that node when polling.
		clientCert, clientKey, caCert, err := issuer.IssueClientCert(n.Name)
		if err != nil {
			http.Error(w, "issue cert: "+err.Error(), http.StatusInternalServerError)
			return
		}

		sshUser := req.SSHUser
		if sshUser == "" {
			sshUser = n.SSHUser
		}
		sshPort := req.SSHPort
		if sshPort == 0 {
			sshPort = n.SSHPort
		}

		ireq := sshdeploy.InstallJobsRequest{
			Cred: sshdeploy.Credential{
				User:         sshUser,
				Host:         n.Hostname,
				Port:         sshPort,
				PrivateKey:   []byte(req.PrivateKey),
				Passphrase:   req.Passphrase,
				SudoPassword: req.SudoPassword,
			},
			NodeName:             n.Name,
			CPMgmtURL:            mgmtURL,
			DaemonBinaryResolver: daemonResolver,
			DaemonBinaryPath:     daemonBinPath,
			ClientCertPEM:        clientCert,
			ClientKeyPEM:         clientKey,
			CACertPEM:            caCert,
			AnthropicAPIKey:      req.AnthropicKey,
			OpenAIAPIKey:         req.OpenAIKey,
		}

		job := reg.Create("install-jobs-runtime", id)
		var actor string
		if u := auth.UserFromContext(r.Context()); u != nil {
			actor = u.Email
		}
		audit.Emit(r, store, db.AuditEntry{
			Action: "node.install_jobs_runtime",
			Target: fmt.Sprintf("node:%d", id),
			Metadata: map[string]any{
				"job_id": job.ID,
				"actor":  actor,
			},
		})

		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			res, err := sshdeploy.InstallJobsRuntime(ctx, ireq, func(line string) { job.Append(line) })
			if err != nil {
				job.Append("✗ install failed: " + err.Error())
				job.Complete(err)
				return
			}
			job.Append(fmt.Sprintf("✓ done — %s · %s/%s", res.Hostname, res.TargetOS, res.TargetArch))
			job.Complete(nil)
		}()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"job_id":  job.ID,
			"node_id": id,
		})
	}
}
