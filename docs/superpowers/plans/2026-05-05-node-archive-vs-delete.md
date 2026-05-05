# Node Archive vs Delete — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give operators two clear, distinct ways to retire a node-provision (or any node): **Archive** (cloud VM gone, all history preserved) and **Delete** (everything gone — VM + node row + history + bucket prefixes). Today's destroy is a hybrid that drops the node row but leaves history orphaned by host name, which is neither.

**Architecture:** New `archived_at` column on `nodes` and `node_provisions`. New `archive` action on both surfaces (terminates VM if applicable, sets status=archived, keeps everything). Existing `DELETE …?destroy=true` extended to optionally **purge** history-by-host (new flag) and to sweep the bucket's per-node runtime prefix. UI gets a two-option menu where Archive is the default offboarding action and Delete is confirm-twice.

**Tech Stack:** Go (controlplane), SQLite + Postgres dual-dialect migrations, React/TypeScript frontend.

---

## File Structure

**New migrations:**
- `controlplane/db/migrations/sqlite/062_archived_at.sql`
- `controlplane/db/migrations/postgres/062_archived_at.sql`

**Modified Go (controlplane):**
- `controlplane/db/nodes.go` — `NodeStatusArchived` constant, `ArchiveNode(id, by)`, `PurgeHostHistory(name)` helpers + tests.
- `controlplane/db/node_provisions.go` — `NodeProvisionArchived` constant, `ArchiveNodeProvision(id, by)` helper.
- `controlplane/api/node_provision_list.go` — extend `NodeProvisionDeleteHandler` with `?purge=true` (host history) and bucket-prefix sweep; new `NodeProvisionArchiveHandler`.
- `controlplane/api/nodes.go` — new `NodeArchiveHandler`.
- `controlplane/server.go` — wire two new POST routes.
- `controlplane/adapters/s3blob/blob.go` — new `DeletePrefix(ctx, prefix)` helper (uses existing minio-go ListObjects + RemoveObject).

**Modified web:**
- `web/src/api.ts` — `archiveNode`, `archiveNodeProvision`, extend `nodeProvisionDelete` with `purge?: boolean` argument.
- `web/src/pages/Nodes.tsx` + `web/src/components/nodes/NodeProvisionsPanel.tsx` — replace single delete button with a small dropdown: "Archive" (default) vs "Delete & purge history" (red, two-step confirm).

**Tests:**
- `controlplane/db/nodes_test.go` — exercise `ArchiveNode` (state flip + ts) and `PurgeHostHistory` (counts before/after).
- `controlplane/api/node_provision_list_test.go` — extend with `TestNodeProvisionArchive` and `TestNodeProvisionDelete_PurgeTrue`.

---

## Task 1 — Migration 062: archived_at columns

**Files:**
- Create: `controlplane/db/migrations/sqlite/062_archived_at.sql`
- Create: `controlplane/db/migrations/postgres/062_archived_at.sql`
- Modify: `controlplane/db/store.go` — embed slot, both dialects.

- [ ] **Step 1: SQLite**

```sql
-- 062_archived_at.sql
-- Adds archived_at TIMESTAMP NULL to both nodes and node_provisions
-- so the operator's offboarding action can flip them to a terminal
-- non-deleting "archived" state (cloud VM gone, history preserved).
ALTER TABLE nodes            ADD COLUMN archived_at TIMESTAMP;
ALTER TABLE nodes            ADD COLUMN archived_by_email TEXT;
ALTER TABLE node_provisions  ADD COLUMN archived_at TIMESTAMP;
ALTER TABLE node_provisions  ADD COLUMN archived_by_email TEXT;
```

- [ ] **Step 2: Postgres** (same DDL — TIMESTAMPTZ for the timestamps)

```sql
ALTER TABLE nodes            ADD COLUMN archived_at TIMESTAMPTZ;
ALTER TABLE nodes            ADD COLUMN archived_by_email TEXT;
ALTER TABLE node_provisions  ADD COLUMN archived_at TIMESTAMPTZ;
ALTER TABLE node_provisions  ADD COLUMN archived_by_email TEXT;
```

