package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/section9labs/okesu/controlplane/audit"
	"github.com/section9labs/okesu/controlplane/auth"
	"github.com/section9labs/okesu/controlplane/db"
	"github.com/section9labs/okesu/controlplane/jobs"
	"github.com/section9labs/okesu/controlplane/sshdeploy"
)

// NodeDeployer is the subset of controlplane.Server functionality that the
// nodes API depends on. Defined as an interface to keep this package free
// of a circular dependency on the parent package.
type NodeDeployer interface {
	IssueClientCert(agent string) (cert, key, ca []byte, err error)
}

// NodesConfig is the deploy-time configuration the API needs.
type NodesConfig struct {
	DaemonBinaryPath  string                          // single-arch fallback
	DaemonBinariesDir string                          // multi-arch directory
	DaimonFilesDir     string
	WebhookSecret     string
	// WebhookURL and MgmtURL are the absolute URLs the deployed daemon
	// will use to reach the CP. These get templated into the agent file
	// during deploy. They may differ from the CP's local listen address
	// (e.g. when the agent reaches the CP through a load balancer or via
	// host.lima.internal from inside a container).
	WebhookURL string
	MgmtURL    string
}

// dbBinaryResolver looks up the daemon binary path for an os/arch from the
// daemon_binaries table. Implements sshdeploy.DaemonBinaryResolver.
type dbBinaryResolver struct {
	store *db.Store
}

func (r *dbBinaryResolver) Resolve(osName, arch string) (string, error) {
	b, err := r.store.DaemonBinaryByOSArch(osName, arch)
	if err != nil {
		return "", err
	}
	return b.Path, nil
}

// nodeJSON is the wire shape returned by the API.
type nodeJSON struct {
	ID               int64    `json:"id"`
	Name             string   `json:"name"`
	Hostname         string   `json:"hostname"`
	// DaemonHostname is what `hostname` returns inside the guest, captured
	// during deploy. The UI prefers it for filtering events by host since
	// daemon events are tagged with the in-guest hostname (which routinely
	// differs from the SSH-target hostname).
	DaemonHostname   string   `json:"daemon_hostname,omitempty"`
	SSHUser          string   `json:"ssh_user"`
	SSHPort          int      `json:"ssh_port"`
	Status           string   `json:"status"`
	StatusMessage    string   `json:"status_message,omitempty"`
	LastStatusAt     string   `json:"last_status_at,omitempty"`
	LastDeployedAt   string   `json:"last_deployed_at,omitempty"`
	AgentsInstalled  []string `json:"agents_installed"`
	Notes            string   `json:"notes,omitempty"`
	CreatedAt        string   `json:"created_at"`
}

func toNodeJSON(n *db.Node) nodeJSON {
	out := nodeJSON{
		ID:             n.ID,
		Name:           n.Name,
		Hostname:       n.Hostname,
		DaemonHostname: n.DaemonHostname.String,
		SSHUser:        n.SSHUser,
		SSHPort:        n.SSHPort,
		Status:         n.Status,
		Notes:          n.Notes.String,
		CreatedAt:      n.CreatedAt.UTC().Format(time.RFC3339),
	}
	if n.StatusMessage.Valid {
		out.StatusMessage = n.StatusMessage.String
	}
	if n.LastStatusAt.Valid {
		out.LastStatusAt = n.LastStatusAt.Time.UTC().Format(time.RFC3339)
	}
	if n.LastDeployedAt.Valid {
		out.LastDeployedAt = n.LastDeployedAt.Time.UTC().Format(time.RFC3339)
	}
	if n.AgentsInstalled.Valid && n.AgentsInstalled.String != "" {
		out.AgentsInstalled = strings.Split(n.AgentsInstalled.String, ",")
	} else {
		out.AgentsInstalled = []string{}
	}
	return out
}

