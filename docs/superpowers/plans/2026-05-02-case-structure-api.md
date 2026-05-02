# Case-Structure Server-Side Aggregation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move the four investigation Overview-tab structure-card aggregations behind a new `GET /api/investigations/{id}/structure` endpoint, with the existing client aggregators preserved as a graceful fallback for federated children running an older binary.

**Architecture:** Thin handler over four Store methods (one new for hosts, three existing). Sort happens in Go after fetch so the existing bundle consumers' ordering is unaffected. Federation pair mirrors `FederatedInvestigationGraph`. Client switches `CaseStructure` to fetch-on-mount with a cancel-flagged `useEffect`; on failure the existing aggregators (extracted to a sibling module) fill the same wire shape.

**Tech Stack:** Go (chi router, sqlite/postgres) backend; React 18 + TypeScript + Vitest frontend. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-05-02-case-structure-api-design.md`

**Branch:** `feat/case-structure-api` (current worktree)

---

## File Structure

| File | Status | Responsibility |
|---|---|---|
| `controlplane/db/investigation_enriched.go` | MODIFY | Add `InvestigationHostItem` + `ListHostsForInvestigation`; extend `InvestigationOrchestrationItem` and the SQL behind `ListOrchestrationsForInvestigation` |
| `controlplane/db/investigation_structure_test.go` | NEW | DB tests for host listing + extended orch shape |
| `controlplane/api/investigation_structure.go` | NEW | `GetInvestigationStructureHandler` + federation pair + sort helper |
| `controlplane/api/investigation_structure_test.go` | NEW | Handler tests (404, JSON shape, federation guard) |
| `controlplane/server.go` | MODIFY | Register `/structure` routes (parent + child) |
| `web/src/api.ts` | MODIFY | `api.investigations.structure(id, cpInstanceID?)` + `InvestigationStructure` + `InvestigationHostItem` types; extend `InvestigationOrchestrationItem` |
| `web/src/components/investigations/caseStructure/derive.ts` | NEW | The four extracted aggregator helpers |
| `web/src/components/investigations/caseStructure/derive.test.ts` | NEW | Unit tests for the aggregators |
| `web/src/components/investigations/CaseStructure.tsx` | MODIFY | Fetch `/structure` on mount; `view = data ?? deriveFromBundle(bundle)`; fallback console.warn |
| `web/src/components/investigations/CaseStructure.test.tsx` | NEW | Component tests for both code paths |
| `docs/architecture.md` | MODIFY | New section under "Investigation Overview" |

**Note on sort placement:** The three existing Store methods sort by recency (`ORDER BY last_seen DESC`, `last_started_at DESC`). The structure cards need descending-by-primary-count. We sort in the handler (Go) rather than changing the SQL so other consumers of the bundle (the IOCs / Runs / Daimons tabs) keep their existing ordering. New host method sorts in SQL because no existing consumer constrains it.

---

## Task 1: DB additions — `InvestigationHostItem` + extended `InvestigationOrchestrationItem`

Backend foundation: one new Store method and one extended struct + SQL. Tests live in a new `investigation_structure_test.go` file in the `db` package so the structure-feature tests sit together.

**Files:**
- Modify: `controlplane/db/investigation_enriched.go`
- Create: `controlplane/db/investigation_structure_test.go`

- [ ] **Step 1: Write the failing test for `ListHostsForInvestigation`**

Create `controlplane/db/investigation_structure_test.go`:

```go
package db

import (
	"database/sql"
	"testing"
)

