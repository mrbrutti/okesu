package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/section9labs/okesu/controlplane/auth"
	"github.com/section9labs/okesu/controlplane/db"
	"github.com/section9labs/okesu/controlplane/federation"
	"github.com/section9labs/okesu/controlplane/federation/s3rpc"
	"github.com/section9labs/okesu/controlplane/orchestrator"
	"github.com/section9labs/okesu/controlplane/tunnel"
)

// idPathParams extracts {id} from the chi route context. Used by the
// per-resource forwarding wrappers to thread the URL parameter into
// the s3 directive's PathParams. Returns nil when no `id` is bound.
func idPathParams(r *http.Request) map[string]string {
	if id := chi.URLParam(r, "id"); id != "" {
		return map[string]string{"id": id}
	}
	return nil
}

// idStepPathParams extracts {id} + {stepID} from the chi route context
// for the orchestration_step_approve directive. Returns nil if either
// param is missing.
func idStepPathParams(r *http.Request) map[string]string {
	id := chi.URLParam(r, "id")
	step := chi.URLParam(r, "stepID")
	if id == "" || step == "" {
		return nil
	}
	return map[string]string{"id": id, "stepID": step}
}

// Phase 9.7: federation writes.
//
// Pattern: each affected parent write handler is wrapped by a thin
// "forwarder" that peeks the request body for `target_cp_instance_id`.
// If set and matches a registered peer, the handler proxies the same
// body to the peer's federation write endpoint (token-authed) and
// streams the response back. Otherwise it falls through to the local
// handler.
//
// The child side is just the existing local handlers wrapped with
// requireFederationToken — same code path, same wire shape, different
// auth.

const proxyTimeout = 30 * time.Second