- [ ] **Step 3: Wire embed slot** in `controlplane/db/store.go` following the 060/061 pattern (`//go:embed migrations/{sqlite,postgres}/062_archived_at.sql` + slice append).

- [ ] **Step 4: Verify**

```bash
cd /Users/matt/Code/Oracle/Okesu/.claude/worktrees/feat-node-managed-deploy
make -s daemon-host cp
rm -f /tmp/test-cp.db && ./okesu-cp serve --db /tmp/test-cp.db --listen :17443 --mgmt-listen :17444 --admin-password test &
sleep 3
sqlite3 /tmp/test-cp.db "PRAGMA table_info(nodes);" | grep archived
sqlite3 /tmp/test-cp.db "PRAGMA table_info(node_provisions);" | grep archived
pkill -f "okesu-cp serve --db /tmp/test-cp.db"
```
Expected: 4 rows total (2 columns × 2 tables).

- [ ] **Step 5: Commit**

```bash
git add controlplane/db/migrations/sqlite/062_archived_at.sql controlplane/db/migrations/postgres/062_archived_at.sql controlplane/db/store.go
git commit -m "feat(db): archived_at columns on nodes + node_provisions"
```

---

## Task 2 — Store helpers: ArchiveNode + ArchiveNodeProvision + PurgeHostHistory

**Files:**
- Modify: `controlplane/db/nodes.go`
- Modify: `controlplane/db/node_provisions.go`
- Test: `controlplane/db/nodes_test.go`

- [ ] **Step 1: Add the constant + helper to `controlplane/db/nodes.go`**

```go
// In the const block at the top of the file:
NodeStatusArchived  = "archived"

// New helpers (add near UpdateNodeStatus):

// ArchiveNode marks a node as archived: sets status='archived',
// stamps archived_at = CURRENT_TIMESTAMP, records the operator's
// email. The node row + all history (events, findings, runs,
// agents — keyed by host name, not FK) survive intact for
// retrospective analysis. Idempotent: archiving an already-
// archived node refreshes the timestamp + email.
func (s *Store) ArchiveNode(id int64, byEmail string) error {
	_, err := s.Exec(`
		UPDATE nodes
		SET status = ?, archived_at = CURRENT_TIMESTAMP,
		    archived_by_email = ?, last_status_at = CURRENT_TIMESTAMP,
		    status_message = 'archived'
		WHERE id = ?
	`, NodeStatusArchived, nullable(byEmail), id)
	return err
}

// PurgeHostHistory removes all history rows that reference a node
// by host name (not FK): events, findings, runs, agents, and any
// run_lines / job_output that joins through them. Used by the
// node-delete-with-purge path so the operator can fully retire a
// host's footprint. Returns the total number of rows deleted across
// all tables for the audit log.
func (s *Store) PurgeHostHistory(host string) (int64, error) {
	if host == "" {
		return 0, nil
	}
	var total int64
	for _, q := range []string{
		`DELETE FROM events     WHERE host = ?`,
		`DELETE FROM findings   WHERE host = ?`,
		`DELETE FROM runs       WHERE node_name = ?`,
		`DELETE FROM agents     WHERE host = ?`,
	} {
		res, err := s.Exec(q, host)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
}
```

- [ ] **Step 2: Add the constant + helper to `controlplane/db/node_provisions.go`**

```go
// In the NodeProvisionStatus const block:
NodeProvisionArchived NodeProvisionStatus = "archived"

// Near SetNodeProvisionError:
func (s *Store) ArchiveNodeProvision(id int64, byEmail string) error {
	_, err := s.Exec(`
		UPDATE node_provisions
		SET status = 'archived', archived_at = CURRENT_TIMESTAMP,
		    archived_by_email = ?, ended_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, nullable(byEmail), id)
	return err
}
```

- [ ] **Step 3: Tests**

In `controlplane/db/nodes_test.go` (or create), add:

```go
func TestArchiveNode(t *testing.T) {
	st := openTempStore(t)
	id, err := st.CreateNode("h1", "10.0.0.1", "root", 22, "")
	if err != nil { t.Fatal(err) }
	if err := st.ArchiveNode(id, "admin@example"); err != nil { t.Fatal(err) }
	row, _ := st.Node(id) // or whatever the Get-by-id is named in this codebase
	if row.Status != NodeStatusArchived { t.Errorf("status=%q want archived", row.Status) }
	// archived_at set; archived_by_email matches; row still exists.
}