// NodesList returns registered nodes, paginated.
// GET /api/nodes?limit=N&offset=N
func NodesList(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		ns, err := store.ListNodes(limit, offset)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := make([]nodeJSON, 0, len(ns))
		for _, n := range ns {
			out = append(out, toNodeJSON(n))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// NodeCreate registers a new node.
type nodeCreateReq struct {
	Name     string `json:"name"`
	Hostname string `json:"hostname"`
	SSHUser  string `json:"ssh_user,omitempty"`
	SSHPort  int    `json:"ssh_port,omitempty"`
	Notes    string `json:"notes,omitempty"`
}

func NodeCreate(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req nodeCreateReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if req.Name == "" || req.Hostname == "" {
			http.Error(w, "name and hostname are required", http.StatusBadRequest)
			return
		}
		id, err := store.CreateNode(req.Name, req.Hostname, req.SSHUser, req.SSHPort, req.Notes)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		n, err := store.NodeByID(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		audit.Emit(r, store, db.AuditEntry{
			Action: "node.create",
			Target: fmt.Sprintf("node:%d", id),
			Metadata: map[string]any{"name": req.Name, "hostname": req.Hostname},
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toNodeJSON(n))
	}
}

// NodeDetail returns one node.
func NodeDetail(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		n, err := store.NodeByID(id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toNodeJSON(n))
	}
}

// NodeDelete removes a node.
func NodeDelete(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		if err := store.DeleteNode(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		audit.Emit(r, store, db.AuditEntry{
			Action: "node.delete",
			Target: fmt.Sprintf("node:%d", id),
		})
		w.WriteHeader(http.StatusNoContent)
	}
}

// AgentLibrary returns the names of agent files the CP can deploy.
func AgentLibrary(cfg NodesConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		names := []string{}
		if cfg.DaimonFilesDir != "" {
			entries, err := os.ReadDir(cfg.DaimonFilesDir)
			if err == nil {
				for _, e := range entries {
					n := e.Name()
					if !e.IsDir() && strings.HasSuffix(n, ".md") {
						names = append(names, strings.TrimSuffix(n, ".md"))
					}
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"agents":             names,
			"daemon_binary_path": cfg.DaemonBinaryPath,
			"agent_files_dir":    cfg.DaimonFilesDir,
		})
	}
}

// nodeDeployReq is the body for POST /api/nodes/{id}/deploy.
type nodeDeployReq struct {
	Agents          []string `json:"agents"`
	PrivateKey      string   `json:"private_key"`
	Passphrase      string   `json:"passphrase,omitempty"`
	AnthropicKey    string   `json:"anthropic_api_key,omitempty"`
	OpenAIKey       string   `json:"openai_api_key,omitempty"`
	IncludeWebhook  bool     `json:"include_webhook"`
	IncludeMgmtCert bool     `json:"include_mgmt_cert"`
}

// NodeDeploy kicks off a deploy job. Returns the job id immediately;
// progress is observable via /api/jobs/{job}/log (SSE).
func NodeDeploy(store *db.Store, reg *jobs.Registry, deployer NodeDeployer, cfg NodesConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		var req nodeDeployReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if len(req.Agents) == 0 {
			http.Error(w, "agents is required", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(req.PrivateKey) == "" {
			http.Error(w, "private_key is required", http.StatusBadRequest)
			return
		}
		n, err := store.NodeByID(id)
		if err != nil {
			http.Error(w, "node not found", http.StatusNotFound)
			return
		}

		// Validate the requested agents exist in the library.
		for _, a := range req.Agents {
			path := filepath.Join(cfg.DaimonFilesDir, a+".md")
			if _, err := os.Stat(path); err != nil {
				http.Error(w, fmt.Sprintf("agent file not found: %s", a), http.StatusBadRequest)
				return
			}
		}
		if _, err := os.Stat(cfg.DaemonBinaryPath); err != nil {
			http.Error(w, "daemon binary not found at "+cfg.DaemonBinaryPath, http.StatusInternalServerError)
			return
		}

		// Issue mTLS client certs (one per agent) if requested.
		certs := map[string]sshdeploy.MgmtCertBundle{}
		if req.IncludeMgmtCert {
			for _, a := range req.Agents {
				cert, key, ca, err := deployer.IssueClientCert(a)
				if err != nil {
					http.Error(w, "issue cert: "+err.Error(), http.StatusInternalServerError)
					return
				}
				certs[a] = sshdeploy.MgmtCertBundle{
					ClientCert: cert,
					ClientKey:  key,
					CACert:     ca,
				}
			}
		}

		webhookSecret := ""
		if req.IncludeWebhook {
			webhookSecret = cfg.WebhookSecret
		}

		// Decide on the binary resolver. If the CP has a multi-arch dir
		// configured, route through the DB-backed resolver so each deploy
		// picks the correct arch on demand.
		var binResolver sshdeploy.DaemonBinaryResolver
		if cfg.DaemonBinariesDir != "" {
			binResolver = &dbBinaryResolver{store: store}
		}

		// Bind a host-key policy to this specific node so the SSH callback
		// can pin / verify against the known_hosts row.
		uploader := ""
		if u := auth.UserFromContext(r.Context()); u != nil {
			uploader = u.Email
		}
		hostKeyCallback := sshdeploy.VerifyingHostKeyCallback(
			sshdeploy.PinPolicyAdapter{
				LookupFn: func() (string, error) {
					kh, err := store.GetKnownHost(id)
					if err != nil {
						if errors.Is(err, sql.ErrNoRows) {
							return "", nil
						}
						return "", err
					}
					return kh.Fingerprint, nil
				},
				PinFn: func(keyType, fingerprint, publicKey string) error {
					return store.UpsertKnownHost(id, keyType, fingerprint, publicKey, uploader)
				},
			},
			n.Name,
			id,
		)

		dreq := sshdeploy.DeployRequest{
			Cred: sshdeploy.Credential{
				User:            n.SSHUser,
				Host:            n.Hostname,
				Port:            n.SSHPort,
				PrivateKey:      []byte(req.PrivateKey),
				Passphrase:      req.Passphrase,
				HostKeyCallback: hostKeyCallback,
			},
			AgentsToInstall:      req.Agents,
			DaemonBinaryPath:     cfg.DaemonBinaryPath,
			DaemonBinaryResolver: binResolver,
			DaimonFilesDir:        cfg.DaimonFilesDir,
			MgmtCerts:            certs,
			WebhookSecret:        webhookSecret,
			WebhookURL:           cfg.WebhookURL,
			MgmtURL:              cfg.MgmtURL,
			AnthropicAPIKey:      req.AnthropicKey,
			OpenAIAPIKey:         req.OpenAIKey,
		}

		job := reg.Create("deploy", id)
		_ = store.UpdateNodeStatus(id, db.NodeStatusDeploying, "deploy job "+job.ID)
		audit.Emit(r, store, db.AuditEntry{
			Action: "node.deploy",
			Target: fmt.Sprintf("node:%d", id),
			Metadata: map[string]any{"agents": req.Agents, "job_id": job.ID},
		})

		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			depRes, err := sshdeploy.Deploy(ctx, dreq, func(line string) { job.Append(line) })
			if err != nil {
				// Surface a host-key mismatch as a structured event so the UI
				// can show the right warning; the audit row records both
				// fingerprints for forensic review.
				var hkErr *sshdeploy.HostKeyMismatchError
				if errors.As(err, &hkErr) {
					_ = store.InsertAudit(db.AuditEntry{
						Action: "node.host_key_mismatch",
						Target: fmt.Sprintf("node:%d", id),
						Result: "denied",
						Metadata: map[string]any{
							"expected": hkErr.Expected,
							"got":      hkErr.Got,
							"got_type": hkErr.GotKeyType,
						},
					})
				}
				job.Append("✗ " + err.Error())
				job.Complete(err)
				_ = store.UpdateNodeStatus(id, db.NodeStatusFailed, err.Error())
				return
			}
			job.Complete(nil)
			_ = store.MarkNodeDeployed(id, req.Agents, depRes.DaemonHostname)
		}()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"job_id":  job.ID,
			"node_id": id,
		})
	}
}

