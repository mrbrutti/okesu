package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/section9labs/okesu/controlplane/db"
	"github.com/section9labs/okesu/controlplane/ioc/feeds"
)

// ── wire shapes ──────────────────────────────────────────────────────────────

// installFeedRequest is the body decoded by InstallFeedHandler.
// Exactly one of FromRegistrySlug or Custom must be set.
type installFeedRequest struct {
	FromRegistrySlug string                `json:"from_registry_slug"`
	Custom           *db.FeedConfigInsert  `json:"custom"`
}

// installFeedResponse is the 201 body returned by InstallFeedHandler.
type installFeedResponse struct {
	ID   int64  `json:"id"`
	Slug string `json:"slug"`
}

// validateFeedResponse is the 200 body returned by ValidateFeedHandler.
type validateFeedResponse struct {
	EntryCount int `json:"entry_count"`
}

// setConsentResponse is the 200 body returned by SetFeedsConsentHandler.
type setConsentResponse struct {
	GrantedAt string `json:"granted_at"`
}

// ── helpers ──────────────────────────────────────────────────────────────────

// decodePathID extracts and parses the {id} chi URL param.
func decodePathID(r *http.Request) (int64, error) {
	return strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
}

// findRegistryDef returns the FeedDef with the given slug, or nil if not found.
func findRegistryDef(slug string) *feeds.FeedDef {
	for i := range feeds.Registry {
		if feeds.Registry[i].Slug == slug {
			return &feeds.Registry[i]
		}
	}
	return nil
}

// ── handlers ─────────────────────────────────────────────────────────────────

// ListFeedsHandler serves GET /api/feeds. Viewer-readable.
// Returns 200 + JSON array of all installed feed configs.
func ListFeedsHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		list, err := store.ListFeedConfigs()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Return an empty array, not null.
		if list == nil {
			list = []db.FeedConfig{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(list)
	}
}

// ListFeedsRegistryHandler serves GET /api/feeds/registry. Viewer-readable.
// Returns the compile-time registry of well-known feeds.
func ListFeedsRegistryHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(feeds.Registry)
	}
}

// InstallFeedHandler serves POST /api/feeds. Admin-only.
//
// Body must set exactly one of:
//   - from_registry_slug: installs from the compile-time Registry.
//   - custom: installs a user-supplied FeedConfigInsert directly.
//
// Registry installs set Enabled=true because the operator's explicit click to
// install implies consent for that specific feed.  This is distinct from
// SeedRegistryDefaults (which inserts with Enabled=false and waits for the
// consent banner flow to flip them on).
func InstallFeedHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req installFeedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}

		hasRegistry := req.FromRegistrySlug != ""
		hasCustom := req.Custom != nil

		if hasRegistry && hasCustom {
			http.Error(w, "exactly one of from_registry_slug or custom must be set, not both", http.StatusBadRequest)
			return
		}
		if !hasRegistry && !hasCustom {
			http.Error(w, "exactly one of from_registry_slug or custom must be set", http.StatusBadRequest)
			return
		}

		var ins db.FeedConfigInsert

		if hasRegistry {
			def := findRegistryDef(req.FromRegistrySlug)
			if def == nil {
				http.Error(w, "unknown registry slug: "+req.FromRegistrySlug, http.StatusBadRequest)
				return
			}
			ins = db.FeedConfigInsert{
				Slug:                   def.Slug,
				Name:                   def.Name,
				Kind:                   def.Kind,
				URL:                    def.URL,
				Subpath:                def.Subpath,
				Parser:                 def.Parser,
				RefreshIntervalSeconds: def.DefaultIntervalSeconds,
				Enabled:                true, // operator's explicit install implies intent
				InstalledFromRegistry:  true,
			}
		} else {
			ins = *req.Custom
		}

		id, err := store.InsertFeedConfig(&ins)
		if err != nil {
			http.Error(w, "insert: "+err.Error(), http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(installFeedResponse{ID: id, Slug: ins.Slug})
	}
}

// UpdateFeedHandler serves PATCH /api/feeds/{id}. Admin-only.
// Body is decoded as db.FeedConfigPatch; nil fields are left unchanged.
// Returns 204 No Content on success.
func UpdateFeedHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := decodePathID(r)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		var patch db.FeedConfigPatch
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if err := store.UpdateFeedConfig(id, &patch); err != nil {
			if errors.Is(err, sql.ErrNoRows) || err.Error() == "feed not found" {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// UninstallFeedHandler serves DELETE /api/feeds/{id}. Admin-only.
// Reconciles the feed to empty (orphaning observations) BEFORE deleting
// the config row so FK constraints are respected and the orphan label
// is set correctly.  Returns 204 No Content on success.
func UninstallFeedHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := decodePathID(r)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		fc, err := store.GetFeedConfig(id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Reconcile to empty: drops all feed-scoped IOCs, orphaning observations.
		if _, err := feeds.Reconcile(store, fc.ID, fc.Slug, nil); err != nil {
			http.Error(w, "reconcile: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if err := store.DeleteFeedConfig(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// RefreshFeedHandler serves POST /api/feeds/{id}/refresh. Admin-only.
// Enqueues a manual refresh via the scheduler.
// Returns 202 Accepted; 400 if scheduler refuses (consent missing or queue full).
func RefreshFeedHandler(scheduler *feeds.Scheduler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := decodePathID(r)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		if err := scheduler.RefreshNow(id); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// ValidateFeedHandler serves POST /api/feeds/validate. Admin-only.
// Body is a db.FeedConfigInsert (prospective spec). Performs fetch + parse
// without committing anything to the iocs table.
// Returns {"entry_count": <int>} on success.
func ValidateFeedHandler(worker *feeds.Worker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var ins db.FeedConfigInsert
		if err := json.NewDecoder(r.Body).Decode(&ins); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		// Build a synthetic FeedConfig from the prospective spec.
		fc := &db.FeedConfig{
			ID:                     0,
			Slug:                   ins.Slug,
			Name:                   ins.Name,
			Kind:                   ins.Kind,
			URL:                    ins.URL,
			Subpath:                ins.Subpath,
			Parser:                 ins.Parser,
			RefreshIntervalSeconds: ins.RefreshIntervalSeconds,
			Enabled:                ins.Enabled,
		}
		entries, err := worker.Gather(fc)
		if err != nil {
			http.Error(w, "gather: "+err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(validateFeedResponse{EntryCount: len(entries)})
	}
}

// SetFeedsConsentHandler serves POST /api/feeds/consent. Admin-only.
// Records the consent timestamp in cp_meta, then enables every
// installed_from_registry feed that was waiting for consent (Enabled=false).
// Custom feeds are left unchanged.
// Returns {"granted_at": "<iso>"}.
func SetFeedsConsentHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		now := time.Now().UTC()
		if err := store.SetFeedsConsentGrantedAt(now); err != nil {
			http.Error(w, "set consent: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Flip enabled=true on all registry-sourced feeds.
		list, err := store.ListFeedConfigs()
		if err != nil {
			http.Error(w, "list feeds: "+err.Error(), http.StatusInternalServerError)
			return
		}
		enabled := true
		for _, fc := range list {
			if !fc.InstalledFromRegistry {
				continue
			}
			if fc.Enabled {
				continue // already on; skip the write
			}
			if err := store.UpdateFeedConfig(fc.ID, &db.FeedConfigPatch{Enabled: &enabled}); err != nil {
				http.Error(w, "enable feed: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(setConsentResponse{GrantedAt: now.Format(time.RFC3339)})
	}
}
