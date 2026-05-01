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
	"github.com/section9labs/okesu/controlplane/ports"
	"github.com/section9labs/okesu/controlplane/sshdeploy"
	"github.com/section9labs/okesu/controlplane/tunnel"
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
	// Secrets, when set, is consulted as a fallback for the SSH private
	// key when a deploy / update / rollback request omits one. Lookup
	// uses the canonical name controlplane.SecretDeploySSHPrivateKey.
	// Operators set the stored key via Settings → Deploy → SSH Key.
	Secrets ports.Secrets
}

// resolvePrivateKey returns the SSH private key to use for a deploy
// request, plus a source label for audit logs.
//
// Priority (Phase 22.8 PR γ wire-through):
//   1. per-request body (operator pasted key for this one deploy) — explicit override
//   2. selector-bound ssh_key secret matching the target node's labels —
//      group-scoped default; uses the same selector grammar groups
//      (PR β) and env_var bindings (PR γ) consume
//   3. legacy stored fallback (deploy/ssh-private-key) — CP-wide default
//
// Returns (key, source, nil) on success or (nil, "", err) when no
// option produces a key. Source labels: "request" | "binding:<name>" |
// "stored". The label flows into the audit_log row for forensic review.
//
// nodeID/store may be zero/nil in callers that don't have a node row
// yet (rare; all current call sites have one) — the selector lookup
// is skipped in that case.
func resolvePrivateKey(ctx context.Context, requestKey string, secrets ports.Secrets, store *db.Store, nodeID int64) ([]byte, string, error) {
	if k := strings.TrimSpace(requestKey); k != "" {
		return []byte(k), "request", nil
	}
	// Selector-bound ssh_key. Scope='node' is the deploy scope; 'any'
	// scope also matches per the resolver's wildcard behaviour.
	if store != nil && nodeID > 0 {
		mk, err := store.MasterKeyFromMeta()
		if err == nil {
			resolved, err := store.ListSecretsForNode(nodeID, db.SecretScopeNode, db.SecretKindSSHKey)
			if err == nil && len(resolved) > 0 {
				// Pick the first matching binding deterministically
				// (resolver returns rows in name order). If multiple
				// bindings hit, the first one wins; admins can author
				// more specific selectors to disambiguate.
				val, err := store.GetSecretValue(resolved[0].Secret.ID, mk)
				if err == nil && val != "" {
					return []byte(val), "binding:" + resolved[0].Secret.Name, nil
				}
			}
		}
	}
	if secrets == nil {
		return nil, "", errors.New("private_key is required (no stored deploy key configured)")
	}
	// Canonical name kept in sync with controlplane.SecretDeploySSHPrivateKey.
	// Hard-coded here rather than imported to avoid a circular dependency
	// (the controlplane package imports api).
	stored, err := secrets.Get(ctx, "deploy/ssh-private-key")
	if err != nil {
		return nil, "", errors.New("private_key is required (no stored deploy key configured)")
	}
	if len(stored) == 0 {
		return nil, "", errors.New("stored deploy key is empty")
	}
	return stored, "stored", nil
}

// dbBinaryResolver looks up the daemon binary path for an os/arch from the
// daemon_binaries table. Implements sshdeploy.DaemonBinaryResolver.
// NewDBBinaryResolver constructs the multi-arch daemon-binary
// resolver backed by the daemon_binaries table. Exported so the
// install-jobs-runtime endpoint can build the same resolver the
// daimon Deploy flow uses.
func NewDBBinaryResolver(store *db.Store) sshdeploy.DaemonBinaryResolver {
	return &dbBinaryResolver{store: store}
}

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

	// Phase 7a — refreshable telemetry. Populated by the tunnel-based
	// "Refresh metadata" probe; null for nodes that haven't been
	// refreshed since the column was added.
	KernelRelease string `json:"kernel_release,omitempty"`
	OSRelease     string `json:"os_release,omitempty"`
	Arch          string `json:"arch,omitempty"`
	CPUCount      int64  `json:"cpu_count,omitempty"`
	MemoryMB      int64  `json:"memory_mb,omitempty"`
	DiskFreeMB    int64  `json:"disk_free_mb,omitempty"`
	OkesuVersion  string `json:"okesu_version,omitempty"`
	MetadataAt    string `json:"metadata_at,omitempty"`

	// AutoUpdatePaused freezes daimon hot-reload for this node. Set
	// from Settings → Deploy or per-node detail page; reflected in
	// the audit log and surfaces as a UI badge.
	AutoUpdatePaused bool `json:"auto_update_paused"`

	// Phase 4 — runtime liveness reported by the jobs runtime via
	// its mgmt-plane poll. Drive the Node-detail "Runtimes" panel.
	JobsRuntimeSeenAt string `json:"jobs_runtime_seen_at,omitempty"`
	TunnelRunning     bool   `json:"tunnel_running"`
	PreferredDispatch string `json:"preferred_dispatch,omitempty"` // "" | "tunnel" | "jobs"

	// Phase 9.6: federation source. Non-nil iff this row was federated
	// from a child CP. Nil for local rows.
	CPSource *CPSourceRef `json:"cp_source,omitempty"`
}

