// Cost estimate endpoint for managed CP deploys (Phase 21.5).
//
// POST /api/federation/cp-provision/estimate is what the +Add CP
// modal hits on every change to (cloud, credential, cloud_params)
// so the operator sees a live "$X/hr · $Y/mo (current: $Z, cap: $B)"
// readout before they click submit. The endpoint is read-only — it
// neither mints tokens nor inserts rows — so it's safe to call as
// often as a debounce allows.
//
// The actual budget enforcement happens at submit time in
// CPProvisionCreateHandler, which re-runs the same math against the
// row it's about to insert. The two paths share the catalog +
// SumActiveMonthlyCostUSDByCredential helper so they can't disagree.

package api

import (
	"encoding/json"
	"net/http"

	"github.com/section9labs/okesu/controlplane/cpprovision"
	"github.com/section9labs/okesu/controlplane/db"
)

// cpProvisionEstimateReq mirrors the relevant subset of cpProvisionReq.
// The endpoint accepts the full create-request shape so the frontend
// doesn't have to maintain two payload types — it just sends the
// same body it would for a real submit.
type cpProvisionEstimateReq struct {
	Cloud        string         `json:"cloud"`
	CredentialID int64          `json:"credential_id"`
	CloudParams  map[string]any `json:"cloud_params"`
}

// cpProvisionEstimateResp is what the modal renders. All USD values
// are pre-rounded to 4 decimals so the UI doesn't need to do its
// own number formatting.
type cpProvisionEstimateResp struct {
	HourlyUSD          *float64 `json:"hourly_usd,omitempty"`
	MonthlyUSD         *float64 `json:"monthly_usd,omitempty"`
	InstanceShape      string   `json:"instance_shape,omitempty"`
	CatalogVersion     string   `json:"catalog_version"`
	Note               string   `json:"note,omitempty"`
	// Budget context — only populated when the credential has a budget.
	MonthlyBudgetUSD   *float64 `json:"monthly_budget_usd,omitempty"`
	CurrentMonthlyUSD  *float64 `json:"current_monthly_usd,omitempty"`
	ProjectedMonthlyUSD *float64 `json:"projected_monthly_usd,omitempty"`
	UnknownActiveCount int      `json:"unknown_active_count,omitempty"`
	WouldExceedBudget  bool     `json:"would_exceed_budget,omitempty"`
}

// CPProvisionEstimateHandler is the read-only preview the modal calls.
// Admin-only at the route layer; we don't repeat the auth check here.
func CPProvisionEstimateHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req cpProvisionEstimateReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}

		resp := cpProvisionEstimateResp{
			CatalogVersion: cpprovision.CatalogVersion,
		}

		shape := cpprovision.ExtractInstanceShape(req.Cloud, req.CloudParams)
		flex := cpprovision.ExtractFlexInputs(req.Cloud, req.CloudParams)
		resp.InstanceShape = shape

		var newMonthlyUSD float64
		if est, ok := cpprovision.Estimate(req.Cloud, shape, flex); ok {
			usd := round4(est.USD)
			monthly := round4(cpprovision.MonthlyUSD(est.USD))
			resp.HourlyUSD = &usd
			resp.MonthlyUSD = &monthly
			resp.Note = est.Note
			newMonthlyUSD = monthly
		} else if shape != "" {
			resp.Note = "no catalog entry — estimate unavailable. Submit will not block on budget."
		}

		// Budget context — best-effort. If credential lookup fails
		// (e.g. id 0 / not yet picked) we silently leave the budget
		// fields empty so the modal can render an estimate-only line
		// while the operator is still filling in the form.
		if req.CredentialID > 0 {
			cred, err := store.GetCloudCredential(req.CredentialID)
			if err == nil && cred.MonthlyBudgetUSD.Valid {
				budget := round4(cred.MonthlyBudgetUSD.Float64)
				resp.MonthlyBudgetUSD = &budget
				currentUSD, unknowns, sumErr := store.SumActiveMonthlyCostUSDByCredential(cred.ID)
				if sumErr == nil {
					current := round4(currentUSD)
					projected := round4(currentUSD + newMonthlyUSD)
					resp.CurrentMonthlyUSD = &current
					resp.ProjectedMonthlyUSD = &projected
					resp.UnknownActiveCount = unknowns
					resp.WouldExceedBudget = projected > budget
				}
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// round4 trims a float to four decimals so the wire format is stable
// and the UI doesn't have to format its own. We deliberately avoid
// fmt.Sprintf("%.4f") because that hands back a string; we want a
// float so the client can do arithmetic if it wants.
func round4(f float64) float64 {
	const scale = 1e4
	if f >= 0 {
		return float64(int64(f*scale+0.5)) / scale
	}
	return float64(int64(f*scale-0.5)) / scale
}
