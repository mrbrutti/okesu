// Phase B (write pipe) — child-side dispatch from s3rpc.Server into the
// existing HTTP handlers. We don't reimplement the handler bodies for
// s3 transport; instead we synthesize an *http.Request from the
// directive's body and run it through the same code path the federation
// HTTPS endpoints use. That keeps validation, audit-log emission, and
// store wiring identical between transports.
//
// The synthetic request injects a `federation@parent` user via
// auth.WithUser so handlers that read auth.UserFromContext (e.g.
// CreateNode → audit.Emit) don't 401.

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/section9labs/okesu/controlplane/auth"
	"github.com/section9labs/okesu/controlplane/db"
	"github.com/section9labs/okesu/controlplane/federation/s3rpc"
	"github.com/section9labs/okesu/controlplane/jobs"
	"github.com/section9labs/okesu/controlplane/tunnel"
)

// NewS3CreateNodeHandler returns an s3rpc.Handler that dispatches a
// `create_node` directive into NodeCreate. The directive Body is the
// JSON payload the parent's NodeCreate handler would have received over
// HTTPS, so we forward it verbatim.
//
// We deliberately use the non-federation NodeCreate (not
// FederationNodeCreate) — the federation wrapper just adds the token
// gate, which the parent has already passed by virtue of writing to the
// child's bucket. Bucket-level auth IS the federation token in this
// transport.
func NewS3CreateNodeHandler(store *db.Store) s3rpc.Handler {
	h := NodeCreate(store)
	return func(ctx context.Context, req s3rpc.Request) s3rpc.Response {
		return runHandlerForDirective(ctx, h, req, http.MethodPost, "/api/nodes", req.IssuedByUser)
	}
}

// NewS3DeployDaimonHandler dispatches a `deploy_daimon` directive into
// NodeDeploy. The directive's PathParams must include `id` (the
// child-side node id); ForwardingNodeDeploy populates this on the
// parent side from the request URL.
//
// Security note: deploy bodies typically include an SSH private key for
// the child-side node. That key transits the shared bucket — operators
// using s3 federation must trust the bucket's IAM/encryption to the
// same level they'd trust an mTLS-protected HTTPS hop.
func NewS3DeployDaimonHandler(store *db.Store, reg *jobs.Registry, deployer NodeDeployer, cfg NodesConfig) s3rpc.Handler {
	h := NodeDeploy(store, reg, deployer, cfg)
	return func(ctx context.Context, req s3rpc.Request) s3rpc.Response {
		return runHandlerForDirective(ctx, h, req, http.MethodPost, "/api/nodes/{id}/deploy", req.IssuedByUser)
	}
}

// NewS3CreateRunHandler dispatches a `create_run` directive into
// CreateRun. The body shape matches what the parent's "Run Agent"
// dialog posts; the child resolves the `node` field against its own
// tunnel registry, so federated runs are bound by the child's
// connected fleet.
func NewS3CreateRunHandler(reg *RunRegistry, tunReg *tunnel.Registry, store *db.Store, agentDirs []string) s3rpc.Handler {
	h := CreateRun(reg, tunReg, store, agentDirs)
	return func(ctx context.Context, req s3rpc.Request) s3rpc.Response {
		return runHandlerForDirective(ctx, h, req, http.MethodPost, "/api/runs", req.IssuedByUser)
	}
}

// NewS3CancelRunHandler dispatches a `cancel_run` directive into
// CancelRun. PathParams must include `id` (the run id).
func NewS3CancelRunHandler(reg *RunRegistry, tunReg *tunnel.Registry, store *db.Store) s3rpc.Handler {
	h := CancelRun(reg, tunReg, store)
	return func(ctx context.Context, req s3rpc.Request) s3rpc.Response {
		return runHandlerForDirective(ctx, h, req, http.MethodPost, "/api/runs/{id}/cancel", req.IssuedByUser)
	}
}

// NewS3FindingSetStatusHandler dispatches a `finding_set_status`
// directive into FindingSetStatus. PathParams must include `id`.
func NewS3FindingSetStatusHandler(store *db.Store) s3rpc.Handler {
	h := FindingSetStatus(store)
	return func(ctx context.Context, req s3rpc.Request) s3rpc.Response {
		return runHandlerForDirective(ctx, h, req, http.MethodPost, "/api/findings/{id}/status", req.IssuedByUser)
	}
}

// NewS3OrchestrationCreateHandler dispatches `orchestration_create`
// into OrchestrationCreate.
func NewS3OrchestrationCreateHandler(store *db.Store) s3rpc.Handler {
	h := OrchestrationCreate(store)
	return func(ctx context.Context, req s3rpc.Request) s3rpc.Response {
		return runHandlerForDirective(ctx, h, req, http.MethodPost, "/api/orchestrations", req.IssuedByUser)
	}
}

// NewS3OrchestrationUpdateHandler dispatches `orchestration_update`
// into OrchestrationUpdate. PathParams must include `id`.
func NewS3OrchestrationUpdateHandler(store *db.Store) s3rpc.Handler {
	h := OrchestrationUpdate(store)
	return func(ctx context.Context, req s3rpc.Request) s3rpc.Response {
		return runHandlerForDirective(ctx, h, req, http.MethodPut, "/api/orchestrations/{id}", req.IssuedByUser)
	}
}