func toNodeJSON(n *db.Node) nodeJSON {
	out := nodeJSON{
		ID:               n.ID,
		Name:             n.Name,
		Hostname:         n.Hostname,
		DaemonHostname:   n.DaemonHostname.String,
		SSHUser:          n.SSHUser,
		SSHPort:          n.SSHPort,
		Status:           n.Status,
		Notes:            n.Notes.String,
		AutoUpdatePaused:  n.AutoUpdatePaused,
		TunnelRunning:     n.TunnelRunning,
		PreferredDispatch: n.PreferredDispatch,
		CreatedAt:         n.CreatedAt.UTC().Format(time.RFC3339),
	}
	if n.JobsRuntimeSeenAt.Valid {
		out.JobsRuntimeSeenAt = n.JobsRuntimeSeenAt.Time.UTC().Format(time.RFC3339)
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
	if n.KernelRelease.Valid {
		out.KernelRelease = n.KernelRelease.String
	}
	if n.OSRelease.Valid {
		out.OSRelease = n.OSRelease.String
	}
	if n.Arch.Valid {
		out.Arch = n.Arch.String
	}
	if n.CPUCount.Valid {
		out.CPUCount = n.CPUCount.Int64
	}
	if n.MemoryMB.Valid {
		out.MemoryMB = n.MemoryMB.Int64
	}
	if n.DiskFreeMB.Valid {
		out.DiskFreeMB = n.DiskFreeMB.Int64
	}
	if n.OkesuVersion.Valid {
		out.OkesuVersion = n.OkesuVersion.String
	}
	if n.MetadataAt.Valid {
		out.MetadataAt = n.MetadataAt.Time.UTC().Format(time.RFC3339)
	}
	return out
}

// NodesList returns registered nodes, paginated.
// GET /api/nodes?limit=N&offset=N
//
// Phase 22.8 PR β wire-through — when the caller has only scoped
// grants (no CP-wide role), the result is filtered through
// Store.FilterVisibleNodes. CP-wide grants short-circuit the filter
// to "see everything", so existing admins/operators (in their
// default-* groups) keep full visibility — no behaviour change.
func NodesList(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		ns, err := store.ListNodes(limit, offset)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Apply scoped-role visibility filter if a user is on the
		// context. Synthetic actors (federation tokens, API tokens
		// without a backing user) skip this — they keep operating on
		// whatever the route layer already gated.
		if u := auth.UserFromContext(r.Context()); u != nil && u.ID > 0 {
			ids := make([]int64, len(ns))
			for i, n := range ns {
				ids[i] = n.ID
			}
			visible, err := store.FilterVisibleNodes(u.ID, "viewer", ids)
			if err == nil {
				keep := make(map[int64]struct{}, len(visible))
				for _, id := range visible {
					keep[id] = struct{}{}
				}
				filtered := make([]*db.Node, 0, len(visible))
				for _, n := range ns {
					if _, ok := keep[n.ID]; ok {
						filtered = append(filtered, n)
					}
				}
				ns = filtered
			}
			// On error, fall through with the unfiltered set — better
			// to over-show than to break the page silently. The route
			// layer's RequireRole already gated on at least viewer.
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

// NodeAutoUpdateToggle flips the per-node freeze pin. When paused, the
// daemons running on this node stop hot-reloading new daimon
// definitions: the mgmt-plane /config endpoint mirrors their loaded
// hash back, so their drift check sees "no change."
//
// PUT /api/nodes/{id}/auto-update  body: {"paused": true|false}
func NodeAutoUpdateToggle(store *db.Store) http.HandlerFunc {
	type req struct {
		Paused bool `json:"paused"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		var body req
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if err := store.SetNodeAutoUpdatePaused(id, body.Paused); err != nil {
			http.Error(w, "store: "+err.Error(), http.StatusInternalServerError)
			return
		}
		audit.Emit(r, store, db.AuditEntry{
			Action: "node.auto_update_paused.set",
			Target: fmt.Sprintf("node:%d", id),
			Metadata: map[string]any{"paused": body.Paused},
		})
		n, err := store.NodeByID(id)
		if err != nil {
			http.Error(w, "store: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toNodeJSON(n))
	}
}

// NodeRefreshMetadata sends a probe over the existing tunnel to collect
// fresh OS/hardware/runtime metadata for a node and stores it. No SSH
// credentials needed; sub-second per node when the tunnel is connected.
//
// 503 when the node has no live tunnel — operator should wait for it to
// reconnect (queue-on-reconnect could come later).
func NodeRefreshMetadata(store *db.Store, tunReg *tunnel.Registry) http.HandlerFunc {
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
		conn := tunReg.Get(n.Name)
		if conn == nil {
			http.Error(w, fmt.Sprintf("node %q has no live tunnel — wait for reconnect", n.Name), http.StatusServiceUnavailable)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
		defer cancel()
		reply, err := conn.SendProbe(ctx, 10*time.Second)
		if err != nil {
			http.Error(w, "probe: "+err.Error(), http.StatusBadGateway)
			return
		}
		if err := store.UpdateNodeMetadata(id, db.NodeMetadataUpdate{
			DaemonHostname: reply.Hostname,
			KernelRelease:  reply.KernelRelease,
			OSRelease:      reply.OSRelease,
			Arch:           reply.Arch,
			CPUCount:       reply.CPUCount,
			MemoryMB:       reply.MemoryMB,
			DiskFreeMB:     reply.DiskFreeMB,
			OkesuVersion:   reply.OkesuVersion,
		}); err != nil {
			http.Error(w, "persist: "+err.Error(), http.StatusInternalServerError)
			return
		}
		audit.Emit(r, store, db.AuditEntry{
			Action: "node.refresh_metadata",
			Target: fmt.Sprintf("node:%d", id),
			Metadata: map[string]any{
				"hostname":       reply.Hostname,
				"kernel_release": reply.KernelRelease,
				"os_release":     reply.OSRelease,
				"probe_error":    reply.Error,
			},
		})
		updated, err := store.NodeByID(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toNodeJSON(updated))
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

// nodeBinaryReq is the body for POST /api/nodes/{id}/update-binary
// and /rollback-binary. The SSH key is operator-supplied per request
// (same UX as the deploy form) — never stored.
type nodeBinaryReq struct {
	PrivateKey   string `json:"private_key"`
	Passphrase   string `json:"passphrase,omitempty"`
	// SudoPassword is forwarded to `sudo -S` on the target when the
	// SSH user isn't root and doesn't have passwordless sudo. Mac
	// developer machines need this; Linux containers running as root
	// don't. Never persisted server-side.
	SudoPassword string `json:"sudo_password,omitempty"`
}

// NodeUpdateBinary swaps the okesu binary on a node with the version
// from the CP's --daemon-binaries-dir, restarting agent services. The
// previous binary is preserved at /usr/local/bin/okesu.previous so a
// rollback action can flip it back.
//
// Returns the job_id immediately; progress is observable via the
// existing /api/jobs/{job}/log SSE stream that deploys already use.
func NodeUpdateBinary(store *db.Store, reg *jobs.Registry, cfg NodesConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		var req nodeBinaryReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		privKey, keySource, err := resolvePrivateKey(r.Context(), req.PrivateKey, cfg.Secrets, store, id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		n, err := store.NodeByID(id)
		if err != nil {
			http.Error(w, "node not found", http.StatusNotFound)
			return
		}

		// Resolve the binary just like the deploy flow does — multi-arch
		// resolver wins, falls back to the single-arch path.
		var binResolver sshdeploy.DaemonBinaryResolver
		if cfg.DaemonBinariesDir != "" {
			binResolver = &dbBinaryResolver{store: store}
		}
		if binResolver == nil {
			if _, err := os.Stat(cfg.DaemonBinaryPath); err != nil {
				http.Error(w, "daemon binary not found at "+cfg.DaemonBinaryPath, http.StatusInternalServerError)
				return
			}
		}

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

		ureq := sshdeploy.UpdateBinaryRequest{
			Cred: sshdeploy.Credential{
				User:            n.SSHUser,
				Host:            n.Hostname,
				Port:            n.SSHPort,
				PrivateKey:      privKey,
				SudoPassword:    req.SudoPassword,
				Passphrase:      req.Passphrase,
				HostKeyCallback: hostKeyCallback,
			},
			DaemonBinaryPath:     cfg.DaemonBinaryPath,
			DaemonBinaryResolver: binResolver,
			AgentNames:           splitAgentsInstalled(n.AgentsInstalled.String),
		}

		job := reg.Create("update-binary", id)
		audit.Emit(r, store, db.AuditEntry{
			Action: "node.update_binary",
			Target: fmt.Sprintf("node:%d", id),
			Metadata: map[string]any{"job_id": job.ID, "agents": ureq.AgentNames, "ssh_key_source": keySource},
		})

		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			err := sshdeploy.UpdateBinary(ctx, ureq, func(line string) { job.Append(line) })
			if err != nil {
				job.Append("✗ " + err.Error())
				job.Complete(err)
				return
			}
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

// NodeRollbackBinary reverts the binary swap performed by the most
// recent NodeUpdateBinary call: rename /usr/local/bin/okesu.previous
// back to /usr/local/bin/okesu and bounce the agents.
func NodeRollbackBinary(store *db.Store, reg *jobs.Registry, cfg NodesConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		var req nodeBinaryReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		privKey, keySource, err := resolvePrivateKey(r.Context(), req.PrivateKey, cfg.Secrets, store, id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		n, err := store.NodeByID(id)
		if err != nil {
			http.Error(w, "node not found", http.StatusNotFound)
			return
		}

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

		rreq := sshdeploy.RollbackBinaryRequest{
			Cred: sshdeploy.Credential{
				User:            n.SSHUser,
				Host:            n.Hostname,
				Port:            n.SSHPort,
				PrivateKey:      privKey,
				SudoPassword:    req.SudoPassword,
				Passphrase:      req.Passphrase,
				HostKeyCallback: hostKeyCallback,
			},
			AgentNames: splitAgentsInstalled(n.AgentsInstalled.String),
		}

		job := reg.Create("rollback-binary", id)
		audit.Emit(r, store, db.AuditEntry{
			Action: "node.rollback_binary",
			Target: fmt.Sprintf("node:%d", id),
			Metadata: map[string]any{"job_id": job.ID, "agents": rreq.AgentNames, "ssh_key_source": keySource},
		})

		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			err := sshdeploy.RollbackBinary(ctx, rreq, func(line string) { job.Append(line) })
			if err != nil {
				job.Append("✗ " + err.Error())
				job.Complete(err)
				return
			}
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

// splitAgentsInstalled converts the comma-separated `agents_installed`
// column into a list, trimming whitespace and dropping empties.
func splitAgentsInstalled(s string) []string {
	if s == "" {
		return nil
	}
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// nodeDeployReq is the body for POST /api/nodes/{id}/deploy.
type nodeDeployReq struct {
	Agents          []string `json:"agents"`
	PrivateKey      string   `json:"private_key"`
	Passphrase      string   `json:"passphrase,omitempty"`
	// SudoPassword forwarded to `sudo -S` on the target — see
	// nodeBinaryReq.SudoPassword for the full rationale.
	SudoPassword    string   `json:"sudo_password,omitempty"`
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
		privKey, keySource, err := resolvePrivateKey(r.Context(), req.PrivateKey, cfg.Secrets, store, id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
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

		// Phase 6: always issue a node-level cert + ship the jobs
		// runtime alongside the daimons. This keeps the deploy
		// footprint coherent — every fresh node lands ready to
		// receive orchestration step jobs without operators having
		// to run a separate install. Failure is non-fatal: if the
		// CP can't issue a cert (no CA configured), we skip and
		// the daimon-only path still completes.
		var jobsRuntime *sshdeploy.MgmtCertBundle
		if jc, jk, jca, jerr := deployer.IssueClientCert(n.Name); jerr == nil {
			jobsRuntime = &sshdeploy.MgmtCertBundle{ClientCert: jc, ClientKey: jk, CACert: jca}
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
				PrivateKey:      privKey,
				SudoPassword:    req.SudoPassword,
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
			JobsRuntime:          jobsRuntime,
			NodeName:             n.Name,
		}

		job := reg.Create("deploy", id)
		_ = store.UpdateNodeStatus(id, db.NodeStatusDeploying, "deploy job "+job.ID)
		audit.Emit(r, store, db.AuditEntry{
			Action: "node.deploy",
			Target: fmt.Sprintf("node:%d", id),
			Metadata: map[string]any{"agents": req.Agents, "job_id": job.ID, "ssh_key_source": keySource},
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
