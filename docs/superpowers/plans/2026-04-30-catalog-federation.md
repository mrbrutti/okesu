# Catalog Federation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make every Catalog API endpoint federation-aware via the established `federation_reads.go` FanOut+merge pattern — `/api/iocs`, single-IOC fetch, observations, and relationships all merge across the parent CP and every healthy child.

**Architecture:** Mirrors how findings/agents/nodes federate today. New child-side exports under `/api/v1/federation/iocs*` (each child publishes its own view); parent-side `Federated*` wrappers in `federation_reads.go` that call the local handler via `httpRecorder` then `agg.FanOut` to children, dedup by `(kind, normalized_value)` (or by predicate-tuple for relationships), and tag each merged row with `[]CPSourceRef`. Same `X-Okesu-Federation-Warning` partial-failure header. Same 10s context timeout.

**Tech Stack:**
- Go (`controlplane/api/`, `controlplane/db/`) — chi router, modernc.org/sqlite + lib/pq
- Existing `federation.Aggregator` (FanOut, FetchJSON), existing `CPSourceRef` from `findings.go:83`, existing `httpRecorder` helper
- React + TypeScript + Tailwind (light theme platform tokens)
- `react-router-dom` for `/catalog/:kind/:value`

---

## File Structure

**Backend create:**
- `controlplane/api/iocs_by_kv.go` — three new local handlers `GetIOCByKVHandler`, `ListIOCObservationsByKVHandler`, `ListIOCRelationshipsByKVHandler`
- `controlplane/api/iocs_by_kv_test.go` — handler tests
- `controlplane/api/federation_iocs.go` — child-side `RenderFederationIOCs*` exports + handlers, plus four parent-side `FederatedX` wrappers
- `controlplane/api/federation_iocs_test.go` — handler tests using mock peers
- `controlplane/api/federation_iocs_merge.go` — three pure merge functions
- `controlplane/api/federation_iocs_merge_test.go` — table-test merge functions

**Backend modify:**
- `controlplane/db/iocs.go` — add `GetIOCByKV`, `ListIOCObservationsByKV`, `ListIOCRelationshipsByKVPaired`; add `IOCRelationshipPaired` type
- `controlplane/server.go` — mount new local routes; mount child-side `/api/v1/federation/iocs*` routes; in viewer+ group, swap `ListIOCs`/`ListIOCObservationsHandler`/`ListIOCRelationshipsHandler` for federated variants

**Frontend modify:**
- `web/src/api.ts` — add `iocByKV`, `iocObservationsByKV`, `iocRelationshipsByKV`; add types `CPSourceRef`, `FederatedIOCRecord`, `FederatedIOCObservation`, `FederatedIOCRelationship`
- `web/src/pages/Catalog.tsx` — add "CPs" column; row click navigates `/catalog/:kind/:value`; render `X-Okesu-Federation-Warning` banner
- `web/src/components/CatalogDrawer.tsx` — pivot to `iocByKV` fetch path; render CP chips
- `web/src/pages/CatalogDetail.tsx` — change route to `:kind/:value`; pivot all fetches; render CP chip header; observations table adds "CP" column; relationships render kv pairs + CPs column
- `web/src/App.tsx` — `/catalog/:id` → `/catalog/:kind/:value`

---

## Task Group A — DB layer

### Task A1: Add IOCRelationshipPaired + three new store methods

**File:** `controlplane/db/iocs.go`

- [ ] **Step A1.1: Add the IOCRelationshipPaired type**

In `controlplane/db/iocs.go`, find `type IOCRelationship struct` (in the relationships file or this one) — actually it's in `controlplane/db/ioc_relationships.go`. Add the new type to that same file:

```go
// IOCRelationshipPaired is IOCRelationship with subject/object resolved
// to (kind, normalized_value) pairs instead of int row ids. Federation
// dedup needs the kv-pair shape because row ids are per-CP-meaningless.
type IOCRelationshipPaired struct {
    SubjectKind     string
    SubjectValue    string // normalized
    Predicate       string
    ObjectKind      string
    ObjectValue     string // normalized
    Source          string
    Confidence      string
}
```

- [ ] **Step A1.2: Add GetIOCByKV to controlplane/db/iocs.go**

Append after the existing `GetIOC`:

```go
// GetIOCByKV looks up a single IOC by (kind, normalized_value). Returns
// sql.ErrNoRows when the row doesn't exist. Used by federation handlers
// where row id isn't a stable cross-CP key.
func (s *Store) GetIOCByKV(kind, normalizedValue string) (*IOCRecord, error) {
    row := s.QueryRow(`
        SELECT id, kind, value, normalized_value, source,
               COALESCE(definition_path,''), COALESCE(confidence,''),
               COALESCE(attribution,''), COALESCE(severity_floor,''),
               COALESCE(classification,''), COALESCE(notes,''),
               COALESCE(name,''), COALESCE(tags,''),
               observation_count, first_seen, last_seen
        FROM iocs WHERE kind = ? AND normalized_value = ?`, kind, normalizedValue)
    var r IOCRecord
    if err := row.Scan(&r.ID, &r.Kind, &r.Value, &r.NormalizedValue, &r.Source,
        &r.DefinitionPath, &r.Confidence, &r.Attribution, &r.SeverityFloor,
        &r.Classification, &r.Notes, &r.Name, &r.Tags,
        &r.ObservationCount, &r.FirstSeen, &r.LastSeen); err != nil {
        return nil, err
    }
    return &r, nil
}
```

- [ ] **Step A1.3: Add ListIOCObservationsByKV**

Append after the existing `ListIOCObservations` in `controlplane/db/iocs.go`:

```go
// ListIOCObservationsByKV returns the observation history for a given
// (kind, normalized_value) pair. Same shape as ListIOCObservations but
// keyed by the cross-CP-stable identifier.
func (s *Store) ListIOCObservationsByKV(kind, normalizedValue string) ([]IOCObservation, error) {
    rows, err := s.Query(`
        SELECT o.ioc_id, COALESCE(o.finding_id,0), COALESCE(o.orchestration_run_id,0), COALESCE(o.host,''), o.observed_at
        FROM ioc_observations o
        JOIN iocs i ON i.id = o.ioc_id
        WHERE i.kind = ? AND i.normalized_value = ?
        ORDER BY o.observed_at DESC`, kind, normalizedValue)
    if err != nil {
        return nil, err
    }
    defer rows.Close()
    var out []IOCObservation
    for rows.Next() {
        var o IOCObservation
        var observedAtRaw sql.NullString
        if err := rows.Scan(&o.IOCID, &o.FindingID, &o.OrchestrationRunID, &o.Host, &observedAtRaw); err != nil {
            return nil, err
        }
        o.ObservedAt = ParseTimestamp(observedAtRaw.String)
        out = append(out, o)
    }
    return out, rows.Err()
}
```

- [ ] **Step A1.4: Add ListIOCRelationshipsByKVPaired**

In `controlplane/db/ioc_relationships.go`, append:

```go
// ListIOCRelationshipsByKVPaired returns every typed edge touching the
// IOC identified by (kind, normalizedValue), with subject and object
// resolved to (kind, normalized_value) pairs. The kv-pair shape lets
// federation dedup edges across CPs where the int row ids differ.
func (s *Store) ListIOCRelationshipsByKVPaired(kind, normalizedValue string) ([]IOCRelationshipPaired, error) {
    rows, err := s.Query(`
        SELECT s.kind, s.normalized_value, r.predicate, o.kind, o.normalized_value,
               COALESCE(r.source,''), COALESCE(r.confidence,'')
        FROM ioc_relationships r
        JOIN iocs s ON s.id = r.subject_id
        JOIN iocs o ON o.id = r.object_id
        WHERE (s.kind = ? AND s.normalized_value = ?)
           OR (o.kind = ? AND o.normalized_value = ?)
        ORDER BY r.id`, kind, normalizedValue, kind, normalizedValue)
    if err != nil {
        return nil, err
    }
    defer rows.Close()
    var out []IOCRelationshipPaired
    for rows.Next() {
        var p IOCRelationshipPaired
        if err := rows.Scan(&p.SubjectKind, &p.SubjectValue, &p.Predicate,
            &p.ObjectKind, &p.ObjectValue, &p.Source, &p.Confidence); err != nil {
            return nil, err
        }
        out = append(out, p)
    }
    return out, rows.Err()
}
```

- [ ] **Step A1.5: Add tests**

Append to `controlplane/db/iocs_test.go`:

```go
func TestGetIOCByKV(t *testing.T) {
    s := openTempStore(t)
    s.UpsertIOC(&IOCUpsert{Kind: "sha256", Value: "ABC", NormalizedValue: "abc", Source: "catalog", Name: "test"})

    got, err := s.GetIOCByKV("sha256", "abc")
    if err != nil {
        t.Fatalf("GetIOCByKV: %v", err)
    }
    if got.Name != "test" {
        t.Errorf("Name = %q, want test", got.Name)
    }

    _, err = s.GetIOCByKV("sha256", "missing")
    if err == nil {
        t.Errorf("expected sql.ErrNoRows for missing kv")
    }
}

func TestListIOCObservationsByKV(t *testing.T) {
    s := openTempStore(t)
    iocID, _, _ := s.UpsertIOC(&IOCUpsert{Kind: "sha256", Value: "abc", NormalizedValue: "abc"})
    eventID, _ := s.InsertEvent(&Event{Ts: 0, Type: "finding", RawJSON: "{}"})
    findingID, _ := s.InsertFinding(&FindingInsert{
        EventID: eventID, Ts: 0, Agent: "a", Host: "host-1",
        Severity: "INFO", Title: "t", RawJSON: "{}",
    })
    if err := s.RecordIOCObservation(iocID, &IOCObservation{FindingID: findingID, Host: "host-1"}); err != nil {
        t.Fatalf("RecordIOCObservation: %v", err)
    }

    got, err := s.ListIOCObservationsByKV("sha256", "abc")
    if err != nil {
        t.Fatalf("ListIOCObservationsByKV: %v", err)
    }
    if len(got) != 1 || got[0].Host != "host-1" {
        t.Errorf("expected one observation host=host-1; got %+v", got)
    }
}
```

Append to `controlplane/db/ioc_relationships_test.go` (or create if absent):

```go
func TestListIOCRelationshipsByKVPaired(t *testing.T) {
    s := openTempStore(t)
    aID, _, _ := s.UpsertIOC(&IOCUpsert{Kind: "domain", Value: "evil.com", NormalizedValue: "evil.com"})
    bID, _, _ := s.UpsertIOC(&IOCUpsert{Kind: "ipv4", Value: "1.2.3.4", NormalizedValue: "1.2.3.4"})
    if err := s.AddIOCRelationship(&IOCRelationshipInsert{
        SubjectID: aID, Predicate: "resolves-to", ObjectID: bID, Source: "agent",
    }); err != nil {
        t.Fatalf("AddIOCRelationship: %v", err)
    }

    rels, err := s.ListIOCRelationshipsByKVPaired("domain", "evil.com")
    if err != nil {
        t.Fatalf("ListIOCRelationshipsByKVPaired: %v", err)
    }
    if len(rels) != 1 {
        t.Fatalf("expected 1 edge; got %d", len(rels))
    }
    r := rels[0]
    if r.SubjectKind != "domain" || r.SubjectValue != "evil.com" ||
        r.Predicate != "resolves-to" || r.ObjectKind != "ipv4" || r.ObjectValue != "1.2.3.4" {
        t.Errorf("edge shape wrong: %+v", r)
    }

    // Querying the object side should also return the same edge.
    relsB, _ := s.ListIOCRelationshipsByKVPaired("ipv4", "1.2.3.4")
    if len(relsB) != 1 || relsB[0].SubjectKind != "domain" {
        t.Errorf("object-side query missing edge or shape wrong: %+v", relsB)
    }
}
```

- [ ] **Step A1.6: Run + commit**

```bash
go test ./controlplane/db/ -run "TestGetIOCByKV|TestListIOCObservationsByKV|TestListIOCRelationshipsByKVPaired" -v -count=1
```

All pass. Then:

```bash
pwd  # MUST show .../feat-catalog-federation
git status  # MUST show "On branch feat/catalog-federation"
git add controlplane/db/iocs.go controlplane/db/iocs_test.go \
        controlplane/db/ioc_relationships.go controlplane/db/ioc_relationships_test.go
git commit -m "db(iocs): GetIOCByKV + ListIOCObservationsByKV + ListIOCRelationshipsByKVPaired"
```

---

## Task Group B — Local by-kv HTTP handlers

### Task B1: Three local by-kv HTTP handlers

**Files:**
- Create: `controlplane/api/iocs_by_kv.go`
- Create: `controlplane/api/iocs_by_kv_test.go`

- [ ] **Step B1.1: Tests first**

`controlplane/api/iocs_by_kv_test.go`:

```go
package api

import (
    "encoding/json"
    "net/http/httptest"
    "testing"

    "github.com/section9labs/okesu/controlplane/db"
)

func TestGetIOCByKV_Found(t *testing.T) {
    st := newTestStore(t)
    st.UpsertIOC(&db.IOCUpsert{Kind: "sha256", Value: "abc", NormalizedValue: "abc", Source: "catalog", Name: "test"})

    rec := httptest.NewRecorder()
    req := httptest.NewRequest("GET", "/api/iocs/by-kv?kind=sha256&value=abc", nil)
    GetIOCByKVHandler(st)(rec, req)
    if rec.Code != 200 {
        t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
    }
    var got db.IOCRecord
    if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
        t.Fatalf("decode: %v", err)
    }
    if got.Name != "test" {
        t.Errorf("Name = %q", got.Name)
    }
}

func TestGetIOCByKV_NotFound(t *testing.T) {
    st := newTestStore(t)
    rec := httptest.NewRecorder()
    req := httptest.NewRequest("GET", "/api/iocs/by-kv?kind=sha256&value=missing", nil)
    GetIOCByKVHandler(st)(rec, req)
    if rec.Code != 404 {
        t.Fatalf("status = %d, want 404", rec.Code)
    }
}

func TestGetIOCByKV_MissingParams(t *testing.T) {
    st := newTestStore(t)
    rec := httptest.NewRecorder()
    req := httptest.NewRequest("GET", "/api/iocs/by-kv?kind=sha256", nil)
    GetIOCByKVHandler(st)(rec, req)
    if rec.Code != 400 {
        t.Fatalf("status = %d, want 400", rec.Code)
    }
}

func TestListIOCObservationsByKV_HTTP(t *testing.T) {
    st := newTestStore(t)
    iocID, _, _ := st.UpsertIOC(&db.IOCUpsert{Kind: "sha256", Value: "abc", NormalizedValue: "abc"})
    eventID, _ := st.InsertEvent(&db.Event{Ts: 0, Type: "finding", RawJSON: "{}"})
    findingID, _ := st.InsertFinding(&db.FindingInsert{
        EventID: eventID, Ts: 0, Agent: "a", Host: "host-1",
        Severity: "INFO", Title: "t", RawJSON: "{}",
    })
    if err := st.RecordIOCObservation(iocID, &db.IOCObservation{FindingID: findingID, Host: "host-1"}); err != nil {
        t.Fatalf("RecordIOCObservation: %v", err)
    }

    rec := httptest.NewRecorder()
    req := httptest.NewRequest("GET", "/api/iocs/by-kv/observations?kind=sha256&value=abc", nil)
    ListIOCObservationsByKVHandler(st)(rec, req)
    if rec.Code != 200 {
        t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
    }
    var got []db.IOCObservation
    json.NewDecoder(rec.Body).Decode(&got)
    if len(got) != 1 || got[0].Host != "host-1" {
        t.Errorf("expected one observation host=host-1; got %+v", got)
    }
}

func TestListIOCRelationshipsByKV_HTTP(t *testing.T) {
    st := newTestStore(t)
    aID, _, _ := st.UpsertIOC(&db.IOCUpsert{Kind: "domain", Value: "evil.com", NormalizedValue: "evil.com"})
    bID, _, _ := st.UpsertIOC(&db.IOCUpsert{Kind: "ipv4", Value: "1.2.3.4", NormalizedValue: "1.2.3.4"})
    st.AddIOCRelationship(&db.IOCRelationshipInsert{SubjectID: aID, Predicate: "resolves-to", ObjectID: bID, Source: "agent"})

    rec := httptest.NewRecorder()
    req := httptest.NewRequest("GET", "/api/iocs/by-kv/relationships?kind=domain&value=evil.com", nil)
    ListIOCRelationshipsByKVHandler(st)(rec, req)
    if rec.Code != 200 {
        t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
    }
    var got []db.IOCRelationshipPaired
    json.NewDecoder(rec.Body).Decode(&got)
    if len(got) != 1 || got[0].Predicate != "resolves-to" {
        t.Errorf("expected one resolves-to edge; got %+v", got)
    }
}
```

- [ ] **Step B1.2: Implement**

`controlplane/api/iocs_by_kv.go`:

```go
package api

import (
    "database/sql"
    "encoding/json"
    "errors"
    "net/http"

    "github.com/section9labs/okesu/controlplane/db"
)

// GetIOCByKVHandler returns a single IOC identified by (kind, value)
// query params. Used as the local-side query for the federated by-kv
// endpoint: row ids are per-CP-meaningless, so cross-CP detail lookups
// must use the (kind, value) shape.
func GetIOCByKVHandler(store *db.Store) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        kind := r.URL.Query().Get("kind")
        value := r.URL.Query().Get("value")
        if kind == "" || value == "" {
            http.Error(w, "missing kind or value", http.StatusBadRequest)
            return
        }
        ioc, err := store.GetIOCByKV(kind, value)
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

// ListIOCObservationsByKVHandler is the (kind, value) twin of
// ListIOCObservationsHandler.
func ListIOCObservationsByKVHandler(store *db.Store) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        kind := r.URL.Query().Get("kind")
        value := r.URL.Query().Get("value")
        if kind == "" || value == "" {
            http.Error(w, "missing kind or value", http.StatusBadRequest)
            return
        }
        obs, err := store.ListIOCObservationsByKV(kind, value)
        if err != nil {
            http.Error(w, err.Error(), http.StatusInternalServerError)
            return
        }
        w.Header().Set("Content-Type", "application/json")
        _ = json.NewEncoder(w).Encode(obs)
    }
}

// ListIOCRelationshipsByKVHandler returns paired (kind, value) edges
// touching the given IOC. The wire shape uses kv pairs instead of int
// ids so federation can dedup by tuple across CPs.
func ListIOCRelationshipsByKVHandler(store *db.Store) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        kind := r.URL.Query().Get("kind")
        value := r.URL.Query().Get("value")
        if kind == "" || value == "" {
            http.Error(w, "missing kind or value", http.StatusBadRequest)
            return
        }
        rels, err := store.ListIOCRelationshipsByKVPaired(kind, value)
        if err != nil {
            http.Error(w, err.Error(), http.StatusInternalServerError)
            return
        }
        w.Header().Set("Content-Type", "application/json")
        _ = json.NewEncoder(w).Encode(rels)
    }
}
```