func TestPurgeHostHistory(t *testing.T) {
	st := openTempStore(t)
	// Seed events/findings/runs/agents under host "h1".
	// IMPLEMENTER: the seed helpers already exist — see how other
	// tests in this package construct rows. Use the same idioms.
	if _, err := st.PurgeHostHistory("h1"); err != nil { t.Fatal(err) }
	// Assert all four tables now return 0 rows for host=h1.
}
```

(Implementer: read existing tests for how rows in each table are seeded.)

- [ ] **Step 4: Verify**

```bash
go test ./controlplane/db/ -run "TestArchiveNode|TestPurgeHostHistory" -v
```

- [ ] **Step 5: Commit**

```bash
git add controlplane/db/nodes.go controlplane/db/node_provisions.go controlplane/db/nodes_test.go
git commit -m "feat(db): ArchiveNode + ArchiveNodeProvision + PurgeHostHistory helpers"
```

---

## Task 3 — s3blob.DeletePrefix helper

**Files:**
- Modify: `controlplane/adapters/s3blob/blob.go`

- [ ] **Step 1: Add the helper**

```go
// DeletePrefix removes every object whose key starts with the given
// prefix. Used by the node/cp destroy paths to sweep the per-node
// or per-CP bucket folder (cp/<id>/nodes/<n>/* and cp/<child>/*).
// Lists in batches and removes via the bulk RemoveObjects channel
// so a thousand-object delete completes in one round-trip.
//
// Best-effort: a partial failure does not roll back; callers log
// the error and proceed (delete-of-row is the operator's final
// intent and a stale JSON object in the bucket is harmless).
func (a *Adapter) DeletePrefix(ctx context.Context, prefix string) error {
	objCh := make(chan minio.ObjectInfo)
	go func() {
		defer close(objCh)
		for obj := range a.cli.ListObjects(ctx, a.bucket, minio.ListObjectsOptions{
			Prefix:    prefix,
			Recursive: true,
		}) {
			if obj.Err != nil {
				continue
			}
			objCh <- obj
		}
	}()
	errCh := a.cli.RemoveObjects(ctx, a.bucket, objCh, minio.RemoveObjectsOptions{})
	for e := range errCh {
		if e.Err != nil {
			return fmt.Errorf("s3blob: delete prefix %q: %w", prefix, e.Err)
		}
	}
	return nil
}
```

- [ ] **Step 2: Verify**

```bash
go build ./controlplane/adapters/s3blob/...
go test ./controlplane/adapters/s3blob/...
```

- [ ] **Step 3: Commit**

```bash
git add controlplane/adapters/s3blob/blob.go
git commit -m "feat(s3blob): DeletePrefix bulk helper for destroy-path bucket sweeps"
```

---

## Task 4 — API: archive endpoints

**Files:**
- Modify: `controlplane/api/nodes.go` — add `NodeArchiveHandler`.
- Modify: `controlplane/api/node_provision_list.go` — add `NodeProvisionArchiveHandler`.
- Modify: `controlplane/server.go` — register routes.
- Test: `controlplane/api/node_provision_list_test.go` extend.

- [ ] **Step 1: NodeArchiveHandler**

```go
// In controlplane/api/nodes.go, near NodeDeleteHandler:

// NodeArchiveHandler is the offboarding action — it sets the node's
// status to 'archived' so it disappears from the active list but
// keeps every row that references the host name (events, findings,
// runs, agents). Use it when the cloud VM is going away but the
// historical data is still useful for retrospective analysis.
//
// No cloud-side teardown happens here: the node-provisions archive
// handler is the right surface when an operator wants the cloud VM
// terminated alongside. This handler exists for nodes that don't
// have a managed-deploy provision (SSH-pushed, manually enrolled).
func NodeArchiveHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		// Best-effort fetch for audit metadata.
		row, _ := store.Node(id) // adapt to existing getter name
		var byEmail string
		if u := auth.UserFromContext(r.Context()); u != nil {
			byEmail = u.Email
		}
		if err := store.ArchiveNode(id, byEmail); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		audit.Emit(r, store, db.AuditEntry{
			Action: "node.archive",
			Target: fmt.Sprintf("node:%d", id),
			Metadata: map[string]any{
				"name": func() string {
					if row != nil { return row.Name }
					return ""
				}(),
			},
		})
		w.WriteHeader(http.StatusNoContent)
	}
}
```

- [ ] **Step 2: NodeProvisionArchiveHandler**

```go
// In controlplane/api/node_provision_list.go:

// NodeProvisionArchiveHandler terminates the cloud VM (best-effort,
// same chain as NodeProvisionDeleteHandler) and archives both the
// linked nodes row AND the node_provisions row. Unlike the destroy
// path, neither row is dropped — the operator can still see the
// host's history in the Nodes UI under the "Archived" filter and
// the provisions panel keeps the row for audit.
//
// Bucket-side: the one-time package blob is deleted (it's dead
// weight after the VM is gone). Per-node runtime prefix
// (cp/<cp>/nodes/<n>/*) is preserved so any in-flight log files
// or last-known-state JSON are still inspectable via mc.
func NodeProvisionArchiveHandler(
	store *db.Store, registry *cpprovision.Registry,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		row, err := store.NodeProvision(id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		var byEmail, destroyErr string
		if u := auth.UserFromContext(r.Context()); u != nil {
			byEmail = u.Email
		}

		// Always terminate the VM if there is one — that's the
		// "archive" intent (compute gone, data preserved).
		if row.CloudResourceID.Valid && row.CloudResourceID.String != "" && row.CredentialID.Valid {
			if prov, perr := registry.Get(row.Cloud); perr == nil {
				if mk, mkErr := store.MasterKeyFromMeta(); mkErr == nil {
					if credBytes, derr := store.DecryptCloudCredential(row.CredentialID.Int64, mk); derr == nil {
						if dErr := prov.Destroy(r.Context(), row.CloudResourceID.String, row.Region, credBytes); dErr != nil {
							destroyErr = dErr.Error()
						}
					}
				}
			}
		}

		// Drop the dead-weight package blob.
		if row.TransportConfigID != 0 {
			_ = deleteNodeProvisionPackageBlob(r.Context(), store, row.TransportConfigID, row.ID)
		}

		// Archive both rows — keep, don't delete.
		if row.NodeID.Valid && row.NodeID.Int64 != 0 {
			if err := store.ArchiveNode(row.NodeID.Int64, byEmail); err != nil {
				log.Printf("node_provisions archive %d: archive node %d: %v", row.ID, row.NodeID.Int64, err)
			}
		}
		if err := store.ArchiveNodeProvision(id, byEmail); err != nil {
			http.Error(w, "archive: "+err.Error(), http.StatusInternalServerError)
			return
		}

		audit.Emit(r, store, db.AuditEntry{
			Action: "node_provision.archive",
			Target: fmt.Sprintf("node_provision:%d", id),
			Metadata: map[string]any{
				"display_name":  row.DisplayName,
				"cloud":         row.Cloud,
				"destroy_error": destroyErr,
			},
		})
		if destroyErr != "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"archived":      true,
				"destroy_error": destroyErr,
			})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