func TestListHostsForInvestigation_EmptyCase(t *testing.T) {
	s := openTempStore(t)
	invID, err := s.CreateInvestigation(&InvestigationInsert{Title: "empty"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := s.ListHostsForInvestigation(invID)
	if err != nil {
		t.Fatalf("ListHosts: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len(hosts) = %d, want 0", len(got))
	}
}

func TestListHostsForInvestigation_SortedByCountDesc(t *testing.T) {
	s := openTempStore(t)
	invID, err := s.CreateInvestigation(&InvestigationInsert{Title: "case"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// 3 findings on host-a, 1 on host-b, 1 with empty host (excluded).
	for i := 0; i < 3; i++ {
		fid := mustStructInsertFinding(t, s, "HIGH", "host-a", "edr-agent", int64(1000+i))
		mustStructLinkFinding(t, s, invID, fid)
	}
	fid := mustStructInsertFinding(t, s, "HIGH", "host-b", "edr-agent", 2000)
	mustStructLinkFinding(t, s, invID, fid)
	fid = mustStructInsertFinding(t, s, "HIGH", "", "edr-agent", 3000)
	mustStructLinkFinding(t, s, invID, fid)

	got, err := s.ListHostsForInvestigation(invID)
	if err != nil {
		t.Fatalf("ListHosts: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(hosts) = %d, want 2 (empty host excluded)", len(got))
	}
	if got[0].Host != "host-a" || got[0].Count != 3 {
		t.Errorf("got[0] = %+v, want host-a/3", got[0])
	}
	if got[1].Host != "host-b" || got[1].Count != 1 {
		t.Errorf("got[1] = %+v, want host-b/1", got[1])
	}
}

// mustStructInsertFinding wraps Store.InsertFinding for the structure
// tests. Locally named to avoid collision with helpers in
// investigation_graph_test.go and others.
func mustStructInsertFinding(t *testing.T, s *Store, severity, host, agent string, ts int64) int64 {
	t.Helper()
	eventID, err := s.InsertEvent(&Event{
		Ts:      ts,
		Type:    "finding",
		Agent:   sql.NullString{String: agent, Valid: agent != ""},
		RawJSON: "{}",
	})
	if err != nil {
		t.Fatalf("InsertEvent: %v", err)
	}
	id, err := s.InsertFinding(&FindingInsert{
		EventID:  eventID,
		Ts:       ts,
		Agent:    sql.NullString{String: agent, Valid: agent != ""},
		Host:     sql.NullString{String: host, Valid: host != ""},
		Severity: sql.NullString{String: severity, Valid: true},
		Title:    sql.NullString{String: "t", Valid: true},
	})
	if err != nil {
		t.Fatalf("InsertFinding: %v", err)
	}
	return id
}

func mustStructLinkFinding(t *testing.T, s *Store, invID, findingID int64) {
	t.Helper()
	if err := s.LinkFindingToInvestigation(invID, findingID); err != nil {
		t.Fatalf("LinkFindingToInvestigation: %v", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
go test ./controlplane/db/ -run TestListHostsForInvestigation -v
```

Expected: FAIL — `s.ListHostsForInvestigation undefined` (compile error).

- [ ] **Step 3: Implement `InvestigationHostItem` + `ListHostsForInvestigation`**

In `controlplane/db/investigation_enriched.go`, add the type and method after `InvestigationOrchestrationItem` (around line 85, before `ListFindingsForInvestigationEnriched`):

```go
// InvestigationHostItem is one row of the case's host rollup.
// Aggregated from the host column of the case's linked findings.
type InvestigationHostItem struct {
	Host  string
	Count int
}

// ListHostsForInvestigation returns distinct non-empty hosts seen
// across the case's linked findings, sorted desc by count. Findings
// with NULL/empty host are excluded.
func (s *Store) ListHostsForInvestigation(invID int64) ([]InvestigationHostItem, error) {
	rows, err := s.Query(`
		SELECT f.host AS host, COUNT(*) AS count
		FROM investigation_findings l
		JOIN findings f ON f.id = l.finding_id
		WHERE l.investigation_id = ? AND f.host IS NOT NULL AND f.host != ''
		GROUP BY f.host
		ORDER BY count DESC, f.host ASC`, invID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []InvestigationHostItem{}
	for rows.Next() {
		var it InvestigationHostItem
		if err := rows.Scan(&it.Host, &it.Count); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run the host tests to verify they pass**

```bash
go test ./controlplane/db/ -run TestListHostsForInvestigation -v
```

Expected: PASS — both host tests green.

- [ ] **Step 5: Write the failing test for the extended orchestration shape**

Append to `controlplane/db/investigation_structure_test.go`:

```go
func TestListOrchestrationsForInvestigation_StatusBreakdown(t *testing.T) {
	s := openTempStore(t)
	invID, err := s.CreateInvestigation(&InvestigationInsert{Title: "case"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Seed orchestration row.
	if _, err := s.Exec(`INSERT INTO orchestrations (id, name) VALUES (10, 'auto-triage')`); err != nil {
		t.Fatalf("seed orchestration: %v", err)
	}

	// Five orchestration_runs across statuses + link each to the case.
	type runSeed struct {
		id     int64
		status string
	}
	seeds := []runSeed{
		{100, "completed"}, {101, "completed"}, {102, "failed"},
		{103, "cancelled"}, {104, "running"},
	}
	for _, r := range seeds {
		if _, err := s.Exec(`INSERT INTO orchestration_runs (id, orchestration_id, status, trigger_kind) VALUES (?, 10, ?, 'manual')`, r.id, r.status); err != nil {
			t.Fatalf("insert run %d: %v", r.id, err)
		}
		if err := s.LinkRunToInvestigation(invID, r.id); err != nil {
			t.Fatalf("link run %d: %v", r.id, err)
		}
	}

	got, err := s.ListOrchestrationsForInvestigation(invID)
	if err != nil {
		t.Fatalf("ListOrch: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(orchs) = %d, want 1", len(got))
	}
	o := got[0]
	if o.RunCount != 5 {
		t.Errorf("RunCount = %d, want 5", o.RunCount)
	}
	if o.Completed != 2 || o.Failed != 1 || o.Cancelled != 1 || o.Running != 1 {
		t.Errorf("status breakdown = %d/%d/%d/%d, want 2/1/1/1",
			o.Completed, o.Failed, o.Cancelled, o.Running)
	}
}
```

- [ ] **Step 6: Run the orchestration test to verify it fails**

```bash
go test ./controlplane/db/ -run TestListOrchestrationsForInvestigation_StatusBreakdown -v
```

Expected: FAIL — `o.Completed undefined` (struct doesn't have the field yet).

- [ ] **Step 7: Extend `InvestigationOrchestrationItem` and its SQL**

In `controlplane/db/investigation_enriched.go`, modify the existing `InvestigationOrchestrationItem` struct (around line 80):

```go
// InvestigationOrchestrationItem groups linked runs by orchestration.
// Each row has the orchestration name, total run count, per-status
// counts (Completed/Failed/Cancelled/Running — anything not in the
// first three is bucketed as Running, matching the operator UI), and
// the most recent run's started_at.
type InvestigationOrchestrationItem struct {
	OrchestrationID   sql.NullInt64
	OrchestrationName string
	RunCount          int
	Completed         int
	Failed            int
	Cancelled         int
	Running           int
	LastStartedAt     string
}
```

And update `ListOrchestrationsForInvestigation` (around line 242) — swap the `SELECT` and `Scan`:

```go
func (s *Store) ListOrchestrationsForInvestigation(invID int64) ([]InvestigationOrchestrationItem, error) {
	rows, err := s.Query(`
		SELECT r.orchestration_id,
		       COALESCE(o.name, '(deleted)') AS orch_name,
		       COUNT(r.id) AS run_count,
		       COUNT(CASE WHEN r.status = 'completed' THEN 1 END) AS completed,
		       COUNT(CASE WHEN r.status = 'failed'    THEN 1 END) AS failed,
		       COUNT(CASE WHEN r.status = 'cancelled' THEN 1 END) AS cancelled,
		       COUNT(CASE WHEN r.status NOT IN ('completed','failed','cancelled') THEN 1 END) AS running,
		       MAX(r.started_at) AS last_started_at
		FROM investigation_runs l
		JOIN orchestration_runs r ON r.id = l.orchestration_run_id
		LEFT JOIN orchestrations o ON o.id = r.orchestration_id
		WHERE l.investigation_id = ?
		GROUP BY r.orchestration_id, COALESCE(o.name, '(deleted)')
		ORDER BY last_started_at DESC`, invID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []InvestigationOrchestrationItem{}
	for rows.Next() {
		var it InvestigationOrchestrationItem
		var lastStarted sql.NullTime
		if err := rows.Scan(&it.OrchestrationID, &it.OrchestrationName,
			&it.RunCount,
			&it.Completed, &it.Failed, &it.Cancelled, &it.Running,
			&lastStarted); err != nil {
			return nil, err
		}
		if lastStarted.Valid {
			it.LastStartedAt = lastStarted.Time.UTC().Format(rfc3339)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}
```

- [ ] **Step 8: Run the orch test to verify it passes**

```bash
go test ./controlplane/db/ -run TestListOrchestrationsForInvestigation_StatusBreakdown -v
```

Expected: PASS.

- [ ] **Step 9: Run the full DB suite to confirm no regressions**

```bash
go test ./controlplane/db/...
```

Expected: PASS — all existing tests still green (the struct extension is additive; the SQL still satisfies any test that only checked `RunCount`).

- [ ] **Step 10: Commit**

```bash
git add controlplane/db/investigation_enriched.go controlplane/db/investigation_structure_test.go
git commit -m "feat(db): host rollup + orch status breakdown for case structure"
```

---

## Task 2: HTTP handler + federation pair + routes

The handler is thin: existence check, four list calls, sort, emit. Federation mirrors `investigation_graph.go` exactly.

**Files:**
- Create: `controlplane/api/investigation_structure.go`
- Create: `controlplane/api/investigation_structure_test.go`
- Modify: `controlplane/server.go`

- [ ] **Step 1: Write the failing 404 test**

Create `controlplane/api/investigation_structure_test.go`:

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

func TestInvestigationStructure_Unknown404(t *testing.T) {
	store := openTempStoreForAPI(t)
	router := chi.NewRouter()
	router.Get("/api/investigations/{id}/structure", GetInvestigationStructureHandler(store))

	req := httptest.NewRequest("GET", "/api/investigations/99999/structure", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestInvestigationStructure_EmptyCase(t *testing.T) {
	store := openTempStoreForAPI(t)
	id, err := store.CreateInvestigation(&db.InvestigationInsert{Title: "empty"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	router := chi.NewRouter()
	router.Get("/api/investigations/{id}/structure", GetInvestigationStructureHandler(store))

	req := httptest.NewRequest("GET", "/api/investigations/"+strconv.FormatInt(id, 10)+"/structure", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var got map[string]any
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, k := range []string{"hosts", "iocs", "daimons", "orchestrations"} {
		v, ok := got[k]
		if !ok {
			t.Errorf("missing key %q", k)
			continue
		}
		arr, ok := v.([]any)
		if !ok {
			t.Errorf("key %q is %T, want []any", k, v)
			continue
		}
		if len(arr) != 0 {
			t.Errorf("key %q has %d entries, want 0", k, len(arr))
		}
	}
}
```

`openTempStoreForAPI` is the existing helper used by `investigation_graph_test.go`. Re-use it.

- [ ] **Step 2: Run the failing tests**

```bash
go test ./controlplane/api/ -run TestInvestigationStructure -v
```

Expected: FAIL — `GetInvestigationStructureHandler undefined`.

- [ ] **Step 3: Implement the handler + federation pair**

Create `controlplane/api/investigation_structure.go`:

```go
// HTTP layer for the investigation structure endpoint. Returns four
// pre-aggregated, pre-sorted lists (hosts / IOCs / daimons /
// orchestrations) so the Overview tab's structure cards don't have to
// roll their own aggregation client-side at scale.
package api

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/section9labs/okesu/controlplane/db"
	"github.com/section9labs/okesu/controlplane/federation"
)

type structureResponse struct {
	Hosts          []db.InvestigationHostItem          `json:"hosts"`
	IOCs           []db.InvestigationIOCItem           `json:"iocs"`
	Daimons        []db.InvestigationDaimonItem        `json:"daimons"`
	Orchestrations []db.InvestigationOrchestrationItem `json:"orchestrations"`
}

// GetInvestigationStructureHandler serves /api/investigations/{id}/structure.
func GetInvestigationStructureHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := investigationIDFromChi(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if _, err := store.GetInvestigation(id); err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		hosts, _ := store.ListHostsForInvestigation(id)
		iocs, _ := store.ListIOCsForInvestigation(id)
		daimons, _ := store.ListDaimonsForInvestigation(id)
		orchs, _ := store.ListOrchestrationsForInvestigation(id)

		// Sort iocs / daimons / orchs desc by primary count. The
		// underlying methods sort by recency (last_seen, last_started)
		// to keep the existing bundle consumers' ordering. The
		// structure cards want the heaviest hitters first.
		sort.SliceStable(iocs, func(i, j int) bool {
			return iocs[i].ObservationCount > iocs[j].ObservationCount
		})
		sort.SliceStable(daimons, func(i, j int) bool {
			if daimons[i].FindingCount != daimons[j].FindingCount {
				return daimons[i].FindingCount > daimons[j].FindingCount
			}
			return daimons[i].LastSeenTs > daimons[j].LastSeenTs
		})
		sort.SliceStable(orchs, func(i, j int) bool {
			return orchs[i].RunCount > orchs[j].RunCount
		})

		// Defensive nil → empty so JSON consumers see [], not null.
		if hosts == nil {
			hosts = []db.InvestigationHostItem{}
		}
		if iocs == nil {
			iocs = []db.InvestigationIOCItem{}
		}
		if daimons == nil {
			daimons = []db.InvestigationDaimonItem{}
		}
		if orchs == nil {
			orchs = []db.InvestigationOrchestrationItem{}
		}

		resp := structureResponse{
			Hosts:          hosts,
			IOCs:           iocs,
			Daimons:        daimons,
			Orchestrations: orchs,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// FederatedInvestigationStructure — parent-side wrapper. Proxies to
// the owning child via ?cp=<instance_id>; falls through to the local
// handler otherwise.
func FederatedInvestigationStructure(store *db.Store, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.Replace(r.URL.Path, "/api/investigations/", "/api/v1/federation/investigations/", 1)
		if handled, _ := proxyToCPByQuery(w, r, agg, path); handled {
			return
		}
		GetInvestigationStructureHandler(store).ServeHTTP(w, r)
	}
}

// FederationInvestigationStructure — child-side, token-authed sibling.
func FederationInvestigationStructure(store *db.Store) http.HandlerFunc {
	return requireFederationToken(store, GetInvestigationStructureHandler(store))
}
```

- [ ] **Step 4: Run the handler tests to verify they pass**

```bash
go test ./controlplane/api/ -run TestInvestigationStructure -v
```

Expected: PASS — both 404 and empty-case tests green.

- [ ] **Step 5: Add the populated-case test**

Append to `controlplane/api/investigation_structure_test.go`:

```go
func TestInvestigationStructure_Populated(t *testing.T) {
	store := openTempStoreForAPI(t)
	id, err := store.CreateInvestigation(&db.InvestigationInsert{Title: "case"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// 2 findings on host-a, 1 on host-b.
	for i, host := range []string{"host-a", "host-a", "host-b"} {
		fid := mustAPIInsertFinding(t, store, "HIGH", host, "edr-agent", int64(1000+i))
		if err := store.LinkFindingToInvestigation(id, fid); err != nil {
			t.Fatalf("link finding: %v", err)
		}
	}

	router := chi.NewRouter()
	router.Get("/api/investigations/{id}/structure", GetInvestigationStructureHandler(store))
	req := httptest.NewRequest("GET", "/api/investigations/"+strconv.FormatInt(id, 10)+"/structure", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var got struct {
		Hosts []struct {
			Host  string `json:"host"`
			Count int    `json:"count"`
		} `json:"hosts"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Hosts) != 2 {
		t.Fatalf("len(hosts) = %d, want 2", len(got.Hosts))
	}
	if got.Hosts[0].Host != "host-a" || got.Hosts[0].Count != 2 {
		t.Errorf("hosts[0] = %+v, want host-a/2", got.Hosts[0])
	}
}

// mustAPIInsertFinding mirrors mustStructInsertFinding from the db
// package — duplicated so the api tests don't depend on the db test
// internal helpers.
func mustAPIInsertFinding(t *testing.T, s *db.Store, severity, host, agent string, ts int64) int64 {
	t.Helper()
	eventID, err := s.InsertEvent(&db.Event{
		Ts:      ts,
		Type:    "finding",
		Agent:   sqlNullString(agent),
		RawJSON: "{}",
	})
	if err != nil {
		t.Fatalf("InsertEvent: %v", err)
	}
	id, err := s.InsertFinding(&db.FindingInsert{
		EventID:  eventID,
		Ts:       ts,
		Agent:    sqlNullString(agent),
		Host:     sqlNullString(host),
		Severity: sqlNullString(severity),
		Title:    sqlNullString("t"),
	})
	if err != nil {
		t.Fatalf("InsertFinding: %v", err)
	}
	return id
}
```

If `sqlNullString` doesn't exist as a helper in `controlplane/api/` — the existing graph test does NOT have it. Define it inline at the bottom of this test file:

```go
func sqlNullString(s string) (out struct {
	String string
	Valid  bool
}) {
	// quick local builder; replace with sql.NullString from database/sql.
	type ns = struct {
		String string
		Valid  bool
	}
	out = ns{String: s, Valid: s != ""}
	return
}
```

Actually — this is cleaner: import `database/sql` directly and inline the literal `sql.NullString{...}` at every call site. Replace `sqlNullString(host)` with `sql.NullString{String: host, Valid: host != ""}`. Drop the helper. Add `"database/sql"` to imports.

Use whichever form the api test files already use; if no sibling helper exists, prefer the inline form.

- [ ] **Step 6: Run the populated test**

```bash
go test ./controlplane/api/ -run TestInvestigationStructure_Populated -v
```

Expected: PASS.

- [ ] **Step 7: Register the routes in `controlplane/server.go`**

Find the existing `/graph` route registrations (around line 675). Insert two new lines next to them — one for the parent-side (`FederatedInvestigationStructure`) and one for the child-side (`FederationInvestigationStructure`).

Locate the parent-side block first (near line 891 area where other `/api/investigations/{id}/...` routes register; the graph one specifically — search for `FederatedInvestigationGraph`). Add:

```go
r.Get("/api/investigations/{id}/structure",
    api.FederatedInvestigationStructure(s.store, s.federationAgg))
```

Locate the child-side `/api/v1/federation/investigations/{id}/graph` line (around line 675). Add:

```go
r.Get("/api/v1/federation/investigations/{id}/structure", api.FederationInvestigationStructure(s.store))
```

The exact identifier for the federation aggregator (`s.federationAgg`) is whatever the existing `FederatedInvestigationGraph` invocation uses — copy that argument name verbatim.

- [ ] **Step 8: Run the full Go test suite**

```bash
go test ./...
```

Expected: PASS — every package green.

- [ ] **Step 9: Commit**

```bash
git add controlplane/api/investigation_structure.go controlplane/api/investigation_structure_test.go controlplane/server.go
git commit -m "feat(api): GET /api/investigations/{id}/structure with federation"
```

---

## Task 3: Frontend API client + types

A focused TypeScript-only change. No tests — types are validated by `tsc`; the helper itself is exercised by the component tests in Tasks 4 and 5.

**Files:**
- Modify: `web/src/api.ts`

- [ ] **Step 1: Find the existing `api.investigations` block**

Read `web/src/api.ts` around the `investigations:` object (currently around line 1289). The structure helper goes alongside `audit`, `report` etc.

- [ ] **Step 2: Add the new types**

Find `InvestigationDaimonItem` and `InvestigationOrchestrationItem` (search for those names — they're defined alongside the bundle types). Add `InvestigationHostItem` next to them, and extend `InvestigationOrchestrationItem`:

```ts
export interface InvestigationHostItem {
  host: string;
  count: number;
}

export interface InvestigationOrchestrationItem {
  // ...existing fields
  OrchestrationID:   { Valid: boolean; Int64: number };
  OrchestrationName: string;
  RunCount:          number;
  Completed:         number;  // NEW
  Failed:            number;  // NEW
  Cancelled:         number;  // NEW
  Running:           number;  // NEW
  LastStartedAt:     string;
}

export interface InvestigationStructure {
  hosts:          InvestigationHostItem[];
  iocs:           InvestigationIOCItem[];
  daimons:        InvestigationDaimonItem[];
  orchestrations: InvestigationOrchestrationItem[];
}
```

The exact field-casing of existing types is mixed (`OrchestrationID`, `RunCount` use PascalCase from the Go struct; the new endpoint's wire JSON tags use snake_case). When you read the existing `InvestigationOrchestrationItem` declaration, mirror its casing for the new fields. The wire JSON from the new handler will be the snake_case form (`run_count`, `completed`, `failed`, `cancelled`, `running`); the TypeScript interface should match the wire shape. If the existing interface uses PascalCase but the JSON is snake_case, that's a pre-existing inconsistency — match the existing convention for backward compat with bundle consumers and use a separate `InvestigationOrchestrationStructureItem` shape if needed.

**To resolve concretely:** read `web/src/api.ts` around the `InvestigationOrchestrationItem` definition. If the existing type uses PascalCase, the JSON wire shape must also be PascalCase (the existing bundle uses Go's default JSON encoding without `json:"..."` tags, which gives PascalCase). In that case the new endpoint's response also uses PascalCase, and the new fields slot in. Add `Completed`, `Failed`, `Cancelled`, `Running` to the existing interface.

- [ ] **Step 3: Add the API helper**

Inside the `investigations:` object in `api`, add:

```ts
    structure: (id: number, cpInstanceID?: string) => {
      const qs = cpInstanceID ? `?cp=${encodeURIComponent(cpInstanceID)}` : '';
      return request<InvestigationStructure>(`/api/investigations/${id}/structure${qs}`);
    },
```

Place it next to the existing `audit:` helper for symmetry.

- [ ] **Step 4: Verify typecheck**

```bash
cd web && npx tsc --noEmit
```

Expected: PASS — no type errors.

- [ ] **Step 5: Commit**

```bash
git add web/src/api.ts
git commit -m "feat(api): InvestigationStructure types + structure() helper"
```

---

## Task 4: Extract aggregators to `caseStructure/derive.ts`

Pure no-behavior-change refactor. Move the four existing aggregators out of `CaseStructure.tsx` into a sibling module, with unit tests. The component itself keeps working unchanged after the imports are repointed.

**Files:**
- Create: `web/src/components/investigations/caseStructure/derive.ts`
- Create: `web/src/components/investigations/caseStructure/derive.test.ts`
- Modify: `web/src/components/investigations/CaseStructure.tsx`

- [ ] **Step 1: Create `derive.ts` with the four aggregators**

Create `web/src/components/investigations/caseStructure/derive.ts`:

```ts
// Pure aggregators that derive structure-card content from the
// InvestigationDetail bundle. These are the fallback path for
// CaseStructure when the /structure endpoint is unavailable
// (federated child running an older binary, transient network
// failure). They produce the same wire shape as the server endpoint
// so the component renders identically from either source.

import type {
  InvestigationDetail,
  InvestigationStructure,
  InvestigationHostItem,
  InvestigationIOCItem,
  InvestigationDaimonItem,
  InvestigationOrchestrationItem,
} from '../../../api';

export function aggregateHosts(b: InvestigationDetail): InvestigationHostItem[] {
  const counts = new Map<string, number>();
  for (const f of b.findings) {
    if (!f.Host.Valid || !f.Host.String) continue;
    counts.set(f.Host.String, (counts.get(f.Host.String) ?? 0) + 1);
  }
  return Array.from(counts, ([host, count]) => ({ host, count }))
    .sort((a, b) => {
      if (a.count !== b.count) return b.count - a.count;
      return a.host.localeCompare(b.host);
    });
}

export function topIOCs(b: InvestigationDetail): InvestigationIOCItem[] {
  return [...b.iocs].sort((a, b) => b.ObservationCount - a.ObservationCount);
}

export function topDaimons(b: InvestigationDetail): InvestigationDaimonItem[] {
  return [...b.daimons].sort((a, b) => {
    if (a.FindingCount !== b.FindingCount) return b.FindingCount - a.FindingCount;
    return b.LastSeenTs - a.LastSeenTs;
  });
}

export function deriveOrchestrations(b: InvestigationDetail): InvestigationOrchestrationItem[] {
  // Group runs by orchestration ID; count statuses; emit the same
  // shape as the server-side response.
  const byID = new Map<number, InvestigationOrchestrationItem>();
  for (const r of b.runs) {
    const id = r.OrchestrationID;
    let row = byID.get(id);
    if (!row) {
      row = {
        OrchestrationID:   { Valid: true, Int64: id },
        OrchestrationName: r.OrchestrationName.Valid ? r.OrchestrationName.String : `orchestration #${id}`,
        RunCount:          0,
        Completed:         0,
        Failed:            0,
        Cancelled:         0,
        Running:           0,
        LastStartedAt:     '',
      };
      byID.set(id, row);
    }
    row.RunCount += 1;
    if      (r.Status === 'completed') row.Completed += 1;
    else if (r.Status === 'failed')    row.Failed += 1;
    else if (r.Status === 'cancelled') row.Cancelled += 1;
    else                                row.Running += 1;
    if (r.StartedAt > row.LastStartedAt) row.LastStartedAt = r.StartedAt;
  }
  return Array.from(byID.values()).sort((a, b) => b.RunCount - a.RunCount);
}

export function deriveFromBundle(b: InvestigationDetail): InvestigationStructure {
  return {
    hosts:          aggregateHosts(b),
    iocs:           topIOCs(b),
    daimons:        topDaimons(b),
    orchestrations: deriveOrchestrations(b),
  };
}
```

- [ ] **Step 2: Write the failing tests for the aggregators**

Create `web/src/components/investigations/caseStructure/derive.test.ts`:

```ts
import { describe, it, expect } from 'vitest';
import {
  aggregateHosts,
  topIOCs,
  topDaimons,
  deriveOrchestrations,
} from './derive';
import type { InvestigationDetail } from '../../../api';

function bundle(over: Partial<InvestigationDetail> = {}): InvestigationDetail {
  return {
    investigation: {
      ID: 1, Title: 't', Status: 'active', Resolution: '', Summary: '',
      CreatedBy: 'me', CreatedAt: '2026-04-30T10:00:00Z',
      ClosedAt: '0001-01-01T00:00:00Z', UpdatedAt: '2026-05-01T00:00:00Z',
    },
    findings: [], runs: [], iocs: [], daimons: [], orchestrations: [], notes: [],
    war_room: false,
    ...over,
  };
}

function f(host: string): InvestigationDetail['findings'][number] {
  return {
    ID: 1, Ts: 0, Severity: { Valid: true, String: 'HIGH' },
    Title: { Valid: true, String: 't' },
    Agent: { Valid: true, String: 'edr-agent' },
    Host:  { Valid: !!host, String: host },
  } as unknown as InvestigationDetail['findings'][number];
}

describe('aggregateHosts', () => {
  it('counts non-empty hosts and sorts desc by count, alpha tiebreak', () => {
    const out = aggregateHosts(bundle({
      findings: [f('a'), f('b'), f('a'), f('c'), f('a'), f('')],
    }));
    expect(out).toEqual([
      { host: 'a', count: 3 },
      { host: 'b', count: 1 },
      { host: 'c', count: 1 },
    ]);
  });

  it('returns empty array for empty bundle', () => {
    expect(aggregateHosts(bundle())).toEqual([]);
  });
});

describe('topIOCs', () => {
  it('sorts by ObservationCount desc', () => {
    const out = topIOCs(bundle({
      iocs: [
        { ID: 1, Kind: 'ip', Value: 'x', Severity: { Valid: false, String: '' }, ObservationCount: 2, HostCount: 1, FirstSeen: '', LastSeen: '' },
        { ID: 2, Kind: 'ip', Value: 'y', Severity: { Valid: false, String: '' }, ObservationCount: 5, HostCount: 1, FirstSeen: '', LastSeen: '' },
      ],
    }));
    expect(out.map((i) => i.ID)).toEqual([2, 1]);
  });
});

describe('topDaimons', () => {
  it('sorts by FindingCount desc, LastSeenTs tiebreak', () => {
    const out = topDaimons(bundle({
      daimons: [
        { Agent: 'a', FindingCount: 3, LastSeenTs: 100 },
        { Agent: 'b', FindingCount: 5, LastSeenTs: 200 },
        { Agent: 'c', FindingCount: 3, LastSeenTs: 300 },
      ],
    }));
    expect(out.map((d) => d.Agent)).toEqual(['b', 'c', 'a']);
  });
});

describe('deriveOrchestrations', () => {
  it('groups runs and counts each status with extras bucketed as Running', () => {
    const out = deriveOrchestrations(bundle({
      runs: [
        { ID: 1, OrchestrationID: 10, OrchestrationName: { Valid: true, String: 'auto-triage' }, Status: 'completed', StartedAt: '2026-04-01' },
        { ID: 2, OrchestrationID: 10, OrchestrationName: { Valid: true, String: 'auto-triage' }, Status: 'completed', StartedAt: '2026-04-02' },
        { ID: 3, OrchestrationID: 10, OrchestrationName: { Valid: true, String: 'auto-triage' }, Status: 'failed',    StartedAt: '2026-04-03' },
        { ID: 4, OrchestrationID: 10, OrchestrationName: { Valid: true, String: 'auto-triage' }, Status: 'cancelled', StartedAt: '2026-04-04' },
        { ID: 5, OrchestrationID: 10, OrchestrationName: { Valid: true, String: 'auto-triage' }, Status: 'pending',   StartedAt: '2026-04-05' },
      ] as unknown as InvestigationDetail['runs'],
    }));
    expect(out).toHaveLength(1);
    expect(out[0].RunCount).toBe(5);
    expect(out[0].Completed).toBe(2);
    expect(out[0].Failed).toBe(1);
    expect(out[0].Cancelled).toBe(1);
    expect(out[0].Running).toBe(1); // 'pending' bucketed as Running
    expect(out[0].LastStartedAt).toBe('2026-04-05');
  });
});
```

- [ ] **Step 3: Run the tests to verify they pass**

```bash
cd web && npx vitest run src/components/investigations/caseStructure/derive.test.ts
```

Expected: PASS — all four describe blocks green.

- [ ] **Step 4: Repoint `CaseStructure.tsx` imports to use the extracted helpers**

Edit `web/src/components/investigations/CaseStructure.tsx`. Add this import at the top (next to the existing imports):

```tsx
import { aggregateHosts, topIOCs, topDaimons, deriveOrchestrations } from './caseStructure/derive';
```

Delete the four local function definitions (`aggregateHosts`, `topIOCs`, `topDaimons`, `topOrchestrations`) at the bottom of the file (around lines 125-170 in the current source).

**One semantics-preserving rewire:** the existing component renders from helper return shapes that are slightly different from the new wire shape. Specifically:

- `aggregateHosts` (old) returned `{distinct, top, list}`. The new helper returns `InvestigationHostItem[]`. The component code that consumed `hosts.distinct`, `hosts.top`, `hosts.list.slice(1, 4)` needs to be rewritten against the array form. Update the JSX:

```tsx
const hostsList = useMemo(() => aggregateHosts(bundle), [bundle]);
// ...
title={`Hosts (${hostsList.length})`}
empty={hostsList.length === 0 ? 'no items linked yet' : null}
topLine={hostsList[0] ? `Most-hit: ${hostsList[0].host} — ${hostsList[0].count} finding${hostsList[0].count === 1 ? '' : 's'}` : null}
rows={hostsList.slice(1, 4).map((h) => ({ key: h.host, label: h.host, suffix: `${h.count}` }))}
```

- `topOrchestrations` (old) returned `{runCount, orchCount, top, list}`. The new helper returns `InvestigationOrchestrationItem[]`. Rewrite the consumer:

```tsx
const orchList = useMemo(() => deriveOrchestrations(bundle), [bundle]);
const totalRuns = useMemo(() => orchList.reduce((acc, o) => acc + o.RunCount, 0), [orchList]);
// ...
title={`Runs (${totalRuns} / ${orchList.length} orch${orchList.length === 1 ? '' : 's'})`}
empty={totalRuns === 0 ? 'no items linked yet' : null}
topLine={orchList[0] ? `Most-run: ${orchList[0].OrchestrationName} — ${orchList[0].RunCount} run${orchList[0].RunCount === 1 ? '' : 's'} (${orchList[0].Completed} ✓ ${orchList[0].Failed} ✗)` : null}
rows={orchList.slice(1, 4).map((o) => ({
  key: o.OrchestrationName,
  label: o.OrchestrationName,
  suffix: `${o.RunCount} run${o.RunCount === 1 ? '' : 's'}`,
}))}
```

- `topIOCs` and `topDaimons` already returned arrays — minimal changes. Just update the variable names and direct array access. The `iocs` and `daimons` consts come from `topIOCs(bundle)` / `topDaimons(bundle)` directly.

- [ ] **Step 5: Run the existing CaseStructure-driven tests**

If `CaseStructure.test.tsx` doesn't exist yet, skip this step. If it does (from a future merge), run:

```bash
cd web && npx vitest run src/components/investigations/CaseStructure.test.tsx
```

Either way, run the broader investigations test suite:

```bash
cd web && npx vitest run src/components/investigations/
```

Expected: PASS — extracted helpers behave identically to the previous in-component logic, so consumers should not regress.

- [ ] **Step 6: Run typecheck**

```bash
cd web && npx tsc --noEmit
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add web/src/components/investigations/caseStructure/derive.ts web/src/components/investigations/caseStructure/derive.test.ts web/src/components/investigations/CaseStructure.tsx
git commit -m "refactor(case-structure): extract aggregators to derive.ts"
```

---

## Task 5: Wire `/structure` fetch + fallback in `CaseStructure`

The component now fetches the new endpoint on mount, falls back to the local `deriveFromBundle` on failure. The component tests cover both code paths plus the cancellation race.

**Files:**
- Modify: `web/src/components/investigations/CaseStructure.tsx`
- Create: `web/src/components/investigations/CaseStructure.test.tsx`

- [ ] **Step 1: Write the failing component tests**

Create `web/src/components/investigations/CaseStructure.test.tsx`:

```typescript
import { describe, it, expect, afterEach, vi } from 'vitest';
import { render, screen, cleanup, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { CaseStructure } from './CaseStructure';
import { api, ApiError, type InvestigationDetail, type InvestigationStructure } from '../../api';

afterEach(cleanup);

vi.mock('../../api', async () => {
  const actual = await vi.importActual<typeof import('../../api')>('../../api');
  return {
    ...actual,
    api: {
      ...actual.api,
      investigations: {
        ...actual.api.investigations,
        structure: vi.fn(),
      },
    },
  };
});

function bundle(over: Partial<InvestigationDetail> = {}): InvestigationDetail {
  return {
    investigation: {
      ID: 1, Title: 't', Status: 'active', Resolution: '', Summary: '',
      CreatedBy: 'me', CreatedAt: '2026-04-30T10:00:00Z',
      ClosedAt: '0001-01-01T00:00:00Z', UpdatedAt: '2026-05-01T00:00:00Z',
    },
    findings: [], runs: [], iocs: [], daimons: [], orchestrations: [], notes: [],
    war_room: false,
    ...over,
  };
}

function structResp(over: Partial<InvestigationStructure> = {}): InvestigationStructure {
  return {
    hosts:          [],
    iocs:           [],
    daimons:        [],
    orchestrations: [],
    ...over,
  };
}

describe('CaseStructure (server-side path)', () => {
  it('renders host count from /structure response', async () => {
    (api.investigations.structure as ReturnType<typeof vi.fn>).mockResolvedValueOnce(
      structResp({ hosts: [{ host: 'edr-fedora-3', count: 7 }, { host: 'web-1', count: 2 }] }),
    );
    render(<MemoryRouter><CaseStructure bundle={bundle()} /></MemoryRouter>);
    await waitFor(() => {
      expect(screen.getByText(/Hosts \(2\)/)).toBeTruthy();
      expect(screen.getByText(/edr-fedora-3 — 7 findings/)).toBeTruthy();
    });
  });
});

describe('CaseStructure (fallback path)', () => {
  it('falls back to bundle-derived numbers on 404', async () => {
    (api.investigations.structure as ReturnType<typeof vi.fn>).mockRejectedValueOnce(
      new ApiError('not found', 404),
    );
    const b = bundle({
      findings: [
        { Host: { Valid: true, String: 'h-a' }, Agent: { Valid: true, String: 'a' }, Severity: { Valid: true, String: 'HIGH' }, ID: 1, Ts: 0, Title: { Valid: true, String: 't' } },
        { Host: { Valid: true, String: 'h-a' }, Agent: { Valid: true, String: 'a' }, Severity: { Valid: true, String: 'HIGH' }, ID: 2, Ts: 0, Title: { Valid: true, String: 't' } },
      ] as unknown as InvestigationDetail['findings'],
    });
    render(<MemoryRouter><CaseStructure bundle={b} /></MemoryRouter>);
    // Bundle-derived hosts: 1 distinct host with count 2.
    await waitFor(() => {
      expect(screen.getByText(/Hosts \(1\)/)).toBeTruthy();
      expect(screen.getByText(/h-a — 2 findings/)).toBeTruthy();
    });
  });

  it('falls back on network error', async () => {
    (api.investigations.structure as ReturnType<typeof vi.fn>).mockRejectedValueOnce(
      new Error('network down'),
    );
    render(<MemoryRouter><CaseStructure bundle={bundle()} /></MemoryRouter>);
    await waitFor(() => {
      expect(screen.getByText(/Hosts \(0\)/)).toBeTruthy();
    });
  });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
cd web && npx vitest run src/components/investigations/CaseStructure.test.tsx
```

Expected: FAIL — the component still uses `deriveFromBundle` directly, no `structure()` fetch yet.

- [ ] **Step 3: Wire fetch + fallback into `CaseStructure.tsx`**

Edit `web/src/components/investigations/CaseStructure.tsx`. Replace the imports block at the top:

```tsx
// CaseStructure: four-card grid summarising case shape across hosts /
// IOCs / daimons / runs+orchestrations. Fetches the pre-aggregated
// /structure endpoint on mount; falls back to client-side derivation
// over the bundle if the fetch fails (federated child running an
// older binary, transient network error). Click-throughs use
// react-router for tab navigation; row clicks fire entity:open.
import { useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { Bot, Hash, Server, Workflow } from 'lucide-react';
import type { InvestigationDetail, InvestigationStructure } from '../../api';
import { api } from '../../api';
import { deriveFromBundle } from './caseStructure/derive';
```

Replace the body of `CaseStructure`:

```tsx
export function CaseStructure({ bundle, cpInstanceID }: Props) {
  const [data, setData] = useState<InvestigationStructure | null>(null);
  const invID = bundle.investigation.ID;

  useEffect(() => {
    let cancelled = false;
    api.investigations.structure(invID, cpInstanceID)
      .then((r) => { if (!cancelled) setData(r); })
      .catch(() => {
        if (!cancelled) {
          setData(null);
          console.warn('case-structure fetch failed; using bundle-derived view', invID);
        }
      });
    return () => { cancelled = true; };
  }, [invID, cpInstanceID]);

  // data === null → either still loading OR fetch failed; either
  // way render from the bundle-derived view. Wire shape is identical
  // so the rest of the render is shared.
  const view: InvestigationStructure = useMemo(
    () => data ?? deriveFromBundle(bundle),
    [data, bundle],
  );

  const hostsList = view.hosts;
  const iocsList = view.iocs;
  const daimonsList = view.daimons;
  const orchList = view.orchestrations;
  const totalRuns = useMemo(() => orchList.reduce((acc, o) => acc + o.RunCount, 0), [orchList]);

  const cpQS = cpInstanceID ? `&cp=${encodeURIComponent(cpInstanceID)}` : '';
  return (
    <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-3">
      <Card
        icon={<Server size={14} className="text-emerald-600" />}
        title={`Hosts (${hostsList.length})`}
        empty={hostsList.length === 0 ? 'no items linked yet' : null}
        topLine={hostsList[0] ? `Most-hit: ${hostsList[0].host} — ${hostsList[0].count} finding${hostsList[0].count === 1 ? '' : 's'}` : null}
        rows={hostsList.slice(1, 4).map((h) => ({ key: h.host, label: h.host, suffix: `${h.count}` }))}
        tabHref={`/investigations/${invID}?tab=findings${cpQS}`}
      />
      <Card
        icon={<Hash size={14} className="text-purple-600" />}
        title={`IOCs (${iocsList.length})`}
        empty={iocsList.length === 0 ? 'no items linked yet' : null}
        topLine={iocsList[0] ? `Most-observed: ${iocsList[0].Kind}:${shortVal(iocsList[0].Value)} — ${iocsList[0].ObservationCount} obs across ${iocsList[0].HostCount} host${iocsList[0].HostCount === 1 ? '' : 's'}` : null}
        rows={iocsList.slice(1, 4).map((i) => ({
          key: `${i.Kind}:${i.Value}`,
          label: `${i.Kind}:${shortVal(i.Value)}`,
          suffix: `${i.ObservationCount} obs`,
          onClick: () => window.dispatchEvent(new CustomEvent('entity:open', { detail: { kind: 'ioc', identityKey: `${i.Kind}:${i.Value}`, cpInstanceID } })),
        }))}
        tabHref={`/investigations/${invID}?tab=iocs${cpQS}`}
      />
      <Card
        icon={<Bot size={14} className="text-indigo-600" />}
        title={`Daimons (${daimonsList.length})`}
        empty={daimonsList.length === 0 ? 'no items linked yet' : null}
        topLine={daimonsList[0] ? `Top emitter: ${daimonsList[0].Agent} — ${daimonsList[0].FindingCount} finding${daimonsList[0].FindingCount === 1 ? '' : 's'}, last seen ${relTime(daimonsList[0].LastSeenTs)}` : null}
        rows={daimonsList.slice(1, 4).map((d) => ({ key: d.Agent, label: d.Agent, suffix: `${d.FindingCount}` }))}
        tabHref={`/investigations/${invID}?tab=daimons${cpQS}`}
      />
      <Card
        icon={<Workflow size={14} className="text-cyan-600" />}
        title={`Runs (${totalRuns} / ${orchList.length} orch${orchList.length === 1 ? '' : 's'})`}
        empty={totalRuns === 0 ? 'no items linked yet' : null}
        topLine={orchList[0] ? `Most-run: ${orchList[0].OrchestrationName} — ${orchList[0].RunCount} run${orchList[0].RunCount === 1 ? '' : 's'} (${orchList[0].Completed} ✓ ${orchList[0].Failed} ✗)` : null}
        rows={orchList.slice(1, 4).map((o) => ({
          key: o.OrchestrationName,
          label: o.OrchestrationName,
          suffix: `${o.RunCount} run${o.RunCount === 1 ? '' : 's'}`,
        }))}
        tabHref={`/investigations/${invID}?tab=runs${cpQS}`}
      />
    </div>
  );
}
```

(The `Card` component, `shortVal`, `relTime` helpers stay at the bottom of the file unchanged.)

- [ ] **Step 4: Run the component tests**

```bash
cd web && npx vitest run src/components/investigations/CaseStructure.test.tsx
```

Expected: PASS — all three tests green.

- [ ] **Step 5: Run the full investigations test suite**

```bash
cd web && npx vitest run src/components/investigations/
```

Expected: PASS — derive tests, CaseStructure tests, plus any pre-existing tests in the directory.

- [ ] **Step 6: Run typecheck**

```bash
cd web && npx tsc --noEmit
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add web/src/components/investigations/CaseStructure.tsx web/src/components/investigations/CaseStructure.test.tsx
git commit -m "feat(case-structure): fetch /structure with bundle-derived fallback"
```

---

## Task 6: Architecture docs + final sweep + PR

- [ ] **Step 1: Update `docs/architecture.md`**

Find the section "Investigation Overview — timeline filter & saved searches" (added by PR #123, around the `## Investigation Overview` cluster). Insert a new section *after* it:

```markdown
## Investigation Overview — case-structure aggregation endpoint

The Overview tab's structure cards (hosts / IOCs / daimons / runs ×
orchestrations) read from a dedicated endpoint
`GET /api/investigations/{id}/structure` rather than aggregating
client-side over the bundle. The endpoint returns four pre-sorted
lists in one JSON payload — host rollup (new), IOCs by observation
count, daimons by finding count, orchestrations by run count with
per-status breakdown (completed / failed / cancelled / running).

The handler
(`controlplane/api/investigation_structure.go`) is thin: existence
check, four `Store.List*ForInvestigation` calls, sort iocs / daimons
/ orchs in Go (the Store methods sort by recency for the IOCs / Runs
/ Daimons tabs; the structure cards want desc-by-primary-count), and
emit. Federation follows the existing graph pattern: parent proxies
via `?cp=<id>`; child is token-authed.

The client (`web/src/components/investigations/CaseStructure.tsx`)
fetches the endpoint on mount and renders from the response.
On any failure (404 from a federated child running an older binary,
transient network error) it falls back to client-side derivation
via `caseStructure/derive.ts` — the same four aggregators that
previously lived inline in the component, now extracted for
testability and reuse on the fallback path. Wire shape is identical
between server and fallback paths so the render is shared.
```

- [ ] **Step 2: Run the full Go suite**

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 3: Run the full vitest suite**

```bash
cd web && npx vitest run
```

Expected: PASS.

- [ ] **Step 4: Run the typechecker**

```bash
cd web && npx tsc --noEmit
```

Expected: PASS.

- [ ] **Step 5: Commit docs**

```bash
git add docs/architecture.md
git commit -m "docs(architecture): case-structure endpoint section"
```

- [ ] **Step 6: Push branch**

```bash
git push -u origin feat/case-structure-api
```

- [ ] **Step 7: Open PR**

```bash
gh pr create --title "feat(api): case-structure server-side aggregation endpoint" --body "$(cat <<'EOF'
## Summary
- Adds `GET /api/investigations/{id}/structure` returning four pre-aggregated, pre-sorted lists (hosts / IOCs / daimons / orchestrations with per-status breakdown).
- Adds `Store.ListHostsForInvestigation` (new) and extends `InvestigationOrchestrationItem` with `Completed/Failed/Cancelled/Running int` fields and the SQL behind them.
- Federation pair (`FederatedInvestigationStructure` + `FederationInvestigationStructure`) follows the existing graph endpoint pattern.
- `CaseStructure.tsx` switches to fetch-on-mount with a graceful fallback to bundle-derived aggregation when the endpoint is unavailable. The four existing aggregators move to `caseStructure/derive.ts` for testability and reuse on the fallback path.

Closes follow-up #2 from `investigation_overview_followups.md`.

Spec: `docs/superpowers/specs/2026-05-02-case-structure-api-design.md`
Plan: `docs/superpowers/plans/2026-05-02-case-structure-api.md`

## Test plan
- [ ] `go test ./...` — full Go suite green
- [ ] `cd web && npx vitest run` — full vitest suite green
- [ ] `cd web && npx tsc --noEmit` — typecheck clean
- [ ] Manual: open an active case with mixed-severity findings ≥ 5 hosts; DevTools → Network → confirm one `/structure` call; cards populate
- [ ] Manual: block `/structure` in DevTools; reload; confirm fallback path renders correct numbers + `console.warn`
- [ ] Manual on a federated case (`cp_source` set): one `/structure?cp=<id>` request; cards populate
- [ ] Manual on a federated child running an older binary without `/structure`: 404 in DevTools, `console.warn` logged, fallback renders correctly

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

---

## Self-review notes

**1. Spec coverage:** Every spec section has a task.
- Wire shape → Task 1 (DB types) + Task 2 (handler emits the JSON) + Task 3 (TS types).
- DB type changes → Task 1.
- Handler implementation → Task 2 (with sort in Go preserving bundle ordering).
- Federation pair → Task 2.
- Routes → Task 2 step 7.
- Client refactor (fetch + fallback) → Task 5.
- Aggregator extraction → Task 4.
- Tests (server, db, client) → Tasks 1, 2, 4, 5.
- Edge cases (empty case, federated 404, network error, race) → Task 5 component tests cover three of four; the empty-case path is exercised by Task 2 step 1.
- Out-of-scope items (caching, threshold, bundle extension) → preserved by not adding tasks for them.

**2. Placeholder scan:** No "TBD" / "TODO" / "add appropriate" lines. Every code step has a concrete code block. The one judgment-call note (Task 3 step 2 about PascalCase vs snake_case casing) explicitly tells the implementer how to resolve by reading existing source — not a placeholder.

**3. Type consistency:**
- `InvestigationHostItem` defined in Task 1 (Go), exposed in Task 3 (TS), consumed in Task 4 + Task 5.
- `InvestigationOrchestrationItem` extension consistent across Task 1 (Go struct), Task 3 (TS interface), Task 4 (`deriveOrchestrations`), Task 5 (consumed in render).
- `InvestigationStructure` defined in Task 3, consumed in Tasks 4 (`deriveFromBundle` return type) and 5 (component state + view).
- `applyTimelineFilter` not referenced — different feature, correctly kept separate.
- Helper names (`aggregateHosts`, `topIOCs`, `topDaimons`, `deriveOrchestrations`, `deriveFromBundle`) consistent across Task 4 (defined) and Task 5 (consumed).
