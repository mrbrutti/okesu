// Managed CP provisioning API surface (Phase 21.3a).
//
// Three admin-only endpoints:
//
//   GET /api/federation/cp-provisioners
//      Lists which clouds have a Provisioner registered. The UI gates
//      the "Managed deploy" cloud picker on this so an operator
//      doesn't try to provision into a cloud whose impl isn't shipped
//      yet (Phase 21.3a registers nothing; 21.3b ships OCI; etc.).
//
//   POST /api/federation/cp-provision
//      Creates a cp_provisions row + kicks the per-cloud Provisioner.
//      Phase 21.3a returns 501 with a structured error pointing at
//      the relevant phase if the registry has no impl for the
//      requested cloud — the row is NOT created in that case.
//
//   GET /api/federation/cp-provisions
//      Returns recent provision rows for the Federation page's
//      "Managed deploys" panel.
//
// The actual cloud API calls live in the per-cloud Provisioner
// packages that register against this CP's cpprovision.Registry. This
// file just owns the HTTP shape + persistence wiring.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/section9labs/okesu/controlplane/audit"
	"github.com/section9labs/okesu/controlplane/auth"
	"github.com/section9labs/okesu/controlplane/cpprovision"
	"github.com/section9labs/okesu/controlplane/db"
)

// CPProvisionersListHandler returns the cloud kinds with a registered
// Provisioner, sorted ascending. Used by the +Add CP modal's "Managed
// deploy" tab to gate the cloud picker.
func CPProvisionersListHandler(reg *cpprovision.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"clouds": reg.Clouds(),
		})
	}
}

// cpProvisionReq is the JSON body for POST /api/federation/cp-provision.
// CloudParams is the per-cloud knob bag — for OCI: subnet OCID, AD,
// image OCID, shape; for AWS: AMI id, instance type, subnet id, SG.
// The Provisioner.Launch impl decodes its own shape from this map.
type cpProvisionReq struct {
	DisplayName  string         `json:"display_name"`
	Region       string         `json:"region"`
	Cloud        string         `json:"cloud"`
	CredentialID int64          `json:"credential_id"`
	CloudParams  map[string]any `json:"cloud_params"`
	// ParentURL is the absolute URL the new CP should call back to
	// for /api/v1/cp/bootstrap. Optional — defaults to the parent's
	// EffectivePublicURL when empty, same as the manual bundle path.
	ParentURL string `json:"parent_url,omitempty"`
	// ForceOverBudget bypasses the per-credential monthly_budget_usd
	// check. The handler returns 409 with a structured payload if a
	// submit would breach the budget; the operator can then re-submit
	// with this flag once they've acked the overage. Default false —
	// the goal of the budget is to make exceeding it require a
	// deliberate second click, not to make it impossible.
	ForceOverBudget bool `json:"force_over_budget,omitempty"`
}

// cpProvisionBudgetExceededResp is the structured 409 the handler
// returns when a submit would push a credential's projected monthly
// spend past its cap. The frontend uses this to show the budget
// callout + "Override budget" checkbox.
type cpProvisionBudgetExceededResp struct {
	Error              string  `json:"error"`
	Reason             string  `json:"reason"` // "budget_exceeded"
	CredentialID       int64   `json:"credential_id"`
	MonthlyBudgetUSD   float64 `json:"monthly_budget_usd"`
	CurrentMonthlyUSD  float64 `json:"current_monthly_usd"`
	ProjectedMonthlyUSD float64 `json:"projected_monthly_usd"`
	NewMonthlyUSD      float64 `json:"new_monthly_usd"`
	UnknownActiveCount int     `json:"unknown_active_count"`
}