- [ ] **Step B1.3: Run + commit**

```bash
go test ./controlplane/api/ -run "TestGetIOCByKV|TestListIOCObservationsByKV|TestListIOCRelationshipsByKV" -v -count=1
go vet ./controlplane/api/
```

Then:

```bash
pwd && git status
git add controlplane/api/iocs_by_kv.go controlplane/api/iocs_by_kv_test.go
git commit -m "api(iocs): by-kv local handlers (single, observations, relationships)"
```

---

## Task Group C — Merge logic

### Task C1: Three pure merge functions

The merge functions are the heart of federation. Pure (no I/O), so heavily unit-tested. Iterate contributing rows in ascending CPSourceRef.InstanceID order so "first non-empty wins" is deterministic.

**Files:**
- Create: `controlplane/api/federation_iocs_merge.go`
- Create: `controlplane/api/federation_iocs_merge_test.go`

- [ ] **Step C1.1: Tests first**

`controlplane/api/federation_iocs_merge_test.go`:

```go
package api

import (
    "reflect"
    "sort"
    "testing"
    "time"

    "github.com/section9labs/okesu/controlplane/db"
)

func ts(s string) time.Time {
    t, _ := time.Parse(time.RFC3339, s)
    return t
}

func TestMergeIOCs_DedupsAndAggregates(t *testing.T) {
    cpA := &CPSourceRef{InstanceID: "cp-a", DisplayName: "A"}
    cpB := &CPSourceRef{InstanceID: "cp-b", DisplayName: "B"}
    rowsA := []*db.IOCRecord{
        {ID: 1, Kind: "sha256", NormalizedValue: "abc", Source: "catalog", Name: "from-a", Tags: "ransomware",
            ObservationCount: 3, FirstSeen: ts("2026-04-01T00:00:00Z"), LastSeen: ts("2026-04-15T00:00:00Z")},
    }
    rowsB := []*db.IOCRecord{
        {ID: 9, Kind: "sha256", NormalizedValue: "abc", Source: "observed", Name: "", Tags: "emotet",
            ObservationCount: 5, FirstSeen: ts("2026-04-10T00:00:00Z"), LastSeen: ts("2026-04-20T00:00:00Z")},
    }
    merged := MergeIOCs([]contribIOCs{{cp: cpA, rows: rowsA}, {cp: cpB, rows: rowsB}})
    if len(merged) != 1 {
        t.Fatalf("expected 1 merged row; got %d", len(merged))
    }
    m := merged[0]
    if m.Source != "catalog" {
        t.Errorf("Source = %q, want catalog (catalog wins over observed)", m.Source)
    }
    if m.Name != "from-a" {
        t.Errorf("Name = %q, want from-a (catalog row's non-empty wins)", m.Name)
    }
    if m.Tags != "ransomware" {
        t.Errorf("Tags = %q, want ransomware (catalog row's non-empty wins)", m.Tags)
    }
    if m.ObservationCount != 8 {
        t.Errorf("ObservationCount = %d, want 8 (3+5)", m.ObservationCount)
    }
    if !m.FirstSeen.Equal(ts("2026-04-01T00:00:00Z")) {
        t.Errorf("FirstSeen = %v, want 2026-04-01 (min)", m.FirstSeen)
    }
    if !m.LastSeen.Equal(ts("2026-04-20T00:00:00Z")) {
        t.Errorf("LastSeen = %v, want 2026-04-20 (max)", m.LastSeen)
    }
    if len(m.CPSources) != 2 || m.CPSources[0].InstanceID != "cp-a" || m.CPSources[1].InstanceID != "cp-b" {
        t.Errorf("CPSources = %+v, want [cp-a cp-b] sorted", m.CPSources)
    }
}

func TestMergeIOCs_PreservesNonOverlapping(t *testing.T) {
    cpA := &CPSourceRef{InstanceID: "cp-a"}
    cpB := &CPSourceRef{InstanceID: "cp-b"}
    rowsA := []*db.IOCRecord{{Kind: "sha256", NormalizedValue: "abc", Source: "catalog"}}
    rowsB := []*db.IOCRecord{{Kind: "ipv4", NormalizedValue: "1.2.3.4", Source: "observed"}}
    merged := MergeIOCs([]contribIOCs{{cp: cpA, rows: rowsA}, {cp: cpB, rows: rowsB}})
    if len(merged) != 2 {
        t.Fatalf("expected 2 rows; got %d", len(merged))
    }
}

func TestMergeIOCObservations_TagsOriginAndSorts(t *testing.T) {
    cpA := &CPSourceRef{InstanceID: "cp-a"}
    cpB := &CPSourceRef{InstanceID: "cp-b"}
    obsA := []db.IOCObservation{{IOCID: 1, Host: "host-a", ObservedAt: ts("2026-04-01T00:00:00Z")}}
    obsB := []db.IOCObservation{{IOCID: 9, Host: "host-b", ObservedAt: ts("2026-04-10T00:00:00Z")}}
    merged := MergeIOCObservations([]contribObs{{cp: cpA, rows: obsA}, {cp: cpB, rows: obsB}})
    if len(merged) != 2 {
        t.Fatalf("expected 2 observations; got %d", len(merged))
    }
    // Newest first
    if merged[0].Host != "host-b" || merged[0].CPSource.InstanceID != "cp-b" {
        t.Errorf("first row should be host-b on cp-b; got %+v", merged[0])
    }
    if merged[1].Host != "host-a" || merged[1].CPSource.InstanceID != "cp-a" {
        t.Errorf("second row should be host-a on cp-a; got %+v", merged[1])
    }
}

func TestMergeIOCRelationships_DedupsByTuple(t *testing.T) {
    cpA := &CPSourceRef{InstanceID: "cp-a"}
    cpB := &CPSourceRef{InstanceID: "cp-b"}
    edge := db.IOCRelationshipPaired{SubjectKind: "domain", SubjectValue: "evil.com",
        Predicate: "resolves-to", ObjectKind: "ipv4", ObjectValue: "1.2.3.4",
        Source: "agent", Confidence: ""}
    edgeB := edge
    edgeB.Source = "" // empty on cp-b — cp-a's value should win
    edgeB.Confidence = "high"

    merged := MergeIOCRelationships([]contribRels{
        {cp: cpA, rows: []db.IOCRelationshipPaired{edge}},
        {cp: cpB, rows: []db.IOCRelationshipPaired{edgeB}},
    })
    if len(merged) != 1 {
        t.Fatalf("expected 1 deduped tuple; got %d", len(merged))
    }
    m := merged[0]
    if m.Source != "agent" {
        t.Errorf("Source = %q, want agent (first non-empty)", m.Source)
    }
    if m.Confidence != "high" {
        t.Errorf("Confidence = %q, want high (first non-empty)", m.Confidence)
    }
    cps := []string{m.CPSources[0].InstanceID, m.CPSources[1].InstanceID}
    sort.Strings(cps)
    if !reflect.DeepEqual(cps, []string{"cp-a", "cp-b"}) {
        t.Errorf("CPSources should be both CPs; got %+v", m.CPSources)
    }
}
```

- [ ] **Step C1.2: Implement**

`controlplane/api/federation_iocs_merge.go`:

```go
package api

import (
    "sort"
    "time"

    "github.com/section9labs/okesu/controlplane/db"
)

// FederatedIOCRecord is the merged wire shape: every IOCRecord field
// plus a CPSources slice listing every CP whose DB has this row.
// Marshaled flat (CPSources alongside the IOCRecord fields).
type FederatedIOCRecord struct {
    *db.IOCRecord
    CPSources []*CPSourceRef `json:"cp_sources"`
}

// FederatedIOCObservation extends IOCObservation with origin-CP tagging.
type FederatedIOCObservation struct {
    db.IOCObservation
    CPSource *CPSourceRef `json:"cp_source"`
}

// FederatedIOCRelationship is the deduped merged edge shape with kv-pair
// endpoints (since per-CP int ids are not comparable across CPs).
type FederatedIOCRelationship struct {
    db.IOCRelationshipPaired
    CPSources []*CPSourceRef `json:"cp_sources"`
}

// contribIOCs / contribObs / contribRels are per-CP contribution
// bundles — tests pass these directly; runtime code builds them
// from the FanOut callback.
type contribIOCs struct {
    cp   *CPSourceRef
    rows []*db.IOCRecord
}

type contribObs struct {
    cp   *CPSourceRef
    rows []db.IOCObservation
}

type contribRels struct {
    cp   *CPSourceRef
    rows []db.IOCRelationshipPaired
}

// MergeIOCs deduplicates by (kind, normalized_value). For text fields,
// first non-empty wins iterating contributors in ascending CP-ID order
// with catalog-source rows ahead of observed-source. Sums
// observation_count, takes min(first_seen) and max(last_seen).
func MergeIOCs(contribs []contribIOCs) []FederatedIOCRecord {
    // Sort contributors so deterministic iteration: catalog source ahead
    // of observed within each CP, CP-ID ascending across the slice.
    // Two-pass: catalog-first iteration first, then observed-first.
    // Simpler equivalent: stable sort by (sourcePriority, cpID).
    type entry struct {
        cp     *CPSourceRef
        prio   int // 0 for catalog, 1 for observed
        record *db.IOCRecord
    }
    var flat []entry
    for _, c := range contribs {
        for _, r := range c.rows {
            prio := 1
            if r.Source == "catalog" {
                prio = 0
            }
            flat = append(flat, entry{cp: c.cp, prio: prio, record: r})
        }
    }
    sort.SliceStable(flat, func(i, j int) bool {
        if flat[i].prio != flat[j].prio {
            return flat[i].prio < flat[j].prio
        }
        return flat[i].cp.InstanceID < flat[j].cp.InstanceID
    })

    type key struct{ kind, val string }
    out := make(map[key]*FederatedIOCRecord)
    for _, e := range flat {
        k := key{kind: e.record.Kind, val: e.record.NormalizedValue}
        if existing, ok := out[k]; ok {
            mergeIOCFields(existing.IOCRecord, e.record)
            existing.CPSources = appendCPSourceUnique(existing.CPSources, e.cp)
            continue
        }
        // First-seen entry: use the record verbatim (it's already the
        // highest-priority contributor due to our sort).
        cpy := *e.record
        out[k] = &FederatedIOCRecord{IOCRecord: &cpy, CPSources: []*CPSourceRef{e.cp}}
    }

    var result []FederatedIOCRecord
    for _, m := range out {
        // Sort CPSources for stable output.
        sort.Slice(m.CPSources, func(i, j int) bool { return m.CPSources[i].InstanceID < m.CPSources[j].InstanceID })
        result = append(result, *m)
    }
    sort.Slice(result, func(i, j int) bool { return result[i].LastSeen.After(result[j].LastSeen) })
    return result
}

// mergeIOCFields folds non-empty fields from src into existing (dst
// keeps any value already set; src fills in blanks). Numeric and time
// fields aggregate.
func mergeIOCFields(dst, src *db.IOCRecord) {
    if dst.Source != "catalog" && src.Source == "catalog" {
        dst.Source = "catalog"
    }
    if dst.DefinitionPath == "" {
        dst.DefinitionPath = src.DefinitionPath
    }
    if dst.Confidence == "" {
        dst.Confidence = src.Confidence
    }
    if dst.Attribution == "" {
        dst.Attribution = src.Attribution
    }
    if dst.SeverityFloor == "" {
        dst.SeverityFloor = src.SeverityFloor
    }
    if dst.Classification == "" {
        dst.Classification = src.Classification
    }
    if dst.Notes == "" {
        dst.Notes = src.Notes
    }
    if dst.Name == "" {
        dst.Name = src.Name
    }
    if dst.Tags == "" {
        dst.Tags = src.Tags
    }
    dst.ObservationCount += src.ObservationCount
    if src.FirstSeen.Before(dst.FirstSeen) || dst.FirstSeen.IsZero() {
        dst.FirstSeen = src.FirstSeen
    }
    if src.LastSeen.After(dst.LastSeen) {
        dst.LastSeen = src.LastSeen
    }
}

// appendCPSourceUnique adds cp to s only if no entry has the same
// InstanceID. Avoids cps being listed twice when the same CP appears
// in multiple contribution batches (shouldn't happen, but defensive).
func appendCPSourceUnique(s []*CPSourceRef, cp *CPSourceRef) []*CPSourceRef {
    for _, x := range s {
        if x.InstanceID == cp.InstanceID {
            return s
        }
    }
    return append(s, cp)
}

// MergeIOCObservations unions observation rows across CPs, tags each
// with its origin CPSourceRef, sorts ObservedAt descending.
func MergeIOCObservations(contribs []contribObs) []FederatedIOCObservation {
    var out []FederatedIOCObservation
    for _, c := range contribs {
        for _, o := range c.rows {
            out = append(out, FederatedIOCObservation{IOCObservation: o, CPSource: c.cp})
        }
    }
    sort.SliceStable(out, func(i, j int) bool { return out[i].ObservedAt.After(out[j].ObservedAt) })
    return out
}

// MergeIOCRelationships dedups by tuple (subject_kv, predicate, object_kv).
// First non-empty wins for Source/Confidence iterating in ascending
// CP-ID order. CPSources lists every CP that recorded the edge.
func MergeIOCRelationships(contribs []contribRels) []FederatedIOCRelationship {
    sort.SliceStable(contribs, func(i, j int) bool { return contribs[i].cp.InstanceID < contribs[j].cp.InstanceID })

    type key struct {
        sk, sv, p, ok, ov string
    }
    out := make(map[key]*FederatedIOCRelationship)
    for _, c := range contribs {
        for _, r := range c.rows {
            k := key{sk: r.SubjectKind, sv: r.SubjectValue, p: r.Predicate, ok: r.ObjectKind, ov: r.ObjectValue}
            if existing, ok := out[k]; ok {
                if existing.Source == "" {
                    existing.Source = r.Source
                }
                if existing.Confidence == "" {
                    existing.Confidence = r.Confidence
                }
                existing.CPSources = appendCPSourceUnique(existing.CPSources, c.cp)
                continue
            }
            cpy := r
            out[k] = &FederatedIOCRelationship{IOCRelationshipPaired: cpy, CPSources: []*CPSourceRef{c.cp}}
        }
    }
    var result []FederatedIOCRelationship
    for _, m := range out {
        sort.Slice(m.CPSources, func(i, j int) bool { return m.CPSources[i].InstanceID < m.CPSources[j].InstanceID })
        result = append(result, *m)
    }
    return result
}
```

- [ ] **Step C1.3: Run + commit**

```bash
go test ./controlplane/api/ -run "TestMergeIOCs|TestMergeIOCObservations|TestMergeIOCRelationships" -v -count=1
go vet ./controlplane/api/
```

All four merge tests pass. Then:

```bash
pwd && git status
git add controlplane/api/federation_iocs_merge.go controlplane/api/federation_iocs_merge_test.go
git commit -m "api(federation/iocs): pure merge functions for list/observations/relationships"
```

---

## Task Group D — Federation child-side exports

### Task D1: Render functions + child-side handlers

The child publishes its IOC view at `/api/v1/federation/iocs*`. Same shape as the local handlers, but mounted under the federation export group with `requireFederationToken` middleware (same as other federation endpoints).

**Files:**
- Create: `controlplane/api/federation_iocs.go` (combined render + child handlers + parent handlers; all federation-IOC code in one file)

- [ ] **Step D1.1: Add render functions and child-side handlers**

`controlplane/api/federation_iocs.go`:

```go
package api

import (
    "context"
    "encoding/json"
    "net/http"
    "sync"
    "time"

    "github.com/section9labs/okesu/controlplane/db"
    "github.com/section9labs/okesu/controlplane/federation"
)

// RenderFederationIOCs produces the JSON the federation S3 publisher
// writes (Phase B+) and that the parent's aggregator HTTP-fetches.
// Capped at 5000 rows by last_seen DESC; matches the established
// RenderFederationFindings shape.
func RenderFederationIOCs(store *db.Store, limit int) ([]byte, error) {
    if limit <= 0 || limit > 5000 {
        limit = 5000
    }
    rows, err := store.ListIOCs(db.IOCListFilter{Limit: limit})
    if err != nil {
        return nil, err
    }
    return json.Marshal(rows)
}

// FederationIOCs serves the local CP's IOC view. Mounted at
// /api/v1/federation/iocs behind requireFederationToken. The parent's
// aggregator pulls this on every /api/iocs request.
func FederationIOCs(store *db.Store) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        body, err := RenderFederationIOCs(store, 5000)
        if err != nil {
            http.Error(w, err.Error(), http.StatusInternalServerError)
            return
        }
        w.Header().Set("Content-Type", "application/json")
        _, _ = w.Write(body)
    }
}

// FederationIOCByKV serves a single IOC by (kind, value) for the
// parent's federated detail handler.
func FederationIOCByKV(store *db.Store) http.HandlerFunc {
    return GetIOCByKVHandler(store)
}

// FederationIOCObservationsByKV — same shape as the local handler,
// re-exported for federation pathing.
func FederationIOCObservationsByKV(store *db.Store) http.HandlerFunc {
    return ListIOCObservationsByKVHandler(store)
}

// FederationIOCRelationshipsByKV — same shape as the local handler,
// re-exported for federation pathing.
func FederationIOCRelationshipsByKV(store *db.Store) http.HandlerFunc {
    return ListIOCRelationshipsByKVHandler(store)
}
```

