// Package s3rpc is the request/response correlation layer for the
// S3 dead-drop federation transport (Phase B). Read-side flows
// (introspect, findings, daimons, ...) are file-snapshot polling —
// no correlation needed. Operator-initiated WRITES from the parent
// against an s3 child (Add Node, Deploy Daimon, Run Agent, ...) DO
// need request/response matching: the parent submits and waits;
// the child picks up the directive on its scan tick, executes it,
// and writes the result back keyed by the same request_id.
//
// On the bucket:
//
//   cp/<parent-id>/outbound/<child-id>/req/<request-id>.json
//     ← parent writes, child reads
//   cp/<child-id>/outbound/<parent-id>/resp/<request-id>.json
//     ← child writes, parent reads
//
// `request_id` is a UUID minted at submit time. Correlation is
// purely by name — no extra index file.
//
// Idempotency: the child's Server tracks executed[request_id] in
// memory. A re-issued directive (parent's first response was lost,
// it retries) gets the cached response back without re-running.
//
// Latency: the loop is bounded by both ends' scan interval (default
// 30s each). Worst case ~60s round-trip; typical ~30-45s. The
// operator UI surfaces "in flight" so this isn't invisible.

package s3rpc

import (
	"encoding/json"
	"time"
)

// Request is the JSON object the parent writes into req/<id>.json.
// `kind` names a handler the child has registered (create_node,
// deploy_daimon, run_agent, ...). `body` is the kind-specific
// payload — the child unmarshals it into its handler's expected
// shape.
//
// `path_params` carries chi-style URL parameters when the underlying
// HTTP handler needs them (e.g. NodeDeploy reads `{id}` via
// chi.URLParam). For directives that don't have path parameters
// (NodeCreate, CreateRun) the field is omitted entirely.
type Request struct {
	RequestID    string            `json:"request_id"`
	Kind         string            `json:"kind"`
	IssuedAt     time.Time         `json:"issued_at"`
	IssuedByUser string            `json:"issued_by_user,omitempty"`
	Body         json.RawMessage   `json:"body"`
	PathParams   map[string]string `json:"path_params,omitempty"`
}

// Response is the JSON object the child writes into resp/<id>.json.
// Status is the explicit success/failure marker — the parent unwraps
// Body when "ok" and surfaces Error to the operator when "error."
//
// HTTPStatus carries the underlying handler's HTTP status when
// available (200 created → 201 created, validation errors → 400,
// auth issues on the child's side → 401/403). The parent's
// forwarding handler relays it back in the operator's HTTP response
// so the dialog UX is identical to the HTTPS path.
type Response struct {
	RequestID   string          `json:"request_id"`
	Kind        string          `json:"kind"`
	CompletedAt time.Time       `json:"completed_at"`
	Status      string          `json:"status"`           // "ok" | "error"
	HTTPStatus  int             `json:"http_status,omitempty"`
	Body        json.RawMessage `json:"body,omitempty"`
	Error       string          `json:"error,omitempty"`
}

// Kind values the publisher / dispatcher know about. Defined as
// constants so a typo in either end produces a compile error rather
// than a silent "no handler" rejection on the child.
const (
	KindCreateNode   = "create_node"
	KindDeployDaimon = "deploy_daimon"
	KindCreateRun    = "create_run"
	// Future kinds the write pipe gains in B.2+:
	//
	// KindCancelRun              = "cancel_run"
	// KindFindingSetStatus       = "finding_set_status"
	// KindOrchestrationCreate    = "orchestration_create"
	// KindOrchestrationUpdate    = "orchestration_update"
	// KindOrchestrationDelete    = "orchestration_delete"
	// KindOrchestrationRunCreate = "orchestration_run_create"
)

// Default poll cadences. Child scans for new req objects this often;
// parent polls for resp objects this often. Keep them aligned with
// the existing s3reader cadence so all federation traffic shares the
// same heartbeat rhythm.
const (
	DefaultServerPollInterval = 30 * time.Second
	DefaultClientPollInterval = 5 * time.Second  // tight client-side because it's bounded by Submit's blocking wait
	DefaultClientTimeout      = 90 * time.Second // give the child two scan ticks before giving up
)
