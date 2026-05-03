# War-Room Notes — collaborative draft editing

## Goal

Add a "war-room draft" alongside the existing single-author note
composer: multiple operators connect to a shared, real-time co-edited
draft buffer scoped to one investigation, see each other's cursors +
typing, and on Send the draft becomes one immutable
`InvestigationNote` row preserving the existing audit chain. The
simple-note path is unchanged — operators pick the right tool for the
job (war-room vs quick comment).

Follow-up #3 from
`docs/superpowers/specs/2026-05-01-investigation-overview-design.md`,
parked in the `investigation_overview_followups.md` memory.

## Architecture

A war-room draft is a Yjs document scoped to one case. Operators
connect to `wss://cp/api/investigations/{id}/draft/ws` (federated path
proxies to the owning child). The Go relay decodes Yjs sync messages
just enough to track presence + persist snapshots; otherwise it
forwards bytes between connected clients. A new
`investigation_note_drafts` table holds the latest snapshot keyed by
investigation ID — one row per case at most, upserted every 5s while
the room is dirty and on disconnect.

On Send, the current Y.Text is converted to plain string and written
as one immutable `InvestigationNote` row via the existing
`Store.AddInvestigationNote`; the draft row is deleted; all clients
receive a `session-end` message and close their panels.

Auth: cookie session for the parent path, federation token for the
child path. The relay rejects unauthenticated upgrades with 401.

A garbage-collection sweep (matches the existing audit-log sweep
pattern) deletes draft rows older than 7 days. Operators see a
"draft will expire in N day(s)" warning in the panel header when the
row is older than 6 days.

## File structure

| File | Status | Responsibility |
|---|---|---|
| `controlplane/db/migrations/sqlite/050_investigation_note_drafts.sql` (+ postgres twin) | NEW | Migration: `investigation_note_drafts` table |
| `controlplane/db/investigation_note_drafts.go` | NEW | `Get/Upsert/Delete InvestigationNoteDraft` Store methods + GC sweep |
| `controlplane/db/investigation_note_drafts_test.go` | NEW | DB tests |
| `controlplane/api/investigation_draft_relay.go` | NEW | Per-case relay hub (in-memory `map[int64]*room`); pure relay logic, no HTTP |
| `controlplane/api/investigation_draft_relay_test.go` | NEW | Relay tests (join, broadcast, snapshot, teardown) |
| `controlplane/api/investigation_draft_ws.go` | NEW | WebSocket handler: upgrade, register client, route Yjs messages, federation parent + child pair |
| `controlplane/api/investigation_draft_ws_test.go` | NEW | WS handler tests using `httptest.NewServer` + `gorilla/websocket` client |
| `controlplane/api/investigation_draft_finalize.go` | NEW | `POST /draft/finalize` — convert current snapshot to immutable note, broadcast session-end. Federation pair (plain JSON proxy). |
| `controlplane/api/investigation_draft_finalize_test.go` | NEW | Finalize tests |
| `controlplane/server.go` | MODIFY | Register four routes (parent + child for /ws and /finalize) |
| `go.mod` / `go.sum` | MODIFY | Add `github.com/gorilla/websocket` |
| `web/src/api.ts` | MODIFY | `api.investigations.finalizeDraft(id, cpInstanceID?)` and the WS URL builder |
| `web/src/components/investigations/warRoomDraft/yjsConnection.ts` | NEW | Y.Doc + WebSocket transport + Awareness wrapper. Encapsulates protocol so the React component is UX-only |
| `web/src/components/investigations/warRoomDraft/yjsConnection.test.ts` | NEW | Connection wrapper tests |
| `web/src/components/investigations/WarRoomDraftPanel.tsx` | NEW | Collab UI: textarea bound to Y.Text, cursor overlays, presence chip row, Send/Cancel |
| `web/src/components/investigations/WarRoomDraftPanel.test.tsx` | NEW | Component tests with a mocked Yjs connection |
| `web/src/pages/InvestigationDetail.tsx` | MODIFY | Add the "Open war-room draft" affordance next to the existing simple-note composer |
| `package.json` | MODIFY | Add `yjs`, `y-protocols` |
| `docs/architecture.md` | MODIFY | New section under "Investigation Overview" |

## Wire protocol

WebSocket binary frames using the standard `y-websocket` envelope
(we port the relevant ~30 lines of the protocol into Go):

