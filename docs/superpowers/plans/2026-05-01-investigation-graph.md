# Investigation Graph View Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** New "Graph" tab on the investigation detail page rendering the case as a bipartite graph (findings on the left; hosts/daimons/IOCs on the right) with edges for each finding↔entity relationship.

**Architecture:** New `GET /api/investigations/{id}/graph?limit=20` endpoint returning `{nodes, edges}` JSON. Server query joins `investigation_findings` + `findings` + `ioc_observations` + `iocs`. Federation routes via the existing `?cp=<instance_id>` proxy convention. Client renders via react-flow (`@xyflow/react`, already a dep) with deterministic bipartite layout + degree-sort. Cap defaults to top-20 findings by severity; soft-cap with banner above.

**Tech Stack:** Go 1.22 (chi router, sqlite/postgres, no migrations), React 18 + TypeScript, @xyflow/react, Vitest.

---

## File structure

### New (server)

- `controlplane/db/investigation_graph.go` — `GetInvestigationGraphData(invID, limit)` store helper; returns the data the API serialises.
- `controlplane/db/investigation_graph_test.go` — store-level tests against in-memory sqlite.
- `controlplane/api/investigation_graph.go` — HTTP handler + federated/federation wrappers.
- `controlplane/api/investigation_graph_test.go` — handler-level tests.

### New (frontend)

- `web/src/components/investigations/CaseGraph.tsx` — react-flow canvas component.
- `web/src/components/investigations/CaseGraph.test.tsx`
- `web/src/components/investigations/graph/layout.ts` — pure layout math (deterministic node positions); easier to unit-test in isolation.
- `web/src/components/investigations/graph/layout.test.ts`

### Modified

- `controlplane/server.go` — mount the two new routes.
- `web/src/api.ts` — add `GraphResponse`, `GraphNode`, `GraphEdge` types + `api.investigations.graph(id, opts)` helper.
- `web/src/pages/InvestigationDetail.tsx` — extend `Tab` union, add `'graph'` to nav, lazy-load + render CaseGraph.
- `docs/architecture.md` — new section.

---

## Task Group A — Server-side store helper

### Task A1: GetInvestigationGraphData query + tests

**Files:**
- Create: `controlplane/db/investigation_graph.go`
- Create: `controlplane/db/investigation_graph_test.go`

- [ ] **Step A1.1: Write the failing test**

```go
// controlplane/db/investigation_graph_test.go
package db

import (
	"testing"
)

func TestInvestigationGraph_EmptyCase(t *testing.T) {
	s := openTempStore(t)
	invID, err := s.CreateInvestigation(&InvestigationInsert{Title: "empty"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := s.GetInvestigationGraphData(invID, 20)
	if err != nil {
		t.Fatalf("graph: %v", err)
	}
	if got.TotalFindings != 0 {
		t.Errorf("TotalFindings = %d, want 0", got.TotalFindings)
	}
	if len(got.Findings) != 0 {
		t.Errorf("Findings = %d, want 0", len(got.Findings))
	}
}

func TestInvestigationGraph_TopByCount(t *testing.T) {
	s := openTempStore(t)
	invID, _ := s.CreateInvestigation(&InvestigationInsert{Title: "case"})

	// Insert 25 findings: 5 CRITICAL, 20 LOW.
	for i := 0; i < 5; i++ {
		fid := mustInsertFinding(t, s, "CRITICAL", "host1", "edr-agent", int64(1000+i))
		mustLinkFinding(t, s, invID, fid)
	}
	for i := 0; i < 20; i++ {
		fid := mustInsertFinding(t, s, "LOW", "host2", "compliance", int64(500+i))
		mustLinkFinding(t, s, invID, fid)
	}

	got, err := s.GetInvestigationGraphData(invID, 20)
	if err != nil {
		t.Fatalf("graph: %v", err)
	}
	if got.TotalFindings != 25 {
		t.Errorf("TotalFindings = %d, want 25", got.TotalFindings)
	}
	if got.LimitApplied != 20 {
		t.Errorf("LimitApplied = %d, want 20", got.LimitApplied)
	}
	if len(got.Findings) != 20 {
		t.Errorf("Findings = %d, want 20", len(got.Findings))
	}
	// Severity order: top 5 must all be CRITICAL.
	for i := 0; i < 5; i++ {
		if got.Findings[i].Severity != "CRITICAL" {
			t.Errorf("Findings[%d].Severity = %q, want CRITICAL", i, got.Findings[i].Severity)
		}
	}
}

func TestInvestigationGraph_LimitClampedTo100(t *testing.T) {
	s := openTempStore(t)
	invID, _ := s.CreateInvestigation(&InvestigationInsert{Title: "case"})
	got, err := s.GetInvestigationGraphData(invID, 9999)
	if err != nil {
		t.Fatalf("graph: %v", err)
	}
	if got.LimitApplied != 100 {
		t.Errorf("LimitApplied = %d, want 100 (clamped)", got.LimitApplied)
	}
}

func TestInvestigationGraph_LimitFloorIsOne(t *testing.T) {
	s := openTempStore(t)
	invID, _ := s.CreateInvestigation(&InvestigationInsert{Title: "case"})
	got, err := s.GetInvestigationGraphData(invID, 0)
	if err != nil {
		t.Fatalf("graph: %v", err)
	}
	if got.LimitApplied != 20 { // 0 → default
		t.Errorf("LimitApplied = %d, want 20 (default)", got.LimitApplied)
	}
}
```

The helpers `openTempStore`, `mustInsertFinding`, `mustLinkFinding` may not exist with those exact names — search the existing tests in `controlplane/db/` first. `openTempStore` is the standard pattern across the package's tests. If `mustInsertFinding` / `mustLinkFinding` don't exist, write thin local helpers that call `s.InsertFinding(...)` and `s.LinkFindingToInvestigation(...)` (or whatever the existing finding/link helpers are named — `grep -n "InsertFinding\|LinkFinding" controlplane/db/*.go`).

- [ ] **Step A1.2: Run test, expect FAIL**

```bash
cd /Users/matt/Code/Oracle/Okesu/.claude/worktrees/feat-investigation-graph
go test ./controlplane/db/ -count=1 -run TestInvestigationGraph
```

Expected: FAIL — `GetInvestigationGraphData` undefined.

- [ ] **Step A1.3: Implement investigation_graph.go**

