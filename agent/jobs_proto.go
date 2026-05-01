package agent

// Wire types for the pull-mode jobs queue.
//
// The CP holds an `node_jobs` queue keyed by node name. The
// host-side `okesu jobs` runtime polls GET /api/v1/agents/jobs every
// few seconds, claims any pending jobs atomically, executes them, and
// reports back via /output (chunked) and /exit (terminal).
//
// Why the wire types live in `agent/`: the daemon side needs them
// to encode/decode requests, and the CP side imports `agent` already
// for shared types like Event. Keeping the protocol here avoids a
// new shared package and keeps the import direction clean (api → agent,
// never the other way).

// JobKind discriminates what the daemon does when it claims a job.
// All payload-shaped fields live in JobPayload and the daemon
// inspects Kind to know which fields to read.
type JobKind string

const (
	// JobKindAgentRun spawns `okesu claude/codex/auto` with the
	// supplied agent definition + prompt. Stdout streams back via
	// /output; exit reports via /exit. The run_id matches the
	// existing `runs.id` so the orchestrator's job-dispatcher can
	// stitch the output into the SSE feed UIs already subscribe to.
	JobKindAgentRun JobKind = "agent_run"

	// JobKindStartTunnel asks the jobs runtime to spawn `okesu node`
	// as a managed child. The child opens the reverse-mTLS tunnel
	// to the CP. Used by the orchestrator when a step explicitly
	// requests `dispatch: tunnel` and no tunnel is currently up.
	JobKindStartTunnel JobKind = "start_tunnel"

	// JobKindStopTunnel sigterms the managed tunnel child if any.
	JobKindStopTunnel JobKind = "stop_tunnel"
)

// Job is one row out of the queue, claimed atomically by a poll.
// The CP sets ID + Kind; the daemon reads Payload based on Kind and
// reports terminal status via Exit.
type Job struct {
	ID      int64       `json:"id"`
	Kind    JobKind     `json:"kind"`
	RunID   string      `json:"run_id,omitempty"` // populated for agent_run
	Payload JobPayload  `json:"payload"`
	// CreatedAt as unix-ms — useful for the daemon to log lag and
	// for the CP to enforce per-step timeouts even if the daemon
	// claims a stale job after a network partition.
	CreatedAt int64 `json:"created_at"`
}

// JobPayload is a union of fields keyed by JobKind. We use a single
// struct rather than typed payload to keep the wire format flat and
// avoid double-marshalling. Daemons ignore fields that don't apply
// to the kind they're handling.
type JobPayload struct {
	// agent_run fields
	Agent        string `json:"agent,omitempty"`
	AgentContent string `json:"agent_content,omitempty"`
	Prompt       string `json:"prompt,omitempty"`
	Provider     string `json:"provider,omitempty"`
	Model        string `json:"model,omitempty"`
	Effort       string `json:"effort,omitempty"`
	MaxTurns     int    `json:"max_turns,omitempty"`

	// Env is per-job environment variables merged into the spawned
	// child's environment. Phase 22.8 PR γ wire-through: the CP
	// resolves selector-bound env_var secrets against the target
	// node's labels and attaches the matching values here, so a
	// `PROD_API_KEY` secret bound to `env=prod` ends up in the
	// daemon's process env automatically.
	//
	// The daemon-side merge is os.Environ() ∪ Env, with Env values
	// winning on key collision. Backwards-compat: empty/missing Env
	// behaves identically to today.
	Env map[string]string `json:"env,omitempty"`

	// start_tunnel fields
	CPMgmtURL string `json:"cp_mgmt_url,omitempty"` // e.g. https://cp.example.com:8444
	CertDir   string `json:"cert_dir,omitempty"`    // where node-tunnel certs live (defaults to /etc/okesu/node-certs)
	NodeName  string `json:"node_name,omitempty"`   // identifier the tunnel registers under
}

// JobsPollResponse is the body of GET /api/v1/agents/jobs.
type JobsPollResponse struct {
	Jobs []Job `json:"jobs"`
	// NextPollMs is the recommended interval before the next poll.
	// The CP can throttle nodes during quiet periods to reduce load
	// without the daemon having to know the policy.
	NextPollMs int `json:"next_poll_ms,omitempty"`
}

// JobOutputChunk is one batch of stdout/stderr from an in-flight
// agent_run. POSTed to /api/v1/agents/jobs/{id}/output as the daemon
// captures lines. The CP appends them to the underlying run_lines so
// the SSE stream the run-detail page subscribes to picks them up.
type JobOutputChunk struct {
	Stream string `json:"stream"` // "stdout" | "stderr"
	Data   string `json:"data"`   // raw text — newlines preserved
}

// JobExit is the terminal report. Posted exactly once per job to
// /api/v1/agents/jobs/{id}/exit. After this the daemon discards the
// job and the CP marks it succeeded/failed.
type JobExit struct {
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error,omitempty"`     // non-empty iff the daemon hit an error before/around running
	// TunnelStarted reports start_tunnel outcome — true if the
	// managed `okesu node` child registered with the tunnel
	// registry, false if the spawn failed.
	TunnelStarted bool `json:"tunnel_started,omitempty"`
}

// HeartbeatExtension fields the jobs runtime adds to the standard
// daimon heartbeat so the CP can track liveness + tunnel state
// without a separate endpoint. Embedded in the existing
// /api/v1/agents/{name}/heartbeat payload via JSON spread on the
// daemon side.
type HeartbeatExtension struct {
	JobsRuntimeReady bool `json:"jobs_runtime_ready,omitempty"` // jobs runtime is the one heartbeating
	TunnelRunning    bool `json:"tunnel_running,omitempty"`     // managed tunnel child is alive
}
