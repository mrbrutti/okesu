# Case Phases Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a "phases" lane on the investigation timeline where operators drag to mark named time ranges (e.g. "Initial detection 14:00–14:35"), persisted as `investigation_phases` rows.

**Architecture:** New table + 4 CRUD endpoints + federation pair. Frontend adds a sibling `<div>` lane (NOT inside the SVG) above the timeline canvas, with rubber-band drag to create phases, click-to-rename, hover-to-delete. Pure stacker handles overlapping phases via greedy first-fit row assignment. Colors are deterministic from `hash(name)`.

**Tech Stack:** Go (chi, sqlite/postgres) backend; React 18 + TypeScript + Vitest frontend. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-05-02-case-phases-design.md`

**Branch:** `feat/case-phases` (current worktree)

**Migration number:** 059 (latest existing tip is `058_investigation_note_drafts.sql`)

---

## File Structure

| File | Status | Responsibility |
|---|---|---|
| `controlplane/db/migrations/sqlite/059_investigation_phases.sql` | NEW | sqlite migration |
| `controlplane/db/migrations/postgres/059_investigation_phases.sql` | NEW | postgres twin |
| `controlplane/db/investigation_phases.go` | NEW | `InvestigationPhase` type, `Insert/List/UpdateName/Delete` Store methods |
| `controlplane/db/investigation_phases_test.go` | NEW | DB tests |
| `controlplane/db/store.go` | MODIFY | Register the new migration via `//go:embed` for both dialects |
| `controlplane/api/investigation_phases.go` | NEW | HTTP handlers (4 local + 4 federation parent + 4 federation child = 12 functions) |
| `controlplane/api/investigation_phases_test.go` | NEW | Handler tests |
| `controlplane/server.go` | MODIFY | Register 8 new routes (4 parent-side + 4 child-side) |
| `web/src/api.ts` | MODIFY | Add `InvestigationPhase` type + `api.investigations.phases.{list,create,update,delete}` |
| `web/src/components/investigations/timeline/types.ts` | MODIFY | Add `'phases'` to `TimelineLane` and `ALL_LANES` (front of array, opt-in) |
| `web/src/components/investigations/timeline/scale.ts` | MODIFY | Add `xToT(x, tMin, tMax, width)` |
| `web/src/components/investigations/timeline/scale.test.ts` | MODIFY | Round-trip test for `xToT` |
| `web/src/components/investigations/timeline/phasesLayer.ts` | NEW | Pure stacker for overlapping phases |
| `web/src/components/investigations/timeline/phasesLayer.test.ts` | NEW | Unit tests |
| `web/src/components/investigations/PhasesLane.tsx` | NEW | Lane component (drag gesture, pill rendering, inline rename, hover delete) |
| `web/src/components/investigations/PhasesLane.test.tsx` | NEW | Component tests |
| `web/src/components/investigations/CaseTimeline.tsx` | MODIFY | Lazy phases-fetch effect + mount `<PhasesLane>` |
| `web/src/components/investigations/CaseTimeline.test.tsx` | MODIFY | Test the lazy-fetch on first lane toggle |
| `docs/architecture.md` | MODIFY | New section under "Investigation Overview" |

---

## Task 1: DB layer — migration + Store CRUD + tests

**Files:**
- Create: `controlplane/db/migrations/sqlite/059_investigation_phases.sql`
- Create: `controlplane/db/migrations/postgres/059_investigation_phases.sql`
- Create: `controlplane/db/investigation_phases.go`
- Create: `controlplane/db/investigation_phases_test.go`
- Modify: `controlplane/db/store.go` (register the new migration via `//go:embed`)

- [ ] **Step 1: Write the sqlite migration**

Create `controlplane/db/migrations/sqlite/059_investigation_phases.sql`:

```sql
-- Operator-defined phases of a case (e.g. "Initial detection 14:00–14:35",
-- "Containment 14:35–16:10"). Free-form name; no category column.
-- Color in the UI is derived from hash(name).
CREATE TABLE investigation_phases (
  id               INTEGER PRIMARY KEY AUTOINCREMENT,
  investigation_id INTEGER NOT NULL REFERENCES investigations(id) ON DELETE CASCADE,
  name             TEXT    NOT NULL,
  start_ts         INTEGER NOT NULL,
  end_ts           INTEGER NOT NULL,
  created_by       TEXT,
  created_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  CHECK (end_ts >= start_ts),
  CHECK (length(name) > 0)
);
CREATE INDEX investigation_phases_inv ON investigation_phases (investigation_id);
```

- [ ] **Step 2: Write the postgres migration**

Create `controlplane/db/migrations/postgres/059_investigation_phases.sql`:

```sql
-- Operator-defined phases of a case (e.g. "Initial detection 14:00–14:35",
-- "Containment 14:35–16:10"). Free-form name; no category column.
-- Color in the UI is derived from hash(name).
CREATE TABLE investigation_phases (
  id               BIGSERIAL PRIMARY KEY,
  investigation_id BIGINT NOT NULL REFERENCES investigations(id) ON DELETE CASCADE,
  name             TEXT   NOT NULL,
  start_ts         BIGINT NOT NULL,
  end_ts           BIGINT NOT NULL,
  created_by       TEXT,
  created_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  CHECK (end_ts >= start_ts),
  CHECK (length(name) > 0)
);
CREATE INDEX investigation_phases_inv ON investigation_phases (investigation_id);
```

- [ ] **Step 3: Register the migration in `controlplane/db/store.go`**

The codebase doesn't auto-discover migration files; each migration N is registered explicitly via `//go:embed` for both dialects. Find the existing block that registers migration 058 (added by PR #126) and add a sibling pair immediately after it.

For sqlite (around line 240-260):

```go
//go:embed migrations/sqlite/059_investigation_phases.sql
var sqliteM059 string
```

Add `sqliteM059` to the sqlite migrations slice.

For postgres (around line 430-450):

```go
//go:embed migrations/postgres/059_investigation_phases.sql
var pgM059 string
```

Add `pgM059` to the postgres migrations slice.

Use `grep -n "sqliteM058\|pgM058" controlplane/db/store.go` to find the exact insertion points. The pattern is one-line `//go:embed` + one-line `var sqliteM0NN string` + one-line append in each slice.

- [ ] **Step 4: Write the failing CRUD tests**

Create `controlplane/db/investigation_phases_test.go`:

```go
package db

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
)

func TestInvestigationPhase_InsertGet(t *testing.T) {
	s := openTempStore(t)
	invID, err := s.CreateInvestigation(&InvestigationInsert{Title: "case"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id, err := s.InsertInvestigationPhase(&InvestigationPhaseInsert{
		InvestigationID: invID,
		Name:            "Initial detection",
		StartTs:         1714559400000,
		EndTs:           1714561500000,
		CreatedBy:       "alice@x",
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if id == 0 {
		t.Errorf("got id=0, want non-zero")
	}
	rows, err := s.ListInvestigationPhases(invID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}
	got := rows[0]
	if got.Name != "Initial detection" {
		t.Errorf("name = %q, want %q", got.Name, "Initial detection")
	}
	if got.StartTs != 1714559400000 || got.EndTs != 1714561500000 {
		t.Errorf("ts = (%d, %d), want (1714559400000, 1714561500000)", got.StartTs, got.EndTs)
	}
	if !got.CreatedBy.Valid || got.CreatedBy.String != "alice@x" {
		t.Errorf("created_by = %v, want alice@x", got.CreatedBy)
	}
}

func TestInvestigationPhase_ListSortedByStartTs(t *testing.T) {
	s := openTempStore(t)
	invID, _ := s.CreateInvestigation(&InvestigationInsert{Title: "case"})
	_, _ = s.InsertInvestigationPhase(&InvestigationPhaseInsert{
		InvestigationID: invID, Name: "B-mid", StartTs: 2000, EndTs: 3000, CreatedBy: "x",
	})
	_, _ = s.InsertInvestigationPhase(&InvestigationPhaseInsert{
		InvestigationID: invID, Name: "A-early", StartTs: 1000, EndTs: 2000, CreatedBy: "x",
	})
	_, _ = s.InsertInvestigationPhase(&InvestigationPhaseInsert{
		InvestigationID: invID, Name: "C-late", StartTs: 3000, EndTs: 4000, CreatedBy: "x",
	})
	rows, err := s.ListInvestigationPhases(invID)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"A-early", "B-mid", "C-late"}
	for i, r := range rows {
		if r.Name != want[i] {
			t.Errorf("rows[%d].Name = %q, want %q", i, r.Name, want[i])
		}
	}
}

func TestInvestigationPhase_UpdateName(t *testing.T) {
	s := openTempStore(t)
	invID, _ := s.CreateInvestigation(&InvestigationInsert{Title: "case"})
	id, _ := s.InsertInvestigationPhase(&InvestigationPhaseInsert{
		InvestigationID: invID, Name: "old", StartTs: 1, EndTs: 2, CreatedBy: "x",
	})
	if err := s.UpdateInvestigationPhaseName(invID, id, "new"); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.ListInvestigationPhases(invID)
	if rows[0].Name != "new" {
		t.Errorf("name = %q, want %q", rows[0].Name, "new")
	}
}

func TestInvestigationPhase_Delete(t *testing.T) {
	s := openTempStore(t)
	invID, _ := s.CreateInvestigation(&InvestigationInsert{Title: "case"})
	id, _ := s.InsertInvestigationPhase(&InvestigationPhaseInsert{
		InvestigationID: invID, Name: "x", StartTs: 1, EndTs: 2, CreatedBy: "x",
	})
	if err := s.DeleteInvestigationPhase(invID, id); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.ListInvestigationPhases(invID)
	if len(rows) != 0 {
		t.Errorf("after delete: len(rows) = %d, want 0", len(rows))
	}
	// Idempotent: delete again returns nil
	if err := s.DeleteInvestigationPhase(invID, id); err != nil {
		t.Errorf("second delete: %v", err)
	}
}

func TestInvestigationPhase_RejectsBackwardsTimeRange(t *testing.T) {
	s := openTempStore(t)
	invID, _ := s.CreateInvestigation(&InvestigationInsert{Title: "case"})
	_, err := s.InsertInvestigationPhase(&InvestigationPhaseInsert{
		InvestigationID: invID, Name: "x", StartTs: 5000, EndTs: 1000, CreatedBy: "x",
	})
	if err == nil {
		t.Errorf("expected CHECK constraint to reject end_ts < start_ts")
	}
}

func TestInvestigationPhase_RejectsEmptyName(t *testing.T) {
	s := openTempStore(t)
	invID, _ := s.CreateInvestigation(&InvestigationInsert{Title: "case"})
	_, err := s.InsertInvestigationPhase(&InvestigationPhaseInsert{
		InvestigationID: invID, Name: "", StartTs: 1, EndTs: 2, CreatedBy: "x",
	})
	if err == nil {
		t.Errorf("expected CHECK constraint to reject empty name")
	}
	// strings.TrimSpace check — implementer should also handle this in the
	// handler layer, but the DB-level check covers \"\" specifically.
	_ = strings.TrimSpace
	_ = sql.ErrNoRows
	_ = errors.Is
}

func TestInvestigationPhase_CascadeOnInvestigationDelete(t *testing.T) {
	// Implementer: this test depends on whether the project has a way to
	// delete an investigation. If not, you can drop this test — the FK
	// constraint with ON DELETE CASCADE is correct by definition. Use the
	// raw SQL DELETE if the codebase doesn't expose a Store method.
	s := openTempStore(t)
	invID, _ := s.CreateInvestigation(&InvestigationInsert{Title: "case"})
	_, _ = s.InsertInvestigationPhase(&InvestigationPhaseInsert{
		InvestigationID: invID, Name: "x", StartTs: 1, EndTs: 2, CreatedBy: "x",
	})
	if _, err := s.Exec(`DELETE FROM investigations WHERE id = ?`, invID); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.ListInvestigationPhases(invID)
	if len(rows) != 0 {
		t.Errorf("after parent delete: len(rows) = %d, want 0 (cascade failed)", len(rows))
	}
}
```

- [ ] **Step 5: Run tests to verify they fail**

```bash
cd /Users/matt/Code/Oracle/Okesu/.claude/worktrees/feat-case-phases
go test ./controlplane/db/ -run TestInvestigationPhase -v
```

Expected: FAIL — `s.InsertInvestigationPhase undefined`.

- [ ] **Step 6: Implement the Store methods**

Create `controlplane/db/investigation_phases.go`:

```go
// Operator-defined phases of an investigation. Each phase is a
// named time range stored as one row; rendering + drag-to-create
// gestures live in the frontend (PhasesLane). Coloring is derived
// from hash(name) on the client; no category column.
package db

import (
	"database/sql"
	"time"
)

type InvestigationPhase struct {
	ID              int64
	InvestigationID int64
	Name            string
	StartTs         int64
	EndTs           int64
	CreatedBy       sql.NullString
	CreatedAt       time.Time
}

type InvestigationPhaseInsert struct {
	InvestigationID int64
	Name            string
	StartTs         int64
	EndTs           int64
	CreatedBy       string
}

// InsertInvestigationPhase creates a new phase row. Returns the row's id.
// Validation (empty name, end < start, name length) is enforced at the
// SQL level via CHECK constraints; the handler also pre-validates with
// friendly 400 messages.
func (s *Store) InsertInvestigationPhase(in *InvestigationPhaseInsert) (int64, error) {
	res, err := s.Exec(`
		INSERT INTO investigation_phases
		  (investigation_id, name, start_ts, end_ts, created_by)
		VALUES (?, ?, ?, ?, ?)`,
		in.InvestigationID, in.Name, in.StartTs, in.EndTs,
		nullable(in.CreatedBy))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListInvestigationPhases returns the case's phases sorted ascending by
// start_ts. Empty slice (not nil) when no rows.
func (s *Store) ListInvestigationPhases(invID int64) ([]InvestigationPhase, error) {
	rows, err := s.Query(`
		SELECT id, investigation_id, name, start_ts, end_ts,
		       created_by, created_at
		FROM investigation_phases
		WHERE investigation_id = ?
		ORDER BY start_ts ASC, id ASC`, invID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []InvestigationPhase{}
	for rows.Next() {
		var p InvestigationPhase
		var createdAt sql.NullTime
		if err := rows.Scan(&p.ID, &p.InvestigationID, &p.Name,
			&p.StartTs, &p.EndTs, &p.CreatedBy, &createdAt); err != nil {
			return nil, err
		}
		if createdAt.Valid {
			p.CreatedAt = createdAt.Time.UTC()
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// UpdateInvestigationPhaseName renames a phase. Idempotent: if the row
// doesn't exist the UPDATE is a no-op and the call returns nil.
func (s *Store) UpdateInvestigationPhaseName(invID, phaseID int64, name string) error {
	_, err := s.Exec(`
		UPDATE investigation_phases
		SET name = ?
		WHERE id = ? AND investigation_id = ?`,
		name, phaseID, invID)
	return err
}

// DeleteInvestigationPhase removes a phase row. Idempotent.
func (s *Store) DeleteInvestigationPhase(invID, phaseID int64) error {
	_, err := s.Exec(`
		DELETE FROM investigation_phases
		WHERE id = ? AND investigation_id = ?`,
		phaseID, invID)
	return err
}
```

NOTE: `nullable(s string) sql.NullString` is an existing helper in the package. If you can't find it, search `grep -n "func nullable" controlplane/db/`.

- [ ] **Step 7: Run tests to verify they pass**

```bash
go test ./controlplane/db/ -run TestInvestigationPhase -v
```

Expected: PASS — all 7 tests green.

- [ ] **Step 8: Run the full DB suite**

```bash
go test ./controlplane/db/...
```

Expected: PASS — every existing test still green; the migration is additive.

- [ ] **Step 9: Commit**

```bash
git add controlplane/db/migrations/sqlite/059_investigation_phases.sql controlplane/db/migrations/postgres/059_investigation_phases.sql controlplane/db/investigation_phases.go controlplane/db/investigation_phases_test.go controlplane/db/store.go
git commit -m "feat(db): investigation_phases table + Store CRUD"
```