```go
// controlplane/db/investigation_graph.go
//
// GetInvestigationGraphData returns the data the API serialises into
// the bipartite graph response: top-N findings by severity desc + Ts
// desc, plus the linked IOCs (with finding-id linkage from
// ioc_observations). Hosts and daimons are derived in Go from the
// findings — no extra query.
package db

import (
	"database/sql"
)

// GraphFinding is the minimal finding shape the graph endpoint needs.
// Hosts + daimons are derived from these in the API layer.
type GraphFinding struct {
	ID       int64
	Ts       int64
	Agent    string
	Host     string
	Severity string
	Title    string
}

// GraphIOC carries one IOC + the set of finding IDs linked to it
// within the kept-findings scope.
type GraphIOC struct {
	ID         int64
	Kind       string
	Value      string
	ObsCount   int64
	HostCount  int64
	FindingIDs []int64
}

// GraphData is the store-layer bundle GetInvestigationGraphData
// returns. The API serialises this into the {nodes, edges} wire
// shape.
type GraphData struct {
	TotalFindings int
	LimitApplied  int
	Findings      []GraphFinding
	IOCs          []GraphIOC
}

// graphLimitClamp normalises the operator-supplied limit to [1, 100],
// defaulting to 20 when zero.
func graphLimitClamp(limit int) int {
	if limit <= 0 {
		return 20
	}
	if limit > 100 {
		return 100
	}
	return limit
}

// graphSeverityRank maps severity strings to sort weights. Mirrors
// the rank used by the investigation report renderer.
const graphSeverityRankSQL = `
CASE COALESCE(f.severity, '')
    WHEN 'CRITICAL' THEN 5
    WHEN 'HIGH'     THEN 4
    WHEN 'MEDIUM'   THEN 3
    WHEN 'LOW'      THEN 2
    WHEN 'INFO'     THEN 1
    ELSE 0
END`

// GetInvestigationGraphData returns the kept findings + linked IOCs
// scoped to those findings. Total finding count is reported alongside
// LimitApplied so the UI can render the "N of M shown" banner.
func (s *Store) GetInvestigationGraphData(invID int64, limit int) (*GraphData, error) {
	limit = graphLimitClamp(limit)
	out := &GraphData{LimitApplied: limit}

	// Total findings on the case (independent of limit).
	if err := s.QueryRow(`
		SELECT COUNT(*) FROM investigation_findings
		 WHERE investigation_id = ?`, invID).Scan(&out.TotalFindings); err != nil {
		return nil, err
	}
	if out.TotalFindings == 0 {
		return out, nil
	}

	// Kept findings: top-N by severity rank desc, then Ts desc.
	rows, err := s.Query(`
		SELECT f.id, f.ts, COALESCE(f.agent, ''), COALESCE(f.host, ''),
		       COALESCE(f.severity, ''), COALESCE(f.title, '')
		  FROM investigation_findings inv
		  JOIN findings f ON f.id = inv.finding_id
		 WHERE inv.investigation_id = ?
		 ORDER BY `+graphSeverityRankSQL+` DESC, f.ts DESC, f.id DESC
		 LIMIT ?`, invID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keptIDs := make([]int64, 0, limit)
	for rows.Next() {
		var f GraphFinding
		if err := rows.Scan(&f.ID, &f.Ts, &f.Agent, &f.Host, &f.Severity, &f.Title); err != nil {
			return nil, err
		}
		out.Findings = append(out.Findings, f)
		keptIDs = append(keptIDs, f.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(keptIDs) == 0 {
		return out, nil
	}

	// IOCs scoped to the kept findings, with the finding-id linkage.
	// One row per (ioc_id, finding_id); collapsed in Go below.
	q, args := buildInClause(`
		SELECT obs.finding_id, i.id, i.kind, COALESCE(i.normalized_value, '')
		  FROM ioc_observations obs
		  JOIN iocs i ON i.id = obs.ioc_id
		 WHERE obs.finding_id IN `, keptIDs)
	linkRows, err := s.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer linkRows.Close()
	type iocAcc struct {
		ID, Kind, Value string
		Findings        map[int64]bool
	}
	byIOC := map[int64]*iocAcc{}
	for linkRows.Next() {
		var fid, iid int64
		var kind, value string
		if err := linkRows.Scan(&fid, &iid, &kind, &value); err != nil {
			return nil, err
		}
		acc, ok := byIOC[iid]
		if !ok {
			acc = &iocAcc{ID: itoa(iid), Kind: kind, Value: value, Findings: map[int64]bool{}}
			byIOC[iid] = acc
		}
		acc.Findings[fid] = true
	}
	if err := linkRows.Err(); err != nil {
		return nil, err
	}

	// Aggregated obs/host counts per IOC (scoped to kept findings).
	q2, args2 := buildInClause(`
		SELECT obs.ioc_id,
		       COUNT(obs.id) AS obs_count,
		       COUNT(DISTINCT COALESCE(obs.host, '')) AS host_count
		  FROM ioc_observations obs
		 WHERE obs.finding_id IN `, keptIDs)
	q2 += ` GROUP BY obs.ioc_id`
	aggRows, err := s.Query(q2, args2...)
	if err != nil {
		return nil, err
	}
	defer aggRows.Close()
	type aggCounts struct {
		Obs, Hosts int64
	}
	aggByIOC := map[int64]aggCounts{}
	for aggRows.Next() {
		var iid int64
		var obs, hosts int64
		if err := aggRows.Scan(&iid, &obs, &hosts); err != nil {
			return nil, err
		}
		aggByIOC[iid] = aggCounts{Obs: obs, Hosts: hosts}
	}
	if err := aggRows.Err(); err != nil {
		return nil, err
	}

	out.IOCs = make([]GraphIOC, 0, len(byIOC))
	for iid, acc := range byIOC {
		fids := make([]int64, 0, len(acc.Findings))
		for fid := range acc.Findings {
			fids = append(fids, fid)
		}
		counts := aggByIOC[iid]
		out.IOCs = append(out.IOCs, GraphIOC{
			ID:         iid,
			Kind:       acc.Kind,
			Value:      acc.Value,
			ObsCount:   counts.Obs,
			HostCount:  counts.Hosts,
			FindingIDs: fids,
		})
	}
	return out, nil
}

// buildInClause expands a slice of int64 IDs into a parameterised
// `IN (?, ?, ...)` clause with the corresponding args slice. Used for
// the IOC linkage queries above. Empty input panics — callers must
// guard.
func buildInClause(prefix string, ids []int64) (string, []any) {
	if len(ids) == 0 {
		panic("buildInClause: empty ids")
	}
	q := prefix + "("
	args := make([]any, 0, len(ids))
	for i, id := range ids {
		if i > 0 {
			q += ", "
		}
		q += "?"
		args = append(args, id)
	}
	q += ")"
	return q, args
}
```

You'll need a tiny `itoa` helper — but we use the int64 ID directly in the wire shape (`i:<kind>:<value>`), so `acc.ID` should actually be an `int64` not a string. Let me fix that by removing the string conversion and the itoa import. Replace `acc = &iocAcc{ID: itoa(iid), ...}` with `acc = &iocAcc{ID: iid, ...}` and change `iocAcc.ID` to `int64`. Then `out.IOCs = append(out.IOCs, GraphIOC{ID: iid, ...})` (no `iid := acc.ID` indirection needed).

