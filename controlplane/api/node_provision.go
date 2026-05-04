// HTTP handler for managed-node provisioning (Phase 21.7, Task 4).
//
// POST /api/node-provision creates a node_provisions row with
// status=queued and kicks the worker goroutine. Mirrors the shape of
// CPProvisionCreateHandler over in cp_provision.go, just one step
// simpler — node provisions don't mint a bootstrap token (the
// transport_config + enrollment_package already cover registration).
//
// Validation rules:
//   - display_name, region, cloud, credential_id, transport_config_id
//     are all required.
//   - cloud must have a registered cpprovision.Provisioner.
//   - transport_config_id must resolve to an existing row (FK check).
//   - cloud_params must contain the per-cloud required keys
//     (subnet, image, shape, etc.) — the form blocks before the worker
//     would.
//
// The worker goroutine is fed a NodeProvisionWorkerConfig built once
// at server boot. When that config's Store is nil (the test default),
// the handler skips the kick — useful for unit tests that only care
// about the wire path.

package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/section9labs/okesu/controlplane/audit"
	"github.com/section9labs/okesu/controlplane/auth"
	"github.com/section9labs/okesu/controlplane/cpprovision"
	"github.com/section9labs/okesu/controlplane/db"
)

// nodeProvisionReq is the JSON body for POST /api/node-provision.
// CloudParams is the per-cloud knob bag (subnet OCID, AD, image OCID,
// shape for OCI; subnet id, AMI id, instance type for AWS) — the
// per-cloud Provisioner.Launch impl decodes its own shape from this.
type nodeProvisionReq struct {
	DisplayName       string         `json:"display_name"`
	Region            string         `json:"region"`
	Cloud             string         `json:"cloud"`
	CredentialID      int64          `json:"credential_id"`
	CloudParams       map[string]any `json:"cloud_params"`
	TransportConfigID int64          `json:"transport_config_id"`
}

// NodeProvisionCreateHandler validates the request, persists a
// node_provisions row (status=queued), emits an audit log entry, and
// kicks the per-cloud worker goroutine. Returns 202 with the new row
// JSON on success.
//
// workerCfg is the full worker dependency bag; when its Store is nil
// (tests / no-op call sites) the handler skips the goroutine kick.
func NodeProvisionCreateHandler(
	store *db.Store,
	registry *cpprovision.Registry,
	workerCfg NodeProvisionWorkerConfig,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req nodeProvisionReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		if req.DisplayName == "" || req.Region == "" || req.Cloud == "" ||
			req.CredentialID == 0 || req.TransportConfigID == 0 {
			http.Error(w, "missing required: display_name, region, cloud, credential_id, transport_config_id", http.StatusBadRequest)
			return
		}
		if _, err := registry.Get(req.Cloud); err != nil {
			http.Error(w, "no provisioner for cloud "+req.Cloud, http.StatusBadRequest)
			return
		}
		if _, err := store.GetTransportConfig(req.TransportConfigID); err != nil {
			http.Error(w, "transport_config not found", http.StatusBadRequest)
			return
		}
		if missing := nodeCloudParamsMissingFields(req.Cloud, req.CloudParams); len(missing) > 0 {
			http.Error(w, fmt.Sprintf("missing cloud_params: %v", missing), http.StatusBadRequest)
			return
		}

		paramsJSON, _ := json.Marshal(req.CloudParams)
		var userID int64
		var userEmail string
		if u := auth.UserFromContext(r.Context()); u != nil {
			userID = u.ID
			userEmail = u.Email
		}
		shape, _ := req.CloudParams["shape"].(string)

		row, err := store.InsertNodeProvision(db.NodeProvisionInsert{
			DisplayName:       req.DisplayName,
			Region:            req.Region,
			Cloud:             req.Cloud,
			CredentialID:      sql.NullInt64{Int64: req.CredentialID, Valid: req.CredentialID != 0},
			CloudParamsJSON:   string(paramsJSON),
			TransportConfigID: req.TransportConfigID,
			InstanceShape:     shape,
			CreatedByUserID:   sql.NullInt64{Int64: userID, Valid: userID != 0},
			CreatedByEmail:    userEmail,
		})
		if err != nil {
			http.Error(w, "insert: "+err.Error(), http.StatusInternalServerError)
			return
		}

		audit.Emit(r, store, db.AuditEntry{
			Action: "node_provision.create",
			Target: fmt.Sprintf("node_provision:%d", row.ID),
			Metadata: map[string]any{
				"cloud":               req.Cloud,
				"region":              req.Region,
				"display_name":        req.DisplayName,
				"transport_config_id": req.TransportConfigID,
			},
		})

		// Kick the worker. workerCfg.Store == nil signals tests / no-op.
		// Worker is detached from the HTTP request lifetime: a slow
		// LaunchInstance call must not be cancelled when this 202
		// returns.
		if workerCfg.Store != nil {
			go RunNodeProvisionWorker(context.Background(), workerCfg, row.ID)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(toNodeProvisionJSON(row))
	}
}