// CPProvisionCreateHandler validates the request, mints a bootstrap
// token, creates a cp_provisions row, and launches the per-cloud
// Provisioner via a background worker. Errors are surfaced verbatim
// — the worker copies them into cp_provisions.error on failure.
//
// workerCfg is the full worker dependency bag (cache, bundle config,
// parent base URL). When workerCfg is the zero value (no workerCfg
// configured at the call site), the handler still inserts the row
// but no worker is kicked — useful for debugging the API surface
// without burning a real cloud account.
func CPProvisionCreateHandler(store *db.Store, reg *cpprovision.Registry, parentMgmtURL string, workerCfg CPProvisionWorkerConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req cpProvisionReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if req.DisplayName == "" || req.Region == "" || req.Cloud == "" || req.CredentialID == 0 {
			http.Error(w, "display_name, region, cloud, credential_id are required", http.StatusBadRequest)
			return
		}
		// Provisioner must exist BEFORE we burn a bootstrap token.
		// Otherwise a 501 here would still consume the token.
		if _, err := reg.Get(req.Cloud); err != nil {
			if errors.Is(err, cpprovision.ErrNoProvisioner) {
				http.Error(w, fmt.Sprintf(
					"no Provisioner registered for cloud %q. Phase 21.3a ships the framework; "+
						"per-cloud impls land in 21.3b (OCI) and 21.3c (AWS). For now, use the "+
						"\"Generate bundle\" tab and run the bundle on a VM you provisioned manually.",
					req.Cloud), http.StatusNotImplemented)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Sanity-check the credential — same cloud, exists, decryptable.
		cred, err := store.GetCloudCredential(req.CredentialID)
		if err != nil {
			http.Error(w, "credential not found", http.StatusNotFound)
			return
		}
		if cred.Cloud != req.Cloud {
			http.Error(w, fmt.Sprintf("credential is for cloud %q, request says %q", cred.Cloud, req.Cloud), http.StatusBadRequest)
			return
		}

		// Cost estimate + budget check. We do this BEFORE minting the
		// bootstrap token so a budget-exceeded 409 doesn't burn one.
		// Unknown shapes (catalog miss) contribute zero — the operator
		// might still want to proceed; the UI flags it.
		shape := cpprovision.ExtractInstanceShape(req.Cloud, req.CloudParams)
		flex := cpprovision.ExtractFlexInputs(req.Cloud, req.CloudParams)
		var newHourlyUSD *float64
		var newMonthlyUSD float64
		if est, ok := cpprovision.Estimate(req.Cloud, shape, flex); ok {
			usd := est.USD
			newHourlyUSD = &usd
			newMonthlyUSD = cpprovision.MonthlyUSD(usd)
		}

		if cred.MonthlyBudgetUSD.Valid && !req.ForceOverBudget {
			currentUSD, unknowns, sumErr := store.SumActiveMonthlyCostUSDByCredential(cred.ID)
			if sumErr != nil {
				http.Error(w, "budget rollup: "+sumErr.Error(), http.StatusInternalServerError)
				return
			}
			projected := currentUSD + newMonthlyUSD
			if projected > cred.MonthlyBudgetUSD.Float64 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(cpProvisionBudgetExceededResp{
					Error:               fmt.Sprintf("would exceed credential's monthly budget (%.2f > %.2f USD)", projected, cred.MonthlyBudgetUSD.Float64),
					Reason:              "budget_exceeded",
					CredentialID:        cred.ID,
					MonthlyBudgetUSD:    cred.MonthlyBudgetUSD.Float64,
					CurrentMonthlyUSD:   currentUSD,
					ProjectedMonthlyUSD: projected,
					NewMonthlyUSD:       newMonthlyUSD,
					UnknownActiveCount:  unknowns,
				})
				return
			}
		}

		// Mint the bootstrap token now, attached to this provision.
		// The worker will pass it to the cloud-init script + the
		// child CP's bootstrap call burns it.
		var userID int64
		var userEmail string
		if u := auth.UserFromContext(r.Context()); u != nil {
			userID = u.ID
			userEmail = u.Email
		}
		parentURL := req.ParentURL
		if parentURL == "" {
			parentURL = parentMgmtURL
		}
		plaintext, tokenID, err := store.IssueCPBootstrapToken(req.DisplayName, req.Region, parentURL, userID, userEmail)
		if err != nil {
			http.Error(w, "issue token: "+err.Error(), http.StatusInternalServerError)
			return
		}

		paramsJSON, _ := json.Marshal(req.CloudParams)
		credName := ""
		if cred.Name != "" {
			credName = cred.Name
		}
		row, err := store.InsertCPProvision(db.CPProvisionInsert{
			DisplayName:       req.DisplayName,
			Region:            req.Region,
			Cloud:             req.Cloud,
			CredentialID:      cred.ID,
			CredentialName:    credName,
			CloudParamsJSON:   string(paramsJSON),
			BundleTokenID:     tokenID,
			EstCostPerHourUSD: newHourlyUSD,
			InstanceShape:     shape,
			CreatedByUserID:   userID,
			CreatedByEmail:    userEmail,
		})
		if err != nil {
			http.Error(w, "insert: "+err.Error(), http.StatusInternalServerError)
			return
		}

		audit.Emit(r, store, db.AuditEntry{
			Action: "cp_provision.create",
			Target: fmt.Sprintf("cp_provision:%d", row.ID),
			Metadata: map[string]any{
				"cloud":         req.Cloud,
				"credential_id": req.CredentialID,
				"display_name":  req.DisplayName,
				"region":        req.Region,
			},
		})

		// Stash the plaintext token where the worker can pick it up
		// (it's not in the DB after this — only the bcrypt hash is)
		// and kick the goroutine. Worker is detached from the HTTP
		// request lifetime; ctx.Background() so a slow LaunchInstance
		// API call doesn't get cancelled when the operator's POST
		// returns 202.
		StashBootstrapTokenPlaintext(row.ID, plaintext)
		if workerCfg.Store != nil {
			go RunCPProvisionWorker(context.Background(), workerCfg, row.ID)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(toCPProvisionJSON(row))
	}
}

// CPProvisionsListHandler returns recent provision rows for the
// Federation page's job list.
func CPProvisionsListHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		rows, err := store.ListCPProvisions(limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := make([]cpProvisionJSON, len(rows))
		for i := range rows {
			out[i] = toCPProvisionJSON(&rows[i])
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// CPProvisionGetHandler returns a single row, including its full log.
// The Federation page uses this to drive a tail-style log viewer
// while a deploy is in flight.
func CPProvisionGetHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		row, err := store.GetCPProvision(id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toCPProvisionJSON(row))
	}
}

// cpProvisionJSON is the wire shape for the Federation page. The log
// is included on per-row GET but truncated to 64KB on the list
// endpoint to keep the page small for operators with many deploys.
type cpProvisionJSON struct {
	ID                int64          `json:"id"`
	DisplayName       string         `json:"display_name"`
	Region            string         `json:"region"`
	Cloud             string         `json:"cloud"`
	CredentialName    string         `json:"credential_name,omitempty"`
	CloudParams       map[string]any `json:"cloud_params,omitempty"`
	Status            string         `json:"status"`
	CloudResourceID   string         `json:"cloud_resource_id,omitempty"`
	CloudResourceURL  string         `json:"cloud_resource_url,omitempty"`
	PeerID            int64          `json:"peer_id,omitempty"`
	Log               string         `json:"log,omitempty"`
	Error             string         `json:"error,omitempty"`
	EstCostPerHourUSD *float64       `json:"est_cost_per_hour_usd,omitempty"`
	InstanceShape     string         `json:"instance_shape,omitempty"`
	CreatedAt         string         `json:"created_at"`
	StartedAt         string         `json:"started_at,omitempty"`
	EndedAt           string         `json:"ended_at,omitempty"`
	CreatedByEmail    string         `json:"created_by_email,omitempty"`
}

func toCPProvisionJSON(p *db.CPProvision) cpProvisionJSON {
	out := cpProvisionJSON{
		ID:          p.ID,
		DisplayName: p.DisplayName,
		Region:      p.Region,
		Cloud:       p.Cloud,
		Status:      string(p.Status),
		Log:         p.Log,
		CreatedAt:   p.CreatedAt.UTC().Format(rfc3339),
	}
	if p.CredentialName.Valid {
		out.CredentialName = p.CredentialName.String
	}
	if p.CloudResourceID.Valid {
		out.CloudResourceID = p.CloudResourceID.String
	}
	if p.CloudResourceURL.Valid {
		out.CloudResourceURL = p.CloudResourceURL.String
	}
	if p.PeerID.Valid {
		out.PeerID = p.PeerID.Int64
	}
	if p.Error.Valid {
		out.Error = p.Error.String
	}
	if p.StartedAt.Valid {
		out.StartedAt = p.StartedAt.Time.UTC().Format(rfc3339)
	}
	if p.EndedAt.Valid {
		out.EndedAt = p.EndedAt.Time.UTC().Format(rfc3339)
	}
	if p.CreatedByEmail.Valid {
		out.CreatedByEmail = p.CreatedByEmail.String
	}
	if p.CloudParamsJSON != "" && p.CloudParamsJSON != "{}" {
		_ = json.Unmarshal([]byte(p.CloudParamsJSON), &out.CloudParams)
	}
	if p.EstCostPerHourUSD.Valid {
		v := p.EstCostPerHourUSD.Float64
		out.EstCostPerHourUSD = &v
	}
	if p.InstanceShape.Valid {
		out.InstanceShape = p.InstanceShape.String
	}
	return out
}