Actually re-reading the code, the cleaner correction is: drop the `iocAcc.ID` field entirely (the map key already is the iid); ditch the `itoa` line. Final shape:

```go
type iocAcc struct {
    Kind, Value string
    Findings    map[int64]bool
}
byIOC := map[int64]*iocAcc{}
// ...
acc = &iocAcc{Kind: kind, Value: value, Findings: map[int64]bool{}}
// ...
out.IOCs = append(out.IOCs, GraphIOC{
    ID:         iid,    // from map key
    Kind:       acc.Kind,
    Value:      acc.Value,
    // ...
})
```

Use this corrected shape when implementing.

- [ ] **Step A1.4: Run test, expect PASS**

```bash
go test ./controlplane/db/ -count=1 -run TestInvestigationGraph -v
```

Expected: 4 PASS.

- [ ] **Step A1.5: Commit**

```bash
git add controlplane/db/investigation_graph.go \
        controlplane/db/investigation_graph_test.go
git commit -m "db(investigations): GetInvestigationGraphData store helper"
```

---

## Task Group B — Server-side handler + federation + route mounts

### Task B1: HTTP handler + federation wrappers + tests

**Files:**
- Create: `controlplane/api/investigation_graph.go`
- Create: `controlplane/api/investigation_graph_test.go`
- Modify: `controlplane/server.go` — two route mounts.

- [ ] **Step B1.1: Implement the handler**

Create `controlplane/api/investigation_graph.go`:

```go
// HTTP layer for the investigation graph view. Handler builds the
// {nodes, edges} wire shape from db.GraphData and serialises as JSON.
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/section9labs/okesu/controlplane/db"
	"github.com/section9labs/okesu/controlplane/federation"
)

// graphNode is one entry in the wire shape's nodes array.
type graphNode struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"` // finding | host | daimon | ioc
	Label         string `json:"label"`
	Severity      string `json:"severity,omitempty"`       // findings only
	Host          string `json:"host,omitempty"`           // findings only
	Agent         string `json:"agent,omitempty"`          // findings only
	FindingCount  int64  `json:"finding_count,omitempty"`  // daimons only
	IOCKind       string `json:"ioc_kind,omitempty"`       // iocs only
	IOCValue      string `json:"ioc_value,omitempty"`      // iocs only
	ObsCount      int64  `json:"obs_count,omitempty"`      // iocs only
	HostCount     int64  `json:"host_count,omitempty"`     // iocs only
}

// graphEdge is one entry in the wire shape's edges array.
type graphEdge struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Target string `json:"target"`
}

// graphResponse mirrors the spec's wire shape.
type graphResponse struct {
	TotalFindings int         `json:"total_findings"`
	LimitApplied  int         `json:"limit_applied"`
	Nodes         []graphNode `json:"nodes"`
	Edges         []graphEdge `json:"edges"`
}

// GetInvestigationGraphHandler serves the bipartite graph data.
func GetInvestigationGraphHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := investigationIDFromChi(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Existence check so we return 404 not an empty graph for
		// unknown IDs.
		if _, err := store.GetInvestigation(id); err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		limit := 20
		if s := r.URL.Query().Get("limit"); s != "" {
			if n, err := strconv.Atoi(s); err == nil {
				limit = n
			}
		}

		data, err := store.GetInvestigationGraphData(id, limit)
		if err != nil {
			http.Error(w, "graph: "+err.Error(), http.StatusInternalServerError)
			return
		}

		resp := buildGraphResponse(data)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// buildGraphResponse converts db.GraphData into the wire shape:
// derives host + daimon nodes from the findings; emits edges per
// finding.
func buildGraphResponse(d *db.GraphData) graphResponse {
	resp := graphResponse{
		TotalFindings: d.TotalFindings,
		LimitApplied:  d.LimitApplied,
		Nodes:         []graphNode{},
		Edges:         []graphEdge{},
	}

	// Track distinct hosts and daimons (with finding counts) keyed by
	// their string identity so we emit each as a single node.
	hostFindings := map[string]bool{} // existence — used for node creation
	daimonFindings := map[string]int64{}

	// Findings → finding nodes; collect host + daimon edges.
	for _, f := range d.Findings {
		fNode := graphNode{
			ID:       fmt.Sprintf("f:%d", f.ID),
			Kind:     "finding",
			Label:    f.Title,
			Severity: f.Severity,
			Host:     f.Host,
			Agent:    f.Agent,
		}
		resp.Nodes = append(resp.Nodes, fNode)

		if f.Host != "" {
			hostID := "h:" + f.Host
			hostFindings[f.Host] = true
			resp.Edges = append(resp.Edges, graphEdge{
				ID:     fNode.ID + "|" + hostID,
				Source: fNode.ID,
				Target: hostID,
			})
		}
		if f.Agent != "" {
			daimonID := "d:" + f.Agent
			daimonFindings[f.Agent]++
			resp.Edges = append(resp.Edges, graphEdge{
				ID:     fNode.ID + "|" + daimonID,
				Source: fNode.ID,
				Target: daimonID,
			})
		}
	}

	// Host nodes.
	for host := range hostFindings {
		resp.Nodes = append(resp.Nodes, graphNode{
			ID:    "h:" + host,
			Kind:  "host",
			Label: host,
		})
	}

	// Daimon nodes.
	for agent, count := range daimonFindings {
		resp.Nodes = append(resp.Nodes, graphNode{
			ID:           "d:" + agent,
			Kind:         "daimon",
			Label:        agent,
			FindingCount: count,
		})
	}

	// IOC nodes + edges.
	for _, i := range d.IOCs {
		iocID := fmt.Sprintf("i:%s:%s", i.Kind, i.Value)
		// Truncated label: kind:...last4 (matches SmartPayload IOC chip).
		label := i.Kind + ":" + lastN(i.Value, 4)
		resp.Nodes = append(resp.Nodes, graphNode{
			ID:        iocID,
			Kind:      "ioc",
			Label:     label,
			IOCKind:   i.Kind,
			IOCValue:  i.Value,
			ObsCount:  i.ObsCount,
			HostCount: i.HostCount,
		})
		for _, fid := range i.FindingIDs {
			fNodeID := fmt.Sprintf("f:%d", fid)
			resp.Edges = append(resp.Edges, graphEdge{
				ID:     fNodeID + "|" + iocID,
				Source: fNodeID,
				Target: iocID,
			})
		}
	}

	return resp
}

// lastN returns the last N characters of s, or all of s when shorter.
// Pure helper; unit-tested via the handler tests.
func lastN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// FederatedInvestigationGraph — parent-side wrapper. Proxies to the
// owning child via ?cp=<instance_id>; falls through to the local
// handler otherwise.
func FederatedInvestigationGraph(store *db.Store, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.Replace(r.URL.Path, "/api/investigations/", "/api/v1/federation/investigations/", 1)
		if handled, _ := proxyToCPByQuery(w, r, agg, path); handled {
			return
		}
		GetInvestigationGraphHandler(store).ServeHTTP(w, r)
	}
}

