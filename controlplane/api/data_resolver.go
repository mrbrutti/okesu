// Data resolver for orchestration step `data:` blocks.
//
// The orchestrator engine handles the *language* (parsing the `data:`
// block, binding the result into the prompt template, persisting the
// snapshot for replay). This file is the *backend*: it owns the
// registry of query handlers, each of which takes a free-form params
// map and returns a structured result for the agent prompt.
//
// New queries are added by registering a handler against a
// "namespace.method" key. The orchestrator validates the format at
// parse time; this layer rejects unknown queries at run time.
//
// Why params is `map[string]any` and not a typed struct per query:
// the spec format is YAML (operators write it by hand), so accepting
// "shape-it-yourself" inputs is the lowest-friction surface. Each
// handler validates its own params and returns a clear error with
// the parameter name on mismatch.

package api

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/section9labs/okesu/controlplane/db"
	"github.com/section9labs/okesu/controlplane/orchestrator"
)

// QueryHandler resolves one query against the CP store. ctx is the
// engine's per-step timeout; handlers should respect it on any call
// that might block.
type QueryHandler func(ctx context.Context, store *db.Store, params map[string]any) (any, error)

// DataResolver implements orchestrator.DataResolver against a static
// registry of handlers. Built once at coordinator boot.
type DataResolver struct {
	store    *db.Store
	handlers map[string]QueryHandler
}

// NewDataResolver constructs the resolver pre-loaded with the
// built-in query set. Operators looking for the canonical list of
// supported queries should grep this file for "registerHandler".
func NewDataResolver(store *db.Store) *DataResolver {
	r := &DataResolver{store: store, handlers: map[string]QueryHandler{}}
	r.registerHandler("findings.list", findingsListQuery)
	r.registerHandler("findings.summary", findingsSummaryQuery)
	r.registerHandler("findings.history", findingsHistoryQuery)
	r.registerHandler("orchestration-runs.list", orchestrationRunsListQuery)
	r.registerHandler("nodes.list", nodesListQuery)
	r.registerHandler("agents.list", agentsListQuery)
	return r
}

// SupportedQueries returns the registered query names, sorted, for
// the docs UI to surface without duplicating the list.
func (r *DataResolver) SupportedQueries() []string {
	names := make([]string, 0, len(r.handlers))
	for k := range r.handlers {
		names = append(names, k)
	}
	// Sort by namespace first, then method, so related queries cluster.
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j-1] > names[j]; j-- {
			names[j-1], names[j] = names[j], names[j-1]
		}
	}
	return names
}

func (r *DataResolver) registerHandler(name string, h QueryHandler) {
	r.handlers[name] = h
}

// Resolve dispatches to the registered handler, or returns a clear
// error listing the supported queries when the operator typo'd.
// Implements orchestrator.DataResolver.
func (r *DataResolver) Resolve(ctx context.Context, query string, params map[string]any) (any, error) {
	h, ok := r.handlers[query]
	if !ok {
		return nil, fmt.Errorf("unknown data query %q (supported: %v)", query, r.SupportedQueries())
	}
	return h(ctx, r.store, params)
}

// Compile-time assertion that DataResolver satisfies the engine's
// interface. Catches drift if the orchestrator package adds methods.
var _ orchestrator.DataResolver = (*DataResolver)(nil)

// ── built-in query handlers ───────────────────────────────────────────

// findingsListQuery wraps store.ListFindings. Mirrors the API params
// of /api/findings so operators only have to learn one filter shape.
//
// Supported params (all optional):
//
//	state:    "open" | "acked" | "queue" | "all"  (default "queue")
//	severity: ["INFO","LOW", ...]                  (case-insensitive)
//	agent:    "edr"
//	host:     "prod-web-01"
//	category: "process"
//	tag:      "auto-triaged"
//	since_ms: <unix-ms>      (ts >=)
//	until_ms: <unix-ms>      (ts <)
//	limit:    1..1000        (default 100)
//	offset:   N
func findingsListQuery(ctx context.Context, store *db.Store, params map[string]any) (any, error) {
	_ = ctx
	f := db.FindingFilter{
		Agent:    paramString(params, "agent"),
		Host:     paramString(params, "host"),
		Category: paramString(params, "category"),
		Tag:      paramString(params, "tag"),
	}
	if sevs, err := paramStringSlice(params, "severity"); err != nil {
		return nil, err
	} else if len(sevs) > 0 {
		// Normalise to upper-case so callers can write either case.
		for i := range sevs {
			sevs[i] = strings.ToUpper(sevs[i])
		}
		f.Severities = sevs
	}
	if v, ok := params["since_ms"]; ok {
		if n, err := paramInt64(v); err != nil {
			return nil, fmt.Errorf("since_ms: %w", err)
		} else {
			f.SinceMs = n
		}
	}
	if v, ok := params["until_ms"]; ok {
		if n, err := paramInt64(v); err != nil {
			return nil, fmt.Errorf("until_ms: %w", err)
		} else {
			f.UntilMs = n
		}
	}
	if v, ok := params["limit"]; ok {
		n, err := paramInt(v)
		if err != nil {
			return nil, fmt.Errorf("limit: %w", err)
		}
		f.Limit = n
	} else {
		f.Limit = 100
	}
	if v, ok := params["offset"]; ok {
		n, err := paramInt(v)
		if err != nil {
			return nil, fmt.Errorf("offset: %w", err)
		}
		f.Offset = n
	}
	switch strings.ToLower(paramString(params, "state")) {
	case "", "queue":
		f.OnlyQueue = true
	case "open":
		f.OnlyOpen = true
	case "acked":
		f.OnlyAcked = true
	case "all":
		// no filter — fall through
	default:
		return nil, fmt.Errorf("state must be queue|open|acked|all, got %q", params["state"])
	}
	return store.ListFindings(f)
}