// proxyIfTargetCP looks at the JSON body for `target_cp_instance_id`.
// Returns:
//
//   - (false, nil)   — no target set, caller should run local handler
//   - (true,  nil)   — proxied to a peer, response already written
//   - (true,  err)   — proxy attempt failed and a 4xx/5xx was already
//                      written; caller should not also touch w
//
// The body is read once and re-attached to r.Body so a falling-through
// caller can still decode it.
//
// `s3Kind` is the s3rpc directive kind to submit when the target peer's
// transport is `s3_dead_drop`. Empty s3Kind means "this directive isn't
// supported over s3 yet" — for those, an s3 target gets a 501 with a
// hint pointing operators back to the read pipe / HTTPS path.
//
// `s3PathParams` carries chi-style URL parameters (e.g. {"id":"42"})
// for directives whose underlying handler reads chi.URLParam. Nil for
// directives without path params.
func proxyIfTargetCP(w http.ResponseWriter, r *http.Request, agg *federation.Aggregator, federationPath, s3Kind string, s3PathParams map[string]string) (handled bool, err error) {
	body, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(body))

	// Probe the body for the target field. Empty body / non-JSON
	// just falls through to the local handler.
	if len(body) == 0 || body[0] != '{' {
		return false, nil
	}
	var probe struct {
		TargetCPInstanceID string `json:"target_cp_instance_id"`
	}
	_ = json.Unmarshal(body, &probe)
	if probe.TargetCPInstanceID == "" {
		return false, nil
	}

	peers, _ := agg.HealthyPeers()
	var target *federation.Peer
	for i := range peers {
		if peers[i].Snapshot.InstanceID == probe.TargetCPInstanceID {
			target = &peers[i]
			break
		}
	}
	if target == nil {
		http.Error(w, "target CP not found or unhealthy: "+probe.TargetCPInstanceID, http.StatusNotFound)
		return true, nil
	}

	// Strip the target_cp_instance_id from the proxied body — the
	// child doesn't need it (it's the ultimate target) and leaving
	// it in could confuse downstream tooling.
	cleaned, _ := stripTargetField(body)

	// S3 dead-drop transport: the child doesn't have an inbound HTTPS
	// path. Use the bucket write pipe (s3rpc) instead, which polls
	// req/<id>.json and writes resp/<id>.json on the child's tick.
	if target.Row.Transport == "s3_dead_drop" {
		return forwardOverS3(w, r, agg, *target, s3Kind, cleaned, s3PathParams), nil
	}

	url := strings.TrimRight(target.Row.URL, "/") + federationPath
	req, err := http.NewRequestWithContext(r.Context(), r.Method, url, bytes.NewReader(cleaned))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return true, err
	}
	req.Header.Set("X-Okesu-Federation-Token", target.Row.Token)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{
		Timeout:   proxyTimeout,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
	}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "proxy to "+target.Snapshot.DisplayName+": "+err.Error(), http.StatusBadGateway)
		return true, err
	}
	defer resp.Body.Close()
	// Stream the child's response back unchanged so the UI sees the
	// usual contract (e.g. {id, name, ...} for NodeCreate).
	for k, vs := range resp.Header {
		// Skip hop-by-hop headers Go's net/http copies on its own.
		if strings.EqualFold(k, "Content-Length") || strings.EqualFold(k, "Transfer-Encoding") {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	// Add a marker header so the operator's audit log can pick up
	// the cross-CP hop.
	w.Header().Set("X-Okesu-Forwarded-To", target.Snapshot.InstanceID)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
	return true, nil
}

// forwardOverS3 dispatches the directive via the s3 write pipe and
// renders the child's Response back to the operator. Always returns
// true (the caller has already decided this is the s3 path).
//
// Empty s3Kind = no Phase B handler exists for this directive yet;
// surface a 501 telling the operator to use the HTTPS path.
func forwardOverS3(w http.ResponseWriter, r *http.Request, agg *federation.Aggregator, peer federation.Peer, s3Kind string, body []byte, pathParams map[string]string) bool {
	if s3Kind == "" {
		http.Error(w,
			fmt.Sprintf("target %s uses s3_dead_drop transport which does not yet support this directive — use the HTTPS path or wait for Phase B.1+",
				peer.Snapshot.DisplayName),
			http.StatusNotImplemented)
		return true
	}
	issuedBy := ""
	if u := auth.UserFromContext(r.Context()); u != nil {
		issuedBy = u.Email
	}
	// Bound the wait at the s3rpc default; operators see the dialog
	// "in flight" while we poll.
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	resp, err := agg.SubmitS3Directive(ctx, peer, s3Kind, body, issuedBy, pathParams)
	if err != nil {
		http.Error(w, "s3 directive: "+err.Error(), http.StatusBadGateway)
		return true
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Okesu-Forwarded-To", peer.Snapshot.InstanceID)
	w.Header().Set("X-Okesu-Forward-Transport", "s3_dead_drop")
	status := resp.HTTPStatus
	if status == 0 {
		if resp.Status == "ok" {
			status = http.StatusOK
		} else {
			status = http.StatusBadGateway
		}
	}
	w.WriteHeader(status)
	if len(resp.Body) > 0 {
		_, _ = w.Write(resp.Body)
		return true
	}
	if resp.Error != "" {
		_ = json.NewEncoder(w).Encode(map[string]string{"error": resp.Error})
		return true
	}
	return true
}

// stripTargetField removes target_cp_instance_id from a JSON body so
// the child doesn't see the parent's targeting field.
func stripTargetField(body []byte) ([]byte, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return body, err
	}
	delete(m, "target_cp_instance_id")
	return json.Marshal(m)
}

// ForwardingNodeCreate wraps NodeCreate. When the body carries
// target_cp_instance_id, the request is proxied to the chosen child
// CP's /api/v1/federation/nodes — so an operator can register a node
// in the East CP from the Global UI without leaving the page.
func ForwardingNodeCreate(store *db.Store, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if handled, _ := proxyIfTargetCP(w, r, agg, "/api/v1/federation/nodes", s3rpc.KindCreateNode, nil); handled {
			return
		}
		NodeCreate(store).ServeHTTP(w, r)
	}
}

// FederationNodeCreate is the child-side endpoint that ForwardingNodeCreate
// proxies to. Token-authed; otherwise identical to NodeCreate.
func FederationNodeCreate(store *db.Store) http.HandlerFunc {
	return requireFederationToken(store, NodeCreate(store))
}