// FederationInvestigationGraph — child-side, token-authed sibling.
func FederationInvestigationGraph(store *db.Store) http.HandlerFunc {
	return requireFederationToken(store, GetInvestigationGraphHandler(store))
}
```

- [ ] **Step B1.2: Mount routes in server.go**

Find the cookie-auth admin group around line 897 in `controlplane/server.go`. Locate this line:

```go
r.Get("/api/investigations/{id}/report.pdf", api.FederatedInvestigationReport(s.store, s.fedAgg))
```

Add this line right after it:

```go
r.Get("/api/investigations/{id}/graph", api.FederatedInvestigationGraph(s.store, s.fedAgg))
```

Find the federation-token group around line 674. Locate this line:

```go
r.Get("/api/v1/federation/investigations/{id}/report.pdf", api.FederationInvestigationReport(s.store))
```

Add this line right after it:

```go
r.Get("/api/v1/federation/investigations/{id}/graph", api.FederationInvestigationGraph(s.store))
```

- [ ] **Step B1.3: Build sanity check**

```bash
mkdir -p controlplane/ui/dist && touch controlplane/ui/dist/.gitkeep
go build ./controlplane/...
```

Expected: clean.

- [ ] **Step B1.4: Write the handler test**

Create `controlplane/api/investigation_graph_test.go`:

```go
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/section9labs/okesu/controlplane/db"
)