// findingsSummaryQuery is the per-CP rollup the dashboard already
// shows: total open, by severity, by agent, by category, last_24h
// trend. Useful as cluster context for batch triage steps.
func findingsSummaryQuery(ctx context.Context, store *db.Store, params map[string]any) (any, error) {
	_ = ctx
	_ = params
	return store.FindingsSummary()
}

// findingsHistoryQuery returns the edit history for one finding —
// useful when an orchestration is drilling into "why did this
// already-tagged finding land in my queue?"
//
// Required params: finding_id (int)
func findingsHistoryQuery(ctx context.Context, store *db.Store, params map[string]any) (any, error) {
	_ = ctx
	v, ok := params["finding_id"]
	if !ok {
		return nil, fmt.Errorf("finding_id is required")
	}
	id, err := paramInt64(v)
	if err != nil {
		return nil, fmt.Errorf("finding_id: %w", err)
	}
	return store.ListFindingEdits(id)
}

// orchestrationRunsListQuery wraps store.ListOrchestrationRunsFiltered.
//
// Supported params:
//
//	status:        ["completed","failed", ...]  (CSV-equivalent)
//	trigger_kind:  ["finding","cron","manual"]
//	since:         "30m" | "1h" | "24h" | "7d" | "30d"   (relative)
//	since_ms:      <unix-ms>                              (absolute, wins over since)
//	limit:         1..1000 (default 100)
//	offset:        N
func orchestrationRunsListQuery(ctx context.Context, store *db.Store, params map[string]any) (any, error) {
	_ = ctx
	f := db.OrchestrationRunFilter{}
	if v, err := paramStringSlice(params, "status"); err != nil {
		return nil, err
	} else {
		f.Status = v
	}
	if v, err := paramStringSlice(params, "trigger_kind"); err != nil {
		return nil, err
	} else {
		f.TriggerKinds = v
	}
	if v, ok := params["since_ms"]; ok {
		if n, err := paramInt64(v); err != nil {
			return nil, fmt.Errorf("since_ms: %w", err)
		} else {
			f.SinceMs = n
		}
	} else if since := paramString(params, "since"); since != "" {
		f.SinceMs = parseSinceMs(since)
	}
	if v, ok := params["limit"]; ok {
		n, err := paramInt(v)
		if err != nil {
			return nil, fmt.Errorf("limit: %w", err)
		}
		f.Limit = n
	} else {
		f.Limit = 100
	}
	if v, ok := params["offset"]; ok {
		n, err := paramInt(v)
		if err != nil {
			return nil, fmt.Errorf("offset: %w", err)
		}
		f.Offset = n
	}
	return store.ListOrchestrationRunsFiltered(f)
}

// nodesListQuery returns the nodes registered on this CP.
//
// Supported params:
//
//	limit:  default 200
//	offset: N
func nodesListQuery(ctx context.Context, store *db.Store, params map[string]any) (any, error) {
	_ = ctx
	limit := 200
	offset := 0
	if v, ok := params["limit"]; ok {
		n, err := paramInt(v)
		if err != nil {
			return nil, fmt.Errorf("limit: %w", err)
		}
		limit = n
	}
	if v, ok := params["offset"]; ok {
		n, err := paramInt(v)
		if err != nil {
			return nil, fmt.Errorf("offset: %w", err)
		}
		offset = n
	}
	return store.ListNodes(limit, offset)
}

// agentsListQuery returns the registered daemon agents (one row per
// (agent, host) pair) so a fleet-health step can see what's
// reporting and how stale.
//
// Supported params:
//
//	limit:  default 500
//	offset: N
func agentsListQuery(ctx context.Context, store *db.Store, params map[string]any) (any, error) {
	_ = ctx
	limit := 500
	offset := 0
	if v, ok := params["limit"]; ok {
		n, err := paramInt(v)
		if err != nil {
			return nil, fmt.Errorf("limit: %w", err)
		}
		limit = n
	}
	if v, ok := params["offset"]; ok {
		n, err := paramInt(v)
		if err != nil {
			return nil, fmt.Errorf("offset: %w", err)
		}
		offset = n
	}
	return store.ListAgents(limit, offset)
}

// ── param coercion helpers ────────────────────────────────────────────

func paramString(params map[string]any, key string) string {
	v, ok := params[key]
	if !ok {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

func paramStringSlice(params map[string]any, key string) ([]string, error) {
	v, ok := params[key]
	if !ok {
		return nil, nil
	}
	switch x := v.(type) {
	case []string:
		return x, nil
	case []any:
		out := make([]string, len(x))
		for i, e := range x {
			s, ok := e.(string)
			if !ok {
				return nil, fmt.Errorf("%s[%d]: expected string, got %T", key, i, e)
			}
			out[i] = s
		}
		return out, nil
	case string:
		// Comma-separated convenience form — matches /api/findings ?severity=INFO,LOW
		out := strings.Split(x, ",")
		for i := range out {
			out[i] = strings.TrimSpace(out[i])
		}
		return out, nil
	}
	return nil, fmt.Errorf("%s: expected list of strings, got %T", key, v)
}

func paramInt(v any) (int, error) {
	switch x := v.(type) {
	case int:
		return x, nil
	case int64:
		return int(x), nil
	case float64:
		return int(x), nil
	case string:
		return strconv.Atoi(x)
	}
	return 0, fmt.Errorf("expected int, got %T", v)
}

func paramInt64(v any) (int64, error) {
	switch x := v.(type) {
	case int:
		return int64(x), nil
	case int64:
		return x, nil
	case float64:
		return int64(x), nil
	case string:
		return strconv.ParseInt(x, 10, 64)
	}
	return 0, fmt.Errorf("expected int64, got %T", v)
}