// proxyToCPByQuery handles the ?cp=<instance_id> query convention used
// by the parent's detail-page handlers. When the query is set and
// matches a healthy peer, the GET is proxied to the child's matching
// federation read endpoint and the body streamed back.
//
// Returns (handled, err). handled=true means the response was already
// written; the caller should NOT touch w. The federationPath should
// already include the resource id (e.g. "/api/v1/federation/nodes/42").
func proxyToCPByQuery(w http.ResponseWriter, r *http.Request, agg *federation.Aggregator, federationPath string) (handled bool, err error) {
	cpID := r.URL.Query().Get("cp")
	if cpID == "" {
		return false, nil
	}
	peers, _ := agg.HealthyPeers()
	var target *federation.Peer
	for i := range peers {
		if peers[i].Snapshot.InstanceID == cpID {
			target = &peers[i]
			break
		}
	}
	if target == nil {
		http.Error(w, "target CP not found or unhealthy: "+cpID, http.StatusNotFound)
		return true, nil
	}
	url := strings.TrimRight(target.Row.URL, "/") + federationPath
	// Pass through any other query params (the receiver may use them).
	q := r.URL.Query()
	q.Del("cp")
	if enc := q.Encode(); enc != "" {
		url += "?" + enc
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, url, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return true, err
	}
	req.Header.Set("X-Okesu-Federation-Token", target.Row.Token)
	req.Header.Set("Accept", "application/json")
	client := &http.Client{
		Timeout:   proxyTimeout,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
	}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "proxy to "+target.Snapshot.DisplayName+": "+err.Error(), http.StatusBadGateway)
		return true, err
	}
	defer resp.Body.Close()
	for k, vs := range resp.Header {
		if strings.EqualFold(k, "Content-Length") || strings.EqualFold(k, "Transfer-Encoding") {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.Header().Set("X-Okesu-Forwarded-To", target.Snapshot.InstanceID)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
	return true, nil
}

// FederatedNodeDetail wraps NodeDetail. When ?cp=<instance_id> is in
// the URL, proxies to that child's /api/v1/federation/nodes/{id}.
func FederatedNodeDetail(store *db.Store, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Reconstruct the federation path from the same chi URL var
		// the local handler uses. We can't access chi.URLParam here
		// because the route hasn't been matched against the
		// federation pattern; just use the raw path.
		// e.g. /api/nodes/42 -> /api/v1/federation/nodes/42
		path := strings.Replace(r.URL.Path, "/api/nodes/", "/api/v1/federation/nodes/", 1)
		if handled, _ := proxyToCPByQuery(w, r, agg, path); handled {
			return
		}
		NodeDetail(store).ServeHTTP(w, r)
	}
}

// FederationNodeDetail is the child-side endpoint
// (GET /api/v1/federation/nodes/{id}). Token-authed sibling of
// NodeDetail.
func FederationNodeDetail(store *db.Store) http.HandlerFunc {
	return requireFederationToken(store, NodeDetail(store))
}

// FederatedAgentDetail wraps AgentDetail. Same ?cp= proxy convention.
func FederatedAgentDetail(store *db.Store, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.Replace(r.URL.Path, "/api/agents/", "/api/v1/federation/daimons/", 1)
		if handled, _ := proxyToCPByQuery(w, r, agg, path); handled {
			return
		}
		AgentDetail(store).ServeHTTP(w, r)
	}
}

// FederationAgentDetail is the child-side endpoint
// (GET /api/v1/federation/daimons/{name}). Token-authed sibling.
func FederationAgentDetail(store *db.Store) http.HandlerFunc {
	return requireFederationToken(store, AgentDetail(store))
}

// FederatedFindingDetail wraps FindingDetail. When ?cp=<instance_id> is
// in the URL, proxies to that child's /api/v1/federation/findings/{id}.
// Without this wrapper a click on a federated finding tries to look up
// the local id space and 404s — finding ids are scoped per-CP, so the
// parent only knows the row through the federated grouped/list calls.
func FederatedFindingDetail(store *db.Store, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.Replace(r.URL.Path, "/api/findings/", "/api/v1/federation/findings/", 1)
		if handled, _ := proxyToCPByQuery(w, r, agg, path); handled {
			return
		}
		FindingDetail(store).ServeHTTP(w, r)
	}
}

// FederationFindingDetail is the child-side endpoint
// (GET /api/v1/federation/findings/{id}). Token-authed sibling.
func FederationFindingDetail(store *db.Store) http.HandlerFunc {
	return requireFederationToken(store, FindingDetail(store))
}

// FederatedRunsForFinding wraps RunsForFinding with the same ?cp= proxy
// — the related-runs panel on the finding detail page would otherwise
// 404 on a federated finding for the same id-space reason.
func FederatedRunsForFinding(store *db.Store, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.Replace(r.URL.Path, "/api/findings/", "/api/v1/federation/findings/", 1)
		if handled, _ := proxyToCPByQuery(w, r, agg, path); handled {
			return
		}
		RunsForFinding(store).ServeHTTP(w, r)
	}
}

// FederationRunsForFinding is the child-side endpoint
// (GET /api/v1/federation/findings/{id}/runs). Token-authed sibling.
func FederationRunsForFinding(store *db.Store) http.HandlerFunc {
	return requireFederationToken(store, RunsForFinding(store))
}

