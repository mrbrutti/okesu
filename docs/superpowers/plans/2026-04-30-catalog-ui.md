# Catalog UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a read-only `/catalog` page that lets operators browse, filter, and inspect every IOC the system knows about — catalog-curated and observation-derived rows alike — with a drawer + full detail page (Overview / Observations / Relationships tabs).

**Architecture:** Two backend handlers (`GET /api/iocs/{id}` and `/{id}/observations`) plus a `source` + `q` filter extension on the existing `GET /api/iocs`. Frontend is three new files (list page, detail page, drawer) plus a sidebar entry and route registration. Mirrors the established `Investigations.tsx` shape, not the 1452-line `Findings.tsx`.

**Tech Stack:**
- Go (`controlplane/api/`, `controlplane/db/`) — chi router, modernc.org/sqlite + lib/pq
- React + TypeScript + Tailwind (`web/src/pages/`, `web/src/components/`, `web/src/lib/`)
- `react-router-dom` for routing; `lucide-react` for icons

---

## File Structure

**Backend modify:**
- `controlplane/db/iocs.go` — extend `IOCListFilter` (`Source`, `Query`); extend `IOCObservation` (`ObservedAt time.Time`); update `ListIOCs` and `ListIOCObservations` SQL/scan
- `controlplane/api/iocs.go` — extend `ListIOCs` handler with `?source=` / `?q=`
- `controlplane/server.go` — mount two new routes

**Backend create:**
- `controlplane/api/iocs_get.go` — `GetIOCHandler`
- `controlplane/api/iocs_observations.go` — `ListIOCObservationsHandler`
- `controlplane/api/iocs_get_test.go`, `controlplane/api/iocs_observations_test.go`, append to `controlplane/api/iocs_test.go` (or create) for the filter-extension tests

**Frontend modify:**
- `web/src/lib/sidebarNav.ts` — add `Catalog` leaf to Triage group
- `web/src/api.ts` — extend `iocs()` filter; add `ioc(id)`, `iocObservations(id)`, `iocRelationships(id)`
- `web/src/App.tsx` — register `/catalog` and `/catalog/:id`

**Frontend create:**
- `web/src/pages/Catalog.tsx` — list page (~300 lines target, mirroring Investigations.tsx shape)
- `web/src/pages/CatalogDetail.tsx` — three-tab detail page
- `web/src/components/CatalogDrawer.tsx` — drawer component

---

## Task Group A — Backend filter extensions

### Task A1: Extend IOCListFilter + ListIOCs SQL

**Files:**
- Modify: `controlplane/db/iocs.go`

- [ ] **Step A1.1: Extend the filter struct**

In `controlplane/db/iocs.go`, update `IOCListFilter` (currently around line 257) to add two new optional fields:

```go
type IOCListFilter struct {
	Kind      string
	FindingID int64
	Source    string // "catalog" | "observed" | "" (any)
	Query     string // matches value, name, or tags via LIKE %q% (case-insensitive)
	Limit     int
}
```

- [ ] **Step A1.2: Honor Source + Query in ListIOCs**

Update the `ListIOCs` function body to add the two new clauses to the existing builder. Find where the existing `if f.Kind != ""` block lives and add immediately after:

```go
	if f.Source != "" {
		clauses = append(clauses, "iocs.source = ?")
		args = append(args, f.Source)
	}
	if f.Query != "" {
		// Case-insensitive LIKE %q% across value, name, tags. SQLite's
		// LOWER on both sides is portable to postgres without rewrites.
		clauses = append(clauses, "(LOWER(iocs.value) LIKE ? OR LOWER(COALESCE(iocs.name,'')) LIKE ? OR LOWER(COALESCE(iocs.tags,'')) LIKE ?)")
		needle := "%" + strings.ToLower(f.Query) + "%"
		args = append(args, needle, needle, needle)
	}
```

- [ ] **Step A1.3: Test the new filters**

Find or create `controlplane/db/iocs_test.go`. Append:

```go
func TestListIOCs_FilterBySource(t *testing.T) {
	s := openTempStore(t)
	s.UpsertIOC(&IOCUpsert{Kind: "sha256", Value: "a", NormalizedValue: "a", Source: "catalog"})
	s.UpsertIOC(&IOCUpsert{Kind: "sha256", Value: "b", NormalizedValue: "b", Source: "observed"})

	rows, err := s.ListIOCs(IOCListFilter{Source: "catalog"})
	if err != nil {
		t.Fatalf("ListIOCs: %v", err)
	}
	if len(rows) != 1 || rows[0].NormalizedValue != "a" {
		t.Errorf("expected only catalog row 'a'; got %+v", rows)
	}
}

func TestListIOCs_FilterByQuery(t *testing.T) {
	s := openTempStore(t)
	s.UpsertIOC(&IOCUpsert{Kind: "sha256", Value: "AbCdEf", NormalizedValue: "abcdef", Source: "catalog", Name: "WannaCry sample"})
	s.UpsertIOC(&IOCUpsert{Kind: "sha256", Value: "abc999", NormalizedValue: "abc999", Source: "catalog", Tags: "ransomware,emotet"})
	s.UpsertIOC(&IOCUpsert{Kind: "ipv4", Value: "1.2.3.4", NormalizedValue: "1.2.3.4", Source: "observed"})

	// Match by value (case-insensitive)
	rows, _ := s.ListIOCs(IOCListFilter{Query: "ABCD"})
	if len(rows) != 1 || rows[0].NormalizedValue != "abcdef" {
		t.Errorf("query=ABCD expected one match (abcdef); got %+v", rows)
	}
	// Match by name
	rows, _ = s.ListIOCs(IOCListFilter{Query: "wannacry"})
	if len(rows) != 1 || rows[0].Name != "WannaCry sample" {
		t.Errorf("query=wannacry expected one match by name; got %+v", rows)
	}
	// Match by tag
	rows, _ = s.ListIOCs(IOCListFilter{Query: "emotet"})
	if len(rows) != 1 || rows[0].NormalizedValue != "abc999" {
		t.Errorf("query=emotet expected one match by tag; got %+v", rows)
	}
}
```

- [ ] **Step A1.4: Run + commit**

```bash
go test ./controlplane/db/ -run TestListIOCs -v -count=1
```

All filter tests must pass. Then:

```bash
git add controlplane/db/iocs.go controlplane/db/iocs_test.go
git commit -m "db(iocs): source + q filters on IOCListFilter"
```

---

### Task A2: Wire source + q through the HTTP handler

**Files:**
- Modify: `controlplane/api/iocs.go`

- [ ] **Step A2.1: Update the handler**

`controlplane/api/iocs.go` currently reads only `kind` and `finding_id`. Update to also read `source` and `q`:

