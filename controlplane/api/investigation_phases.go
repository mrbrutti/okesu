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

// Parent-side wrappers: proxy via ?cp=<instance_id> when set; otherwise
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
