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
	"strings"

	"github.com/section9labs/okesu/controlplane/audit"
	"github.com/section9labs/okesu/controlplane/auth"
	"github.com/section9labs/okesu/controlplane/cpprovision"
	"github.com/section9labs/okesu/controlplane/db"
)

// NodeProvisionWorkerConfig + RunNodeProvisionWorker live in
// node_provision_worker.go.

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
		// Cred-cloud cross-check: catch "AWS cred picked for OCI request"
		// synchronously here rather than letting the worker fail on
		// decrypt/unmarshal. Mirrors cp_provision.go.
		cred, err := store.GetCloudCredential(req.CredentialID)
		if err != nil {
			http.Error(w, "credential not found", http.StatusBadRequest)
			return
		}
		if cred.Cloud != req.Cloud {
			http.Error(w, fmt.Sprintf("credential is for cloud %q, request says %q", cred.Cloud, req.Cloud), http.StatusBadRequest)
			return
		}
		if _, err := store.GetTransportConfig(req.TransportConfigID); err != nil {
			http.Error(w, "transport_config not found", http.StatusBadRequest)
			return
		}
		shape, _ := req.CloudParams["shape"].(string)
		if missing := nodeCloudParamsMissingFields(req.Cloud, shape, req.CloudParams); len(missing) > 0 {
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

		row, err := store.InsertNodeProvision(db.NodeProvisionInsert{
			DisplayName:       req.DisplayName,
			Region:            req.Region,
			Cloud:             req.Cloud,
			CredentialID:      sql.NullInt64{Int64: req.CredentialID, Valid: req.CredentialID != 0},
			CredentialName:    cred.Name,
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
// keys. Mirrors the OCI/AWS provisioner's own validation
// (controlplane/cpprovision/{oci,aws}/*.go::decodeLaunchParams) so
// the form blocks before the worker would. The shape argument lets
// us conditionally require flex-shape sizing keys (ocpus +
// memory_in_gbs) on OCI without rejecting fixed shapes.
func nodeCloudParamsMissingFields(cloud, shape string, params map[string]any) []string {
	required := map[string][]string{
		// Mirror oci.decodeLaunchParams: compartment_id, availability_domain,
		// subnet_id, image_id, shape are unconditional. ocpus + memory_in_gbs
		// are conditional on isFlexShape — handled below.
		"oci": {"compartment_id", "availability_domain", "subnet_id", "image_id", "shape"},
		// Mirror aws.decodeLaunchParams: instance_type + subnet_id + ami_id +
		// security_group_ids unconditional.
		"aws": {"instance_type", "subnet_id", "ami_id", "security_group_ids"},
	}
	keys, ok := required[cloud]
	if !ok {
		return nil
	}
	missing := []string{}
	check := func(k string) {
		v, has := params[k]
		if !has {
			missing = append(missing, k)
			return
		}
		switch tv := v.(type) {
		case string:
			if tv == "" {
				missing = append(missing, k)
			}
		case []any:
			if len(tv) == 0 {
				missing = append(missing, k)
			}
		case nil:
			missing = append(missing, k)
		}
	}
	for _, k := range keys {
		check(k)
	}
	// OCI flex-shape conditional: ocpus + memory_in_gbs only required
	// when the shape itself ends in .Flex (matches isFlexShape in
	// oci.go:321). Fixed shapes accept either presence-or-absence.
	if cloud == "oci" && isFlexLikeShape(shape) {
		// Numeric values (json.Decode produces float64); presence is
		// what we check, the worker validates the value range.
		if _, has := params["ocpus"]; !has {
			missing = append(missing, "ocpus")
		}
		if _, has := params["memory_in_gbs"]; !has {
			missing = append(missing, "memory_in_gbs")
		}
	}
	return missing
}

// isFlexLikeShape mirrors cpprovision/oci.isFlexShape — duplicated
// here because the api package must not depend on the per-cloud
// provisioner. Kept tiny on purpose so the next reader can confirm
// it's the same check.
func isFlexLikeShape(s string) bool {
	return strings.HasSuffix(s, ".Flex") || strings.HasSuffix(s, ".Flex.A1")
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