```

- [ ] **Step 3: Wire routes in server.go**

Locate the admin-only chi.Group where node-provision routes register (Phase 21.7 block, around lines 1140–1151):

```go
r.Post("/api/nodes/{id}/archive",                api.NodeArchiveHandler(s.store))
r.Post("/api/node-provisions/{id}/archive",      api.NodeProvisionArchiveHandler(s.store, s.cpProvisioners))
```

- [ ] **Step 4: Verify**

```bash
go build ./...
go test ./controlplane/api/ -run TestNodeProvisionArchive -v   # the test you'll write next
```

- [ ] **Step 5: Commit**

```bash
git add controlplane/api/nodes.go controlplane/api/node_provision_list.go controlplane/server.go
git commit -m "feat(api): archive endpoints for nodes + node-provisions"
```

---

## Task 5 — DELETE handler extended: ?purge=true + bucket prefix sweep

**Files:**
- Modify: `controlplane/api/node_provision_list.go::NodeProvisionDeleteHandler`
- Test: `controlplane/api/node_provision_list_test.go` extend.

- [ ] **Step 1: Extend the delete handler**

After the existing nodes-row + node_provisions row deletes, add:

```go
purge := r.URL.Query().Get("purge") == "true"
hostName := ""
if row.NodeID.Valid && row.NodeID.Int64 != 0 {
    if n, err := store.Node(row.NodeID.Int64); err == nil && n != nil {
        hostName = n.Name
    }
}
// (NodeID has been NULL'd already by DeleteNode SET NULL cascade if
// we deleted it above — capture name BEFORE the delete in the new
// version; refactor accordingly.)
if purge && hostName != "" {
    if n, err := store.PurgeHostHistory(hostName); err != nil {
        log.Printf("node_provisions delete %d: purge history for %q: %v", row.ID, hostName, err)
    } else if n > 0 {
        log.Printf("node_provisions delete %d: purged %d history rows for host %q", row.ID, n, hostName)
    }
}

// Bucket sweep: when the operator chose Delete (vs Archive), we
// also remove the per-node runtime prefix cp/<cp_id>/nodes/<id>/*.
// Skipped for Archive (which preserves bucket state).
if row.NodeID.Valid && row.NodeID.Int64 != 0 && row.TransportConfigID != 0 {
    // We need cp_id from the transport_config to compose the prefix.
    if tc, err := store.GetTransportConfig(row.TransportConfigID); err == nil && tc.CPID.Valid {
        prefix := fmt.Sprintf("cp/%s/nodes/%d/", tc.CPID.String, row.NodeID.Int64)
        if blob, blobErr := s3blobNew(r.Context(), tc); blobErr == nil {
            if err := blob.DeletePrefix(r.Context(), prefix); err != nil {
                log.Printf("node_provisions delete %d: bucket sweep %q: %v", row.ID, prefix, err)
            }
        }
    }
}
```

(Implementer: refactor the order so `hostName` is captured before `DeleteNode` runs; the existing helper `deleteNodeProvisionPackageBlob` already constructs the s3blob client — extract a tiny shared `s3blobNew(ctx, tc)` helper to reuse.)

- [ ] **Step 2: Tests**

Add `TestNodeProvisionDelete_PurgeTrue`: seed events/findings/runs for the host, call DELETE with `?purge=true`, assert all three are gone.

Add `TestNodeProvisionArchive`: assert the row stays with status='archived' and `archived_at` set; assert events/findings/runs survive.

- [ ] **Step 3: Verify**

```bash
go test ./controlplane/api/ -run "TestNodeProvisionDelete|TestNodeProvisionArchive" -v
```

- [ ] **Step 4: Commit**

```bash
git add controlplane/api/node_provision_list.go controlplane/api/node_provision_list_test.go
git commit -m "feat(api): node-provision delete supports ?purge=true + bucket prefix sweep"
```

---

## Task 6 — UI: dropdown with Archive (default) + Delete (confirm-twice)

**Files:**
- Modify: `web/src/api.ts` — `archiveNode`, `archiveNodeProvision`, extend `nodeProvisionDelete` signature.
- Modify: `web/src/components/nodes/NodeProvisionsPanel.tsx` — destructive-action dropdown.
- Modify: `web/src/pages/Nodes.tsx` (node detail row) — same dropdown.

- [ ] **Step 1: API client methods**

```ts
// In web/src/api.ts:
  archiveNode: (id: number) =>
    request<void>(`/api/nodes/${id}/archive`, { method: 'POST' }),
  archiveNodeProvision: (id: number) =>
    request<void | { archived: boolean; destroy_error?: string }>(
      `/api/node-provisions/${id}/archive`, { method: 'POST' },
    ),