---

## Task 2: HTTP handlers + federation pair + routes + tests

**Files:**
- Create: `controlplane/api/investigation_phases.go`
- Create: `controlplane/api/investigation_phases_test.go`
- Modify: `controlplane/server.go` (8 new route registrations)

- [ ] **Step 1: Write the failing tests**

Create `controlplane/api/investigation_phases_test.go`:

```go
package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/section9labs/okesu/controlplane/db"
)

func TestInvestigationPhases_Unknown404(t *testing.T) {
	store := newSeededTestStore(t)
	router := chi.NewRouter()
	router.Get("/api/investigations/{id}/phases", GetInvestigationPhasesHandler(store))

	req := httptest.NewRequest("GET", "/api/investigations/99999/phases", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestInvestigationPhases_EmptyList(t *testing.T) {
	store := newSeededTestStore(t)
	id := mustCreateInvestigation(t, store, "case")
	router := chi.NewRouter()
	router.Get("/api/investigations/{id}/phases", GetInvestigationPhasesHandler(store))

	req := httptest.NewRequest("GET", "/api/investigations/"+strconv.FormatInt(id, 10)+"/phases", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var got []map[string]any
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("len(got) = %d, want 0", len(got))
	}
}

func TestInvestigationPhases_Create(t *testing.T) {
	store := newSeededTestStore(t)
	id := mustCreateInvestigation(t, store, "case")
	router := chi.NewRouter()
	router.Post("/api/investigations/{id}/phases", CreateInvestigationPhaseHandler(store))

	body := bytes.NewBufferString(`{"name":"Initial detection","start_ts":1000,"end_ts":2000}`)
	req := httptest.NewRequest("POST", "/api/investigations/"+strconv.FormatInt(id, 10)+"/phases", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp struct{ ID int64 `json:"id"` }
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.ID == 0 {
		t.Errorf("id = 0")
	}
	rows, _ := store.ListInvestigationPhases(id)
	if len(rows) != 1 || rows[0].Name != "Initial detection" {
		t.Errorf("rows = %+v, want one named \"Initial detection\"", rows)
	}
}

func TestInvestigationPhases_CreateRejectsEmptyName(t *testing.T) {
	store := newSeededTestStore(t)
	id := mustCreateInvestigation(t, store, "case")
	router := chi.NewRouter()
	router.Post("/api/investigations/{id}/phases", CreateInvestigationPhaseHandler(store))

	for _, name := range []string{`""`, `"   "`, `"` + strings.Repeat("x", 101) + `"`} {
		body := bytes.NewBufferString(`{"name":` + name + `,"start_ts":1,"end_ts":2}`)
		req := httptest.NewRequest("POST", "/api/investigations/"+strconv.FormatInt(id, 10)+"/phases", body)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("name=%s: status = %d, want 400", name, w.Code)
		}
	}
}

func TestInvestigationPhases_CreateRejectsBackwardsTime(t *testing.T) {
	store := newSeededTestStore(t)
	id := mustCreateInvestigation(t, store, "case")
	router := chi.NewRouter()
	router.Post("/api/investigations/{id}/phases", CreateInvestigationPhaseHandler(store))

	body := bytes.NewBufferString(`{"name":"x","start_ts":5000,"end_ts":1000}`)
	req := httptest.NewRequest("POST", "/api/investigations/"+strconv.FormatInt(id, 10)+"/phases", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestInvestigationPhases_Update(t *testing.T) {
	store := newSeededTestStore(t)
	invID := mustCreateInvestigation(t, store, "case")
	phaseID, _ := store.InsertInvestigationPhase(&db.InvestigationPhaseInsert{
		InvestigationID: invID, Name: "old", StartTs: 1, EndTs: 2, CreatedBy: "x",
	})
	router := chi.NewRouter()
	router.Patch("/api/investigations/{id}/phases/{phase_id}", UpdateInvestigationPhaseHandler(store))

	body := bytes.NewBufferString(`{"name":"new"}`)
	req := httptest.NewRequest("PATCH", "/api/investigations/"+strconv.FormatInt(invID, 10)+"/phases/"+strconv.FormatInt(phaseID, 10), body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	rows, _ := store.ListInvestigationPhases(invID)
	if len(rows) != 1 || rows[0].Name != "new" {
		t.Errorf("name not updated: %+v", rows)
	}
}

func TestInvestigationPhases_Delete(t *testing.T) {
	store := newSeededTestStore(t)
	invID := mustCreateInvestigation(t, store, "case")
	phaseID, _ := store.InsertInvestigationPhase(&db.InvestigationPhaseInsert{
		InvestigationID: invID, Name: "x", StartTs: 1, EndTs: 2, CreatedBy: "x",
	})
	router := chi.NewRouter()
	router.Delete("/api/investigations/{id}/phases/{phase_id}", DeleteInvestigationPhaseHandler(store))

	req := httptest.NewRequest("DELETE", "/api/investigations/"+strconv.FormatInt(invID, 10)+"/phases/"+strconv.FormatInt(phaseID, 10), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	rows, _ := store.ListInvestigationPhases(invID)
	if len(rows) != 0 {
		t.Errorf("after delete: len(rows) = %d, want 0", len(rows))
	}
}

func TestFederationInvestigationPhases_RejectsWithoutToken(t *testing.T) {
	store := newSeededTestStore(t)
	router := chi.NewRouter()
	router.Get("/api/v1/federation/investigations/{id}/phases", FederationInvestigationPhasesList(store))

	req := httptest.NewRequest("GET", "/api/v1/federation/investigations/1/phases", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func mustCreateInvestigation(t *testing.T, store *db.Store, title string) int64 {
	t.Helper()
	id, err := store.CreateInvestigation(&db.InvestigationInsert{Title: title})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return id
}
```

NOTE: `mustCreateInvestigation` may already exist in this package as `mustCreateInvestigationForRelayTest` (added by PR #126). If so, drop the helper from this file and use the existing one. Search: `grep -n "func mustCreateInvestigation" controlplane/api/`.

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./controlplane/api/ -run "TestInvestigationPhases_|TestFederationInvestigationPhases_" -v
```

Expected: FAIL — handler symbols undefined.

- [ ] **Step 3: Implement the handlers + federation pair**

Create `controlplane/api/investigation_phases.go`:

```go
// HTTP layer for investigation phases — operator-defined named time
// ranges on the case timeline. Federation follows the existing
// parent-proxy / child-token pattern.
package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/section9labs/okesu/controlplane/db"
	"github.com/section9labs/okesu/controlplane/federation"
)

const phaseNameMaxLen = 100

// --- LIST ---------------------------------------------------------------

func GetInvestigationPhasesHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		invID, err := investigationIDFromChi(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if _, err := store.GetInvestigation(invID); err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		phases, _ := store.ListInvestigationPhases(invID)
		if phases == nil {
			phases = []db.InvestigationPhase{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(phases)
	}
}

// --- CREATE -------------------------------------------------------------

type createPhaseReq struct {
	Name    string `json:"name"`
	StartTs int64  `json:"start_ts"`
	EndTs   int64  `json:"end_ts"`
}

func CreateInvestigationPhaseHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		invID, err := investigationIDFromChi(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if _, err := store.GetInvestigation(invID); err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		var req createPhaseReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		req.Name = strings.TrimSpace(req.Name)
		if req.Name == "" {
			http.Error(w, "name is required", http.StatusBadRequest)
			return
		}
		if len(req.Name) > phaseNameMaxLen {
			http.Error(w, "name exceeds 100 chars", http.StatusBadRequest)
			return
		}
		if req.StartTs > req.EndTs {
			http.Error(w, "start_ts must be <= end_ts", http.StatusBadRequest)
			return
		}
		author := userIdentityFromContext(r.Context())
		id, err := store.InsertInvestigationPhase(&db.InvestigationPhaseInsert{
			InvestigationID: invID,
			Name:            req.Name,
			StartTs:         req.StartTs,
			EndTs:           req.EndTs,
			CreatedBy:       author,
		})
		if err != nil {
			http.Error(w, "insert: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id})
	}
}

// --- UPDATE -------------------------------------------------------------

type updatePhaseReq struct {
	Name string `json:"name"`
}

func UpdateInvestigationPhaseHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		invID, err := investigationIDFromChi(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		phaseID, err := childIDFromChi(r, "phase_id")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var req updatePhaseReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		req.Name = strings.TrimSpace(req.Name)
		if req.Name == "" || len(req.Name) > phaseNameMaxLen {
			http.Error(w, "name required (1-100 chars)", http.StatusBadRequest)
			return
		}
		if err := store.UpdateInvestigationPhaseName(invID, phaseID, req.Name); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// --- DELETE -------------------------------------------------------------

func DeleteInvestigationPhaseHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		invID, err := investigationIDFromChi(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		phaseID, err := childIDFromChi(r, "phase_id")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := store.DeleteInvestigationPhase(invID, phaseID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// --- FEDERATION PAIRS --------------------------------------------------

// Parent-side wrappers: proxy via ?cp=<instance_id> when set, otherwise
// fall through to the local handler.

func FederatedInvestigationPhasesList(store *db.Store, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.Replace(r.URL.Path, "/api/investigations/", "/api/v1/federation/investigations/", 1)
		if handled, _ := proxyToCPByQuery(w, r, agg, path); handled {
			return
		}
		GetInvestigationPhasesHandler(store).ServeHTTP(w, r)
	}
}

func FederatedInvestigationPhaseCreate(store *db.Store, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.Replace(r.URL.Path, "/api/investigations/", "/api/v1/federation/investigations/", 1)
		if handled, _ := proxyToCPByQueryPost(w, r, agg, path); handled {
			return
		}
		CreateInvestigationPhaseHandler(store).ServeHTTP(w, r)
	}
}

func FederatedInvestigationPhaseUpdate(store *db.Store, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.Replace(r.URL.Path, "/api/investigations/", "/api/v1/federation/investigations/", 1)
		if handled, _ := proxyToCPByQueryPost(w, r, agg, path); handled {
			return
		}
		UpdateInvestigationPhaseHandler(store).ServeHTTP(w, r)
	}
}

func FederatedInvestigationPhaseDelete(store *db.Store, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.Replace(r.URL.Path, "/api/investigations/", "/api/v1/federation/investigations/", 1)
		if handled, _ := proxyToCPByQueryPost(w, r, agg, path); handled {
			return
		}
		DeleteInvestigationPhaseHandler(store).ServeHTTP(w, r)
	}
}

// Child-side wrappers: token-authed via the standard middleware.

func FederationInvestigationPhasesList(store *db.Store) http.HandlerFunc {
	return requireFederationToken(store, GetInvestigationPhasesHandler(store))
}

func FederationInvestigationPhaseCreate(store *db.Store) http.HandlerFunc {
	return requireFederationToken(store, CreateInvestigationPhaseHandler(store))
}

func FederationInvestigationPhaseUpdate(store *db.Store) http.HandlerFunc {
	return requireFederationToken(store, UpdateInvestigationPhaseHandler(store))
}

func FederationInvestigationPhaseDelete(store *db.Store) http.HandlerFunc {
	return requireFederationToken(store, DeleteInvestigationPhaseHandler(store))
}
```

NOTE: `proxyToCPByQueryPost` was added in PR #126 (war-room notes finalize endpoint) — it forwards POST/PATCH/DELETE bodies. Its location: `controlplane/api/federation_writes.go`. Reuse it; do NOT add another POST proxy helper.

- [ ] **Step 4: Register the routes in `controlplane/server.go`**

Find the existing parent-side `/draft/finalize` route registration (added by PR #126, around line 905-906). Add immediately after it:

```go
		r.Get(   "/api/investigations/{id}/phases",            api.FederatedInvestigationPhasesList(s.store, s.fedAgg))
		r.Post(  "/api/investigations/{id}/phases",            api.FederatedInvestigationPhaseCreate(s.store, s.fedAgg))
		r.Patch( "/api/investigations/{id}/phases/{phase_id}", api.FederatedInvestigationPhaseUpdate(s.store, s.fedAgg))
		r.Delete("/api/investigations/{id}/phases/{phase_id}", api.FederatedInvestigationPhaseDelete(s.store, s.fedAgg))
```

Find the child-side federation `/draft/finalize` route registration (around line 680). Add immediately after it:

```go
		r.Get(   "/api/v1/federation/investigations/{id}/phases",            api.FederationInvestigationPhasesList(s.store))
		r.Post(  "/api/v1/federation/investigations/{id}/phases",            api.FederationInvestigationPhaseCreate(s.store))
		r.Patch( "/api/v1/federation/investigations/{id}/phases/{phase_id}", api.FederationInvestigationPhaseUpdate(s.store))
		r.Delete("/api/v1/federation/investigations/{id}/phases/{phase_id}", api.FederationInvestigationPhaseDelete(s.store))
```

- [ ] **Step 5: Run handler tests**

```bash
go test ./controlplane/api/ -run "TestInvestigationPhases_|TestFederationInvestigationPhases_" -v
```

Expected: PASS — all 8 tests green.

- [ ] **Step 6: Run the full Go suite**

```bash
go test ./...
```

Expected: PASS — every package green.

- [ ] **Step 7: Commit**

```bash
git add controlplane/api/investigation_phases.go controlplane/api/investigation_phases_test.go controlplane/server.go
git commit -m "feat(api): GET/POST/PATCH/DELETE /api/investigations/{id}/phases with federation"
```

---

## Task 3: Frontend API client + types + xToT helper

**Files:**
- Modify: `web/src/api.ts`
- Modify: `web/src/components/investigations/timeline/types.ts`
- Modify: `web/src/components/investigations/timeline/scale.ts`
- Modify: `web/src/components/investigations/timeline/scale.test.ts`

- [ ] **Step 1: Add `InvestigationPhase` type to `api.ts`**

Edit `web/src/api.ts`. Find the `InvestigationDetail` bundle type (around line 746) and add this type just before or after it:

```ts
// Operator-defined phase of a case (e.g. "Initial detection 14:00–14:35").
// Renders as a pill in the timeline's "phases" lane. Color is derived
// from hash(name); no category column.
export interface InvestigationPhase {
  ID:              number;
  InvestigationID: number;
  Name:            string;
  StartTs:         number;
  EndTs:           number;
  CreatedBy:       { Valid: boolean; String: string };
  CreatedAt:       string;
}
```

- [ ] **Step 2: Add the API helpers**

Edit `web/src/api.ts`. Inside the `investigations:` block (around line 1340-1400, near the existing `audit/structure/finalizeDraft` helpers), add:

```ts
    phases: {
      list: (id: number, cpInstanceID?: string) => {
        const qs = cpInstanceID ? `?cp=${encodeURIComponent(cpInstanceID)}` : '';
        return request<InvestigationPhase[]>(`/api/investigations/${id}/phases${qs}`);
      },
      create: (id: number, body: { name: string; start_ts: number; end_ts: number }, cpInstanceID?: string) => {
        const qs = cpInstanceID ? `?cp=${encodeURIComponent(cpInstanceID)}` : '';
        return request<{ id: number }>(`/api/investigations/${id}/phases${qs}`, {
          method: 'POST',
          body: JSON.stringify(body),
        });
      },
      update: (id: number, phaseID: number, body: { name: string }, cpInstanceID?: string) => {
        const qs = cpInstanceID ? `?cp=${encodeURIComponent(cpInstanceID)}` : '';
        return request<void>(`/api/investigations/${id}/phases/${phaseID}${qs}`, {
          method: 'PATCH',
          body: JSON.stringify(body),
        });
      },
      delete: (id: number, phaseID: number, cpInstanceID?: string) => {
        const qs = cpInstanceID ? `?cp=${encodeURIComponent(cpInstanceID)}` : '';
        return request<void>(`/api/investigations/${id}/phases/${phaseID}${qs}`, {
          method: 'DELETE',
        });
      },
    },
```

- [ ] **Step 3: Extend `TimelineLane` and `ALL_LANES`**

Edit `web/src/components/investigations/timeline/types.ts`. Update lines 5-12 and 83-84:

```ts
export type TimelineLane =
  | 'phases'      // NEW — opt-in
  | 'lifecycle'
  | 'findings'
  | 'runs'
  | 'notes'
  | 'iocs'
  | 'daimons'
  | 'audit';

// ...

export const DEFAULT_LANES_ON: ReadonlyArray<TimelineLane> = ['lifecycle', 'findings', 'runs', 'notes'];
export const ALL_LANES: ReadonlyArray<TimelineLane> = ['phases', 'lifecycle', 'findings', 'runs', 'notes', 'iocs', 'daimons', 'audit'];
```

`'phases'` is added to the FRONT of `ALL_LANES` so the toggle bar shows it first; it is NOT added to `DEFAULT_LANES_ON` so the lane is opt-in (matches `audit`).

- [ ] **Step 4: Write the failing `xToT` round-trip test**

Edit `web/src/components/investigations/timeline/scale.test.ts`. Append:

```ts
import { describe, it, expect } from 'vitest';
import { tToX, xToT } from './scale';

describe('xToT', () => {
  it('round-trips with tToX for several timestamps + ranges', () => {
    const range = { tMin: 1000, tMax: 5000 };
    const width = 1000;
    for (const t of [1000, 1500, 2500, 3750, 4999, 5000]) {
      const x = tToX(t, range.tMin, range.tMax, width);
      const back = xToT(x, range.tMin, range.tMax, width);
      expect(Math.round(back)).toBe(t);
    }
  });

  it('clamps x outside the drawable range', () => {
    expect(xToT(-100, 1000, 5000, 1000)).toBe(1000);
    expect(xToT(2000,  1000, 5000, 1000)).toBe(5000);
  });

  it('returns tMin for zero-width drawable area', () => {
    expect(xToT(50, 1000, 5000, 0)).toBe(1000);
  });
});
```

NOTE: if the file already has imports, just add `xToT` to the existing import line and append this describe block.

- [ ] **Step 5: Run the test to verify it fails**

```bash
cd /Users/matt/Code/Oracle/Okesu/.claude/worktrees/feat-case-phases/web
npx vitest run src/components/investigations/timeline/scale.test.ts
```

Expected: FAIL — `xToT` undefined.

- [ ] **Step 6: Implement `xToT`**

Edit `web/src/components/investigations/timeline/scale.ts`. Append after `tToX`:

```ts
/**
 * xToT — inverse of tToX. Given a pixel x within the drawable width,
 * returns the corresponding timestamp in [tMin, tMax]. Clamps x outside
 * the drawable area. Returns tMin if width is zero.
 */
export function xToT(x: number, tMin: number, tMax: number, width: number): number {
  if (width <= 0) return tMin;
  const clamped = Math.min(Math.max(x, 0), width);
  return tMin + (clamped / width) * (tMax - tMin);
}
```

- [ ] **Step 7: Run tests + typecheck**

```bash
cd web && npx vitest run src/components/investigations/timeline/scale.test.ts && npx tsc --noEmit
```

Expected: PASS, tsc clean.

- [ ] **Step 8: Commit**

```bash
git add web/src/api.ts web/src/components/investigations/timeline/types.ts web/src/components/investigations/timeline/scale.ts web/src/components/investigations/timeline/scale.test.ts
git commit -m "feat(web): InvestigationPhase types + phases API client + xToT helper"
```

---

## Task 4: Pure stacker — `phasesLayer.ts`

**Files:**
- Create: `web/src/components/investigations/timeline/phasesLayer.ts`
- Create: `web/src/components/investigations/timeline/phasesLayer.test.ts`

- [ ] **Step 1: Write the failing tests**

Create `web/src/components/investigations/timeline/phasesLayer.test.ts`:

```ts
import { describe, it, expect } from 'vitest';
import { layoutPhases } from './phasesLayer';
import type { InvestigationPhase } from '../../../api';

function p(id: number, start: number, end: number): InvestigationPhase {
  return {
    ID: id,
    InvestigationID: 1,
    Name: `phase-${id}`,
    StartTs: start,
    EndTs: end,
    CreatedBy: { Valid: false, String: '' },
    CreatedAt: '',
  };
}

const range = { tMin: 0, tMax: 1000 };
const width = 1000;
const labelW = 80;

describe('layoutPhases', () => {
  it('returns empty array for empty input', () => {
    expect(layoutPhases([], range, width, labelW)).toEqual([]);
  });

  it('places a single phase on row 0', () => {
    const out = layoutPhases([p(1, 100, 300)], range, width, labelW);
    expect(out).toHaveLength(1);
    expect(out[0].row).toBe(0);
    expect(out[0].x).toBe(labelW + 100);
    expect(out[0].width).toBe(200);
  });

  it('places non-overlapping phases on row 0', () => {
    const out = layoutPhases([p(1, 0, 300), p(2, 400, 700)], range, width, labelW);
    expect(out.map((o) => o.row)).toEqual([0, 0]);
  });

  it('overlapping phases stack on rows 0 and 1', () => {
    const out = layoutPhases([p(1, 0, 500), p(2, 300, 700)], range, width, labelW);
    expect(out.map((o) => o.row)).toEqual([0, 1]);
  });

  it('three-way overlap stacks on rows 0, 1, 2', () => {
    const out = layoutPhases(
      [p(1, 0, 800), p(2, 100, 600), p(3, 200, 700)],
      range, width, labelW,
    );
    expect(out.map((o) => o.row).sort()).toEqual([0, 1, 2]);
  });

  it('touching boundaries (start of B == end of A) both go on row 0', () => {
    const out = layoutPhases([p(1, 0, 400), p(2, 400, 700)], range, width, labelW);
    expect(out.map((o) => o.row)).toEqual([0, 0]);
  });

  it('phase outside visible range still in output', () => {
    // tToX clamps to canvas edges; the layout helper itself doesn't drop
    // anything — caller decides whether to render off-screen pills.
    const out = layoutPhases([p(1, 5000, 6000)], range, width, labelW);
    expect(out).toHaveLength(1);
    // x clamped to right edge; width clamped to 0.
    expect(out[0].x).toBe(labelW + width);
  });

  it('places earlier-starting phase on lower row when both stay open', () => {
    // p(1) starts at 0, p(2) starts at 100. p(2) starts inside p(1)'s
    // span → must go to row 1. p(3) starts after p(1) ends but before
    // p(2) ends → row 0 (slot freed by p(1)).
    const out = layoutPhases(
      [p(1, 0, 300), p(2, 100, 700), p(3, 400, 600)],
      range, width, labelW,
    );
    const byID = new Map(out.map((o) => [o.phase.ID, o]));
    expect(byID.get(1)!.row).toBe(0);
    expect(byID.get(2)!.row).toBe(1);
    expect(byID.get(3)!.row).toBe(0);
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
cd web && npx vitest run src/components/investigations/timeline/phasesLayer.test.ts
```

Expected: FAIL — `layoutPhases` undefined.

- [ ] **Step 3: Implement the stacker**

Create `web/src/components/investigations/timeline/phasesLayer.ts`:

```ts
// Pure first-fit row assignment for overlapping phases. Sorts by
// start_ts ascending and places each phase on the lowest-numbered
// row whose latest end_ts is <= this phase's start_ts. New row if
// every existing row overlaps. Pixel positions are computed via
// tToX (output clamps to drawable area; callers decide whether to
// render off-screen pills).

import type { InvestigationPhase } from '../../../api';
import { tToX, type Range } from './scale';

export interface PhaseLayout {
  phase: InvestigationPhase;
  row:   number;
  x:     number;
  width: number;
}

export function layoutPhases(
  phases: InvestigationPhase[],
  range: Range,
  drawableWidth: number,
  laneLabelWidth: number,
): PhaseLayout[] {
  if (phases.length === 0) return [];

  const sorted = [...phases].sort((a, b) => {
    if (a.StartTs !== b.StartTs) return a.StartTs - b.StartTs;
    return a.ID - b.ID;
  });

  // rowEnds[i] = latest end_ts placed on row i. Empty array = no rows yet.
  const rowEnds: number[] = [];
  const out: PhaseLayout[] = [];

  for (const phase of sorted) {
    let placedRow = -1;
    for (let i = 0; i < rowEnds.length; i++) {
      if (rowEnds[i] <= phase.StartTs) {
        placedRow = i;
        break;
      }
    }
    if (placedRow === -1) {
      placedRow = rowEnds.length;
      rowEnds.push(phase.EndTs);
    } else {
      rowEnds[placedRow] = phase.EndTs;
    }
    const x0 = laneLabelWidth + tToX(phase.StartTs, range.tMin, range.tMax, drawableWidth);
    const x1 = laneLabelWidth + tToX(phase.EndTs, range.tMin, range.tMax, drawableWidth);
    out.push({
      phase,
      row:   placedRow,
      x:     x0,
      width: Math.max(0, x1 - x0),
    });
  }

  return out;
}
```

- [ ] **Step 4: Run the tests + typecheck**

```bash
cd web && npx vitest run src/components/investigations/timeline/phasesLayer.test.ts && npx tsc --noEmit
```

Expected: PASS — 8 tests green; tsc clean.

- [ ] **Step 5: Commit**

```bash
git add web/src/components/investigations/timeline/phasesLayer.ts web/src/components/investigations/timeline/phasesLayer.test.ts
git commit -m "feat(timeline): pure phasesLayer stacker for overlapping phases"
```

---

## Task 5: `<PhasesLane>` component — drag, rename, delete

**Files:**
- Create: `web/src/components/investigations/PhasesLane.tsx`
- Create: `web/src/components/investigations/PhasesLane.test.tsx`

- [ ] **Step 1: Write the failing tests**

Create `web/src/components/investigations/PhasesLane.test.tsx`:

```tsx
import { describe, it, expect, afterEach, vi } from 'vitest';
import { render, screen, cleanup, fireEvent } from '@testing-library/react';
import { PhasesLane } from './PhasesLane';
import { api, type InvestigationPhase } from '../../api';
import type { Range } from './timeline/scale';

afterEach(cleanup);

vi.mock('../../api', async () => {
  const actual = await vi.importActual<typeof import('../../api')>('../../api');
  return {
    ...actual,
    api: {
      ...actual.api,
      investigations: {
        ...actual.api.investigations,
        phases: {
          list:   vi.fn().mockResolvedValue([]),
          create: vi.fn().mockResolvedValue({ id: 7 }),
          update: vi.fn().mockResolvedValue(undefined),
          delete: vi.fn().mockResolvedValue(undefined),
        },
      },
    },
  };
});

const range: Range = { tMin: 0, tMax: 1000 };
const props = {
  invID: 1,
  cpInstanceID: undefined as string | undefined,
  range,
  drawableWidth: 1000,
  laneLabelWidth: 80,
  onPhasesChange: vi.fn(),
};

function phase(id: number, name: string, start: number, end: number): InvestigationPhase {
  return {
    ID: id,
    InvestigationID: 1,
    Name: name,
    StartTs: start,
    EndTs: end,
    CreatedBy: { Valid: false, String: '' },
    CreatedAt: '',
  };
}

describe('PhasesLane', () => {
  it('renders empty-state hint when phases is empty', () => {
    render(<PhasesLane {...props} phases={[]} />);
    expect(screen.getByText(/drag here to mark a phase/i)).toBeTruthy();
  });

  it('renders phase pills for each phase', () => {
    render(<PhasesLane {...props} phases={[
      phase(1, 'Initial detection', 100, 300),
      phase(2, 'Containment', 400, 700),
    ]} />);
    expect(screen.getByText('Initial detection')).toBeTruthy();
    expect(screen.getByText('Containment')).toBeTruthy();
  });

  it('clicking a phase pill swaps it for an input', () => {
    render(<PhasesLane {...props} phases={[phase(1, 'old', 100, 300)]} />);
    fireEvent.click(screen.getByText('old'));
    const input = screen.getByDisplayValue('old') as HTMLInputElement;
    expect(input).toBeTruthy();
  });

  it('renaming a phase calls api.update + onPhasesChange', async () => {
    const onChange = vi.fn();
    render(<PhasesLane {...props} onPhasesChange={onChange} phases={[phase(1, 'old', 100, 300)]} />);
    fireEvent.click(screen.getByText('old'));
    const input = screen.getByDisplayValue('old') as HTMLInputElement;
    fireEvent.change(input, { target: { value: 'new' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    await new Promise((r) => setTimeout(r, 0));
    expect(api.investigations.phases.update).toHaveBeenCalledWith(1, 1, { name: 'new' }, undefined);
    expect(onChange).toHaveBeenCalled();
  });

  it('Escape on rename input reverts without calling api.update', async () => {
    const updateMock = api.investigations.phases.update as ReturnType<typeof vi.fn>;
    updateMock.mockClear();
    render(<PhasesLane {...props} phases={[phase(1, 'old', 100, 300)]} />);
    fireEvent.click(screen.getByText('old'));
    const input = screen.getByDisplayValue('old') as HTMLInputElement;
    fireEvent.change(input, { target: { value: 'new' } });
    fireEvent.keyDown(input, { key: 'Escape' });
    expect(updateMock).not.toHaveBeenCalled();
    expect(screen.getByText('old')).toBeTruthy();
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
cd web && npx vitest run src/components/investigations/PhasesLane.test.tsx
```

Expected: FAIL — `PhasesLane` undefined.

- [ ] **Step 3: Implement the component**

Create `web/src/components/investigations/PhasesLane.tsx`:

```tsx
// PhasesLane — sibling of the timeline SVG; not inside it. Operators
// drag inside the lane background to mark a time range, type a name,
// hit Enter. Click a pill to rename inline; hover to reveal an X
// delete button. Color is derived from hash(name); no picker UI.
import { useMemo, useRef, useState } from 'react';
import { api, type InvestigationPhase } from '../../api';
import { layoutPhases } from './timeline/phasesLayer';
import { xToT, type Range } from './timeline/scale';

const LANE_HEIGHT = 28;
const MIN_DRAG_PX = 5;

interface Props {
  invID: number;
  cpInstanceID?: string;
  phases: InvestigationPhase[];
  range: Range;
  drawableWidth: number;
  laneLabelWidth: number;
  onPhasesChange: () => void;
}

type DragState =
  | { kind: 'idle' }
  | { kind: 'dragging'; startX: number; currentX: number }
  | { kind: 'naming'; startX: number; endX: number; value: string };

export function PhasesLane({
  invID, cpInstanceID, phases, range, drawableWidth, laneLabelWidth, onPhasesChange,
}: Props) {
  const containerRef = useRef<HTMLDivElement>(null);
  const [drag, setDrag] = useState<DragState>({ kind: 'idle' });
  const [editingID, setEditingID] = useState<number | null>(null);
  const [editValue, setEditValue] = useState('');
  const [hoveredID, setHoveredID] = useState<number | null>(null);

  const layout = useMemo(
    () => layoutPhases(phases, range, drawableWidth, laneLabelWidth),
    [phases, range, drawableWidth, laneLabelWidth],
  );
  const rowCount = Math.max(1, ...layout.map((l) => l.row + 1));
  const containerWidth = laneLabelWidth + drawableWidth;

  // x within the container (offsetX-style). Used by drag handlers.
  function localX(e: React.PointerEvent): number {
    const rect = containerRef.current!.getBoundingClientRect();
    return e.clientX - rect.left;
  }

  function onPointerDownLane(e: React.PointerEvent) {
    if ((e.target as HTMLElement).dataset.role === 'phase-pill') return;
    if ((e.target as HTMLElement).dataset.role === 'phase-delete') return;
    const x = localX(e);
    if (x < laneLabelWidth) return; // labels area; ignore
    (e.target as Element).setPointerCapture?.(e.pointerId);
    setDrag({ kind: 'dragging', startX: x, currentX: x });
  }

  function onPointerMoveLane(e: React.PointerEvent) {
    if (drag.kind !== 'dragging') return;
    setDrag({ ...drag, currentX: localX(e) });
  }

  function onPointerUpLane(e: React.PointerEvent) {
    if (drag.kind !== 'dragging') return;
    const endX = localX(e);
    if (Math.abs(endX - drag.startX) < MIN_DRAG_PX) {
      setDrag({ kind: 'idle' });
      return;
    }
    setDrag({
      kind: 'naming',
      startX: Math.min(drag.startX, endX),
      endX:   Math.max(drag.startX, endX),
      value:  '',
    });
  }

  function commitNew() {
    if (drag.kind !== 'naming') return;
    const name = drag.value.trim();
    if (!name) {
      setDrag({ kind: 'idle' });
      return;
    }
    const startTs = xToT(drag.startX - laneLabelWidth, range.tMin, range.tMax, drawableWidth);
    const endTs   = xToT(drag.endX   - laneLabelWidth, range.tMin, range.tMax, drawableWidth);
    setDrag({ kind: 'idle' });
    api.investigations.phases.create(invID, {
      name,
      start_ts: Math.round(startTs),
      end_ts:   Math.round(endTs),
    }, cpInstanceID).then(onPhasesChange).catch(() => {});
  }

  function startEdit(p: InvestigationPhase) {
    setEditingID(p.ID);
    setEditValue(p.Name);
  }

  function commitEdit(p: InvestigationPhase) {
    const name = editValue.trim();
    setEditingID(null);
    if (!name || name === p.Name) return;
    api.investigations.phases.update(invID, p.ID, { name }, cpInstanceID)
      .then(onPhasesChange).catch(() => {});
  }

  function deletePhase(p: InvestigationPhase) {
    if (!window.confirm(`Delete phase "${p.Name}"?`)) return;
    api.investigations.phases.delete(invID, p.ID, cpInstanceID)
      .then(onPhasesChange).catch(() => {});
  }

  return (
    <div
      ref={containerRef}
      className="relative border-b border-border bg-slate-50/40"
      style={{ width: containerWidth, height: rowCount * LANE_HEIGHT }}
      onPointerDown={onPointerDownLane}
      onPointerMove={onPointerMoveLane}
      onPointerUp={onPointerUpLane}
    >
      {/* Lane label */}
      <span className="absolute left-1.5 top-1.5 text-[11px] text-ink-mute">phases</span>

      {/* Empty-state hint */}
      {phases.length === 0 && drag.kind === 'idle' && (
        <span
          className="absolute inset-0 flex items-center justify-center text-[11px] text-ink-mute italic pointer-events-none"
          style={{ paddingLeft: laneLabelWidth }}
        >
          drag here to mark a phase
        </span>
      )}

      {/* Mid-drag rubber band */}
      {drag.kind === 'dragging' && (
        <div
          className="absolute rounded bg-blue-200/60 ring-1 ring-blue-500"
          style={{
            left:   Math.min(drag.startX, drag.currentX),
            width:  Math.abs(drag.currentX - drag.startX),
            top:    4,
            height: LANE_HEIGHT - 8,
          }}
        />
      )}

      {/* Naming input */}
      {drag.kind === 'naming' && (
        <input
          autoFocus
          value={drag.value}
          onChange={(e) => setDrag({ ...drag, value: e.target.value })}
          onBlur={commitNew}
          onKeyDown={(e) => {
            if (e.key === 'Enter') commitNew();
            if (e.key === 'Escape') setDrag({ kind: 'idle' });
          }}
          placeholder="phase name"
          className="absolute rounded border border-blue-500 px-1 text-[11px] bg-white"
          style={{
            left:   drag.startX,
            width:  Math.max(80, drag.endX - drag.startX),
            top:    4,
            height: LANE_HEIGHT - 8,
          }}
        />
      )}

      {/* Phase pills */}
      {layout.map((entry) => {
        const p = entry.phase;
        const isEditing = editingID === p.ID;
        const isHovered = hoveredID === p.ID;
        const hue = hashHue(p.Name);
        const bg     = `hsl(${hue}, 70%, 92%)`;
        const ring   = `hsl(${hue}, 60%, 65%)`;
        const text   = `hsl(${hue}, 55%, 28%)`;
        return (
          <div key={p.ID} style={{ position: 'absolute', left: entry.x, top: entry.row * LANE_HEIGHT + 4, width: Math.max(entry.width, 4), height: LANE_HEIGHT - 8 }}>
            {isEditing ? (
              <input
                autoFocus
                value={editValue}
                onChange={(e) => setEditValue(e.target.value)}
                onBlur={() => commitEdit(p)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') commitEdit(p);
                  if (e.key === 'Escape') setEditingID(null);
                }}
                className="w-full h-full rounded border border-blue-500 px-1 text-[11px] bg-white"
              />
            ) : (
              <>
                <div
                  data-role="phase-pill"
                  role="button"
                  className="rounded ring-1 px-1.5 text-[10px] font-medium truncate cursor-pointer"
                  style={{ backgroundColor: bg, color: text, boxShadow: `inset 0 0 0 1px ${ring}`, height: '100%', display: 'flex', alignItems: 'center' }}
                  onClick={() => startEdit(p)}
                  onMouseEnter={() => setHoveredID(p.ID)}
                  onMouseLeave={() => setHoveredID(null)}
                >
                  {p.Name}
                </div>
                {isHovered && (
                  <button
                    data-role="phase-delete"
                    onClick={(e) => { e.stopPropagation(); deletePhase(p); }}
                    className="absolute right-0.5 top-1/2 -translate-y-1/2 w-4 h-4 rounded-full bg-white border border-red-300 text-red-600 text-[10px] leading-none hover:bg-red-50"
                    aria-label={`Delete phase ${p.Name}`}
                  >
                    ×
                  </button>
                )}
              </>
            )}
          </div>
        );
      })}
    </div>
  );
}

function hashHue(s: string): number {
  let h = 0;
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) | 0;
  return ((h % 360) + 360) % 360;
}
```

- [ ] **Step 4: Run tests + typecheck**

```bash
cd web && npx vitest run src/components/investigations/PhasesLane.test.tsx && npx tsc --noEmit
```

Expected: PASS — 5 tests green; tsc clean.

- [ ] **Step 5: Commit**

```bash
git add web/src/components/investigations/PhasesLane.tsx web/src/components/investigations/PhasesLane.test.tsx
git commit -m "feat(web): PhasesLane component — drag-create + rename + delete"
```

---

## Task 6: Wire `<PhasesLane>` into `CaseTimeline`

**Files:**
- Modify: `web/src/components/investigations/CaseTimeline.tsx`
- Modify: `web/src/components/investigations/CaseTimeline.test.tsx`

- [ ] **Step 1: Append a failing integration test**

Append to `web/src/components/investigations/CaseTimeline.test.tsx`:

```tsx
describe('CaseTimeline phases lane', () => {
  it('lazy-fetches phases the first time the lane is toggled on', async () => {
    const apiMod = await import('../../api');
    const listMock = vi.spyOn(apiMod.api.investigations.phases, 'list').mockResolvedValue([]);
    render(<MemoryRouter><CaseTimeline bundle={bundle()} /></MemoryRouter>);
    // Lane is opt-in; not fetched until toggled.
    expect(listMock).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: /^phases$/i }));
    await new Promise((r) => setTimeout(r, 0));
    expect(listMock).toHaveBeenCalledTimes(1);
    listMock.mockRestore();
  });

  it('does not include phases in DEFAULT_LANES_ON', () => {
    render(<MemoryRouter><CaseTimeline bundle={bundle()} /></MemoryRouter>);
    const phasesBtn = screen.getByRole('button', { name: /^phases$/i });
    expect(phasesBtn.getAttribute('aria-pressed')).toBe('false');
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
cd web && npx vitest run src/components/investigations/CaseTimeline.test.tsx -t "phases lane"
```

Expected: FAIL — the lane isn't wired yet.

- [ ] **Step 3: Add lazy-fetch + mount in `CaseTimeline.tsx`**

Edit `web/src/components/investigations/CaseTimeline.tsx`. Add to the imports:

```tsx
import type { InvestigationPhase } from '../../api';
import { PhasesLane } from './PhasesLane';
```

Inside the component body, alongside the existing `audit` lazy-fetch (around line 41-47), add a parallel block:

```tsx
const [phases, setPhases] = useState<InvestigationPhase[]>([]);
const phasesFetchedRef = useRef(false);

useEffect(() => {
  if (!activeLanes.includes('phases') || phasesFetchedRef.current) return;
  phasesFetchedRef.current = true;
  api.investigations.phases.list(bundle.investigation.ID, cpInstanceID)
    .then(setPhases)
    .catch(() => { phasesFetchedRef.current = false; });
}, [activeLanes, bundle.investigation.ID, cpInstanceID]);

const refreshPhases = () => {
  api.investigations.phases.list(bundle.investigation.ID, cpInstanceID)
    .then(setPhases).catch(() => {});
};
```

Then mount `<PhasesLane>` between the toggle bar and the SVG. Find the existing `<TimelineFilterBar>` mount (around line 200) and add immediately before it:

```tsx
{activeLanes.includes('phases') && range && (
  <PhasesLane
    invID={bundle.investigation.ID}
    cpInstanceID={cpInstanceID}
    phases={phases}
    range={range}
    drawableWidth={drawableWidth}
    laneLabelWidth={LANE_LABEL_W}
    onPhasesChange={refreshPhases}
  />
)}
```

NOTE: `LANE_LABEL_W` and `drawableWidth` are existing variables in scope. `range` may be `null` until the auto-fit useEffect populates it on first render — the guard prevents rendering with a null range.

- [ ] **Step 4: Run the targeted tests**

```bash
cd web && npx vitest run src/components/investigations/CaseTimeline.test.tsx -t "phases lane"
```

Expected: PASS — 2 tests green.

- [ ] **Step 5: Run the full vitest + tsc**

```bash
cd web && npx vitest run && npx tsc --noEmit
```

Expected: PASS — full suite + typecheck clean.

- [ ] **Step 6: Commit**

```bash
git add web/src/components/investigations/CaseTimeline.tsx web/src/components/investigations/CaseTimeline.test.tsx
git commit -m "feat(web): mount PhasesLane in CaseTimeline with lazy fetch"
```

---

## Task 7: Architecture docs + final sweep + PR

- [ ] **Step 1: Update `docs/architecture.md`**

Find the section "Investigation Overview — war-room collaborative drafts" (added by PR #126). Insert a new section *after* it:

```markdown
## Investigation Overview — case phases (timeline annotations)

Operators can mark named time ranges on the case timeline ("Initial
detection 14:00–14:35", "Containment 14:35–16:10"). Phases persist
in the `investigation_phases` table and render as a dedicated
"phases" lane at the top of the timeline (toggleable, opt-in,
lazy-fetched on first toggle).

The lane is a sibling React `<div>` directly above the SVG canvas
(NOT an SVG row), so the rubber-band drag gesture and inline rename
input use plain DOM event handlers without fighting SVG event
propagation. Pointer-down inside the lane background starts a
rubber-band selection; pointer-up opens an inline `<input>` for the
phase name; Enter commits via `POST /api/investigations/{id}/phases`
which inserts one row.

Click an existing phase pill to rename inline (`PATCH .../{phase_id}`).
Hover to reveal an `×` delete button (`DELETE .../{phase_id}` with
window.confirm). No resize via drag-the-edges; operators delete and
re-create to fix a boundary.

Phase colors are derived deterministically from `hash(name)` — no
picker UI, no category column. Same name always renders with the
same color across sessions. Overlapping phases stack vertically
within the lane via the pure first-fit row-assignment helper at
`web/src/components/investigations/timeline/phasesLayer.ts`.

Federation: each handler (`Federated*` parent-side, `Federation*`
child-side) follows the same template as the case-structure and
war-room-draft endpoints. The parent's
`?cp=<instance_id>` query proxies the request to the owning child;
the child's `/api/v1/federation/...` route is token-authed.
```

- [ ] **Step 2: Run the full Go test suite**

```bash
cd /Users/matt/Code/Oracle/Okesu/.claude/worktrees/feat-case-phases
go test ./...
```

Expected: PASS — every package green.

- [ ] **Step 3: Run the full vitest suite**

```bash
cd web && npx vitest run
```

Expected: PASS.

- [ ] **Step 4: Run typecheck**

```bash
cd web && npx tsc --noEmit
```

Expected: PASS.

- [ ] **Step 5: Commit docs**

```bash
git add docs/architecture.md
git commit -m "docs(architecture): case phases section"
```

- [ ] **Step 6: Push branch**

```bash
git push -u origin feat/case-phases
```

If SSH agent refuses (intermittent in this project), report and STOP — the user pushes manually.

- [ ] **Step 7: Open PR**

```bash
gh pr create --title "feat(timeline): case phases — pinned timeline annotations" --body "$(cat <<'EOF'
## Summary
- Operators can mark named time ranges on the case timeline ("Initial detection 14:00–14:35", "Containment 14:35–16:10").
- New `investigation_phases` table holds the rows; CRUD endpoints under `/api/investigations/{id}/phases[/{phase_id}]` with the standard parent-proxy / child-token federation pair.
- Frontend adds a sibling `<div>` "phases" lane (NOT inside the SVG) above the timeline canvas: rubber-band drag-to-create, click-to-rename, hover-to-delete. Colors are deterministic from `hash(name)` — no picker, no categories.
- Lane is opt-in (not in `DEFAULT_LANES_ON`); lazy-fetched on first toggle.
- Pure stacker handles overlapping phases via greedy first-fit row assignment.

Closes follow-up #5 from `investigation_overview_followups.md` — the last remaining item from the Investigation Overview backlog (PR #94).

Spec: `docs/superpowers/specs/2026-05-02-case-phases-design.md`
Plan: `docs/superpowers/plans/2026-05-02-case-phases.md`

## Test plan
- [ ] `go test ./...` — full Go suite green
- [ ] `cd web && npx vitest run` — full vitest suite green
- [ ] `cd web && npx tsc --noEmit` — typecheck clean
- [ ] Manual: open an active case; toggle the `phases` lane on; drag inside the lane to mark "Initial detection"; type the name + Enter; pill renders with a deterministic color
- [ ] Manual: drag again with overlap; new phase stacks below in row 1
- [ ] Manual: click a pill → rename inline → Enter; hover → click `×` → confirm; pill disappears
- [ ] Manual: reload the page → phases persist
- [ ] Manual on a federated case (`cp_source` set) → phases load via the federated endpoint

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

---

## Self-review notes

**1. Spec coverage:** Every spec section has a task.
- DB layer (migration + Store CRUD + tests) → Task 1.
- Wire shape + endpoints + validation + federation → Task 2.
- `api.ts` plumbing + `TimelineLane` extension + `xToT` → Task 3.
- Pure stacker → Task 4.
- `<PhasesLane>` (drag, rename, delete, hover) → Task 5.
- `CaseTimeline.tsx` integration (lazy fetch + mount) → Task 6.
- Architecture docs + sweep + PR → Task 7.
- Out-of-scope items preserved (no resize, no time edit, no categories, no color picker, no real-time sync, no analytics, no lane reordering).

**2. Placeholder scan:** No "TBD"/"TODO"/"add appropriate"/"similar to" lines. Each step has a concrete code block. The two NOTE callouts (Task 1 step 6 about `nullable`; Task 2 step 1 about `mustCreateInvestigation` collision) point to existing helpers the implementer needs to grep for — not placeholders.

**3. Type consistency:**
- `InvestigationPhase` defined in Task 3 (`api.ts`), consumed in Tasks 4 (`phasesLayer.ts`), 5 (`PhasesLane.tsx`), 6 (`CaseTimeline.tsx`).
- `InvestigationPhaseInsert` defined in Task 1 (Go), used in Task 2 handler signature.
- `Range` type from `scale.ts` consumed by both `phasesLayer.ts` (Task 4) and `PhasesLane.tsx` (Task 5).
- `xToT` defined Task 3, used in Task 5 to convert drag-end pixel x → timestamp.
- `tToX` already exists; used by Task 4's stacker.
- `layoutPhases` defined Task 4, consumed Task 5.
- Endpoint paths (`/api/investigations/{id}/phases[/{phase_id}]`) consistent across Tasks 1 (route registration), 2 (handler tests), 3 (`api.ts` helpers).
- Migration number `059` consistent across Tasks 1 (sqlite + postgres) and the docs.
