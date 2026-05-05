// Cost-estimate handler for the managed-node deploy form (Phase 21.7,
// Task 6). POST /api/node-provision/estimate is what the +Add Node
// modal hits on every change to (cloud, cloud_params) so the operator
// sees a live "$X/hr" preview before submit. Read-only: never mints
// tokens, never inserts rows, safe to call as often as a debounce
// allows.
//
// Sister handler to cp_provision_estimate.go — same catalog
// (cpprovision.Estimate / FlexInputs / ExtractInstanceShape) so the
// CP-side and node-side estimates can't disagree. Node-side returns a
// trimmer body — there's no per-credential budget rollup yet for
// node provisions, so the response is just {hourly_usd}.

package api

import (
	"encoding/json"
	"net/http"

	"github.com/section9labs/okesu/controlplane/cpprovision"
	"github.com/section9labs/okesu/controlplane/db"
)

type nodeProvisionEstimateReq struct {
	Cloud        string         `json:"cloud"`
	CredentialID int64          `json:"credential_id"`
	CloudParams  map[string]any `json:"cloud_params"`
}

// NodeProvisionEstimateHandler is the read-only preview the +Add Node
// modal calls. Admin-only at the route layer; we don't repeat that
// gate here. Returns hourly_usd=0 for unknown shapes — the modal
// surfaces a "no catalog entry" hint based on the empty number.
func NodeProvisionEstimateHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req nodeProvisionEstimateReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		shape := cpprovision.ExtractInstanceShape(req.Cloud, req.CloudParams)
		flex := cpprovision.ExtractFlexInputs(req.Cloud, req.CloudParams)
		var hourly float64
		if est, ok := cpprovision.Estimate(req.Cloud, shape, flex); ok {
			hourly = est.USD
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]float64{"hourly_usd": hourly})
	}
}