// Extend the existing:
  nodeProvisionDelete: (id: number, opts: { destroy?: boolean; purge?: boolean } = {}) => {
    const qs: string[] = [];
    if (opts.destroy) qs.push('destroy=true');
    if (opts.purge) qs.push('purge=true');
    return request<void | { deleted: boolean; destroy_error?: string }>(
      `/api/node-provisions/${id}${qs.length ? '?' + qs.join('&') : ''}`,
      { method: 'DELETE' },
    );
  },
```

- [ ] **Step 2: Replace the panel's destructive action with a small menu**

Pattern: a single button "Retire ▾" that opens a popover with two rows:
1. **Archive** (default, no confirm beyond the popover click) — calls `archiveNodeProvision(id)`.
2. **Delete & purge history** (red, requires typing the node name into a confirmation field) — calls `nodeProvisionDelete(id, { destroy: true, purge: true })`.

(Implementer: see the existing two-tier confirm in `Federation.tsx::ManagedDeploysPanel` — it uses `window.confirm`; we want something nicer here. Use a small inline `<details>` or a click-twice pattern; pick whichever matches the codebase's existing destructive-action UX.)

- [ ] **Step 3: Smoke test**

```bash
make redeploy-local
```
Open the Nodes page, locate a managed-deploy row in `<NodeProvisionsPanel/>`, click Retire → Archive → confirm row flips to status=archived and disappears from the active list. Click Retire on a different row → Delete → type the name → confirm row + nodes row + history all gone.

- [ ] **Step 4: Commit**

```bash
git add web/src/api.ts web/src/pages/Nodes.tsx web/src/components/nodes/
git commit -m "feat(web/nodes): Retire dropdown — Archive (default) vs Delete & purge"
```

---

## Task 7 — End-to-end on OCI

- [ ] **Step 1: Provision two managed nodes**

Submit two `POST /api/node-provision` requests — one designated for archive, one for delete-with-purge.

- [ ] **Step 2: Generate history**

Fire one or two `agent_run`s on each node so events/runs accumulate.

- [ ] **Step 3: Archive one**

```bash
curl -sk -b /tmp/c.txt -X POST "https://localhost:7443/api/node-provisions/N/archive"
```
Verify:
- OCI VM TERMINATED.
- `nodes` row exists, `status='archived'`, `archived_at` set.
- `runs` for that host still present.
- Bucket package blob gone; `cp/<cp>/nodes/<n>/*` runtime prefix preserved.

- [ ] **Step 4: Delete the other with purge**

```bash
curl -sk -b /tmp/c.txt -X DELETE "https://localhost:7443/api/node-provisions/M?destroy=true&purge=true"
```
Verify:
- OCI VM TERMINATED.
- `nodes` row gone.
- `runs` / `events` / `findings` for that host: zero rows.
- `cp/<cp>/nodes/<m>/*` bucket prefix: empty.

- [ ] **Step 5: Open PR**

```bash
git push -u origin feat/node-archive-vs-delete
gh pr create --title "feat(nodes): Archive vs Delete — explicit retirement actions" --body @docs/superpowers/plans/2026-05-05-node-archive-vs-delete.md
```

---

## Out of scope / explicit non-goals

- Cp_provision archive: cp_provisions today already keeps the row when you destroy a CP — but the `federation_peers` row is dropped (Task 9 of PR #130). An archive flow for CPs is out of scope here; the symmetry case can be a follow-up if operators ask.
- Restoring an archived node (re-deploy on the same name, recover the historical findings): out of scope. Archive is a terminal state in this PR.
- Bucket-side archive (move-to-cold-storage instead of preserving the active prefix): out of scope.
- Mass-archive / bulk delete UI: out of scope; per-row only.