// nodeCloudParamsMissingFields lists per-cloud required cloud_params
// keys. Mirrors the OCI/AWS provisioner's own validation so the form
// blocks before the worker rejects it.
func nodeCloudParamsMissingFields(cloud string, params map[string]any) []string {
	required := map[string][]string{
		"oci": {"shape", "subnet_id", "image_id", "availability_domain", "ocpus", "memory_in_gbs"},
		"aws": {"instance_type", "subnet_id", "ami_id"},
	}
	keys, ok := required[cloud]
	if !ok {
		return nil
	}
	var missing []string
	for _, k := range keys {
		v, has := params[k]
		if !has {
			missing = append(missing, k)
			continue
		}
		switch tv := v.(type) {
		case string:
			if tv == "" {
				missing = append(missing, k)
			}
		case nil:
			missing = append(missing, k)
		}
	}
	return missing
}

// toNodeProvisionJSON renders a NodeProvision row as a wire map. The
// shape mirrors cpProvisionJSON's emit logic: omit zero-valued nullable
// columns, surface the resource id/url/log when present.
func toNodeProvisionJSON(p *db.NodeProvision) map[string]any {
	out := map[string]any{
		"id":                  p.ID,
		"display_name":        p.DisplayName,
		"region":              p.Region,
		"cloud":               p.Cloud,
		"transport_config_id": p.TransportConfigID,
		"status":              string(p.Status),
		"created_at":          p.CreatedAt.UTC().Format(rfc3339),
	}
	if p.CloudResourceID.Valid {
		out["cloud_resource_id"] = p.CloudResourceID.String
	}
	if p.CloudResourceURL.Valid {
		out["cloud_resource_url"] = p.CloudResourceURL.String
	}
	if p.NodeID.Valid {
		out["node_id"] = p.NodeID.Int64
	}
	if p.CredentialID.Valid {
		out["credential_id"] = p.CredentialID.Int64
	}
	if p.CredentialName.Valid {
		out["credential_name"] = p.CredentialName.String
	}
	if p.InstanceShape.Valid {
		out["instance_shape"] = p.InstanceShape.String
	}
	if p.EstCostPerHourUSD.Valid {
		out["est_cost_per_hour_usd"] = p.EstCostPerHourUSD.Float64
	}
	if p.Error.Valid {
		out["error"] = p.Error.String
	}
	if p.StartedAt.Valid {
		out["started_at"] = p.StartedAt.Time.UTC().Format(rfc3339)
	}
	if p.EndedAt.Valid {
		out["ended_at"] = p.EndedAt.Time.UTC().Format(rfc3339)
	}
	if p.CreatedByEmail.Valid {
		out["created_by_email"] = p.CreatedByEmail.String
	}
	if p.Log != "" {
		out["log"] = p.Log
	}
	if p.CloudParamsJSON != "" && p.CloudParamsJSON != "{}" {
		var cp map[string]any
		if err := json.Unmarshal([]byte(p.CloudParamsJSON), &cp); err == nil {
			out["cloud_params"] = cp
		}
	}
	return out
}

// NodeProvisionWorkerConfig is the worker dependency bag.
//
// TODO(Task 5): expand this struct + replace the RunNodeProvisionWorker
// stub below with the real implementation. Task 4 only needs the
// Store field so the handler's `workerCfg.Store == nil` short-circuit
// works for tests, and so server-boot wiring can pass a real Store
// once Task 5 lands.
type NodeProvisionWorkerConfig struct {
	Store    *db.Store
	Registry *cpprovision.Registry
	// (full config in Task 5: bundle/cache/transport plumbing.)
}

// RunNodeProvisionWorker is a Task-4 stub. The real implementation
// lands in Task 5 (controlplane/api/node_provision_worker.go) and
// MUST replace this declaration — do not leave both. Keeping the stub
// in this file lets Task 4 ship + test independently.
//
// TODO(Task 5): delete this stub when node_provision_worker.go lands.
func RunNodeProvisionWorker(_ context.Context, _ NodeProvisionWorkerConfig, _ int64) {}