// NewS3OrchestrationDeleteHandler dispatches `orchestration_delete`
// into OrchestrationDelete. PathParams must include `id`.
func NewS3OrchestrationDeleteHandler(store *db.Store) s3rpc.Handler {
	h := OrchestrationDelete(store)
	return func(ctx context.Context, req s3rpc.Request) s3rpc.Response {
		return runHandlerForDirective(ctx, h, req, http.MethodDelete, "/api/orchestrations/{id}", req.IssuedByUser)
	}
}

// NewS3OrchestrationRunCreateHandler dispatches `orchestration_run_create`
// into OrchestrationRunCreate. PathParams must include `id`.
func NewS3OrchestrationRunCreateHandler(store *db.Store, coord *OrchestrationCoordinator) s3rpc.Handler {
	h := OrchestrationRunCreate(store, coord)
	return func(ctx context.Context, req s3rpc.Request) s3rpc.Response {
		return runHandlerForDirective(ctx, h, req, http.MethodPost, "/api/orchestrations/{id}/run", req.IssuedByUser)
	}
}

// NewS3OrchestrationRunCancelHandler dispatches `orchestration_run_cancel`
// into OrchestrationRunCancel. PathParams must include `id`.
func NewS3OrchestrationRunCancelHandler(store *db.Store) s3rpc.Handler {
	h := OrchestrationRunCancel(store)
	return func(ctx context.Context, req s3rpc.Request) s3rpc.Response {
		return runHandlerForDirective(ctx, h, req, http.MethodPost, "/api/orchestration-runs/{id}/cancel", req.IssuedByUser)
	}
}

// NewS3OrchestrationStepApproveHandler dispatches
// `orchestration_step_approve` into OrchestrationStepApprove.
// PathParams must include `id` + `stepID`.
func NewS3OrchestrationStepApproveHandler(store *db.Store, coord *OrchestrationCoordinator) s3rpc.Handler {
	h := OrchestrationStepApprove(store, coord)
	return func(ctx context.Context, req s3rpc.Request) s3rpc.Response {
		return runHandlerForDirective(ctx, h, req, http.MethodPost,
			"/api/orchestration-runs/{id}/steps/{stepID}/approve", req.IssuedByUser)
	}
}

// NewS3OrchestrationRunsBulkCancelHandler dispatches
// `orchestration_runs_bulk_cancel` into OrchestrationRunsBulkCancel.
func NewS3OrchestrationRunsBulkCancelHandler(store *db.Store) s3rpc.Handler {
	h := OrchestrationRunsBulkCancel(store)
	return func(ctx context.Context, req s3rpc.Request) s3rpc.Response {
		return runHandlerForDirective(ctx, h, req, http.MethodPost,
			"/api/orchestration-runs/bulk-cancel", req.IssuedByUser)
	}
}

// NewS3OrchestrationRunsBulkRetryHandler dispatches
// `orchestration_runs_bulk_retry` into OrchestrationRunsBulkRetry.
func NewS3OrchestrationRunsBulkRetryHandler(store *db.Store, coord *OrchestrationCoordinator) s3rpc.Handler {
	h := OrchestrationRunsBulkRetry(store, coord)
	return func(ctx context.Context, req s3rpc.Request) s3rpc.Response {
		return runHandlerForDirective(ctx, h, req, http.MethodPost,
			"/api/orchestration-runs/bulk-retry", req.IssuedByUser)
	}
}

// runHandlerForDirective is the common bridge: build an http.Request
// carrying the directive Body, attach a synthetic user matching the
// parent's audit identity, dispatch, and translate the recorder's
// response back into an s3rpc.Response.
func runHandlerForDirective(
	ctx context.Context,
	h http.HandlerFunc,
	req s3rpc.Request,
	method, path, issuedBy string,
) s3rpc.Response {
	body := []byte(req.Body)
	if len(body) == 0 {
		body = []byte("{}")
	}
	httpReq, _ := http.NewRequestWithContext(ctx, method, path, bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")

	// chi.URLParam reads from the route context attached to the
	// request. Handlers like NodeDeploy use it to pull `{id}` out of
	// the path. We can't run the real router (the synthetic request
	// bypasses it), so we set up the context manually with whatever
	// path params the directive carried.
	if len(req.PathParams) > 0 {
		rctx := chi.NewRouteContext()
		for k, v := range req.PathParams {
			rctx.URLParams.Add(k, v)
		}
		httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), chi.RouteCtxKey, rctx))
	}

	// Synthetic user — handlers that emit audit entries pull email
	// from auth.UserFromContext. The federation token gate uses
	// `federation@parent`; we mirror that here so the audit log shows
	// the same actor for both transports. The user is never persisted.
	if issuedBy == "" {
		issuedBy = "federation@parent"
	}
	syntheticUser := &db.User{Email: issuedBy, Role: "admin"}
	httpReq = httpReq.WithContext(auth.WithUser(httpReq.Context(), syntheticUser))

	rr := httpRecorder()
	h.ServeHTTP(rr, httpReq)

	resp := s3rpc.Response{
		HTTPStatus: rr.code,
	}
	if rr.code >= 200 && rr.code < 300 {
		resp.Status = "ok"
		resp.Body = json.RawMessage(rr.body)
	} else {
		resp.Status = "error"
		resp.Error = string(bytes.TrimSpace(rr.body))
		resp.Body = json.RawMessage(rr.body)
	}
	return resp
}