```
byte 0:    messageType — 0=sync, 1=awareness, 2=session-end (server→client only)
remainder: payload
```

For `messageType=0` (sync): a varint sub-type + Yjs-encoded delta.
The relay decodes the sub-type to distinguish "client sending its
update" (forward + persist) from "client requesting full state"
(reply with current snapshot).

For `messageType=1` (awareness): the relay forwards verbatim and
tracks the latest awareness payload per client so a late joiner can
be backfilled.

For `messageType=2` (session-end, server→client only): emitted on
finalize success. Body is `{"note_id": <int>}`. Clients close their
WebSocket on receipt. The server treats inbound type 2 from a
client as a no-op (clients should never send it).

The relay only decodes Yjs internals on:
1. **Snapshot persistence** (5s tick + on disconnect) — applies
   buffered updates to the server-side `*y.Doc` and serializes the
   merged state to BLOB.
2. **Finalize** — reads the `body` Y.Text out as a plain string.

Hot-path message forwarding is byte-level memcpy via the
per-client `send` channel.

### Relay state per case (in memory)

```go
type room struct {
    investigationID int64
    clients         map[*wsClient]struct{}
    awareness       map[uint64][]byte // clientID → latest awareness payload
    ydoc            *Y.Doc            // server-side merged snapshot
    dirty           bool              // set on sync update; cleared on snapshot
    snapshotTimer   *time.Timer
    teardownTimer   *time.Timer       // 30s grace after last leave
    mu              sync.Mutex
}
```

All rooms live in a process-wide `map[int64]*room` guarded by a
`sync.RWMutex` on the `RelayHub`. Empty rooms are torn down 30s
after the last disconnect — gives a quick reconnect a chance to
land in the same room without re-loading from DB.

## DB row shape (`investigation_note_drafts`)

```sql
CREATE TABLE investigation_note_drafts (
  investigation_id INTEGER PRIMARY KEY REFERENCES investigations(id) ON DELETE CASCADE,
  ydoc_state       BLOB    NOT NULL,
  updated_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

One row per case at most. `ydoc_state` is `Y.encodeStateAsUpdate(doc)`
bytes — opaque to SQL, restored by the relay on first connect after
process restart or after empty-room teardown.

Snapshot cadence:
1. Every 5s while a room has at least one client and `dirty == true`.
2. Immediately when the last client disconnects.
3. Immediately on Send (just before the row is deleted).

GC sweep (daily, matches existing audit-log sweep pattern):

```sql
DELETE FROM investigation_note_drafts
WHERE updated_at < datetime('now', '-7 days')
  AND investigation_id NOT IN (... live in-memory rooms ...)
```

The "live rooms" exclusion is a Go-side filter — pass the snapshot
of live room IDs into the sweep call.

## Server-side handlers

### Local WebSocket handler

```go
func GetInvestigationDraftWSHandler(store *db.Store, hub *RelayHub) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        invID, err := investigationIDFromChi(r)
        if err != nil { http.Error(w, err.Error(), 400); return }
        if _, err := store.GetInvestigation(invID); err != nil {
            http.Error(w, "not found", 404); return
        }
        u := auth.UserFromContext(r.Context())
        if u == nil { http.Error(w, "unauthenticated", 401); return }

        ws, err := upgrader.Upgrade(w, r, nil)
        if err != nil { return }

        client := &wsClient{
            ws:     ws,
            user:   u,
            roomID: invID,
            send:   make(chan []byte, 64),
        }
        room := hub.JoinOrLoad(invID, store, client)
        defer room.Leave(client)
        client.run(r.Context())
    }
}
```

`hub.JoinOrLoad` is the only function that touches the
`map[int64]*room`; guards with `RWMutex.Lock` for create, `RLock`
for lookup. On first join: allocates the room, loads `ydoc_state`
from DB if present, constructs the server-side `*y.Doc`, starts the
5s snapshot timer.

`room.Leave(client)` removes the client, clears its awareness entry,
broadcasts an awareness-leave to the others, schedules teardown in
30s if the room is now empty, forces a final snapshot.

`client.run(ctx)` runs two goroutines (read pump + write pump) in
the standard gorilla/websocket pattern. Read pump applies sync
updates to the room's `ydoc` under the room mutex, broadcasts raw
bytes to other clients, updates awareness map, handles message-type
2 explicitly (server→client only — clients ignore inbound type 2).
Write pump drains `client.send` and writes to the WebSocket.

Snapshot tick captures bytes under the mutex, releases the mutex,
then writes to DB — DB I/O never blocks the relay.

### Federation pair

```go
func FederatedInvestigationDraftWS(store *db.Store, hub *RelayHub, agg *federation.Aggregator) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        cpID := r.URL.Query().Get("cp")
        if cpID != "" {
            proxyDraftWS(w, r, agg, cpID)
            return
        }
        GetInvestigationDraftWSHandler(store, hub).ServeHTTP(w, r)
    }
}