```go
func ListIOCs(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		filter := db.IOCListFilter{
			Kind:   q.Get("kind"),
			Source: q.Get("source"),
			Query:  q.Get("q"),
			Limit:  100,
		}
		if v := q.Get("finding_id"); v != "" {
			id, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				http.Error(w, "bad finding_id", http.StatusBadRequest)
				return
			}
			filter.FindingID = id
		}
		out, err := store.ListIOCs(filter)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}
```

Update the function-level comment to mention the new filters:

```go
// ListIOCs returns IOCs filtered by:
//   - kind         (sha256|ipv4|domain|yara_rule|sigma_rule|...)
//   - source       (catalog|observed)
//   - q            (LIKE %q% against value, name, or tags; case-insensitive)
//   - finding_id   (joins ioc_observations to filter to a single finding)
//
// With no filter, returns the most recent 100 by last_seen.
```

- [ ] **Step A2.2: Test the HTTP filters**

Append to `controlplane/api/iocs_test.go` (create if absent — search for `func TestListIOCs` first to see if any exists):

```go
package api

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/section9labs/okesu/controlplane/db"
)

func TestListIOCs_SourceQueryParam(t *testing.T) {
	st := newTestStore(t)
	st.UpsertIOC(&db.IOCUpsert{Kind: "sha256", Value: "a", NormalizedValue: "a", Source: "catalog"})
	st.UpsertIOC(&db.IOCUpsert{Kind: "sha256", Value: "b", NormalizedValue: "b", Source: "observed"})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/iocs?source=catalog", nil)
	ListIOCs(st)(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var rows []db.IOCRecord
	if err := json.NewDecoder(rec.Body).Decode(&rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 1 || rows[0].NormalizedValue != "a" {
		t.Errorf("?source=catalog expected one match; got %+v", rows)
	}
}

func TestListIOCs_QueryParam(t *testing.T) {
	st := newTestStore(t)
	st.UpsertIOC(&db.IOCUpsert{Kind: "sha256", Value: "abc", NormalizedValue: "abc", Source: "catalog", Tags: "ransomware"})
	st.UpsertIOC(&db.IOCUpsert{Kind: "sha256", Value: "xyz", NormalizedValue: "xyz", Source: "catalog"})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/iocs?q=RANSOM", nil)
	ListIOCs(st)(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var rows []db.IOCRecord
	json.NewDecoder(rec.Body).Decode(&rows)
	if len(rows) != 1 || rows[0].NormalizedValue != "abc" {
		t.Errorf("?q=RANSOM expected one match by tag; got %+v", rows)
	}
}
```

If `iocs_test.go` doesn't exist, the imports above are complete. If it does exist, just append the two functions and ensure the imports include `encoding/json` and `net/http/httptest`.

- [ ] **Step A2.3: Run + commit**

```bash
go test ./controlplane/api/ -run TestListIOCs -v -count=1
```

Then:

```bash
git add controlplane/api/iocs.go controlplane/api/iocs_test.go
git commit -m "api(iocs): wire source + q filters through ListIOCs handler"
```

---

## Task Group B — GET /api/iocs/{id}

### Task B1: GetIOCHandler

**Files:**
- Create: `controlplane/api/iocs_get.go`
- Create: `controlplane/api/iocs_get_test.go`
- Modify: `controlplane/server.go`

- [ ] **Step B1.1: Test first**

`controlplane/api/iocs_get_test.go`:

```go
package api

import (
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/section9labs/okesu/controlplane/db"
)

func TestGetIOC_Found(t *testing.T) {
	st := newTestStore(t)
	id, _, err := st.UpsertIOC(&db.IOCUpsert{
		Kind: "sha256", Value: "abc", NormalizedValue: "abc",
		Source: "catalog", Name: "test",
	})
	if err != nil {
		t.Fatalf("UpsertIOC: %v", err)
	}

	r := chi.NewRouter()
	r.Get("/api/iocs/{id}", GetIOCHandler(st))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/iocs/"+strconv.FormatInt(id, 10), nil)
	r.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got db.IOCRecord
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != id || got.Name != "test" {
		t.Errorf("got %+v", got)
	}
}

func TestGetIOC_NotFound(t *testing.T) {
	st := newTestStore(t)
	r := chi.NewRouter()
	r.Get("/api/iocs/{id}", GetIOCHandler(st))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/iocs/9999", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestGetIOC_BadID(t *testing.T) {
	st := newTestStore(t)
	r := chi.NewRouter()
	r.Get("/api/iocs/{id}", GetIOCHandler(st))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/iocs/not-a-number", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
```

- [ ] **Step B1.2: Run the test (must fail)**

```bash
go test ./controlplane/api/ -run TestGetIOC -v -count=1
```

Expected: FAIL with `undefined: GetIOCHandler`.

- [ ] **Step B1.3: Implement the handler**

`controlplane/api/iocs_get.go`:

```go
package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/section9labs/okesu/controlplane/db"
)

// GetIOCHandler returns a single IOC row by id. Used by the Catalog
// drawer + detail page when navigated-to directly (deep link).
func GetIOCHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := chi.URLParam(r, "id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			http.Error(w, "bad ioc id", http.StatusBadRequest)
			return
		}
		ioc, err := store.GetIOC(id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ioc)
	}
}
```

- [ ] **Step B1.4: Run the test (must pass)**

```bash
go test ./controlplane/api/ -run TestGetIOC -v -count=1
```

All three subtests PASS.

- [ ] **Step B1.5: Mount the route**

In `controlplane/server.go`, find the cookie-auth viewer+ group containing the existing `r.Get("/api/iocs", ...)` and `r.Get("/api/iocs/{id}/relationships", ...)` lines. Add the new route:

```go
		r.Get("/api/iocs/{id}", api.GetIOCHandler(s.store))
```

Place it BEFORE the `/api/iocs/{id}/relationships` line so the more-general `{id}` route appears first in the source — chi handles ordering correctly regardless, but matching the source order to the URL hierarchy is the convention.

- [ ] **Step B1.6: Commit**

```bash
go vet ./controlplane/api/
git add controlplane/api/iocs_get.go controlplane/api/iocs_get_test.go controlplane/server.go
git commit -m "api(iocs): GET /api/iocs/{id} — single record fetch"
```

---

## Task Group C — GET /api/iocs/{id}/observations

### Task C1: Extend IOCObservation with ObservedAt

**Files:**
- Modify: `controlplane/db/iocs.go`

- [ ] **Step C1.1: Add ObservedAt to the struct**

In `controlplane/db/iocs.go`, find `type IOCObservation struct` (around line 47) and add an `ObservedAt time.Time` field:

```go
type IOCObservation struct {
	IOCID              int64
	FindingID          int64 // 0 if not linked to a finding
	OrchestrationRunID int64 // 0 if not linked to a run
	Host               string
	ObservedAt         time.Time
}
```

- [ ] **Step C1.2: Update ListIOCObservations SQL + scan**

Find `func (s *Store) ListIOCObservations` and update the SELECT to include `observed_at`, plus the scan call:

```go
func (s *Store) ListIOCObservations(iocID int64) ([]IOCObservation, error) {
	rows, err := s.Query(`
		SELECT ioc_id, COALESCE(finding_id,0), COALESCE(orchestration_run_id,0), COALESCE(host,''), observed_at
		FROM ioc_observations WHERE ioc_id = ? ORDER BY observed_at DESC`, iocID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IOCObservation
	for rows.Next() {
		var o IOCObservation
		var observedAtRaw any
		if err := rows.Scan(&o.IOCID, &o.FindingID, &o.OrchestrationRunID, &o.Host, &observedAtRaw); err != nil {
			return nil, err
		}
		o.ObservedAt = ParseTimestamp(observedAtRaw)
		out = append(out, o)
	}
	return out, rows.Err()
}
```

`ParseTimestamp` already exists in `controlplane/db/timestamps.go` (Phase 22.3). Match other call sites (e.g., findings) that use it.

- [ ] **Step C1.3: Existing tests continue to pass**

```bash
go test ./controlplane/db/ -count=1
```

The existing `TestRecordObservation_CreatesLink` test in `iocs_test.go` doesn't assert on ObservedAt; it'll keep passing. If for some reason that test does scan ObservedAt and fails, update its assertion to allow `time.Since(o.ObservedAt) < 5*time.Second`.

- [ ] **Step C1.4: Commit**

```bash
git add controlplane/db/iocs.go
git commit -m "db(iocs): IOCObservation.ObservedAt — surface observed_at to callers"
```

---

### Task C2: ListIOCObservationsHandler

**Files:**
- Create: `controlplane/api/iocs_observations.go`
- Create: `controlplane/api/iocs_observations_test.go`
- Modify: `controlplane/server.go`

- [ ] **Step C2.1: Test first**

`controlplane/api/iocs_observations_test.go`:

```go
package api

import (
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/section9labs/okesu/controlplane/db"
)

func TestListIOCObservations_HTTP(t *testing.T) {
	st := newTestStore(t)
	iocID, _, _ := st.UpsertIOC(&db.IOCUpsert{
		Kind: "sha256", Value: "abc", NormalizedValue: "abc",
	})
	// Seed a finding so the FK on ioc_observations.finding_id is satisfied.
	eventID, err := st.InsertEvent(&db.Event{Ts: 0, Type: "finding", RawJSON: "{}"})
	if err != nil {
		t.Fatalf("InsertEvent: %v", err)
	}
	findingID, err := st.InsertFinding(&db.FindingInsert{
		EventID: eventID, Ts: 0, Agent: "a", Host: "host-1",
		Severity: "INFO", Title: "t", RawJSON: "{}",
	})
	if err != nil {
		t.Fatalf("InsertFinding: %v", err)
	}
	if err := st.RecordIOCObservation(iocID, &db.IOCObservation{FindingID: findingID, Host: "host-1"}); err != nil {
		t.Fatalf("RecordIOCObservation: %v", err)
	}

	r := chi.NewRouter()
	r.Get("/api/iocs/{id}/observations", ListIOCObservationsHandler(st))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/iocs/"+strconv.FormatInt(iocID, 10)+"/observations", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got []db.IOCObservation
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 observation; got %d", len(got))
	}
	if got[0].FindingID != findingID || got[0].Host != "host-1" {
		t.Errorf("observation shape wrong: %+v", got[0])
	}
	if got[0].ObservedAt.IsZero() {
		t.Errorf("ObservedAt should not be zero")
	}
}

func TestListIOCObservations_BadID(t *testing.T) {
	st := newTestStore(t)
	r := chi.NewRouter()
	r.Get("/api/iocs/{id}/observations", ListIOCObservationsHandler(st))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/iocs/not-a-number/observations", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
```

- [ ] **Step C2.2: Implement the handler**

`controlplane/api/iocs_observations.go`:

```go
package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/section9labs/okesu/controlplane/db"
)

// ListIOCObservationsHandler returns the observation history for a
// given IOC: every (finding_id, run_id, host, observed_at) row in
// ioc_observations linked to this IOC. Used by the Catalog detail
// page's Observations tab.
func ListIOCObservationsHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := chi.URLParam(r, "id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			http.Error(w, "bad ioc id", http.StatusBadRequest)
			return
		}
		obs, err := store.ListIOCObservations(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(obs)
	}
}
```

- [ ] **Step C2.3: Mount the route**

In `controlplane/server.go`, add (next to the other `/api/iocs/{id}/...` routes):

```go
		r.Get("/api/iocs/{id}/observations", api.ListIOCObservationsHandler(s.store))
```

- [ ] **Step C2.4: Run + commit**

```bash
go test ./controlplane/api/ -run TestListIOCObservations -v -count=1
go vet ./controlplane/api/
git add controlplane/api/iocs_observations.go controlplane/api/iocs_observations_test.go controlplane/server.go
git commit -m "api(iocs): GET /api/iocs/{id}/observations — observation history"
```

---

## Task Group D — Frontend api.ts extensions

### Task D1: Extend iocs() filter; add ioc, iocObservations, iocRelationships

**Files:**
- Modify: `web/src/api.ts`

- [ ] **Step D1.1: Find the existing iocs() helper**

Search for `iocs:` in `web/src/api.ts` (currently around line 609). The current shape is:

```ts
iocs: (filter: { findingID?: number; kind?: string } = {}) => { ... }
```

- [ ] **Step D1.2: Extend the filter shape and add the new helpers**

Replace the existing `iocs:` block with:

```ts
  // IOCs (Phase 22.1+; Catalog UI extends with source/q + per-id endpoints).
  // Without filters returns the most recent 100 by last_seen.
  iocs: (filter: { findingID?: number; kind?: string; source?: string; q?: string } = {}) => {
    const p = new URLSearchParams();
    if (filter.findingID) p.set('finding_id', String(filter.findingID));
    if (filter.kind)      p.set('kind', filter.kind);
    if (filter.source)    p.set('source', filter.source);
    if (filter.q)         p.set('q', filter.q);
    const qs = p.toString();
    return request<IOCRecord[]>(`/api/iocs${qs ? '?' + qs : ''}`);
  },

  ioc: (id: number) =>
    request<IOCRecord>(`/api/iocs/${id}`),

  iocObservations: (id: number) =>
    request<IOCObservation[]>(`/api/iocs/${id}/observations`),

  iocRelationships: (id: number) =>
    request<IOCRelationship[]>(`/api/iocs/${id}/relationships`),
```