(The child-side endpoints reuse the local handlers verbatim — there's no semantic difference between "local CP's view of its own IOCs" and "child's view as fetched by a parent". The federation token middleware is the only thing that differs at mount time.)

- [ ] **Step D1.2: Add the parent-side federated wrappers to the same file**

Append to `controlplane/api/federation_iocs.go`:

```go
// FederatedListIOCs is the parent-side handler that wraps ListIOCs.
// Pattern mirrors FederatedFindingsList in federation_reads.go: run
// local handler via httpRecorder, then FanOut peers via FetchJSON,
// merge by (kind, normalized_value), tag each row with []CPSourceRef.
func FederatedListIOCs(store *db.Store, agg *federation.Aggregator) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        // Local first.
        localRR := httpRecorder()
        ListIOCs(store).ServeHTTP(localRR, r)
        if localRR.code != http.StatusOK {
            w.WriteHeader(localRR.code)
            _, _ = w.Write(localRR.body)
            return
        }
        var localRows []*db.IOCRecord
        _ = json.Unmarshal(localRR.body, &localRows)

        localCP, err := selfCPSource(store)
        if err != nil {
            http.Error(w, err.Error(), http.StatusInternalServerError)
            return
        }
        contribs := []contribIOCs{{cp: localCP, rows: localRows}}

        // Fan out to peers.
        ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
        defer cancel()
        path := "/api/v1/federation/iocs"
        if rq := r.URL.RawQuery; rq != "" {
            path += "?" + rq
        }
        var mu sync.Mutex
        results, err := agg.FanOut(ctx, func(ctx context.Context, peer federation.Peer) error {
            var rows []*db.IOCRecord
            if err := agg.FetchJSON(ctx, peer, path, &rows); err != nil {
                return err
            }
            mu.Lock()
            defer mu.Unlock()
            contribs = append(contribs, contribIOCs{cp: peerCPSource(peer), rows: rows})
            return nil
        })
        if err != nil {
            w.Header().Set("X-Okesu-Federation-Warning", err.Error())
        }
        if pErr := federation.AnyError(results); pErr != nil {
            w.Header().Set("X-Okesu-Federation-Warning", pErr.Error())
        }

        merged := MergeIOCs(contribs)
        w.Header().Set("Content-Type", "application/json")
        _ = json.NewEncoder(w).Encode(merged)
    }
}

// FederatedGetIOCByKV: single (kind, value) detail merged across CPs.
// 404 if no CP has the row.
func FederatedGetIOCByKV(store *db.Store, agg *federation.Aggregator) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        kind := r.URL.Query().Get("kind")
        value := r.URL.Query().Get("value")
        if kind == "" || value == "" {
            http.Error(w, "missing kind or value", http.StatusBadRequest)
            return
        }
        localCP, err := selfCPSource(store)
        if err != nil {
            http.Error(w, err.Error(), http.StatusInternalServerError)
            return
        }
        contribs := []contribIOCs{}
        if rec, err := store.GetIOCByKV(kind, value); err == nil {
            contribs = append(contribs, contribIOCs{cp: localCP, rows: []*db.IOCRecord{rec}})
        }

        ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
        defer cancel()
        path := "/api/v1/federation/iocs/by-kv?kind=" + urlQuery(kind) + "&value=" + urlQuery(value)
        var mu sync.Mutex
        results, _ := agg.FanOut(ctx, func(ctx context.Context, peer federation.Peer) error {
            var rec db.IOCRecord
            if err := agg.FetchJSON(ctx, peer, path, &rec); err != nil {
                return err
            }
            mu.Lock()
            defer mu.Unlock()
            contribs = append(contribs, contribIOCs{cp: peerCPSource(peer), rows: []*db.IOCRecord{&rec}})
            return nil
        })
        if pErr := federation.AnyError(results); pErr != nil {
            w.Header().Set("X-Okesu-Federation-Warning", pErr.Error())
        }
        merged := MergeIOCs(contribs)
        if len(merged) == 0 {
            http.Error(w, "not found", http.StatusNotFound)
            return
        }
        w.Header().Set("Content-Type", "application/json")
        _ = json.NewEncoder(w).Encode(merged[0])
    }
}

// FederatedListIOCObservationsByKV: union observations across CPs.
func FederatedListIOCObservationsByKV(store *db.Store, agg *federation.Aggregator) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        kind := r.URL.Query().Get("kind")
        value := r.URL.Query().Get("value")
        if kind == "" || value == "" {
            http.Error(w, "missing kind or value", http.StatusBadRequest)
            return
        }
        localCP, err := selfCPSource(store)
        if err != nil {
            http.Error(w, err.Error(), http.StatusInternalServerError)
            return
        }
        local, _ := store.ListIOCObservationsByKV(kind, value)
        contribs := []contribObs{{cp: localCP, rows: local}}

        ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
        defer cancel()
        path := "/api/v1/federation/iocs/by-kv/observations?kind=" + urlQuery(kind) + "&value=" + urlQuery(value)
        var mu sync.Mutex
        results, _ := agg.FanOut(ctx, func(ctx context.Context, peer federation.Peer) error {
            var rows []db.IOCObservation
            if err := agg.FetchJSON(ctx, peer, path, &rows); err != nil {
                return err
            }
            mu.Lock()
            defer mu.Unlock()
            contribs = append(contribs, contribObs{cp: peerCPSource(peer), rows: rows})
            return nil
        })
        if pErr := federation.AnyError(results); pErr != nil {
            w.Header().Set("X-Okesu-Federation-Warning", pErr.Error())
        }
        merged := MergeIOCObservations(contribs)
        w.Header().Set("Content-Type", "application/json")
        _ = json.NewEncoder(w).Encode(merged)
    }
}

// FederatedListIOCRelationshipsByKV: union edges across CPs, dedup by tuple.
func FederatedListIOCRelationshipsByKV(store *db.Store, agg *federation.Aggregator) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        kind := r.URL.Query().Get("kind")
        value := r.URL.Query().Get("value")
        if kind == "" || value == "" {
            http.Error(w, "missing kind or value", http.StatusBadRequest)
            return
        }
        localCP, err := selfCPSource(store)
        if err != nil {
            http.Error(w, err.Error(), http.StatusInternalServerError)
            return
        }
        local, _ := store.ListIOCRelationshipsByKVPaired(kind, value)
        contribs := []contribRels{{cp: localCP, rows: local}}

        ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
        defer cancel()
        path := "/api/v1/federation/iocs/by-kv/relationships?kind=" + urlQuery(kind) + "&value=" + urlQuery(value)
        var mu sync.Mutex
        results, _ := agg.FanOut(ctx, func(ctx context.Context, peer federation.Peer) error {
            var rows []db.IOCRelationshipPaired
            if err := agg.FetchJSON(ctx, peer, path, &rows); err != nil {
                return err
            }
            mu.Lock()
            defer mu.Unlock()
            contribs = append(contribs, contribRels{cp: peerCPSource(peer), rows: rows})
            return nil
        })
        if pErr := federation.AnyError(results); pErr != nil {
            w.Header().Set("X-Okesu-Federation-Warning", pErr.Error())
        }
        merged := MergeIOCRelationships(contribs)
        w.Header().Set("Content-Type", "application/json")
        _ = json.NewEncoder(w).Encode(merged)
    }
}

// selfCPSource builds a CPSourceRef for the local CP from store metadata.
func selfCPSource(store *db.Store) (*CPSourceRef, error) {
    meta, err := store.CPMeta()
    if err != nil {
        return nil, err
    }
    return &CPSourceRef{
        InstanceID:  meta.InstanceID,
        DisplayName: meta.DisplayName,
        Region:      meta.Region,
    }, nil
}

// peerCPSource builds a CPSourceRef from a federation peer snapshot.
func peerCPSource(peer federation.Peer) *CPSourceRef {
    return &CPSourceRef{
        InstanceID:  peer.Snapshot.InstanceID,
        DisplayName: peer.Snapshot.DisplayName,
        Region:      peer.Snapshot.Region,
    }
}

// urlQuery escapes a query-string value. Inputs are validated kind/value
// strings but we use the standard library's encoder anyway since
// values can contain `&`, `=`, or `+` (think IOC values that are URLs).
func urlQuery(s string) string {
    return url.QueryEscape(s)
}
```

(Add `"net/url"` to the imports at the top of `federation_iocs.go`.)

(Also: confirm `db.CPMeta` struct field names match `InstanceID`, `DisplayName`, `Region`. If they differ, adjust `selfCPSource` accordingly. Read `controlplane/db/cp_meta.go` to confirm before writing.)

- [ ] **Step D1.3: Tests for the federated handlers**

`controlplane/api/federation_iocs_test.go`:

```go
package api

import (
    "encoding/json"
    "net/http/httptest"
    "testing"

    "github.com/section9labs/okesu/controlplane/db"
)

// Local-only path: no peers configured. The federated handler should
// return the same shape as the local handler with a single CPSource
// per row (the local CP).
func TestFederatedListIOCs_LocalOnly(t *testing.T) {
    st := newTestStore(t)
    st.UpsertIOC(&db.IOCUpsert{Kind: "sha256", Value: "abc", NormalizedValue: "abc", Source: "catalog", Name: "test"})

    rec := httptest.NewRecorder()
    req := httptest.NewRequest("GET", "/api/iocs", nil)
    FederatedListIOCs(st, nilAggregator(t)).ServeHTTP(rec, req)
    if rec.Code != 200 {
        t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
    }
    var rows []FederatedIOCRecord
    if err := json.NewDecoder(rec.Body).Decode(&rows); err != nil {
        t.Fatalf("decode: %v", err)
    }
    if len(rows) != 1 {
        t.Fatalf("expected 1 row; got %d", len(rows))
    }
    if rows[0].Name != "test" {
        t.Errorf("Name = %q", rows[0].Name)
    }
    if len(rows[0].CPSources) != 1 {
        t.Errorf("expected 1 CPSource; got %d", len(rows[0].CPSources))
    }
}

// Build a *federation.Aggregator with no peers — so FanOut is a no-op
// but the handler still runs the local query + merge path.
func nilAggregator(t *testing.T) *federation.Aggregator {
    // The aggregator's FanOut iterates HealthyPeers, which queries the
    // peers table. An empty store produces zero peers, which is what
    // we want for a local-only test. Real federation_reads_test.go uses
    // the same approach — read it for the canonical signature.
    return federation.NewAggregator(/* the test's store */)  // adjust to the real constructor
}
```

NOTE: read `controlplane/api/federation_reads_test.go` to find the canonical pattern for testing federated handlers (mock peers via httptest.Server, etc.). Match that pattern. The skeleton above is a starting point.

The full handler tests should cover, per the spec:

- No peers (local-only) — done above
- One healthy peer with overlapping (kind, value) — assert dedup + sum
- One peer fails — assert `X-Okesu-Federation-Warning` header set, local rows still returned

- [ ] **Step D1.4: Run + commit**

```bash
go test ./controlplane/api/ -run "TestFederatedListIOCs|TestFederationIOCs|TestFederatedGetIOC|TestFederatedListIOCObservations|TestFederatedListIOCRelationships" -v -count=1
go vet ./controlplane/api/
```

All pass. Then:

```bash
pwd && git status
git add controlplane/api/federation_iocs.go controlplane/api/federation_iocs_test.go
git commit -m "api(federation/iocs): child exports + parent FederatedX wrappers"
```

---

## Task Group E — Server.go wiring

### Task E1: Mount routes

**File:** `controlplane/server.go`

- [ ] **Step E1.1: Find the existing IOC route block in the viewer+ group**

Look for the lines mounting `/api/iocs`, `/api/iocs/{id}`, `/api/iocs/{id}/observations`, `/api/iocs/{id}/relationships` (around lines 633-650 based on earlier exploration). Replace those single-CP handler mounts with the federated variants:

```go
// Catalog IOC routes — federated where federation is on, local otherwise.
// Legacy id-based detail routes stay mounted unchanged for external
// integrations that still pass int row ids; the UI uses the by-kv variants.
r.Get("/api/iocs", api.FederatedListIOCs(s.store, s.fedAgg))
r.Get("/api/iocs/by-kv", api.FederatedGetIOCByKV(s.store, s.fedAgg))
r.Get("/api/iocs/by-kv/observations", api.FederatedListIOCObservationsByKV(s.store, s.fedAgg))
r.Get("/api/iocs/by-kv/relationships", api.FederatedListIOCRelationshipsByKV(s.store, s.fedAgg))

// Legacy local-only routes — kept for external integrations.
r.Get("/api/iocs/cross-cp-patterns", api.ListCrossCPPatternsHandler(s.store))
r.Get("/api/iocs/{id}", api.GetIOCHandler(s.store))
r.Get("/api/iocs/{id}/observations", api.ListIOCObservationsHandler(s.store))
r.Get("/api/iocs/{id}/relationships", api.ListIOCRelationshipsHandler(s.store))
r.Get("/api/stix2/iocs", api.STIX2ExportHandler(s.store))
r.Get("/api/catalog/yara-rules.yar", api.YARARulesYarHandler(s.store))
```

(Cross-CP-patterns, STIX, YARA endpoints stay local-only — federation for those is a future scope.)

- [ ] **Step E1.2: Mount child-side exports under the federation group**

Find the existing federation export route group (search for `r.Get("/api/v1/federation/findings"` to locate it). Add alongside:

```go
r.Get("/api/v1/federation/iocs", api.FederationIOCs(s.store))
r.Get("/api/v1/federation/iocs/by-kv", api.FederationIOCByKV(s.store))
r.Get("/api/v1/federation/iocs/by-kv/observations", api.FederationIOCObservationsByKV(s.store))
r.Get("/api/v1/federation/iocs/by-kv/relationships", api.FederationIOCRelationshipsByKV(s.store))
```

These are inside the `requireFederationToken`-wrapped block, same as the existing `/api/v1/federation/findings` etc.

- [ ] **Step E1.3: Build + commit**

```bash
go build ./controlplane/...
go test ./controlplane/api/ -count=1
```

All clean. Then:

```bash
pwd && git status
git add controlplane/server.go
git commit -m "server: mount federated catalog routes + child-side /api/v1/federation/iocs"
```

---

## Task Group F — Frontend api.ts

### Task F1: Types + helpers

**File:** `web/src/api.ts`

- [ ] **Step F1.1: Install web deps if not present**

```bash
cd web && (test -d node_modules || npm install --silent) && cd ..
```

- [ ] **Step F1.2: Add CPSourceRef + federated types**

Find the `IOCRecord` type block in `web/src/api.ts` (around line 302). Find or import `CPSourceRef` — it's used by federated finding types; check `web/src/api.ts` for `CPSourceRef` or `cp_sources`. If the type already exists, reuse it. Otherwise add:

```ts
// CPSourceRef identifies which CP a row originated from in federated
// responses. Mirrors controlplane/api.CPSourceRef.
export interface CPSourceRef {
  instance_id: string;
  display_name?: string;
  region?: string;
}
```

After IOCRecord, add:

```ts
// FederatedIOCRecord is the merged wire shape returned by federated
// catalog endpoints — IOCRecord fields plus the list of CPs that
// contribute to this merged row.
export interface FederatedIOCRecord extends IOCRecord {
  cp_sources: CPSourceRef[];
}

// FederatedIOCObservation tags each observation row with its origin CP.
export interface FederatedIOCObservation extends IOCObservation {
  cp_source: CPSourceRef;
}

// FederatedIOCRelationship: deduped tuple-based edge with origin CPs.
// Replaces the int-id-based IOCRelationship for federation paths.
export interface FederatedIOCRelationship {
  SubjectKind: string;
  SubjectValue: string;
  Predicate: string;
  ObjectKind: string;
  ObjectValue: string;
  Source: string;
  Confidence: string;
  cp_sources: CPSourceRef[];
}
```

- [ ] **Step F1.3: Update iocs() return type and add new helpers**

Update the existing `iocs:` block to return `FederatedIOCRecord[]`:

```ts
  iocs: (filter: { findingID?: number; kind?: string; source?: string; q?: string } = {}) => {
    const p = new URLSearchParams();
    if (filter.findingID) p.set('finding_id', String(filter.findingID));
    if (filter.kind)      p.set('kind', filter.kind);
    if (filter.source)    p.set('source', filter.source);
    if (filter.q)         p.set('q', filter.q);
    const qs = p.toString();
    return request<FederatedIOCRecord[]>(`/api/iocs${qs ? '?' + qs : ''}`);
  },
```

Replace the old `ioc(id)`, `iocObservations(id)`, `iocRelationships(id)` helpers with kv-based variants:

```ts
  iocByKV: (kind: string, value: string) =>
    request<FederatedIOCRecord>(`/api/iocs/by-kv?kind=${encodeURIComponent(kind)}&value=${encodeURIComponent(value)}`),

  iocObservationsByKV: (kind: string, value: string) =>
    request<FederatedIOCObservation[]>(`/api/iocs/by-kv/observations?kind=${encodeURIComponent(kind)}&value=${encodeURIComponent(value)}`),

  iocRelationshipsByKV: (kind: string, value: string) =>
    request<FederatedIOCRelationship[]>(`/api/iocs/by-kv/relationships?kind=${encodeURIComponent(kind)}&value=${encodeURIComponent(value)}`),
```

(The old `ioc(id)`, `iocObservations(id)`, `iocRelationships(id)` helpers can stay if other consumers reference them; check with `grep -rn "api\.ioc\b\|api\.iocObservations\b\|api\.iocRelationships\b" web/src` before removing. The Catalog page is the only consumer per Phase 22.6's intent — safe to delete.)

- [ ] **Step F1.4: Build + commit**

```bash
cd web && npm run build && cd ..
```

Build passes. Then:

```bash
pwd && git status
git add web/src/api.ts
git commit -m "web(api): federated catalog types + iocByKV/iocObservationsByKV/iocRelationshipsByKV"
```

---

## Task Group G — Catalog list page

### Task G1: Add CPs column + route change

**Files:**
- Modify: `web/src/pages/Catalog.tsx`
- Modify: `web/src/App.tsx`

- [ ] **Step G1.1: Update Catalog.tsx for federated rows**

Read the current `web/src/pages/Catalog.tsx`. Two functional changes:

1. Replace `IOCRecord` references with `FederatedIOCRecord` (the response type now includes `cp_sources`)
2. Add a new "CPs" column rendering `cp_sources` as small chips

3. Change row click to navigate to `/catalog/${kind}/${encodeURIComponent(normalized_value)}` instead of `/catalog/${id}`

4. Add federation warning banner: read the `X-Okesu-Federation-Warning` response header (the `request<T>` helper would need to return headers too, OR fetch it separately — easiest is a separate `fetch` for the header)

For simplicity, pass on the warning banner in this task and add it in a later step; for now just focus on the row shape and route.

Apply these targeted edits:

```ts
import { api, type FederatedIOCRecord } from '../api';
```

```ts
const [rows, setRows] = useState<FederatedIOCRecord[] | null>(null);
```

Add a column header in the table:

```tsx
<th className="px-3 py-2 font-medium hidden md:table-cell">CPs</th>
```

Add the matching cell in each row:

```tsx
<td className="px-3 py-2 hidden md:table-cell">
  <CPChips sources={r.cp_sources} />
</td>
```

Replace the row's `onClick` with the kv-based navigation:

```tsx
onClick={() => setDrawerIOC(r)}
// no change to drawer; the drawer's onOpenFullPage uses the row's kv
```

Update `onOpenFullPage` to use kind+normalized_value:

```tsx
onOpenFullPage={() => navigate(`/catalog/${drawerIOC.Kind}/${encodeURIComponent(drawerIOC.NormalizedValue)}`)}
```

Add the `CPChips` helper near the bottom:

```tsx
function CPChips({ sources }: { sources: CPSourceRef[] | undefined }) {
  if (!sources || sources.length === 0) return <span className="text-ink-mute">—</span>;
  return (
    <div className="flex flex-wrap gap-1">
      {sources.map(s => (
        <span key={s.instance_id}
              className="px-1.5 py-0.5 text-[10px] rounded bg-slate-100 text-ink-dim ring-1 ring-border"
              title={s.region || s.display_name || s.instance_id}>
          {s.display_name || s.instance_id}
        </span>
      ))}
    </div>
  );
}
```

Update the import line for `CPSourceRef`:

```ts
import { api, type FederatedIOCRecord, type CPSourceRef } from '../api';
```

- [ ] **Step G1.2: Update App.tsx route**

Change:

```tsx
<Route path="/catalog/:id" element={<CatalogDetailPage />} />
```

to:

```tsx
<Route path="/catalog/:kind/:value" element={<CatalogDetailPage />} />
```

- [ ] **Step G1.3: Build + commit**

```bash
cd web && npm run build && cd ..
```

Build passes. Then:

```bash
pwd && git status
git add web/src/pages/Catalog.tsx web/src/App.tsx
git commit -m "web(catalog): CPs column + /catalog/:kind/:value route"
```

---

## Task Group H — CatalogDrawer

### Task H1: Show CP chips + pivot link

**File:** `web/src/components/CatalogDrawer.tsx`

- [ ] **Step H1.1: Update drawer**

Read the current `web/src/components/CatalogDrawer.tsx`. Two changes:

1. Replace `IOCRecord` with `FederatedIOCRecord` so the drawer can render `cp_sources`
2. Add a CPs section near the top (above Identity)

```ts
import { api, type FederatedIOCRecord, type CPSourceRef } from '../api';
```

```ts
ioc: FederatedIOCRecord;
```

Render a CPs row immediately under the kind/name/value header block:

```tsx
{ioc.cp_sources && ioc.cp_sources.length > 0 && (
  <div className="flex flex-wrap gap-1 mb-3">
    {ioc.cp_sources.map(s => (
      <span key={s.instance_id}
            className="px-1.5 py-0.5 text-[10px] rounded bg-slate-100 text-ink-dim ring-1 ring-border"
            title={s.region || s.display_name || s.instance_id}>
        {s.display_name || s.instance_id}
      </span>
    ))}
  </div>
)}
```

The relationship-count fetch needs to use `iocRelationshipsByKV` instead of `iocRelationships`:

```ts
api.iocRelationshipsByKV(ioc.Kind, ioc.NormalizedValue)
   .then(rels => setRelCount(rels?.length ?? 0))
   .catch(() => setRelCount(0));
```

- [ ] **Step H1.2: Build + commit**

```bash
cd web && npm run build && cd ..
git add web/src/components/CatalogDrawer.tsx
git commit -m "web(catalog/drawer): render CP chips + use iocRelationshipsByKV"
```

---

## Task Group I — CatalogDetail page

### Task I1: Pivot to kv routing + render federated shapes

**File:** `web/src/pages/CatalogDetail.tsx`

- [ ] **Step I1.1: Update detail page**

Replace the three `useEffect` fetches and the route param parsing:

```ts
import { useParams, Link, useNavigate } from 'react-router-dom';
import {
  api,
  type FederatedIOCRecord,
  type FederatedIOCObservation,
  type FederatedIOCRelationship,
  type CPSourceRef,
} from '../api';
```

```ts
const { kind, value } = useParams();
const decodedValue = value ? decodeURIComponent(value) : '';
const [ioc, setIOC] = useState<FederatedIOCRecord | null>(null);
const [observations, setObservations] = useState<FederatedIOCObservation[] | null>(null);
const [relationships, setRelationships] = useState<FederatedIOCRelationship[] | null>(null);
```

Replace the three fetch effects:

```ts
useEffect(() => {
  if (!kind || !decodedValue) { setError('bad kv'); return; }
  api.iocByKV(kind, decodedValue).then(setIOC).catch(e => setError(String(e)));
}, [kind, decodedValue]);

useEffect(() => {
  if (!kind || !decodedValue || tab !== 'observations' || observations !== null) return;
  api.iocObservationsByKV(kind, decodedValue).then(r => setObservations(r ?? [])).catch(() => setObservations([]));
}, [kind, decodedValue, tab, observations]);

useEffect(() => {
  if (!kind || !decodedValue || tab !== 'relationships' || relationships !== null) return;
  api.iocRelationshipsByKV(kind, decodedValue).then(r => setRelationships(r ?? [])).catch(() => setRelationships([]));
}, [kind, decodedValue, tab, relationships]);
```

Render CP chips in the header (immediately under the title block):

```tsx
{ioc.cp_sources && ioc.cp_sources.length > 0 && (
  <div className="flex flex-wrap gap-1 mt-1 ml-9">
    <span className="text-[10px] uppercase tracking-wider text-ink-mute mr-1">Seen on</span>
    {ioc.cp_sources.map(s => (
      <span key={s.instance_id}
            className="px-1.5 py-0.5 text-[10px] rounded bg-brand-50 text-brand-700 ring-1 ring-brand-100"
            title={s.region || s.display_name || s.instance_id}>
        {s.display_name || s.instance_id}
      </span>
    ))}
  </div>
)}
```

Update the **Observations tab** to add a "CP" column and use `cp_source`:

```tsx
<thead>
  <tr className="text-left text-[11px] uppercase tracking-wide text-ink-mute bg-slate-50 border-b border-border">
    <th className="px-3 py-2 font-medium w-24">Finding</th>
    <th className="px-3 py-2 font-medium w-24">Run</th>
    <th className="px-3 py-2 font-medium">Host</th>
    <th className="px-3 py-2 font-medium w-32">CP</th>
    <th className="px-3 py-2 font-medium w-44">Observed at</th>
  </tr>
</thead>
```

```tsx
<td className="px-3 py-2 text-ink-dim text-xs">
  {o.cp_source?.display_name || o.cp_source?.instance_id || '—'}
</td>
```

Update the **Relationships tab** to render kv-pair endpoints instead of `#42`:

```tsx
<thead>
  <tr className="text-left text-[11px] uppercase tracking-wide text-ink-mute bg-slate-50 border-b border-border">
    <th className="px-3 py-2 font-medium w-20">Direction</th>
    <th className="px-3 py-2 font-medium">Subject</th>
    <th className="px-3 py-2 font-medium w-32">Predicate</th>
    <th className="px-3 py-2 font-medium">Object</th>
    <th className="px-3 py-2 font-medium w-32">CPs</th>
  </tr>
</thead>
```

```tsx
{rows.map((rel, i) => {
  const subjectIsThis = rel.SubjectKind === kind && rel.SubjectValue === decodedValue;
  const otherKind = subjectIsThis ? rel.ObjectKind : rel.SubjectKind;
  const otherValue = subjectIsThis ? rel.ObjectValue : rel.SubjectValue;
  return (
    <tr key={i}
        className="border-b border-border last:border-b-0 hover:bg-slate-50/60 cursor-pointer"
        onClick={() => navigate(`/catalog/${otherKind}/${encodeURIComponent(otherValue)}`)}>
      <td className="px-3 py-2 text-ink-mute text-xs">{subjectIsThis ? '→ out' : '← in'}</td>
      <td className="px-3 py-2 text-ink font-mono text-[11px]">
        <span className="text-ink-dim">{rel.SubjectKind}</span> {rel.SubjectValue}
        {subjectIsThis && <span className="text-ink-mute"> (this)</span>}
      </td>
      <td className="px-3 py-2 font-mono text-[11px] text-ink">{rel.Predicate}</td>
      <td className="px-3 py-2 text-ink font-mono text-[11px]">
        <span className="text-ink-dim">{rel.ObjectKind}</span> {rel.ObjectValue}
        {!subjectIsThis && <span className="text-ink-mute"> (this)</span>}
      </td>
      <td className="px-3 py-2 text-ink-dim text-xs">
        {rel.cp_sources?.map(s => s.display_name || s.instance_id).join(', ') || '—'}
      </td>
    </tr>
  );
})}
```

- [ ] **Step I1.2: Build + commit**

```bash
cd web && npm run build && cd ..
git add web/src/pages/CatalogDetail.tsx
git commit -m "web(catalog/detail): pivot to /catalog/:kind/:value + render federated shapes"
```

---

## Task Group J — Federation warning banner

### Task J1: Surface X-Okesu-Federation-Warning header

The Catalog list page should show a yellow banner when the parent's federated handler couldn't reach all peers.

**Files:**
- Modify: `web/src/api.ts` — extend `request<T>` to optionally return headers, or add a `requestWithHeaders<T>` variant
- Modify: `web/src/pages/Catalog.tsx` — read the warning header, render a `FederationWarningBanner`
- Optional: `web/src/components/FederationWarningBanner.tsx` — small reusable component

- [ ] **Step J1.1: Check whether other federated pages already do this**

```bash
grep -rn "X-Okesu-Federation-Warning\|federation-warning" web/src/
```

If there's a pattern in use (e.g., Findings shows a banner), match it. If not, add a simple one.

- [ ] **Step J1.2: Plan the simplest implementation**

If `request<T>` is private and tightly scoped: the cheapest route is for the Catalog page to use `fetch` directly for one specific call (the list endpoint) so it can read headers. This avoids touching the shared API client surface.

```ts
// In Catalog.tsx, replace the api.iocs(...) call with:
const url = '/api/iocs?' + params.toString();
const res = await fetch(url, { credentials: 'same-origin' });
if (!res.ok) throw new Error(await res.text());
const warning = res.headers.get('X-Okesu-Federation-Warning');
const rows = await res.json();
setRows(rows ?? []);
setWarning(warning);
```

This keeps the shared `api.ts` clean and only the Catalog page knows about the warning header. If the same pattern is needed elsewhere later, factor a shared helper.

- [ ] **Step J1.3: Add a small banner**

In `Catalog.tsx`, above the table:

```tsx
{warning && (
  <div className="text-xs text-yellow-700 bg-yellow-50 border border-yellow-200 px-3 py-2 rounded-md">
    Showing partial results — federated peers reported errors: {warning}
  </div>
)}
```

- [ ] **Step J1.4: Build + commit**

```bash
cd web && npm run build && cd ..
git add web/src/pages/Catalog.tsx
git commit -m "web(catalog): surface X-Okesu-Federation-Warning as yellow banner"
```

---

## Task Group Z — Docs + final test sweep + PR body

### Task Z1: Architecture doc

**File:** `docs/architecture.md`

- [ ] **Step Z1.1: Append a Catalog federation section**

Append after the existing "Catalog UI" section:

```markdown
## Catalog federation

Every Catalog API endpoint now federates via the
`controlplane/api/federation_reads.go` FanOut+merge pattern. The parent
CP's `/catalog` page shows merged rows from itself plus every healthy
child:

- **List**: dedup by `(kind, normalized_value)`. For text metadata,
  first non-empty wins iterating by ascending CP-ID with catalog-source
  preferred over observed. `observation_count` sums; `first_seen` =
  min, `last_seen` = max. Each merged row carries
  `cp_sources []CPSourceRef`.
- **Detail / observations / relationships**: fetched via
  `(kind, normalized_value)` query params; merged by the same pattern.
  Relationships dedup by tuple `(subject_kind, subject_value, predicate,
  object_kind, object_value)` since per-CP int IDs aren't comparable.

Child-side exports live at `/api/v1/federation/iocs*` behind
`requireFederationToken`. The legacy id-based local routes
(`/api/iocs/{id}`, `/{id}/observations`, `/{id}/relationships`) stay
mounted for external integrations; the UI uses the by-kv variants.

Build matrix unchanged: `CGO_ENABLED=0` everywhere.
```

- [ ] **Step Z1.2: Commit**

```bash
git add docs/architecture.md
git commit -m "docs(architecture): catalog federation"
```

---

### Task Z2: Full sweep

```bash
go test ./... 2>&1 | tail -50
cd web && npm run build && cd ..
```

Both must pass. Pre-existing UI dist embed false positive (`controlplane/ui/static.go`) is the same as prior phases; ignore. Anything else: STOP and report.

---

### Task Z3: PR body

**Create:** `docs/superpowers/plans/2026-04-30-catalog-federation-pr-body.md`

```markdown
## Summary

Make every Catalog API endpoint federation-aware. Parent CP's
`/catalog` page merges rows from itself plus every healthy child;
detail/observations/relationships likewise.

- Backend: four new federated wrappers (`FederatedListIOCs`,
  `FederatedGetIOCByKV`, `FederatedListIOCObservationsByKV`,
  `FederatedListIOCRelationshipsByKV`) in
  `controlplane/api/federation_iocs.go`.
- Backend: four new child-side exports under
  `/api/v1/federation/iocs*` behind `requireFederationToken`.
- Backend: three pure merge functions (`MergeIOCs`,
  `MergeIOCObservations`, `MergeIOCRelationships`) — heavily
  unit-tested with deterministic CP-ID iteration.
- Backend: three new local handlers `GetIOCByKVHandler`,
  `ListIOCObservationsByKVHandler`,
  `ListIOCRelationshipsByKVHandler` for the kv-shaped detail
  endpoints. The legacy id-based routes stay mounted for external
  integrations.
- Frontend: `/catalog/:id` → `/catalog/:kind/:value` with URL-encoded
  value. Catalog list and detail pages render `cp_sources` as chips;
  observations table adds a CP column; relationships render kv pairs
  with click-to-pivot.
- Frontend: `X-Okesu-Federation-Warning` header surfaces as a yellow
  banner above the list when a peer fails.

## Test plan

Lab smoke (post-merge, operator-side):

- [ ] `go test ./...` passes
- [ ] `npm run build` clean in `web/`
- [ ] On a parent CP federated to a child: drop the same yara_rule
      into both catalog directories; SIGHUP; navigate to `/catalog`;
      verify a single deduped row with both CP names in the chips,
      summed observation_count, max(last_seen).
- [ ] Observe an IOC on each CP; navigate to its
      `/catalog/{kind}/{value}` detail page; verify the Observations
      tab shows both observations with their per-CP origin labels.
- [ ] Add a `resolves-to` relationship on each CP between the same
      `(domain → ipv4)` pair; verify the Relationships tab shows one
      deduped tuple with both CPs in the CPs column.
- [ ] Stop a child CP; reload `/catalog`; verify the yellow banner
      appears and the list still renders the parent's rows.

## Files

- New endpoints (parent-side):
  `GET /api/iocs/by-kv?kind=&value=`,
  `GET /api/iocs/by-kv/observations?kind=&value=`,
  `GET /api/iocs/by-kv/relationships?kind=&value=`.
- New endpoints (child-side):
  `GET /api/v1/federation/iocs`,
  `GET /api/v1/federation/iocs/by-kv`,
  `GET /api/v1/federation/iocs/by-kv/observations`,
  `GET /api/v1/federation/iocs/by-kv/relationships`.
- `GET /api/iocs` now federated (was local-only).
- New page route `/catalog/:kind/:value` (replaces `/catalog/:id`).

## Spec / plan

- Spec: `docs/superpowers/specs/2026-04-30-catalog-federation-design.md`
- Plan: `docs/superpowers/plans/2026-04-30-catalog-federation.md`

## Open follow-ups

- CP-chip click filtering on the list page
- Aggregator-backed snapshot caching for IOCs (avoid per-page-load fan-out)
- Catalog YAML federation propagation parent↔child
- Per-CP observation count breakdown in the detail header
- Unify the X-Okesu-Federation-Warning banner pattern across pages (build a `<FederationWarningBanner />`)
```

- [ ] **Step Z3.1: Commit**

```bash
git add docs/superpowers/plans/2026-04-30-catalog-federation-pr-body.md
git commit -m "docs: catalog federation PR body"
```

---

### Task Z4: DO NOT push or open the PR

The controller (the human operator) handles `git push` and `gh pr create`.

---

## Self-review checklist

- [ ] All four federated endpoints have parent-side handler + child-side export + merge function (Groups C/D)
- [ ] Merge semantics deterministic: ascending CP-ID iteration order, catalog-source priority within each CP (Group C)
- [ ] Observations preserve per-row CPSource tagging (no dedup); relationships dedup by tuple (Group C)
- [ ] Wire shapes match the spec: `cp_sources []CPSourceRef` on records, `cp_source CPSourceRef` on observations, kv-pair endpoints on relationships (Groups C/F)
- [ ] Frontend route changes from `:id` to `:kind/:value` (Groups G/I)
- [ ] CP chips visible on list, drawer header, detail header, observations CP column, relationships CPs column (Groups G/H/I)
- [ ] `X-Okesu-Federation-Warning` surfaces as a yellow banner (Group J)
- [ ] No build matrix changes — `grep -n "CGO_ENABLED" Makefile` should still show only `=0`
- [ ] Legacy `/api/iocs/{id}` routes stay mounted for external integrations (Group E)
- [ ] No placeholders in any task; every step shows literal code/command