func FederationInvestigationDraftWS(store *db.Store, hub *RelayHub) http.HandlerFunc {
    return requireFederationToken(store, getInvestigationDraftWSHandlerFederated(store, hub))
}
```

`proxyDraftWS` (the only really-novel code in this PR):

```go
func proxyDraftWS(w http.ResponseWriter, r *http.Request, agg *federation.Aggregator, cpID string) {
    target, err := agg.WSURLFor(cpID, r.URL.Path)
    if err != nil { http.Error(w, "cp not found", 502); return }

    headers := http.Header{
        "X-Okesu-Federation-Token":    {agg.TokenFor(cpID)},
        "X-Okesu-Federation-Operator": {operatorEmail(r)},
    }
    childConn, _, err := websocket.DefaultDialer.Dial(target, headers)
    if err != nil { http.Error(w, "cp unreachable: "+err.Error(), 502); return }
    defer childConn.Close()

    parentConn, err := upgrader.Upgrade(w, r, nil)
    if err != nil { return }
    defer parentConn.Close()

    done := make(chan struct{})
    go pumpBytes(parentConn, childConn, done, "parent→child")
    go pumpBytes(childConn, parentConn, done, "child→parent")
    <-done
}
```

`pumpBytes` is a `for` loop reading binary frames from one side and
writing to the other; closes on read error.

Child-side `getInvestigationDraftWSHandlerFederated` is identical to
the local handler except the user identity comes from the
`X-Okesu-Federation-Operator` header set by the parent on dial.

### Finalize endpoint

```
POST /api/investigations/{id}/draft/finalize
{ "author": "alice@org" }       // optional; defaults to authenticated email

→ 200 { "note_id": 123 }
→ 409 { "error": "another finalize in flight" }
→ 404 { "error": "no draft exists" }
```

Server-side: opens the room (loading from DB if not in memory),
reads the current `body` Y.Text as a string, calls
`Store.AddInvestigationNote`, deletes the draft row, broadcasts a
`messageType=2` session-end frame to all connected WS clients, then
closes the room (cancels the snapshot timer, removes from the hub).

A per-investigation finalize mutex on the `RelayHub` prevents two
concurrent finalize requests from racing — the second returns 409.

### Routes registered in `server.go`

```go
r.Get( "/api/investigations/{id}/draft/ws",       api.FederatedInvestigationDraftWS(s.store, s.draftHub, s.fedAgg))
r.Post("/api/investigations/{id}/draft/finalize", api.FederatedInvestigationDraftFinalize(s.store, s.draftHub, s.fedAgg))