// FederationRunSync is the child-side blocking-run endpoint used by
// the orchestrator's federatedDispatcher. The parent posts a step
// description (agent + prompt + node) and the child runs it locally
// via its tunnel runtime, then returns the structured DispatchResult.
//
// Signature: POST /api/v1/federation/runs/sync
//
//	body: { agent, node, prompt, timeout_seconds? }
//	resp: orchestrator.DispatchResult JSON
//
// Token-authed via the same federation gate as every other federation
// endpoint. The synthetic `federation@parent` user is injected for the
// audit trail.
func FederationRunSync(reg *RunRegistry, tunReg *tunnel.Registry, store *db.Store, agentDirs []string) http.HandlerFunc {
	return requireFederationToken(store, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Agent          string `json:"agent"`
			Node           string `json:"node"`
			Prompt         string `json:"prompt"`
			TimeoutSeconds int    `json:"timeout_seconds"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if req.Prompt == "" || req.Node == "" {
			http.Error(w, "node and prompt are required", http.StatusBadRequest)
			return
		}

		ctx := r.Context()
		if req.TimeoutSeconds > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, time.Duration(req.TimeoutSeconds)*time.Second)
			defer cancel()
		}

		result, err := runStepLocal(ctx, reg, tunReg, store, agentDirs, orchestrator.DispatchRequest{
			AgentName:    req.Agent,
			NodeSelector: req.Node,
			Prompt:       req.Prompt,
			Timeout:      time.Duration(req.TimeoutSeconds) * time.Second,
		})
		if err != nil {
			// Surface the dispatch error in the result body so the
			// parent's engine records it on the step. We use 200 +
			// `error` rather than 5xx so the parent can distinguish
			// "step failed cleanly" from "transport broke."
			result = orchestrator.DispatchResult{
				Status: orchestrator.StepStatusFailed,
				Error:  err.Error(),
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	})
}

// ── orchestration federation wrappers ───────────────────────────────

// FederatedOrchestrationsList returns the local list merged with each
// federated child's. Each remote row carries cp_source so the UI can
// show where it came from. Same fan-out pattern as
// FederatedFindingsList.
func FederatedOrchestrationsList(store *db.Store, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		localRR := httpRecorder()
		OrchestrationsList(store).ServeHTTP(localRR, r)
		if localRR.code != http.StatusOK {
			w.WriteHeader(localRR.code)
			_, _ = w.Write(localRR.body)
			return
		}
		var localRows []map[string]any
		_ = json.Unmarshal(localRR.body, &localRows)

		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		var mu sync.Mutex
		merged := append([]map[string]any{}, localRows...)
		results, _ := agg.FanOut(ctx, func(ctx context.Context, peer federation.Peer) error {
			var rows []map[string]any
			if err := agg.FetchJSON(ctx, peer, "/api/v1/federation/orchestrations", &rows); err != nil {
				return err
			}
			tag := map[string]any{
				"instance_id":  peer.Snapshot.InstanceID,
				"display_name": peer.Snapshot.DisplayName,
				"region":       peer.Snapshot.Region,
			}
			for i := range rows {
				rows[i]["cp_source"] = tag
			}
			mu.Lock()
			merged = append(merged, rows...)
			mu.Unlock()
			return nil
		})
		if pErr := federation.AnyError(results); pErr != nil {
			w.Header().Set("X-Okesu-Federation-Warning", pErr.Error())
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(merged)
	}
}

// FederationOrchestrationsList — child-side token-authed sibling.
func FederationOrchestrationsList(store *db.Store) http.HandlerFunc {
	return requireFederationToken(store, OrchestrationsList(store))
}

// ── investigations federation wrappers ──────────────────────────────

// FederatedInvestigationsList returns the local list merged with each
// federated child's. Each remote row carries cp_source so the
// investigations page UI can show the case's owning CP and deep-link
// to the child via ?cp=<id> on detail.
//
// Investigation detail (the workspace tabs) stays child-scoped via
// the existing ?cp= proxy convention — federated cases aren't
// "merged" into a parent case; they're surfaced for visibility.
func FederatedInvestigationsList(store *db.Store, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		localRR := httpRecorder()
		ListInvestigationsHandler(store).ServeHTTP(localRR, r)
		if localRR.code != http.StatusOK {
			w.WriteHeader(localRR.code)
			_, _ = w.Write(localRR.body)
			return
		}
		var localRows []map[string]any
		_ = json.Unmarshal(localRR.body, &localRows)

		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		var mu sync.Mutex
		merged := append([]map[string]any{}, localRows...)
		results, _ := agg.FanOut(ctx, func(ctx context.Context, peer federation.Peer) error {
			var rows []map[string]any
			if err := agg.FetchJSON(ctx, peer, "/api/v1/federation/investigations", &rows); err != nil {
				return err
			}
			tag := map[string]any{
				"instance_id":  peer.Snapshot.InstanceID,
				"display_name": peer.Snapshot.DisplayName,
				"region":       peer.Snapshot.Region,
			}
			for i := range rows {
				rows[i]["cp_source"] = tag
			}
			mu.Lock()
			merged = append(merged, rows...)
			mu.Unlock()
			return nil
		})
		if pErr := federation.AnyError(results); pErr != nil {
			w.Header().Set("X-Okesu-Federation-Warning", pErr.Error())
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(merged)
	}
}

// FederationInvestigationsList — child-side token-authed sibling.
// Same query-string contract as ListInvestigationsHandler (?status,
// ?limit). Used by the parent's aggregator over HTTPS and by the
// s3publisher for the bucket-cached snapshot.
func FederationInvestigationsList(store *db.Store) http.HandlerFunc {
	return requireFederationToken(store, ListInvestigationsHandler(store))
}

// FederatedInvestigationDetail proxies a single GET via ?cp= to the
// owning child CP, falling through to the local store otherwise.
// Mirrors FederatedOrchestrationDetail's shape.
func FederatedInvestigationDetail(store *db.Store, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.Replace(r.URL.Path, "/api/investigations/", "/api/v1/federation/investigations/", 1)
		if handled, _ := proxyToCPByQuery(w, r, agg, path); handled {
			return
		}
		GetInvestigationHandler(store).ServeHTTP(w, r)
	}
}

// FederationInvestigationDetail — child-side token-authed sibling.
func FederationInvestigationDetail(store *db.Store) http.HandlerFunc {
	return requireFederationToken(store, GetInvestigationHandler(store))
}

// FederatedCreateInvestigation forwards POST /api/investigations to
// the owning child CP when `?cp=<instance_id>` is set. Without
// the query param, the case is created locally.
//
// This is what makes "Open in investigation" work from a federated
// finding's drawer: the finding lives on the child, so the case +
// link must also live there (the parent has no row for the finding,
// so the FK on investigation_findings would fail). The UI passes
// `?cp=` from `finding.cp_source.instance_id` when promoting a
// federated finding.
func FederatedCreateInvestigation(store *db.Store, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if handled, _ := proxyWriteByQuery(w, r, agg, "/api/v1/federation/investigations", "", nil); handled {
			return
		}
		CreateInvestigationHandler(store).ServeHTTP(w, r)
	}
}

// FederationCreateInvestigation — child-side, token-authed sibling.
// Reuses the local CreateInvestigationHandler so `from_finding_id`
// resolves against the child's local finding id space.
func FederationCreateInvestigation(store *db.Store) http.HandlerFunc {
	return requireFederationToken(store, CreateInvestigationHandler(store))
}

// FederatedLinkFindingToInvestigation forwards PUT /api/investigations/
// {id}/findings/{fid} to the owning child via `?cp=<id>`. Used when
// linking a federated finding to a federated case from the parent UI.
func FederatedLinkFindingToInvestigation(store *db.Store, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.Replace(r.URL.Path, "/api/investigations/", "/api/v1/federation/investigations/", 1)
		if handled, _ := proxyWriteByQuery(w, r, agg, path, "", nil); handled {
			return
		}
		LinkFindingToInvestigationHandler(store).ServeHTTP(w, r)
	}
}

// FederationLinkFindingToInvestigation — child-side, token-authed.
func FederationLinkFindingToInvestigation(store *db.Store) http.HandlerFunc {
	return requireFederationToken(store, LinkFindingToInvestigationHandler(store))
}

// FederatedLinkRunToInvestigation forwards PUT /api/investigations/
// {id}/runs/{run_id} to the owning child via `?cp=<id>`. Used when
// the workspace's "Run orchestration" button creates a run on a
// federated child and immediately links it back to the case there.
func FederatedLinkRunToInvestigation(store *db.Store, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.Replace(r.URL.Path, "/api/investigations/", "/api/v1/federation/investigations/", 1)
		if handled, _ := proxyWriteByQuery(w, r, agg, path, "", nil); handled {
			return
		}
		LinkRunToInvestigationHandler(store).ServeHTTP(w, r)
	}
}

// FederationLinkRunToInvestigation — child-side, token-authed.
func FederationLinkRunToInvestigation(store *db.Store) http.HandlerFunc {
	return requireFederationToken(store, LinkRunToInvestigationHandler(store))
}

// FederatedListInvestigationsForFinding — read proxy for the
// case-membership lookup used by the InvestigateDialog. Without
// this wrapper, asking "what cases is this federated finding in?"
// hits the parent's local DB (which doesn't have the finding) and
// silently returns 0 rows.
func FederatedListInvestigationsForFinding(store *db.Store, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.Replace(r.URL.Path, "/api/findings/", "/api/v1/federation/findings/", 1)
		if handled, _ := proxyToCPByQuery(w, r, agg, path); handled {
			return
		}
		ListInvestigationsForFindingHandler(store).ServeHTTP(w, r)
	}
}

// FederationListInvestigationsForFinding — child-side, token-authed.
func FederationListInvestigationsForFinding(store *db.Store) http.HandlerFunc {
	return requireFederationToken(store, ListInvestigationsForFindingHandler(store))
}

// FederatedOrchestrationDetail proxies a single GET via ?cp= to the
// owning child CP, falling through to the local store otherwise.
func FederatedOrchestrationDetail(store *db.Store, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.Replace(r.URL.Path, "/api/orchestrations/", "/api/v1/federation/orchestrations/", 1)
		if handled, _ := proxyToCPByQuery(w, r, agg, path); handled {
			return
		}
		OrchestrationDetail(store).ServeHTTP(w, r)
	}
}

func FederationOrchestrationDetail(store *db.Store) http.HandlerFunc {
	return requireFederationToken(store, OrchestrationDetail(store))
}

// FederatedOrchestrationCreate forwards by `target_cp_instance_id` in
// the body — same convention as Add Node. When set, the orchestration
// is created on the chosen child instead of the parent.
func FederatedOrchestrationCreate(store *db.Store, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if handled, _ := proxyIfTargetCP(w, r, agg, "/api/v1/federation/orchestrations", s3rpc.KindOrchestrationCreate, nil); handled {
			return
		}
		OrchestrationCreate(store).ServeHTTP(w, r)
	}
}

func FederationOrchestrationCreate(store *db.Store) http.HandlerFunc {
	return requireFederationToken(store, OrchestrationCreate(store))
}

// FederatedOrchestrationUpdate / Delete proxy by ?cp= for the case
// where the orchestration lives on a child and an operator edits it
// from the Global UI.
func FederatedOrchestrationUpdate(store *db.Store, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.Replace(r.URL.Path, "/api/orchestrations/", "/api/v1/federation/orchestrations/", 1)
		if handled, _ := proxyWriteByQuery(w, r, agg, path, s3rpc.KindOrchestrationUpdate, idPathParams(r)); handled {
			return
		}
		OrchestrationUpdate(store).ServeHTTP(w, r)
	}
}

func FederationOrchestrationUpdate(store *db.Store) http.HandlerFunc {
	return requireFederationToken(store, OrchestrationUpdate(store))
}

func FederatedOrchestrationDelete(store *db.Store, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.Replace(r.URL.Path, "/api/orchestrations/", "/api/v1/federation/orchestrations/", 1)
		if handled, _ := proxyWriteByQuery(w, r, agg, path, s3rpc.KindOrchestrationDelete, idPathParams(r)); handled {
			return
		}
		OrchestrationDelete(store).ServeHTTP(w, r)
	}
}

func FederationOrchestrationDelete(store *db.Store) http.HandlerFunc {
	return requireFederationToken(store, OrchestrationDelete(store))
}

// FederatedOrchestrationRunCreate proxies the run-trigger to the CP
// that owns the orchestration. ?cp=<id> tells the parent to forward;
// without it, the run starts locally.
func FederatedOrchestrationRunCreate(store *db.Store, coord *OrchestrationCoordinator, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.Replace(r.URL.Path, "/api/orchestrations/", "/api/v1/federation/orchestrations/", 1)
		if handled, _ := proxyWriteByQuery(w, r, agg, path, s3rpc.KindOrchestrationRunCreate, idPathParams(r)); handled {
			return
		}
		OrchestrationRunCreate(store, coord).ServeHTTP(w, r)
	}
}

func FederationOrchestrationRunCreate(store *db.Store, coord *OrchestrationCoordinator) http.HandlerFunc {
	return requireFederationToken(store, OrchestrationRunCreate(store, coord))
}

// FederatedOrchestrationRunsList federates the runs history. Each
// peer's runs are tagged with cp_source and merged with local rows.
//
// Two response shapes follow the local handler:
//   - default      → JSON array of runs
//   - ?counts=1    → { rows: [...], counts_by_status: { status: int } }
//                   counts are summed across local + every reachable
//                   peer so the runs-tab status pills are fleet-wide.
//
// The query string is forwarded verbatim to each peer so all filter
// params (status, orchestration_id, trigger_kind, since, q, limit,
// offset) propagate.
func FederatedOrchestrationRunsList(store *db.Store, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wantCounts := r.URL.Query().Get("counts") == "1"

		localRR := httpRecorder()
		OrchestrationRunsList(store).ServeHTTP(localRR, r)
		if localRR.code != http.StatusOK {
			w.WriteHeader(localRR.code)
			_, _ = w.Write(localRR.body)
			return
		}

		merged, mergedCounts := unmarshalRunsListBody(localRR.body, wantCounts)

		// Forward the same query string to every peer.
		peerPath := "/api/v1/federation/orchestration-runs"
		if rq := r.URL.RawQuery; rq != "" {
			peerPath += "?" + rq
		}

		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		var mu sync.Mutex
		results, _ := agg.FanOut(ctx, func(ctx context.Context, peer federation.Peer) error {
			rawBytes, err := agg.FetchRaw(ctx, peer, peerPath)
			if err != nil {
				return err
			}
			peerRows, peerCounts := unmarshalRunsListBody(rawBytes, wantCounts)
			tag := map[string]any{
				"instance_id":  peer.Snapshot.InstanceID,
				"display_name": peer.Snapshot.DisplayName,
				"region":       peer.Snapshot.Region,
			}
			for i := range peerRows {
				peerRows[i]["cp_source"] = tag
			}
			mu.Lock()
			merged = append(merged, peerRows...)
			for k, v := range peerCounts {
				mergedCounts[k] += v
			}
			mu.Unlock()
			return nil
		})
		if pErr := federation.AnyError(results); pErr != nil {
			w.Header().Set("X-Okesu-Federation-Warning", pErr.Error())
		}
		// Sort newest-first by started_at.
		sort.SliceStable(merged, func(i, j int) bool {
			si, _ := merged[i]["started_at"].(string)
			sj, _ := merged[j]["started_at"].(string)
			return si > sj
		})
		w.Header().Set("Content-Type", "application/json")
		if wantCounts {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"rows":             merged,
				"counts_by_status": mergedCounts,
			})
			return
		}
		_ = json.NewEncoder(w).Encode(merged)
	}
}

// unmarshalRunsListBody handles both response shapes — bare JSON
// array (default) or wrapped object when ?counts=1 was requested.
// Always returns a non-nil rows slice + counts map so callers can
// merge without nil checks.
func unmarshalRunsListBody(body []byte, wantCounts bool) ([]map[string]any, map[string]int) {
	rows := []map[string]any{}
	counts := map[string]int{}
	if wantCounts {
		var wrapped struct {
			Rows           []map[string]any `json:"rows"`
			CountsByStatus map[string]int   `json:"counts_by_status"`
		}
		if err := json.Unmarshal(body, &wrapped); err == nil {
			rows = wrapped.Rows
			counts = wrapped.CountsByStatus
		}
	} else {
		_ = json.Unmarshal(body, &rows)
	}
	if rows == nil {
		rows = []map[string]any{}
	}
	if counts == nil {
		counts = map[string]int{}
	}
	return rows, counts
}

func FederationOrchestrationRunsList(store *db.Store) http.HandlerFunc {
	return requireFederationToken(store, OrchestrationRunsList(store))
}

// FederatedOrchestrationRunDetail proxies via ?cp= when the run lives
// on a child CP.
func FederatedOrchestrationRunDetail(store *db.Store, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.Replace(r.URL.Path, "/api/orchestration-runs/", "/api/v1/federation/orchestration-runs/", 1)
		if handled, _ := proxyToCPByQuery(w, r, agg, path); handled {
			return
		}
		OrchestrationRunDetail(store).ServeHTTP(w, r)
	}
}

func FederationOrchestrationRunDetail(store *db.Store) http.HandlerFunc {
	return requireFederationToken(store, OrchestrationRunDetail(store))
}

// FederatedOrchestrationStepApprove proxies the approve POST to the
// CP that owns the run.
func FederatedOrchestrationStepApprove(store *db.Store, coord *OrchestrationCoordinator, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.Replace(r.URL.Path, "/api/orchestration-runs/", "/api/v1/federation/orchestration-runs/", 1)
		if handled, _ := proxyWriteByQuery(w, r, agg, path, s3rpc.KindOrchestrationStepApprove, idStepPathParams(r)); handled {
			return
		}
		OrchestrationStepApprove(store, coord).ServeHTTP(w, r)
	}
}

func FederationOrchestrationStepApprove(store *db.Store, coord *OrchestrationCoordinator) http.HandlerFunc {
	return requireFederationToken(store, OrchestrationStepApprove(store, coord))
}

func FederatedOrchestrationRunCancel(store *db.Store, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.Replace(r.URL.Path, "/api/orchestration-runs/", "/api/v1/federation/orchestration-runs/", 1)
		if handled, _ := proxyWriteByQuery(w, r, agg, path, s3rpc.KindOrchestrationRunCancel, idPathParams(r)); handled {
			return
		}
		OrchestrationRunCancel(store).ServeHTTP(w, r)
	}
}

func FederationOrchestrationRunCancel(store *db.Store) http.HandlerFunc {
	return requireFederationToken(store, OrchestrationRunCancel(store))
}

// FederatedOrchestrationRunsBulkCancel routes the POST to a child CP
// when ?cp= is set; otherwise operates on the local DB. The UI
// partitions a multi-CP selection into one POST per CP, so each
// invocation sees only ids belonging to one CP — no server-side
// split-and-fan-out needed.
func FederatedOrchestrationRunsBulkCancel(store *db.Store, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if handled, _ := proxyWriteByQuery(w, r, agg, "/api/v1/federation/orchestration-runs/bulk-cancel", s3rpc.KindOrchestrationRunsBulkCnl, nil); handled {
			return
		}
		OrchestrationRunsBulkCancel(store).ServeHTTP(w, r)
	}
}

func FederationOrchestrationRunsBulkCancel(store *db.Store) http.HandlerFunc {
	return requireFederationToken(store, OrchestrationRunsBulkCancel(store))
}

// FederatedOrchestrationRunsBulkRetry — same routing rule as cancel.
// Coordinator is required so the local handler can spawn fresh runs
// when ?cp= is unset; child CPs use their own coord on the federation
// endpoint.
func FederatedOrchestrationRunsBulkRetry(store *db.Store, coord *OrchestrationCoordinator, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if handled, _ := proxyWriteByQuery(w, r, agg, "/api/v1/federation/orchestration-runs/bulk-retry", s3rpc.KindOrchestrationRunsBulkRetry, nil); handled {
			return
		}
		OrchestrationRunsBulkRetry(store, coord).ServeHTTP(w, r)
	}
}

func FederationOrchestrationRunsBulkRetry(store *db.Store, coord *OrchestrationCoordinator) http.HandlerFunc {
	return requireFederationToken(store, OrchestrationRunsBulkRetry(store, coord))
}

// proxyWriteByQuery is the write-side counterpart of proxyToCPByQuery:
// when ?cp=<instance_id> is present and matches a healthy peer, the
// (POST/PATCH/PUT/DELETE) request — with body intact — is forwarded to
// the child's federation endpoint and the response streamed back. Used
// by the Kanban-board status-drag flow so an operator can move a
// federated finding between columns from the parent UI.
//
// `s3Kind` and `s3PathParams` mirror proxyIfTargetCP: when the resolved
// peer's transport is `s3_dead_drop`, the request is dispatched via
// the s3rpc write pipe instead of an HTTPS POST. Empty `s3Kind` =>
// 501 for s3 peers (the directive isn't supported on this transport
// yet).
func proxyWriteByQuery(w http.ResponseWriter, r *http.Request, agg *federation.Aggregator, federationPath, s3Kind string, s3PathParams map[string]string) (handled bool, err error) {
	cpID := r.URL.Query().Get("cp")
	if cpID == "" {
		return false, nil
	}
	peers, _ := agg.HealthyPeers()
	var target *federation.Peer
	for i := range peers {
		if peers[i].Snapshot.InstanceID == cpID {
			target = &peers[i]
			break
		}
	}
	if target == nil {
		http.Error(w, "target CP not found or unhealthy: "+cpID, http.StatusNotFound)
		return true, nil
	}
	body, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(body))

	if target.Row.Transport == "s3_dead_drop" {
		return forwardOverS3(w, r, agg, *target, s3Kind, body, s3PathParams), nil
	}

	url := strings.TrimRight(target.Row.URL, "/") + federationPath
	q := r.URL.Query()
	q.Del("cp")
	if enc := q.Encode(); enc != "" {
		url += "?" + enc
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, url, bytes.NewReader(body))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return true, err
	}
	req.Header.Set("X-Okesu-Federation-Token", target.Row.Token)
	if ct := r.Header.Get("Content-Type"); ct != "" {
		req.Header.Set("Content-Type", ct)
	} else {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{
		Timeout:   proxyTimeout,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
	}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "proxy to "+target.Snapshot.DisplayName+": "+err.Error(), http.StatusBadGateway)
		return true, err
	}
	defer resp.Body.Close()
	for k, vs := range resp.Header {
		if strings.EqualFold(k, "Content-Length") || strings.EqualFold(k, "Transfer-Encoding") {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.Header().Set("X-Okesu-Forwarded-To", target.Snapshot.InstanceID)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
	return true, nil
}

// FederatedFindingSetStatus wraps FindingSetStatus with the ?cp= proxy.
// The Kanban-board drag handler posts ?cp=<id> when the dragged card
// belongs to a federated child; without the proxy the parent's local
// id space wouldn't have the finding and the request would 404.
func FederatedFindingSetStatus(store *db.Store, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.Replace(r.URL.Path, "/api/findings/", "/api/v1/federation/findings/", 1)
		if handled, _ := proxyWriteByQuery(w, r, agg, path, s3rpc.KindFindingSetStatus, idPathParams(r)); handled {
			return
		}
		FindingSetStatus(store).ServeHTTP(w, r)
	}
}

// FederationFindingSetStatus is the child-side endpoint
// (POST /api/v1/federation/findings/{id}/status). Token-authed sibling.
func FederationFindingSetStatus(store *db.Store) http.HandlerFunc {
	return requireFederationToken(store, FindingSetStatus(store))
}