// JobStatus returns a single job snapshot.
func JobStatus(reg *jobs.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		j := reg.Get(chi.URLParam(r, "id"))
		if j == nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		out := map[string]any{
			"id":          j.ID,
			"node_id":     j.NodeID,
			"type":        j.Type,
			"status":      j.Status,
			"error":       j.Error,
			"started_at":  j.StartedAt.Format(time.RFC3339),
			"lines":       j.Lines(),
		}
		if !j.FinishedAt.IsZero() {
			out["finished_at"] = j.FinishedAt.Format(time.RFC3339)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// JobLogStream is an SSE endpoint that pushes log lines as they arrive.
func JobLogStream(reg *jobs.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		j := reg.Get(chi.URLParam(r, "id"))
		if j == nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")

		// Replay buffered lines first.
		for _, line := range j.Lines() {
			fmt.Fprintf(w, "data: %s\n\n", line)
		}
		flusher.Flush()

		ch, cancel := j.Subscribe()
		defer cancel()
		finished := j.Finished()

		for {
			select {
			case <-r.Context().Done():
				return
			case line, ok := <-ch:
				if !ok {
					// Job finished and channel closed.
					fmt.Fprintf(w, "event: done\ndata: %s\n\n", j.Status)
					flusher.Flush()
					return
				}
				fmt.Fprintf(w, "data: %s\n\n", line)
				flusher.Flush()
			case <-finished:
				// Drain any remaining buffered lines, then send done event.
				fmt.Fprintf(w, "event: done\ndata: %s\n\n", j.Status)
				flusher.Flush()
				return
			}
		}
	}
}