- [ ] **Step D1.3: Add the IOCObservation + IOCRelationship types**

Find where `IOCRecord` is declared in `web/src/api.ts` (search for `export.*IOCRecord` or `IOCRecord` definition). After it, add (or modify if either already exists):

```ts
export type IOCObservation = {
  IOCID: number;
  FindingID: number;            // 0 if not linked to a finding
  OrchestrationRunID: number;   // 0 if not linked to a run
  Host: string;
  ObservedAt: string;           // RFC3339
};

export type IOCRelationship = {
  ID: number;
  SubjectID: number;
  Predicate: string;
  ObjectID: number;
  Source: string;
  Confidence?: string;
  Attributes?: Record<string, unknown>;
};
```

The Go side returns capital-letter field names because the structs don't tag with `json:""`. Verify by reading `controlplane/db/iocs.go` and `controlplane/db/ioc_relationships.go` — if the structs DO have `json:""` tags that lowercase the names, update the TypeScript types accordingly.

- [ ] **Step D1.4: Build to verify the types**

```bash
cd web && bun run build
```

Should pass. If it fails on missing types in some other file, that means a downstream consumer references a field name we're now exposing — track it down before commit.

- [ ] **Step D1.5: Commit**

```bash
cd ..
git add web/src/api.ts
git commit -m "web(api): extend iocs() filter; add ioc, iocObservations, iocRelationships"
```

---

## Task Group E — Sidebar + route + Catalog list page

### Task E1: Sidebar + route registration

**Files:**
- Modify: `web/src/lib/sidebarNav.ts`
- Modify: `web/src/App.tsx`

- [ ] **Step E1.1: Add the Catalog leaf to the Triage group**

In `web/src/lib/sidebarNav.ts`, find the `'triage'` group and add a `Catalog` entry after `Live Events`:

```ts
import {
  Activity, AlertTriangle, BookOpen, ClipboardList,
  LayoutDashboard, Library, Network, Server, Layers, Settings,
  Sparkles, Workflow,
} from 'lucide-react';
```

(Add `Library` to the import.)

```ts
  { kind: 'group', group: {
    id: 'triage', label: 'Triage', items: [
      { to: '/findings',       label: 'Findings',       icon: AlertTriangle, enabled: true },
      { to: '/investigations', label: 'Investigations', icon: ClipboardList, enabled: true },
      { to: '/events',         label: 'Live Events',    icon: Activity,      enabled: true },
      { to: '/catalog',        label: 'Catalog',        icon: Library,       enabled: true },
    ],
  }},
```

- [ ] **Step E1.2: Register routes in App.tsx**

In `web/src/App.tsx`, find the `<Route path="/findings" ...>` block. Add (after `/investigations/:id` so Catalog routes group with the other Triage targets):

```tsx
            <Route path="/catalog" element={<CatalogPage />} />
            <Route path="/catalog/:id" element={<CatalogDetailPage />} />
```

Add the imports at the top of the file:

```tsx
import CatalogPage from './pages/Catalog';
import CatalogDetailPage from './pages/CatalogDetail';
```

(Match the import style used for `InvestigationsPage`/`InvestigationDetailPage`.)

