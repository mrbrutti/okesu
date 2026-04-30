// Reachable-nodes endpoint for the Run-Agent dialog (and any caller
// that wants to dispatch ad-hoc work). Replaces the older
// /api/nodes/connected which only surfaced tunnel-attached nodes —
// hiding every node that's reachable via HTTPS pull or S3 dead-drop.
//
// A node is "reachable" if at least one of:
//
//   • a reverse tunnel is attached on this CP (real-time gRPC stream)
//   • the okesu-jobs runtime polled within the freshness window
//     (HTTPS pull mode — works for `transport='https'`; same column
//      also bumps for `transport='s3'` via the s3scanner heartbeat
//      sweep, so this gate covers both)
//
// Each item carries the dispatch method that would currently be used
// so the UI can show a transport chip per node and the operator can
// tell apart "real-time tunnel" from "queue-and-claim".

package api

import (
	"encoding/json"
	"net/http"

	"github.com/section9labs/okesu/controlplane/db"
	"github.com/section9labs/okesu/controlplane/tunnel"
)

// ReachableNode is one row of GET /api/nodes/reachable.
type ReachableNode struct {
	Name      string `json:"name"`
	Transport string `json:"transport"`        // 'https' | 's3' | '' for legacy
	// Method is what dispatch would use *right now* — tunnel preferred,
	// pull-mode fallback. Empty string is reserved for future
	// transports we don't yet recognise here.
	Method   string `json:"method"`             // 'tunnel' | 'pull'
	LastSeen string `json:"last_seen,omitempty"` // ISO-8601 from jobs_runtime_seen_at
}

// ReachableNodes returns the set of nodes the CP can dispatch ad-hoc
// runs to via any transport. Tunnel attachment wins when present;
// otherwise we fall back to the pull-mode freshness check.
//
// The endpoint is intentionally cheap: one query against `nodes`
// joined to the in-memory tunnel registry. No agent-library lookup,
// no per-node deep status. Operators tag a hovered chip for full
// context in the UI.
func ReachableNodes(tunReg *tunnel.Registry, store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		// `jobs_runtime_seen_at > now-120s` matches
		// jobsRuntimeFreshOnThisCP's window so the "is this dispatchable?"
		// answer agrees between this read and the dispatch decision.
		rows, err := store.Query(`
			SELECT name, transport,
			       CASE WHEN tunnel_running = 1 THEN 1 ELSE 0 END,
			       CASE WHEN jobs_runtime_seen_at IS NOT NULL
			            AND jobs_runtime_seen_at > datetime('now', '-120 seconds')
			            THEN 1 ELSE 0 END,
			       jobs_runtime_seen_at
			FROM nodes
			WHERE status = 'ready'
			ORDER BY name`)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		// Tunnel registry is the source of truth for "is the connection
		// alive *right now*" — the DB column lags a few seconds. We
		// cross-reference each row.
		tunNames := map[string]struct{}{}
		for _, n := range tunReg.Names() {
			tunNames[n] = struct{}{}
		}

		out := []ReachableNode{}
		for rows.Next() {
			var (
				name      string
				transport string
				dbTunnel  int
				pullFresh int
				lastSeen  *string
			)
			if err := rows.Scan(&name, &transport, &dbTunnel, &pullFresh, &lastSeen); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			_, tunAlive := tunNames[name]
			if tunAlive {
				it := ReachableNode{Name: name, Transport: transport, Method: "tunnel"}
				if lastSeen != nil {
					it.LastSeen = *lastSeen
				}
				out = append(out, it)
				continue
			}
			if pullFresh == 1 {
				it := ReachableNode{Name: name, Transport: transport, Method: "pull"}
				if lastSeen != nil {
					it.LastSeen = *lastSeen
				}
				out = append(out, it)
				continue
			}
			// Not currently reachable — skip. The dialog only wants
			// dispatchable rows. The full Nodes page shows everything.
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}
