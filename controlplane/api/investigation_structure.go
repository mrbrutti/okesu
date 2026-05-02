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