r.Get( "/api/v1/federation/investigations/{id}/draft/ws",       api.FederationInvestigationDraftWS(s.store, s.draftHub))
r.Post("/api/v1/federation/investigations/{id}/draft/finalize", api.FederationInvestigationDraftFinalize(s.store, s.draftHub))
```

`s.draftHub` is the new `*RelayHub` allocated at server startup,
lifetime-tied to the process. One hub per CP.

### Failure modes

- Unknown investigation ID → 404 before WS upgrade.
- Unauthenticated → 401 before WS upgrade.
- Federation child unreachable → 502 from the proxy on dial.
- WebSocket frame parse error mid-session → close with code 1003;
  client reconnects; relay reuses the room.
- Snapshot DB write fails → log + keep relay running; in-memory
  state unaffected; retry on next 5s tick.
- Hub map contention → mutex; per-client throughput bounded by the
  64-buffered send channel.
- Concurrent finalize → 409 on the loser.

## Client UX

`<WarRoomDraftPanel>` mounts under the existing `<NotesPanel>`. The
panel header gets a button "Open war-room draft" next to the
existing "Add note" button. Clicking expands the panel into the
collab layout; clicking again collapses it. If a draft already
exists for the case (DB row present or relay reports a non-empty
room), the button label switches to "Join war-room draft" with a
small chip showing participant count.

### Component layout

```
┌──────────────────────────────────────────────────────────────────────┐
│  ●  alice@org   ●  bob@org   ●  you (charlie@org)         [1 unsent] │  presence row
├──────────────────────────────────────────────────────────────────────┤
│                                                                      │
│  Initial detection at 14:02 — edr-fedora-3 ⎸                         │
│  showing CRIT findings against the auth daemon.                      │  draft body
│  Bob's cursor: ⎸ adding pivot table next.                            │  (Y.Text-bound textarea
│  ⎸                                                                   │   with cursor overlays)
│                                                                      │
├──────────────────────────────────────────────────────────────────────┤
│  Plain text · 423 chars                           [Cancel]   [Send]  │  footer
└──────────────────────────────────────────────────────────────────────┘
```

- **Presence row**: one chip per connected user (email + colored
  dot, color hashed from email so Alice always shows the same
  color). Current operator's chip says "you" + email, sits last.
  "1 unsent" indicator on the right is whether the local doc has
  unflushed local changes (transient, ~50ms).

- **Draft body**: `<textarea>` bound to the Yjs `Y.Text`. Hand-rolled
  ~50 LOC binding: `Y.Text.observe()` updates the textarea's `value`;
  `onChange` diffs against the previous Y.Text and applies as one
  Yjs transaction. Other operators' cursors render as
  absolute-positioned `<div>` overlays computed from awareness
  state. Own cursor uses the native textarea cursor.

- **Footer**: live char count, Cancel button (closes WS, leaves
  room — others may still be editing), Send button (POSTs to
  `/finalize`).

### `yjsConnection.ts` interface

```ts
export interface YjsConnection {
  ydoc: Y.Doc;
  awareness: Awareness;
  status: 'connecting' | 'connected' | 'closed';
  participants: Participant[];                 // React-friendly snapshot
  sendFinalize: () => Promise<{ noteID: number }>;
  close: () => void;
}

export function connectWarRoomDraft(
  invID: number,
  cpInstanceID: string | undefined,
  user: { email: string; displayName: string },
): YjsConnection;
```

The component talks only to this interface; tests mock it without
dragging Yjs into vitest's environment.

### Edge cases

- **WS reconnect.** On unexpected close, the connection wrapper
  exponential-backoff reconnects (250ms, 500ms, 1s, 2s, capped at
  5s). `status` flips to 'connecting' for chip UI feedback. Yjs
  handles resync automatically — late-state delta sent on connect.
- **Send while disconnected.** Send button disabled when
  `status !== 'connected'`.
- **Send race.** Server-side: first finalize wins, second gets 409.
  Client: pre-disable on click + optimistic awareness broadcast
  ("alice is finalizing…"). Conservative; covers the rare race.
- **Operator opens war-room while finalize is mid-flight.** They
  join, but the room is about to close. Session-end arrives within
  ms; panel closes; new note appears in the list.
- **Composer closed locally with unsaved local-only edits.** Yjs
  flushes on close; 5s relay tick snapshots. Worst case: ~5s of
  edits could be lost only if BOTH client AND relay snapshot fail —
  acceptable.

## Lifecycle

```
[no draft]
   │
   │   operator A clicks "Open war-room draft"
   ▼
[room: 1 client, doc empty]
   │
   │   B joins
   ▼
[room: 2 clients, doc filling]   ← snapshots every 5s, persisted to DB
   │
   │     ┌─── A clicks Cancel (just leaves room) ──┐
   │     ▼                                          │
   │  [room: 1 client]                              │
   │     │                                          │
   │     │   B clicks Cancel too                    │
   │     ▼                                          │
   │  [room: 0 clients] ── 30s teardown timer ──→ [no in-mem room]
   │                                                │   draft row stays in DB
   │                                                ▼
   │                                          anyone re-opens → load from DB → resume
   │
   │   anyone clicks Send
   ▼
POST /finalize → write InvestigationNote → delete draft row → broadcast session-end
   │
   ▼
