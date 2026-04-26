package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/section9labs/okesu/controlplane/audit"
	"github.com/section9labs/okesu/controlplane/db"
)

// SystemDBConfig captures the runtime knobs the Settings → Database page
// surfaces alongside the live stats. Read-only — operators change these by
// updating env/flags and restarting the CP, not via the API.
type SystemDBConfig struct {
	EventTTLDays int `json:"event_ttl_days"`
}

// DBStats handles GET /api/system/db/stats.
func DBStats(store *db.Store, cfg SystemDBConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		stats, err := store.Stats()
		if err != nil {
			http.Error(w, "stats: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"stats":  stats,
			"config": cfg,
		})
	}
}

// DBVacuum handles POST /api/system/db/vacuum.
// VACUUM is blocking; we keep the request open while it runs. Operators run
// this rarely (after a big retention prune) so the latency is acceptable.
func DBVacuum(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		if err := store.Vacuum(); err != nil {
			audit.Emit(r, store, db.AuditEntry{
				Action:   "system.db_vacuum",
				Result:   "error",
				Metadata: map[string]any{"error": err.Error()},
			})
			http.Error(w, "vacuum: "+err.Error(), http.StatusInternalServerError)
			return
		}
		dur := time.Since(started)
		audit.Emit(r, store, db.AuditEntry{
			Action:   "system.db_vacuum",
			Metadata: map[string]any{"duration_ms": dur.Milliseconds()},
		})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":          true,
			"duration_ms": dur.Milliseconds(),
		})
	}
}

// DBPruneEvents handles POST /api/system/db/prune-events?older_than_days=N.
// Manual operator-triggered prune (the retention goroutine does this on a
// schedule; this endpoint is for one-off cleanup).
func DBPruneEvents(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		days, _ := strconv.Atoi(r.URL.Query().Get("older_than_days"))
		if days <= 0 {
			http.Error(w, "older_than_days must be > 0", http.StatusBadRequest)
			return
		}
		cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour).UnixMilli()
		n, err := store.PruneEventsOlderThan(cutoff)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		audit.Emit(r, store, db.AuditEntry{
			Action: "system.events_prune",
			Metadata: map[string]any{
				"older_than_days": days,
				"deleted":         n,
			},
		})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"deleted":         n,
			"older_than_days": days,
		})
	}
}