The components don't exist yet — the build will fail until E2 lands. That's fine (we're scaffolding the wiring first).

- [ ] **Step E1.3: Commit (sidebar only — defer App.tsx until pages exist)**

Actually this is awkward — App.tsx imports won't compile without the page files. Reorder: scaffold the page stubs in E2 first, then return to wire App.tsx.

```bash
git add web/src/lib/sidebarNav.ts
git commit -m "web(sidebar): Catalog leaf in Triage group"
```

(App.tsx changes will be committed together with the page scaffolds in E2.)

---

### Task E2: Catalog.tsx list page

**Files:**
- Create: `web/src/pages/Catalog.tsx`
- Create: `web/src/pages/CatalogDetail.tsx` (stub)
- Modify: `web/src/App.tsx`

- [ ] **Step E2.1: Read Investigations.tsx as a reference shape**

```bash
cat web/src/pages/Investigations.tsx | head -50
```

Match the page layout: outer wrapper, header with title + description, filter bar, table.

- [ ] **Step E2.2: Create the list page**

`web/src/pages/Catalog.tsx`:

```tsx
import { useEffect, useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Library } from 'lucide-react';

import { api, type IOCRecord } from '../api';
import CatalogDrawer from '../components/CatalogDrawer';

// Fixed list of kinds operators can filter by. Mirrors validKinds from
// controlplane/ioc/catalog/catalog.go — kept in sync by hand for now.
const ALL_KINDS = ['sha256', 'sha1', 'md5', 'ipv4', 'ipv6', 'domain', 'url', 'cve', 'mitre', 'yara_rule', 'sigma_rule'];

export default function CatalogPage() {
  const navigate = useNavigate();
  const [rows, setRows] = useState<IOCRecord[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [kindFilter, setKindFilter] = useState<Set<string>>(new Set(ALL_KINDS));
  const [sourceFilter, setSourceFilter] = useState<'' | 'catalog' | 'observed'>('');
  const [query, setQuery] = useState('');
  const [drawerIOC, setDrawerIOC] = useState<IOCRecord | null>(null);

  // Server-side: only kind+source+q filters are pushed; client-side
  // we further narrow by the kind multi-select since the API only
  // accepts a single kind value.
  useEffect(() => {
    setError(null);
    api.iocs({ source: sourceFilter || undefined, q: query || undefined })
      .then(setRows)
      .catch(e => setError(String(e)));
  }, [sourceFilter, query]);

  const filtered = useMemo(() => {
    if (!rows) return null;
    return rows.filter(r => kindFilter.has(r.Kind));
  }, [rows, kindFilter]);

  return (
    <div className="p-6 space-y-4">
      <header className="flex items-center gap-3">
        <Library className="w-6 h-6 text-zinc-400" />
        <div>
          <h1 className="text-xl font-semibold text-zinc-100">Catalog</h1>
          <p className="text-sm text-zinc-400">Every IOC the system knows about — catalog-curated and observation-derived.</p>
        </div>
      </header>

      <div className="flex flex-wrap items-center gap-3">
        <div className="flex flex-wrap gap-1">
          {ALL_KINDS.map(k => {
            const active = kindFilter.has(k);
            return (
              <button
                key={k}
                onClick={() => {
                  const next = new Set(kindFilter);
                  if (active) next.delete(k); else next.add(k);
                  setKindFilter(next);
                }}
                className={
                  'px-2 py-0.5 text-xs rounded border ' +
                  (active
                    ? 'bg-zinc-700 text-zinc-100 border-zinc-600'
                    : 'bg-zinc-900 text-zinc-500 border-zinc-800')
                }
              >
                {k}
              </button>
            );
          })}
        </div>
        <select
          value={sourceFilter}
          onChange={e => setSourceFilter(e.target.value as '' | 'catalog' | 'observed')}
          className="text-sm bg-zinc-900 border border-zinc-800 rounded px-2 py-1 text-zinc-200"
        >
          <option value="">All sources</option>
          <option value="catalog">Catalog</option>
          <option value="observed">Observed</option>
        </select>
        <input
          type="search"
          placeholder="Search value, name, tags…"
          value={query}
          onChange={e => setQuery(e.target.value)}
          className="text-sm bg-zinc-900 border border-zinc-800 rounded px-2 py-1 text-zinc-200 flex-1 max-w-md"
        />
      </div>

      {error && <div className="text-red-400 text-sm">{error}</div>}

      {filtered === null ? (
        <div className="text-sm text-zinc-500">Loading…</div>
      ) : filtered.length === 0 ? (
        <div className="text-sm text-zinc-500">No IOCs match these filters.</div>
      ) : (
        <table className="w-full text-sm border-collapse">
          <thead className="text-zinc-400 border-b border-zinc-800">
            <tr>
              <th className="text-left py-2 pr-3">Kind</th>
              <th className="text-left py-2 pr-3">Value</th>
              <th className="text-left py-2 pr-3 hidden md:table-cell">Name</th>
              <th className="text-left py-2 pr-3 hidden md:table-cell">Tags</th>
              <th className="text-left py-2 pr-3">Severity</th>
              <th className="text-left py-2 pr-3">Source</th>
              <th className="text-right py-2 pr-3">Observed</th>
              <th className="text-left py-2 pr-3 hidden lg:table-cell">First seen</th>
              <th className="text-left py-2 pr-3 hidden lg:table-cell">Last seen</th>
            </tr>
          </thead>
          <tbody>
            {filtered.map(r => (
              <tr
                key={r.ID}
                onClick={() => setDrawerIOC(r)}
                className="border-b border-zinc-900 hover:bg-zinc-900/40 cursor-pointer"
              >
                <td className="py-2 pr-3 font-mono text-xs text-zinc-400">{r.Kind}</td>
                <td className="py-2 pr-3 font-mono text-xs text-zinc-200 truncate max-w-xs">{r.Value}</td>
                <td className="py-2 pr-3 hidden md:table-cell text-zinc-300">{r.Name || '—'}</td>
                <td className="py-2 pr-3 hidden md:table-cell">
                  <TagChips tags={r.Tags} />
                </td>
                <td className="py-2 pr-3 text-zinc-300">{r.SeverityFloor || '—'}</td>
                <td className="py-2 pr-3 text-zinc-300">{r.Source}</td>
                <td className="py-2 pr-3 text-right text-zinc-300">{r.ObservationCount}</td>
                <td className="py-2 pr-3 hidden lg:table-cell text-zinc-500 text-xs">{r.FirstSeen}</td>
                <td className="py-2 pr-3 hidden lg:table-cell text-zinc-500 text-xs">{r.LastSeen}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {drawerIOC && (
        <CatalogDrawer
          ioc={drawerIOC}
          onClose={() => setDrawerIOC(null)}
          onOpenFullPage={() => navigate(`/catalog/${drawerIOC.ID}`)}
        />
      )}
    </div>
  );
}

// TagChips renders the comma-separated tags column as up to 3 small
// chips with a "+N more" overflow indicator (non-interactive — click
// the row for the full list in the drawer).
function TagChips({ tags }: { tags: string }) {
  if (!tags) return <span className="text-zinc-600">—</span>;
  const parts = tags.split(',').map(t => t.trim()).filter(Boolean);
  const visible = parts.slice(0, 3);
  const overflow = parts.length - visible.length;
  return (
    <div className="flex flex-wrap gap-1">
      {visible.map(t => (
        <span key={t} className="px-1.5 py-0.5 text-xs rounded bg-zinc-800 text-zinc-300">{t}</span>
      ))}
      {overflow > 0 && (
        <span className="px-1.5 py-0.5 text-xs rounded bg-zinc-900 text-zinc-500 border border-zinc-800">+{overflow}</span>
      )}
    </div>
  );
}
```

If `IOCRecord` is not exported from `web/src/api.ts`, search for `IOCRecord` in api.ts and add `export` to its type declaration.

- [ ] **Step E2.3: Stub CatalogDetail and CatalogDrawer**

`web/src/pages/CatalogDetail.tsx` (full implementation comes in Task G):

```tsx
import { useParams, Link } from 'react-router-dom';

export default function CatalogDetailPage() {
  const { id } = useParams();
  return (
    <div className="p-6">
      <Link to="/catalog" className="text-sm text-zinc-400 hover:text-zinc-200">← Back to Catalog</Link>
      <h1 className="text-xl font-semibold text-zinc-100 mt-2">IOC #{id}</h1>
      <p className="text-sm text-zinc-500 mt-1">Detail page — coming in Task G.</p>
    </div>
  );
}
```

`web/src/components/CatalogDrawer.tsx` (full implementation comes in Task F):

```tsx
import type { IOCRecord } from '../api';

export default function CatalogDrawer({
  ioc,
  onClose,
  onOpenFullPage,
}: {
  ioc: IOCRecord;
  onClose: () => void;
  onOpenFullPage: () => void;
}) {
  return (
    <div className="fixed inset-y-0 right-0 w-96 bg-zinc-950 border-l border-zinc-800 p-4 overflow-y-auto">
      <div className="flex items-center justify-between mb-4">
        <span className="font-mono text-xs text-zinc-500">{ioc.Kind}</span>
        <button onClick={onClose} className="text-zinc-500 hover:text-zinc-200">✕</button>
      </div>
      <div className="text-zinc-200 font-mono text-sm break-all mb-4">{ioc.Value}</div>
      <p className="text-xs text-zinc-500">Drawer body — coming in Task F.</p>
      <button
        onClick={onOpenFullPage}
        className="mt-6 w-full text-sm bg-zinc-800 hover:bg-zinc-700 text-zinc-100 py-2 rounded"
      >
        Open full page →
      </button>
    </div>
  );
}
```

- [ ] **Step E2.4: Wire App.tsx**

Apply the route+import edits from Step E1.2 now (postponed earlier).

- [ ] **Step E2.5: Build + commit**

```bash
cd web && bun run build && cd ..
```

Must pass.

```bash
git add web/src/pages/Catalog.tsx web/src/pages/CatalogDetail.tsx \
        web/src/components/CatalogDrawer.tsx web/src/App.tsx
git commit -m "web(catalog): list page + drawer/detail stubs + route registration"
```

---

## Task Group F — CatalogDrawer (full)

### Task F1: Drawer body sections

**Files:**
- Modify: `web/src/components/CatalogDrawer.tsx`

- [ ] **Step F1.1: Replace stub with full drawer**

Overwrite `web/src/components/CatalogDrawer.tsx`:

```tsx
import { useEffect, useState } from 'react';
import { api, type IOCRecord, type IOCRelationship } from '../api';

export default function CatalogDrawer({
  ioc,
  onClose,
  onOpenFullPage,
}: {
  ioc: IOCRecord;
  onClose: () => void;
  onOpenFullPage: () => void;
}) {
  const [relCount, setRelCount] = useState<number | null>(null);

  // Fetch relationship count once. We don't have a HEAD endpoint; the
  // array length from the existing /relationships handler is fine for
  // v1 since edges per IOC tend to be small (<20 in practice).
  useEffect(() => {
    api.iocRelationships(ioc.ID).then(rels => setRelCount(rels.length)).catch(() => setRelCount(0));
  }, [ioc.ID]);

  return (
    <div className="fixed inset-y-0 right-0 w-96 bg-zinc-950 border-l border-zinc-800 p-4 overflow-y-auto">
      <div className="flex items-center justify-between mb-4">
        <span className="font-mono text-xs text-zinc-500 uppercase tracking-wider">{ioc.Kind}</span>
        <button onClick={onClose} className="text-zinc-500 hover:text-zinc-200" aria-label="Close drawer">✕</button>
      </div>

      <div className="text-zinc-100 font-mono text-sm break-all mb-1">{ioc.Name || ioc.Value}</div>
      {ioc.Name && <div className="text-zinc-500 font-mono text-xs break-all mb-3">{ioc.Value}</div>}

      <Section label="Identity">
        <KV k="Source" v={ioc.Source} />
        <KV k="Normalized" v={ioc.NormalizedValue} mono />
        {ioc.DefinitionPath && <KV k="Definition" v={ioc.DefinitionPath} mono />}
      </Section>

      {(ioc.Tags || ioc.SeverityFloor || ioc.Classification || ioc.Attribution || ioc.Confidence || ioc.Notes) && (
        <Section label="Curated metadata">
          {ioc.Tags && (
            <div className="flex flex-wrap gap-1 mb-2">
              {ioc.Tags.split(',').map(t => t.trim()).filter(Boolean).map(t => (
                <span key={t} className="px-1.5 py-0.5 text-xs rounded bg-zinc-800 text-zinc-300">{t}</span>
              ))}
            </div>
          )}
          {ioc.SeverityFloor && <KV k="Severity" v={ioc.SeverityFloor} />}
          {ioc.Classification && <KV k="Classification" v={ioc.Classification} />}
          {ioc.Attribution && <KV k="Attribution" v={ioc.Attribution} />}
          {ioc.Confidence && <KV k="Confidence" v={ioc.Confidence} />}
          {ioc.Notes && <div className="mt-2 text-xs text-zinc-400 whitespace-pre-wrap">{ioc.Notes}</div>}
        </Section>
      )}

      <Section label="Observability">
        <KV k="Count" v={String(ioc.ObservationCount)} />
        <KV k="First seen" v={ioc.FirstSeen} />
        <KV k="Last seen" v={ioc.LastSeen} />
      </Section>

      <div className="mt-6 space-y-2">
        <button
          onClick={onOpenFullPage}
          className="w-full text-sm bg-zinc-800 hover:bg-zinc-700 text-zinc-100 py-2 rounded"
        >
          Observations · Relationships{relCount !== null ? ` (${relCount})` : ''} →
        </button>
        <button
          onClick={onOpenFullPage}
          className="w-full text-xs text-zinc-500 hover:text-zinc-300"
        >
          Open full page
        </button>
      </div>
    </div>
  );
}

function Section({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="mb-4 pb-4 border-b border-zinc-900 last:border-b-0">
      <div className="text-xs uppercase tracking-wider text-zinc-500 mb-2">{label}</div>
      {children}
    </div>
  );
}

function KV({ k, v, mono }: { k: string; v: string; mono?: boolean }) {
  return (
    <div className="flex justify-between gap-3 text-xs mb-1">
      <span className="text-zinc-500">{k}</span>
      <span className={mono ? 'font-mono text-zinc-300 break-all text-right' : 'text-zinc-300 text-right'}>{v}</span>
    </div>
  );
}
```

- [ ] **Step F1.2: Build + commit**

```bash
cd web && bun run build && cd ..
git add web/src/components/CatalogDrawer.tsx
git commit -m "web(catalog): full drawer body — identity, metadata, observability"
```

---

## Task Group G — CatalogDetail (full)

### Task G1: Three-tab detail page

**Files:**
- Modify: `web/src/pages/CatalogDetail.tsx`

- [ ] **Step G1.1: Replace stub with three-tab page**

Overwrite `web/src/pages/CatalogDetail.tsx`:

```tsx
import { useEffect, useState } from 'react';
import { Link, useParams, useNavigate } from 'react-router-dom';

import { api, type IOCRecord, type IOCObservation, type IOCRelationship } from '../api';

type Tab = 'overview' | 'observations' | 'relationships';

export default function CatalogDetailPage() {
  const { id } = useParams();
  const navigate = useNavigate();
  const iocID = Number(id);
  const [ioc, setIOC] = useState<IOCRecord | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [tab, setTab] = useState<Tab>('overview');
  const [observations, setObservations] = useState<IOCObservation[] | null>(null);
  const [relationships, setRelationships] = useState<IOCRelationship[] | null>(null);

  useEffect(() => {
    if (!iocID) { setError('bad id'); return; }
    api.ioc(iocID).then(setIOC).catch(e => setError(String(e)));
  }, [iocID]);

  useEffect(() => {
    if (!iocID || tab !== 'observations' || observations !== null) return;
    api.iocObservations(iocID).then(setObservations).catch(() => setObservations([]));
  }, [iocID, tab, observations]);

  useEffect(() => {
    if (!iocID || tab !== 'relationships' || relationships !== null) return;
    api.iocRelationships(iocID).then(setRelationships).catch(() => setRelationships([]));
  }, [iocID, tab, relationships]);

  if (error) return <div className="p-6 text-red-400 text-sm">{error}</div>;
  if (!ioc) return <div className="p-6 text-zinc-500 text-sm">Loading…</div>;

  return (
    <div className="p-6 space-y-4">
      <Link to="/catalog" className="text-sm text-zinc-400 hover:text-zinc-200">← Back to Catalog</Link>

      <div>
        <div className="font-mono text-xs text-zinc-500 uppercase tracking-wider">{ioc.Kind}</div>
        <h1 className="text-xl font-semibold text-zinc-100 break-all">{ioc.Name || ioc.Value}</h1>
        {ioc.Name && <div className="text-zinc-500 font-mono text-sm break-all mt-1">{ioc.Value}</div>}
      </div>

      <div className="flex gap-1 border-b border-zinc-800">
        <TabBtn active={tab === 'overview'} onClick={() => setTab('overview')}>Overview</TabBtn>
        <TabBtn active={tab === 'observations'} onClick={() => setTab('observations')}>Observations</TabBtn>
        <TabBtn active={tab === 'relationships'} onClick={() => setTab('relationships')}>Relationships</TabBtn>
      </div>

      {tab === 'overview' && <OverviewTab ioc={ioc} />}
      {tab === 'observations' && <ObservationsTab rows={observations} />}
      {tab === 'relationships' && <RelationshipsTab rows={relationships} currentID={iocID} navigate={navigate} />}
    </div>
  );
}

function TabBtn({ active, onClick, children }: { active: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      onClick={onClick}
      className={
        'px-3 py-2 text-sm border-b-2 -mb-px ' +
        (active ? 'border-zinc-100 text-zinc-100' : 'border-transparent text-zinc-500 hover:text-zinc-300')
      }
    >
      {children}
    </button>
  );
}

function OverviewTab({ ioc }: { ioc: IOCRecord }) {
  const isRule = ioc.Kind === 'yara_rule' || ioc.Kind === 'sigma_rule';
  return (
    <div className="space-y-4">
      <Section label="Identity">
        <KV k="Source" v={ioc.Source} />
        <KV k="Normalized" v={ioc.NormalizedValue} mono />
        {ioc.DefinitionPath && <KV k="Definition" v={ioc.DefinitionPath} mono />}
      </Section>

      <Section label="Curated metadata">
        {ioc.Tags && (
          <div className="flex flex-wrap gap-1 mb-2">
            {ioc.Tags.split(',').map(t => t.trim()).filter(Boolean).map(t => (
              <span key={t} className="px-1.5 py-0.5 text-xs rounded bg-zinc-800 text-zinc-300">{t}</span>
            ))}
          </div>
        )}
        {ioc.SeverityFloor && <KV k="Severity" v={ioc.SeverityFloor} />}
        {ioc.Classification && <KV k="Classification" v={ioc.Classification} />}
        {ioc.Attribution && <KV k="Attribution" v={ioc.Attribution} />}
        {ioc.Confidence && <KV k="Confidence" v={ioc.Confidence} />}
        {ioc.Notes && <div className="mt-2 text-xs text-zinc-400 whitespace-pre-wrap">{ioc.Notes}</div>}
      </Section>

      <Section label="Observability">
        <KV k="Count" v={String(ioc.ObservationCount)} />
        <KV k="First seen" v={ioc.FirstSeen} />
        <KV k="Last seen" v={ioc.LastSeen} />
      </Section>

      {isRule && (
        <Section label="Rule body">
          <div className="max-h-[60vh] overflow-y-auto rounded bg-zinc-950 border border-zinc-800">
            <pre className="text-xs text-zinc-200 p-3 whitespace-pre-wrap break-all"><code>{ioc.Value}</code></pre>
          </div>
        </Section>
      )}
    </div>
  );
}

function ObservationsTab({ rows }: { rows: IOCObservation[] | null }) {
  if (rows === null) return <div className="text-sm text-zinc-500">Loading…</div>;
  if (rows.length === 0) return <div className="text-sm text-zinc-500">No observations recorded.</div>;
  return (
    <table className="w-full text-sm border-collapse">
      <thead className="text-zinc-400 border-b border-zinc-800">
        <tr>
          <th className="text-left py-2 pr-3">Finding</th>
          <th className="text-left py-2 pr-3">Run</th>
          <th className="text-left py-2 pr-3">Host</th>
          <th className="text-left py-2 pr-3">Observed at</th>
        </tr>
      </thead>
      <tbody>
        {rows.map((o, i) => (
          <tr key={i} className="border-b border-zinc-900">
            <td className="py-2 pr-3">
              {o.FindingID > 0
                ? <Link to={`/findings?id=${o.FindingID}`} className="text-zinc-200 hover:text-zinc-100">#{o.FindingID}</Link>
                : <span className="text-zinc-600">—</span>}
            </td>
            <td className="py-2 pr-3">
              {o.OrchestrationRunID > 0
                ? <Link to={`/agents?tab=runs&id=${o.OrchestrationRunID}`} className="text-zinc-200 hover:text-zinc-100">#{o.OrchestrationRunID}</Link>
                : <span className="text-zinc-600">—</span>}
            </td>
            <td className="py-2 pr-3 text-zinc-300 font-mono text-xs">{o.Host || '—'}</td>
            <td className="py-2 pr-3 text-zinc-500 text-xs">{o.ObservedAt}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function RelationshipsTab({ rows, currentID, navigate }: { rows: IOCRelationship[] | null; currentID: number; navigate: ReturnType<typeof useNavigate> }) {
  if (rows === null) return <div className="text-sm text-zinc-500">Loading…</div>;
  if (rows.length === 0) return <div className="text-sm text-zinc-500">No relationships recorded.</div>;
  return (
    <table className="w-full text-sm border-collapse">
      <thead className="text-zinc-400 border-b border-zinc-800">
        <tr>
          <th className="text-left py-2 pr-3">Direction</th>
          <th className="text-left py-2 pr-3">Subject</th>
          <th className="text-left py-2 pr-3">Predicate</th>
          <th className="text-left py-2 pr-3">Object</th>
          <th className="text-left py-2 pr-3">Source</th>
        </tr>
      </thead>
      <tbody>
        {rows.map(rel => {
          const outgoing = rel.SubjectID === currentID;
          const otherID = outgoing ? rel.ObjectID : rel.SubjectID;
          return (
            <tr key={rel.ID} className="border-b border-zinc-900 hover:bg-zinc-900/40 cursor-pointer" onClick={() => navigate(`/catalog/${otherID}`)}>
              <td className="py-2 pr-3 text-zinc-500 text-xs">{outgoing ? '→ out' : '← in'}</td>
              <td className="py-2 pr-3 text-zinc-300">#{rel.SubjectID}{rel.SubjectID === currentID && ' (this)'}</td>
              <td className="py-2 pr-3 font-mono text-xs text-zinc-200">{rel.Predicate}</td>
              <td className="py-2 pr-3 text-zinc-300">#{rel.ObjectID}{rel.ObjectID === currentID && ' (this)'}</td>
              <td className="py-2 pr-3 text-zinc-500 text-xs">{rel.Source}</td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}

function Section({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="rounded border border-zinc-800 bg-zinc-950 p-4">
      <div className="text-xs uppercase tracking-wider text-zinc-500 mb-3">{label}</div>
      {children}
    </div>
  );
}

function KV({ k, v, mono }: { k: string; v: string; mono?: boolean }) {
  return (
    <div className="flex justify-between gap-3 text-xs mb-1">
      <span className="text-zinc-500">{k}</span>
      <span className={mono ? 'font-mono text-zinc-300 break-all text-right' : 'text-zinc-300 text-right'}>{v}</span>
    </div>
  );
}
```

- [ ] **Step G1.2: Build + commit**

```bash
cd web && bun run build && cd ..
git add web/src/pages/CatalogDetail.tsx
git commit -m "web(catalog): three-tab detail page (Overview/Observations/Relationships)"
```

---

## Task Group Z — Docs + final + PR body

### Task Z1: Architecture doc

**Files:**
- Modify: `docs/architecture.md`

- [ ] **Step Z1.1: Append a Catalog UI section**

Append after the existing "Catalog rule extensions (Phase 22.5)" section:

```markdown
## Catalog UI

Operator-facing read-only browse over the `iocs` table. Single page at
`/catalog` with a sortable list (kind/source/search filters), a side
drawer for quick scan, and a `/catalog/:id` detail page with three
tabs (Overview, Observations, Relationships). Rule bodies for
`yara_rule` and `sigma_rule` kinds render in `<pre><code>` on the
Overview tab — no syntax highlighting library in v1.

Backend additions: `GET /api/iocs/{id}` (single record),
`GET /api/iocs/{id}/observations` (observation history), and
`source` + `q` filters on the existing `GET /api/iocs`. All under
the cookie-auth viewer+ group.

Frontend: `web/src/pages/Catalog.tsx`,
`web/src/pages/CatalogDetail.tsx`, `web/src/components/CatalogDrawer.tsx`.
Sidebar entry under the `Triage` group.
```

- [ ] **Step Z1.2: Commit**

```bash
git add docs/architecture.md
git commit -m "docs(architecture): catalog UI section"
```

---

### Task Z2: Full test sweep

```bash
go test ./... 2>&1 | tail -50
cd web && bun run build && cd ..
```

Expected: every package other than the pre-existing `controlplane/ui/static.go` (UI dist embed false positive) passes; web build is clean.

If anything else fails: STOP and report.

---

### Task Z3: PR body

**Files:**
- Create: `docs/superpowers/plans/2026-04-30-catalog-ui-pr-body.md`

```markdown
## Summary

A read-only `/catalog` page that lets operators browse, filter, and
inspect every IOC the system knows about — catalog-curated and
observation-derived rows alike — with a side drawer + full detail
page (Overview / Observations / Relationships tabs).

- New page at `/catalog` with kind / source / search filters.
- Drawer for quick scan; "Open full page →" navigates to `/catalog/:id`.
- Detail page tabs: Overview (identity, metadata, observability,
  rule body for yara_rule/sigma_rule), Observations
  (finding/run/host/observed_at), Relationships (typed edges,
  click-to-pivot).
- Sidebar entry under the existing `Triage` group.
- Backend additions: `GET /api/iocs/{id}`,
  `GET /api/iocs/{id}/observations`, and `source` + `q` filters on
  the existing `GET /api/iocs`.

No schema changes. Built on top of the Phase 22.1–22.5 work.

## Test plan

Lab smoke (post-merge, operator-side):

- [ ] `go test ./...` passes (excluding pre-existing UI dist embed)
- [ ] `bun run build` clean
- [ ] Drop the existing `catalog/iocs/example-yara-mimikatz.yaml` and
      `example-sigma-suspicious-powershell.yaml` into a CP's catalog
      directory; SIGHUP; navigate to `/catalog`; verify both rows
      appear with the right kind / source / name / tags / severity.
- [ ] Click a yara_rule row → drawer slides in with metadata + tags;
      "Open full page →" navigates to `/catalog/:id`.
- [ ] On the detail page, switch to the Observations tab — should be
      empty for catalog-only rows. Manually emit an `enrich_ioc`
      action against an observed sha256 and re-check; observation
      row should appear with finding link, host, observed_at.
- [ ] Switch to Relationships tab — `resolves-to` edge between domain
      and ipv4 IOCs should appear with both endpoints linkable.
- [ ] Filter the list by `?source=catalog` and `?q=mimikatz` —
      narrowing works.

## Files

- New endpoints: `GET /api/iocs/{id}`, `GET /api/iocs/{id}/observations`.
- Filter additions on existing `GET /api/iocs`: `?source=`, `?q=`.
- New pages: `web/src/pages/Catalog.tsx`, `web/src/pages/CatalogDetail.tsx`.
- New component: `web/src/components/CatalogDrawer.tsx`.

## Spec / plan

- Spec: `docs/superpowers/specs/2026-04-29-catalog-ui-design.md`
- Plan: `docs/superpowers/plans/2026-04-30-catalog-ui.md`

## Open follow-ups

- Syntax highlighting for rule bodies (deferred; revisit if operators
  ask). Library candidates: `shiki` (heavier, prettier) or
  `prism-react-renderer` (lighter).
- Relationship graph viz (list view in v1).
- Pagination for `/api/iocs` once the catalog grows past ~1k rows.
- Inline catalog editing — currently YAML-on-disk + auto-load.
- Frontend test framework — codebase has none today; v1 QA is
  `bun run build` + lab-smoke.
```

- [ ] **Step Z3.1: Commit**

```bash
git add docs/superpowers/plans/2026-04-30-catalog-ui-pr-body.md
git commit -m "docs: catalog UI PR body"
```

---

### Task Z4: DO NOT push or open the PR

The controller (the human operator) handles `git push` and `gh pr create`.

---

## Self-review checklist

- [ ] All spec deliverables covered:
  - **List page with filters** → Group E2
  - **Drawer** → Group F
  - **Detail page with three tabs** → Group G
  - **Sidebar entry** → Group E1
  - **Backend handlers + filter extension** → Groups A, B, C
- [ ] No CGO / build matrix changes — Frontend changes only on the React side; Go changes are pure-Go.
- [ ] Migration parity: no migrations needed (Phase 22.5's 041 already added `name`/`tags`).
- [ ] Drawer + detail page share rendering helpers via `Section` / `KV` patterns (some duplication is fine — they're 5-line components and YAGNI says don't extract a shared file yet).
- [ ] Observations table includes `ObservedAt` (required Group C1's struct extension).
- [ ] No placeholders in any task; every step shows literal code/command.