[room torn down, all WS closed, list shows new immutable note]
```

## Testing

### DB layer (`investigation_note_drafts_test.go`)

- Upsert creates a row when none exists; updates `ydoc_state` when one does.
- Get returns `sql.ErrNoRows` for unknown investigation_id.
- Delete is idempotent.
- 7-day GC sweep deletes only stale rows (excluding live-room IDs).

### Relay (`investigation_draft_relay_test.go`)

- `Hub.JoinOrLoad` creates a room on first join, returns the same
  room on second join for the same investigation.
- Two clients in a room: bytes sent by A reach B (and vice versa)
  without re-decode.
- Snapshot tick writes `ydoc_state` to DB after a sync update;
  clears `dirty`.
- Empty room teardown fires after the 30s grace; in-flight new
  joiner cancels the timer.
- Awareness leave broadcast on disconnect.

### WS handler (`investigation_draft_ws_test.go`)

- 401 when unauthenticated.
- 404 when investigation doesn't exist.
- Two `httptest.NewServer`-backed clients connecting to the same
  case can echo Yjs sync bytes through the relay.
- Federation parent → child proxy: client connects to parent's
  `?cp=<id>` path, parent dials child stub, bytes round-trip. Uses
  two `httptest.NewServer` instances.
- Federation token rejection on the child path without
  `X-Okesu-Federation-Token`.

### Finalize (`investigation_draft_finalize_test.go`)

- Finalize converts the current Y.Text to string, calls
  `Store.AddInvestigationNote`, deletes the draft row.
- Two finalize requests in flight: first wins, second returns 409.
- Federation: parent finalize proxies to child via plain JSON proxy
  (this one is not WS — easier).

### Client connection wrapper (`yjsConnection.test.ts`)

- `connectWarRoomDraft` returns an object with `status: 'connecting'`
  initially, flips to `'connected'` after WS opens.
- Reconnect on unexpected close — fakes a closed WS, verifies new
  socket opened after the backoff delay.
- `sendFinalize` POSTs to `/api/investigations/{id}/draft/finalize`.

### Component (`WarRoomDraftPanel.test.tsx`)

- Renders the participant chip row from a stubbed `participants`
  array.
- Disables Send when `status !== 'connected'`.
- Click Send calls `sendFinalize` and triggers `onClose` on success.
- Shows the "draft expires in N day(s)" warning when the snapshot
  is older than 6 days.

### Manual lab smoke (post-merge)

- Open an investigation in two browsers (different operators).
  Click "Open war-room draft" in both. Type in one — text appears
  in the other within ~250ms. Cursor positions update live.
- Disconnect operator A's network. B continues editing; A's chip
  flips to a "disconnected" gray. Reconnect A within 5s — A's
  edits sync via Yjs resync; B sees them merged.
- Operator A clicks Send. Both panels close; new note appears at
  the top of the notes list for both.
- Open a war-room draft on a federated case (`cp_source` set).
  Confirm one WS connection in DevTools targeting
  `/api/investigations/{id}/draft/ws?cp=<id>`. Confirm presence +
  bytes flow.
- Close both browsers without sending. Wait 1 minute. Reopen one.
  The draft text is still there (loaded from the DB row).

## Out of scope (explicit cuts)

- **Markdown / rich-text editor.** Plain Y.Text + textarea.
  Markdown rendering can ship in a follow-up by swapping the binding
  for `y-prosemirror` + `tiptap`; the relay protocol doesn't change.
- **Operation history / undo across clients.** Yjs gives local undo
  for free; cross-client undo is intentionally not exposed.
- **Diff view between draft revisions.** A draft is one document;
  it ends as one note. We don't surface intermediate snapshots.
- **Streaming on the simple-note path.** The simple-note composer
  stays single-author with no presence indicators.
- **RBAC beyond "logged-in operator on this case."** Anyone who can
  view the case can join the war room. No per-user lock, no
  "request handoff" UX, no observer-only mode.
- **Edit-after-send.** Sent notes remain immutable. The whole
  point of choosing draft-co-edit (Q2 = A) was preserving the audit
  trail.
- **Cross-process state sharing.** A multi-replica CP would need a
  Redis pub/sub for the relay to broadcast across replicas. We
  stay single-process for v1; lab + production typically run a
  single CP instance per tenancy.
- **Mobile / touch cursor rendering.** Cursor overlays target
  desktop; mobile users get the textarea + presence chips but not
  floating cursors.
- **Multiple parallel drafts per case.** Q3 = A picked the
  single-shared-room model. Two analysts wanting parallel drafts
  use the simple-note path (one types and sends, the other types
  and sends; result is two separate notes).