func TestInvestigationGraph_Returns404OnUnknown(t *testing.T) {
	store := newSeededTestStore(t)
	router := chi.NewRouter()
	router.Get("/api/investigations/{id}/graph", GetInvestigationGraphHandler(store))

	req := httptest.NewRequest("GET", "/api/investigations/99999/graph", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestInvestigationGraph_EmptyCase(t *testing.T) {
	store := newSeededTestStore(t)
	id, err := store.CreateInvestigation(&db.InvestigationInsert{Title: "empty"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	router := chi.NewRouter()
	router.Get("/api/investigations/{id}/graph", GetInvestigationGraphHandler(store))

	req := httptest.NewRequest("GET", "/api/investigations/"+strconv.FormatInt(id, 10)+"/graph", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp graphResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.TotalFindings != 0 {
		t.Errorf("TotalFindings = %d", resp.TotalFindings)
	}
	if len(resp.Nodes) != 0 {
		t.Errorf("Nodes = %d", len(resp.Nodes))
	}
	if len(resp.Edges) != 0 {
		t.Errorf("Edges = %d", len(resp.Edges))
	}
}

func TestInvestigationGraph_BuildResponse(t *testing.T) {
	// Pure-function test on buildGraphResponse with a synthetic db.GraphData.
	data := &db.GraphData{
		TotalFindings: 3,
		LimitApplied:  20,
		Findings: []db.GraphFinding{
			{ID: 1, Severity: "HIGH", Title: "Cron", Host: "h1", Agent: "edr-agent"},
			{ID: 2, Severity: "LOW", Title: "User", Host: "h1", Agent: "edr-agent"},
			{ID: 3, Severity: "INFO", Title: "Trivial", Host: "h2", Agent: ""},
		},
		IOCs: []db.GraphIOC{
			{ID: 100, Kind: "sha256", Value: "deadbeef", ObsCount: 2, HostCount: 1, FindingIDs: []int64{1, 2}},
		},
	}
	resp := buildGraphResponse(data)

	// 3 findings + 2 hosts + 1 daimon + 1 ioc = 7 nodes.
	if len(resp.Nodes) != 7 {
		t.Errorf("Nodes = %d, want 7", len(resp.Nodes))
	}
	// Edges: f1→h1, f1→edr, f1→i; f2→h1, f2→edr, f2→i; f3→h2 = 7 edges.
	if len(resp.Edges) != 7 {
		t.Errorf("Edges = %d, want 7", len(resp.Edges))
	}
	// daimon node finding_count is 2 (f1+f2; f3 has no agent).
	for _, n := range resp.Nodes {
		if n.Kind == "daimon" && n.ID == "d:edr-agent" {
			if n.FindingCount != 2 {
				t.Errorf("daimon finding_count = %d, want 2", n.FindingCount)
			}
		}
	}
}

func TestLastN(t *testing.T) {
	cases := map[string]string{
		"deadbeefcafe": "cafe",
		"abc":          "abc",
		"":             "",
		"1234":         "1234",
	}
	for in, want := range cases {
		if got := lastN(in, 4); got != want {
			t.Errorf("lastN(%q, 4) = %q, want %q", in, got, want)
		}
	}
}
```

- [ ] **Step B1.5: Run tests + final build**

```bash
go test ./controlplane/api/... -count=1 -run "InvestigationGraph|LastN"
go build ./controlplane/...
go vet ./controlplane/...
rm -rf controlplane/ui/dist
```

Expected: 4 PASS, build + vet clean.

- [ ] **Step B1.6: Commit**

```bash
git add controlplane/api/investigation_graph.go \
        controlplane/api/investigation_graph_test.go \
        controlplane/server.go
git commit -m "api(investigations): /graph endpoint with federation proxy"
```

---

## Task Group C — Frontend api.ts types + helper

### Task C1: Add GraphResponse types + api helper

**Files:**
- Modify: `web/src/api.ts`

- [ ] **Step C1.1: Add types**

Find the existing investigation types in `web/src/api.ts` (search for `InvestigationDetail`). Right after the last investigation-related interface, add:

```ts
// Graph view types (Phase 25.x). Returned by
// GET /api/investigations/{id}/graph. The bipartite layout puts
// findings on the left and hosts/daimons/IOCs on the right.

export type GraphNodeKind = 'finding' | 'host' | 'daimon' | 'ioc';

export interface GraphNode {
  id: string;
  kind: GraphNodeKind;
  label: string;
  // finding-only:
  severity?: string;
  host?: string;
  agent?: string;
  // daimon-only:
  finding_count?: number;
  // ioc-only:
  ioc_kind?: string;
  ioc_value?: string;
  obs_count?: number;
  host_count?: number;
}

export interface GraphEdge {
  id: string;
  source: string;
  target: string;
}

export interface GraphResponse {
  total_findings: number;
  limit_applied: number;
  nodes: GraphNode[];
  edges: GraphEdge[];
}
```

- [ ] **Step C1.2: Add api helper**

Find the `investigations:` block in the `api` const (search for `investigations: {`). Inside that block, add a new method next to the existing `audit` helper:

```ts
graph: (id: number, opts?: { cpInstanceID?: string; limit?: number }) => {
  const qs: string[] = [];
  if (opts?.cpInstanceID) qs.push(`cp=${encodeURIComponent(opts.cpInstanceID)}`);
  if (opts?.limit !== undefined) qs.push(`limit=${opts.limit}`);
  const suffix = qs.length > 0 ? `?${qs.join('&')}` : '';
  return request<GraphResponse>(`/api/investigations/${id}/graph${suffix}`);
},
```

- [ ] **Step C1.3: Type-check + commit**

```bash
cd /Users/matt/Code/Oracle/Okesu/.claude/worktrees/feat-investigation-graph/web
node_modules/.bin/tsc -b
```

Expected: clean.

```bash
cd /Users/matt/Code/Oracle/Okesu/.claude/worktrees/feat-investigation-graph
git add web/src/api.ts
git commit -m "web(api): GraphResponse types + api.investigations.graph helper"
```

---

## Task Group D — CaseGraph component

### Task D1: Pure layout helpers

**Files:**
- Create: `web/src/components/investigations/graph/layout.ts`
- Create: `web/src/components/investigations/graph/layout.test.ts`

- [ ] **Step D1.1: Write the failing test**

```ts
// web/src/components/investigations/graph/layout.test.ts
import { describe, it, expect } from 'vitest';
import { computePositions } from './layout';
import type { GraphNode, GraphEdge } from '../../../api';

const makeFinding = (id: number, severity = 'LOW'): GraphNode => ({
  id: `f:${id}`,
  kind: 'finding',
  label: `Finding ${id}`,
  severity,
});
const makeHost = (h: string): GraphNode => ({ id: `h:${h}`, kind: 'host', label: h });
const makeDaimon = (a: string): GraphNode => ({ id: `d:${a}`, kind: 'daimon', label: a });
const makeIOC = (kind: string, value: string): GraphNode => ({
  id: `i:${kind}:${value}`, kind: 'ioc', label: `${kind}:${value.slice(-4)}`,
  ioc_kind: kind, ioc_value: value,
});
const edge = (s: string, t: string): GraphEdge => ({ id: `${s}|${t}`, source: s, target: t });

describe('computePositions', () => {
  it('returns empty positions for empty input', () => {
    const out = computePositions([], []);
    expect(out.size).toBe(0);
  });

  it('places findings in the left column at x=80', () => {
    const out = computePositions([makeFinding(1)], []);
    const f = out.get('f:1');
    expect(f).toBeDefined();
    expect(f!.x).toBe(80);
  });

  it('places hosts/daimons/IOCs in the right column at x=520', () => {
    const out = computePositions(
      [makeFinding(1), makeHost('h1'), makeDaimon('a'), makeIOC('sha256', 'deadbeef')],
      [
        edge('f:1', 'h:h1'),
        edge('f:1', 'd:a'),
        edge('f:1', 'i:sha256:deadbeef'),
      ],
    );
    expect(out.get('h:h1')!.x).toBe(520);
    expect(out.get('d:a')!.x).toBe(520);
    expect(out.get('i:sha256:deadbeef')!.x).toBe(520);
  });

  it('orders right-column nodes hosts → daimons → IOCs', () => {
    const out = computePositions(
      [makeFinding(1), makeHost('h1'), makeDaimon('a'), makeIOC('sha256', 'deadbeef')],
      [
        edge('f:1', 'h:h1'),
        edge('f:1', 'd:a'),
        edge('f:1', 'i:sha256:deadbeef'),
      ],
    );
    const hY = out.get('h:h1')!.y;
    const dY = out.get('d:a')!.y;
    const iY = out.get('i:sha256:deadbeef')!.y;
    expect(hY).toBeLessThan(dY);
    expect(dY).toBeLessThan(iY);
  });

  it('sorts findings by edge degree desc within the left column', () => {
    // f:1 has 2 edges, f:2 has 1 edge → f:1 above f:2
    const out = computePositions(
      [makeFinding(1), makeFinding(2), makeHost('h1'), makeHost('h2')],
      [
        edge('f:1', 'h:h1'),
        edge('f:1', 'h:h2'),
        edge('f:2', 'h:h1'),
      ],
    );
    expect(out.get('f:1')!.y).toBeLessThan(out.get('f:2')!.y);
  });
});
```

- [ ] **Step D1.2: Run, expect FAIL**

```bash
cd /Users/matt/Code/Oracle/Okesu/.claude/worktrees/feat-investigation-graph/web
npm test -- --run src/components/investigations/graph/layout.test.ts
```

- [ ] **Step D1.3: Implement layout.ts**

```ts
// web/src/components/investigations/graph/layout.ts
//
// Pure layout math for the bipartite Graph view. Computes deterministic
// (x, y) positions for each node:
//
//   - Findings stack vertically in the left column (x=80).
//   - Right column (x=520) groups by kind: hosts top, then daimons,
//     then IOCs, with vertical gaps between kinds.
//   - Within each column-section, nodes sort by edge degree desc
//     (most-connected at the top).
//
// Output is a Map<nodeID, {x, y}> consumed by the react-flow renderer.

import type { GraphNode, GraphEdge } from '../../../api';

export interface Position {
  x: number;
  y: number;
}

export const LEFT_X = 80;
export const RIGHT_X = 520;
export const NODE_HEIGHT = 56;
export const VERTICAL_GAP = 12;
export const KIND_GAP = 24;

export function computePositions(
  nodes: GraphNode[],
  edges: GraphEdge[],
): Map<string, Position> {
  const out = new Map<string, Position>();

  // Compute degree per node id.
  const degree = new Map<string, number>();
  for (const e of edges) {
    degree.set(e.source, (degree.get(e.source) ?? 0) + 1);
    degree.set(e.target, (degree.get(e.target) ?? 0) + 1);
  }
  const degOf = (id: string) => degree.get(id) ?? 0;

  // Bucket by kind.
  const findings: GraphNode[] = [];
  const hosts: GraphNode[] = [];
  const daimons: GraphNode[] = [];
  const iocs: GraphNode[] = [];
  for (const n of nodes) {
    if (n.kind === 'finding') findings.push(n);
    else if (n.kind === 'host') hosts.push(n);
    else if (n.kind === 'daimon') daimons.push(n);
    else if (n.kind === 'ioc') iocs.push(n);
  }

  // Sort each bucket by degree desc, then label ascending for stability.
  const sortByDegree = (a: GraphNode, b: GraphNode) => {
    const dd = degOf(b.id) - degOf(a.id);
    if (dd !== 0) return dd;
    return a.label.localeCompare(b.label);
  };
  findings.sort(sortByDegree);
  hosts.sort(sortByDegree);
  daimons.sort(sortByDegree);
  iocs.sort(sortByDegree);

  // Place findings in the left column.
  let y = 40;
  for (const f of findings) {
    out.set(f.id, { x: LEFT_X, y });
    y += NODE_HEIGHT + VERTICAL_GAP;
  }

  // Place hosts, daimons, IOCs in the right column with kind gaps.
  y = 40;
  for (const h of hosts) {
    out.set(h.id, { x: RIGHT_X, y });
    y += NODE_HEIGHT + VERTICAL_GAP;
  }
  if (hosts.length > 0 && (daimons.length > 0 || iocs.length > 0)) y += KIND_GAP;
  for (const d of daimons) {
    out.set(d.id, { x: RIGHT_X, y });
    y += NODE_HEIGHT + VERTICAL_GAP;
  }
  if (daimons.length > 0 && iocs.length > 0) y += KIND_GAP;
  for (const i of iocs) {
    out.set(i.id, { x: RIGHT_X, y });
    y += NODE_HEIGHT + VERTICAL_GAP;
  }
  return out;
}
```

- [ ] **Step D1.4: Run + commit**

```bash
npm test -- --run src/components/investigations/graph/layout.test.ts
node_modules/.bin/tsc -b
```

Expected: 5 PASS.

```bash
cd /Users/matt/Code/Oracle/Okesu/.claude/worktrees/feat-investigation-graph
git add web/src/components/investigations/graph/layout.ts \
        web/src/components/investigations/graph/layout.test.ts
git commit -m "web(graph): pure layout helper for bipartite columns"
```

### Task D2: CaseGraph component

**Files:**
- Create: `web/src/components/investigations/CaseGraph.tsx`
- Create: `web/src/components/investigations/CaseGraph.test.tsx`

- [ ] **Step D2.1: Write the failing test**

```tsx
// web/src/components/investigations/CaseGraph.test.tsx
import { describe, it, expect, afterEach, vi, beforeEach } from 'vitest';
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { CaseGraph } from './CaseGraph';

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

beforeEach(() => {
  vi.mock('../../api', async () => {
    const actual = await vi.importActual<typeof import('../../api')>('../../api');
    return {
      ...actual,
      api: {
        ...actual.api,
        investigations: {
          ...actual.api.investigations,
          graph: vi.fn(),
        },
      },
    };
  });
});

import { api } from '../../api';

const baseGraph = {
  total_findings: 1,
  limit_applied: 20,
  nodes: [
    { id: 'f:1', kind: 'finding' as const, label: 'Cron', severity: 'HIGH' },
    { id: 'h:edr-1', kind: 'host' as const, label: 'edr-1' },
  ],
  edges: [{ id: 'f:1|h:edr-1', source: 'f:1', target: 'h:edr-1' }],
};

describe('CaseGraph', () => {
  it('renders empty-state message when bundleFindingsCount === 0', () => {
    render(
      <MemoryRouter>
        <CaseGraph investigationID={1} bundleFindingsCount={0} />
      </MemoryRouter>,
    );
    expect(screen.getByText(/no findings linked yet/i)).toBeTruthy();
  });

  it('fetches and renders nodes from the api', async () => {
    (api.investigations.graph as ReturnType<typeof vi.fn>).mockResolvedValue(baseGraph);
    render(
      <MemoryRouter>
        <CaseGraph investigationID={1} bundleFindingsCount={1} />
      </MemoryRouter>,
    );
    await waitFor(() => {
      expect(screen.getByText(/Cron/)).toBeTruthy();
    });
    expect(screen.getByText(/edr-1/)).toBeTruthy();
  });

  it('shows the cap banner when total_findings > limit_applied', async () => {
    (api.investigations.graph as ReturnType<typeof vi.fn>).mockResolvedValue({
      ...baseGraph,
      total_findings: 42,
    });
    render(
      <MemoryRouter>
        <CaseGraph investigationID={1} bundleFindingsCount={42} />
      </MemoryRouter>,
    );
    await waitFor(() => {
      expect(screen.getByText(/20 of 42 findings shown/i)).toBeTruthy();
    });
  });

  it('clicking a finding node fires entity:open', async () => {
    (api.investigations.graph as ReturnType<typeof vi.fn>).mockResolvedValue(baseGraph);
    const events: unknown[] = [];
    const handler = (e: Event) => events.push((e as CustomEvent).detail);
    window.addEventListener('entity:open', handler);
    try {
      render(
        <MemoryRouter>
          <CaseGraph investigationID={1} bundleFindingsCount={1} />
        </MemoryRouter>,
      );
      await waitFor(() => screen.getByText(/Cron/));
      fireEvent.click(screen.getByText(/Cron/));
      expect(events[0]).toMatchObject({ kind: 'finding', identityKey: '1' });
    } finally {
      window.removeEventListener('entity:open', handler);
    }
  });
});
```

- [ ] **Step D2.2: Run, expect FAIL**

```bash
npm test -- --run src/components/investigations/CaseGraph.test.tsx
```

Expected: FAIL — module doesn't exist.

- [ ] **Step D2.3: Implement CaseGraph.tsx**

```tsx
// web/src/components/investigations/CaseGraph.tsx
//
// Bipartite relationship view for an investigation. Findings on
// the left, hosts/daimons/IOCs on the right; edges where a finding
// touches an entity. Uses @xyflow/react (already a dep — used by
// orchestration run canvas).
//
// Click affordances reuse the SmartPayload `entity:open` event bus
// + EntityDrawerHost (PR #82). Hosts have no drawer — clicking
// navigates to /findings filtered by host instead.
import { useEffect, useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { ReactFlow, Background, BackgroundVariant, Controls, type Edge, type Node } from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import { api, type GraphNode, type GraphResponse } from '../../api';
import { computePositions } from './graph/layout';

interface Props {
  investigationID: number;
  cpInstanceID?: string;
  bundleFindingsCount: number;
}

export function CaseGraph({ investigationID, cpInstanceID, bundleFindingsCount }: Props) {
  const [data, setData] = useState<GraphResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const navigate = useNavigate();

  useEffect(() => {
    if (bundleFindingsCount === 0) {
      setData(null);
      return;
    }
    let cancelled = false;
    setLoading(true);
    setError(null);
    api.investigations
      .graph(investigationID, { cpInstanceID })
      .then((resp) => {
        if (!cancelled) {
          setData(resp);
          setLoading(false);
        }
      })
      .catch((e) => {
        if (!cancelled) {
          setError(e instanceof Error ? e.message : String(e));
          setLoading(false);
        }
      });
    return () => { cancelled = true; };
  }, [investigationID, cpInstanceID, bundleFindingsCount]);

  const { rfNodes, rfEdges } = useMemo(() => {
    if (!data) return { rfNodes: [] as Node[], rfEdges: [] as Edge[] };
    const positions = computePositions(data.nodes, data.edges);
    const rfNodes: Node[] = data.nodes.map((n) => {
      const pos = positions.get(n.id) ?? { x: 0, y: 0 };
      return {
        id: n.id,
        type: 'default',
        position: pos,
        data: { label: renderNodeLabel(n), graphNode: n },
        draggable: false,
        selectable: true,
      };
    });
    const rfEdges: Edge[] = data.edges.map((e) => ({
      id: e.id,
      source: e.source,
      target: e.target,
      type: 'smoothstep',
      style: { stroke: '#94a3b8', strokeWidth: 1 },
    }));
    return { rfNodes, rfEdges };
  }, [data]);

  function handleNodeClick(_e: React.MouseEvent, node: Node) {
    const g = node.data?.graphNode as GraphNode | undefined;
    if (!g) return;
    if (g.kind === 'finding') {
      const id = g.id.slice(2); // strip "f:"
      window.dispatchEvent(new CustomEvent('entity:open', { detail: { kind: 'finding', identityKey: id, cpInstanceID } }));
    } else if (g.kind === 'daimon') {
      const name = g.id.slice(2);
      window.dispatchEvent(new CustomEvent('entity:open', { detail: { kind: 'daimon', identityKey: name, cpInstanceID } }));
    } else if (g.kind === 'ioc') {
      const key = g.id.slice(2); // strip "i:"
      window.dispatchEvent(new CustomEvent('entity:open', { detail: { kind: 'ioc', identityKey: key, cpInstanceID } }));
    } else if (g.kind === 'host') {
      const host = g.id.slice(2);
      const cpQS = cpInstanceID ? `&cp=${encodeURIComponent(cpInstanceID)}` : '';
      navigate(`/findings?host=${encodeURIComponent(host)}${cpQS}`);
    }
  }

  if (bundleFindingsCount === 0) {
    return (
      <div className="border border-border rounded-md bg-white p-8 text-center text-sm text-ink-mute italic">
        No findings linked yet — link findings on the Findings tab to see relationships.
      </div>
    );
  }

  return (
    <div className="border border-border rounded-md bg-white" style={{ minHeight: 600 }}>
      {data && data.total_findings > data.limit_applied && (
        <div className="px-3 py-2 border-b border-border text-xs text-ink-dim bg-amber-50">
          {data.limit_applied} of {data.total_findings} findings shown — sorted by severity. Use the Findings tab for finer control.
        </div>
      )}
      {error && (
        <div className="px-3 py-2 border-b border-border text-xs text-red-700 bg-red-50">
          Couldn't load graph data: {error}{' '}
          <button
            type="button"
            onClick={() => {
              setError(null);
              setData(null);
              // Trigger a re-fetch by bumping a state — simplest is to
              // re-set bundleFindingsCount via the parent on next poll;
              // for now Retry is a manual reload of the same effect.
              setLoading(true);
              api.investigations.graph(investigationID, { cpInstanceID })
                .then(setData).catch((e) => setError(String(e)))
                .finally(() => setLoading(false));
            }}
            className="ml-2 underline"
          >
            Retry
          </button>
        </div>
      )}
      {loading && !data && (
        <div className="p-8 text-center text-sm text-ink-mute italic">Loading graph…</div>
      )}
      {data && (
        <div style={{ height: 600 }}>
          <ReactFlow
            nodes={rfNodes}
            edges={rfEdges}
            onNodeClick={handleNodeClick}
            nodesDraggable={false}
            nodesConnectable={false}
            elementsSelectable={true}
            fitView
            proOptions={{ hideAttribution: true }}
          >
            <Background variant={BackgroundVariant.Dots} gap={16} size={1} />
            <Controls showInteractive={false} />
          </ReactFlow>
        </div>
      )}
    </div>
  );
}

function renderNodeLabel(n: GraphNode): string {
  if (n.kind === 'finding') {
    const idNum = n.id.slice(2);
    return `Finding #${idNum} — ${truncate(n.label, 40)}`;
  }
  if (n.kind === 'daimon') {
    return `${n.label} (${n.finding_count ?? 0} findings)`;
  }
  if (n.kind === 'ioc') {
    return `${n.label} — ${n.obs_count ?? 0} obs / ${n.host_count ?? 0} hosts`;
  }
  return n.label;
}

function truncate(s: string, n: number): string {
  if (s.length <= n) return s;
  return s.slice(0, n - 1) + '…';
}
```

- [ ] **Step D2.4: Run + commit**

```bash
cd /Users/matt/Code/Oracle/Okesu/.claude/worktrees/feat-investigation-graph/web
npm test -- --run src/components/investigations/CaseGraph.test.tsx
node_modules/.bin/tsc -b
```

Expected: 4 PASS.

```bash
cd /Users/matt/Code/Oracle/Okesu/.claude/worktrees/feat-investigation-graph
git add web/src/components/investigations/CaseGraph.tsx \
        web/src/components/investigations/CaseGraph.test.tsx
git commit -m "web(graph): CaseGraph react-flow component"
```

---

## Task Group E — Tab wiring in InvestigationDetail.tsx

### Task E1: Add 'graph' tab + lazy-load

**Files:**
- Modify: `web/src/pages/InvestigationDetail.tsx`

- [ ] **Step E1.1: Extend the Tab type**

Find this line near the top of the file:

```tsx
type Tab = 'overview' | 'findings' | 'runs' | 'iocs' | 'daimons' | 'orchestrations' | 'notes' | 'audit';
```

Replace with:

```tsx
type Tab = 'overview' | 'graph' | 'findings' | 'runs' | 'iocs' | 'daimons' | 'orchestrations' | 'notes' | 'audit';
```

- [ ] **Step E1.2: Add the lazy import + Suspense wrapper**

Near the top of the file, add a lazy import next to the existing `OrchestrationRunCanvas` lazy import (search `lazy(`):

```tsx
import { Suspense, lazy } from 'react';
const CaseGraph = lazy(() => import('../components/investigations/CaseGraph').then((m) => ({ default: m.CaseGraph })));
```

(If `Suspense, lazy` is already imported, just add the `CaseGraph = lazy(...)` line.)

- [ ] **Step E1.3: Add the tab nav entry**

Find the tab navigation block (search for `tab === 'overview'` to locate the surrounding structure — it's a list of tab buttons). Each tab is a button with an onClick that calls `setTab(...)`. Find the `'overview'` tab entry and add a `'graph'` button right after it:

```tsx
<button
  onClick={() => setTab('graph')}
  className={cn(
    'px-3 py-1.5 text-xs rounded-md',
    tab === 'graph' ? 'bg-brand-50 text-brand-700' : 'text-ink-mute hover:text-ink',
  )}
>
  Graph
</button>
```

(The exact className shape matches what the existing tab buttons use; copy the exact structure of the adjacent `'overview'` button.)

- [ ] **Step E1.4: Render CaseGraph for tab === 'graph'**

In the tab-render block (search for `{tab === 'overview' && (`), add a new block right after the overview block:

```tsx
{tab === 'graph' && (
  <Suspense fallback={<div className="p-8 text-center text-sm text-ink-mute italic">Loading graph…</div>}>
    <CaseGraph
      investigationID={invID}
      cpInstanceID={cpInstanceID}
      bundleFindingsCount={bundle.findings.length}
    />
  </Suspense>
)}
```

- [ ] **Step E1.5: Type-check + run all tests**

```bash
cd /Users/matt/Code/Oracle/Okesu/.claude/worktrees/feat-investigation-graph/web
node_modules/.bin/tsc -b
npm test -- --run
```

Both must pass.

- [ ] **Step E1.6: Commit**

```bash
cd /Users/matt/Code/Oracle/Okesu/.claude/worktrees/feat-investigation-graph
git add web/src/pages/InvestigationDetail.tsx
git commit -m "web(investigations): Graph tab in case detail"
```

---

## Task Group Z — docs + sweep + PR body

### Task Z1: Architecture doc append

**Files:**
- Modify: `docs/architecture.md`

- [ ] **Step Z1.1: Append a section**

```markdown
## Investigation graph view (bipartite)

A "Graph" tab on the investigation detail page renders the case as a bipartite relationship graph: linked findings on the left, hosts / daimons / IOCs on the right, with edges showing which finding touches which entity. Surfaces the "this IOC is the hub across N findings" pattern that the summary cards on the Overview tab can't make visible.

Server-side: `GET /api/investigations/{id}/graph?limit=20` returns `{nodes, edges, total_findings, limit_applied}`. Joins `investigation_findings` + `findings` + `ioc_observations` + `iocs`. Caps at 20 findings (max 100) sorted by severity desc + Ts desc; client renders a "20 of N shown" banner when capped. Federation routes via the existing `?cp=<instance_id>` proxy convention.

Client-side: `web/src/components/investigations/CaseGraph.tsx` uses `@xyflow/react` (already a dep — orchestration run canvas) with deterministic bipartite layout (`graph/layout.ts`) and degree-sort within each column. Click affordances reuse the SmartPayload `entity:open` event bus + `EntityDrawerHost` portal. Hosts have no drawer; clicking navigates to `/findings?host=…` instead.
```

```bash
git add docs/architecture.md
git commit -m "docs(architecture): investigation graph view"
```

### Task Z2: Test sweep

```bash
cd /Users/matt/Code/Oracle/Okesu/.claude/worktrees/feat-investigation-graph
mkdir -p controlplane/ui/dist && touch controlplane/ui/dist/.gitkeep
go test ./... -count=1 -timeout 180s 2>&1 | grep -E "^(FAIL|ok|---)" | head -25
rm -rf controlplane/ui/dist
cd web && node_modules/.bin/tsc -b && npm test -- --run 2>&1 | tail -8
```

All must pass.

### Task Z3: PR body

**Create:** `docs/superpowers/plans/2026-05-01-investigation-graph-pr-body.md`

```markdown
## Summary

Follow-up #1 from the Investigation Overview backlog. Adds a "Graph" tab to the investigation detail page rendering the case as a bipartite relationship graph: linked findings on the left, the entities they touch (hosts / daimons / IOCs) on the right, with edges showing which finding hits which entity. Operators get an at-a-glance "this IOC is the hub across N findings" pattern that the summary cards on the Overview tab can't surface.

- New endpoint `GET /api/investigations/{id}/graph?limit=20` returning `{nodes, edges, total_findings, limit_applied}`. Joins `investigation_findings` + `findings` + `ioc_observations` + `iocs`. Federation routes via the existing `?cp=<instance_id>` proxy convention.
- New client component `CaseGraph.tsx` using `@xyflow/react` (already a dep — orchestration run canvas). Deterministic bipartite layout (`graph/layout.ts`) with degree-sort within each column.
- Cap behavior: top-20 findings by severity desc + Ts desc; banner shows "20 of N findings shown" when over the cap. Operator narrows further via the Findings tab.
- Click affordances reuse the SmartPayload `entity:open` event bus + `EntityDrawerHost` portal. Hosts have no drawer; clicking navigates to `/findings?host=…` instead.
- Tab order: `overview → graph → findings → runs → iocs → daimons → orchestrations → notes → audit`.

The graph view is a static layout — no force-directed physics, no drag-to-rearrange. Pan and zoom only.

## Test plan

Unit tests in this PR:
- `controlplane/db/investigation_graph_test.go` — empty-case, top-N-by-severity, limit clamping (4 tests).
- `controlplane/api/investigation_graph_test.go` — handler returns 404/empty/correct response shape; `lastN` helper (4 tests).
- `web/src/components/investigations/graph/layout.test.ts` — column placement + kind ordering + degree sort (5 tests).
- `web/src/components/investigations/CaseGraph.test.tsx` — empty state, fetch + render, cap banner, click → entity:open (4 tests).

Total: 17 new tests + full Go suite + full vitest suite all green.

Manual lab smoke (post-merge):
- [ ] Open an active case with 5+ findings, 1+ shared IOC, multiple hosts. Click "Graph" tab. Verify bipartite layout renders.
- [ ] Click a finding → FindingDrawer opens.
- [ ] Click an IOC → IOC chip drawer / navigation works.
- [ ] Click a host → navigates to `/findings?host=…`.
- [ ] Trigger the cap by linking 25 findings; verify banner shows "20 of 25 findings shown".
- [ ] Federated case (`cp_source` set) → graph endpoint proxies to child correctly.

## Files

**New (server):**
- `controlplane/db/investigation_graph.go` (+ test)
- `controlplane/api/investigation_graph.go` (+ test)

**New (frontend):**
- `web/src/components/investigations/graph/layout.ts` (+ test)
- `web/src/components/investigations/CaseGraph.tsx` (+ test)

**Modified:**
- `controlplane/server.go` — mount the two new routes (cookie-auth admin + federation-token).
- `web/src/api.ts` — `GraphResponse` types + `api.investigations.graph` helper.
- `web/src/pages/InvestigationDetail.tsx` — extend `Tab` union, add tab nav entry, lazy-load + render CaseGraph.
- `docs/architecture.md` — new section.

## Spec / plan

- Spec: `docs/superpowers/specs/2026-05-01-investigation-graph-design.md`
- Plan: `docs/superpowers/plans/2026-05-01-investigation-graph.md`

## Closes / refs

Implements item #1 from `investigation_overview_followups.md`. Four remaining backlog items unchanged.
```

```bash
git add docs/superpowers/plans/2026-05-01-investigation-graph-pr-body.md
git commit -m "docs: investigation graph PR body"
```

### Task Z4: DO NOT push or open the PR

The controller (the human operator) handles `git push` and `gh pr create`.
