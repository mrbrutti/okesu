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

	"github.com/section9labs/okesu/controlplane/auth"
	"github.com/section9labs/okesu/controlplane/db"
	"github.com/section9labs/okesu/controlplane/federation/s3rpc"
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
