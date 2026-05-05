// HTTP handlers for the node-provisions list/get/delete surface
// (Phase 21.7, Task 6). Sister handlers to cp_provision.go's
// CPProvisionsListHandler / CPProvisionGetHandler / CPProvisionDelete-
// Handler — wire shape and idempotency contract are intentionally
// the same so the UI can talk to both panels with one client.
//
// Delete optionally tears down the cloud-side instance via the
// per-cloud Provisioner.Destroy. Cleanup-gap from PR #129: when the
// provision row was linked to a nodes row by the s3scanner, that row
// is also dropped here — operators expect "delete this managed-deploy"
// to make the node disappear from the inventory, not leave a ghost.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/section9labs/okesu/controlplane/adapters/s3blob"
	"github.com/section9labs/okesu/controlplane/audit"
	"github.com/section9labs/okesu/controlplane/cpprovision"
	"github.com/section9labs/okesu/controlplane/db"
)

// NodeProvisionsListHandler returns recent node-provision rows for
// the Nodes page's managed-deploy panel. Default limit 50, capped at
// 200 (the store further caps at 500 — we cap lower here so the UI's
// list stays small for operators with hundreds of rows).
func NodeProvisionsListHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit := 50
		if s := r.URL.Query().Get("limit"); s != "" {
			if n, err := strconv.Atoi(s); err == nil && n > 0 {
				limit = n
			}
		}
		if limit > 200 {
			limit = 200
		}
		rows, err := store.ListNodeProvisions(limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := []map[string]any{}
		for i := range rows {
			out = append(out, toNodeProvisionJSON(&rows[i]))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// NodeProvisionGetHandler returns a single row including its full log.
// The Nodes page uses this to drive the tail-style log viewer while a
// deploy is in flight.
func NodeProvisionGetHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		row, err := store.NodeProvision(id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toNodeProvisionJSON(row))
	}
}

// NodeProvisionDeleteHandler removes a node_provisions row. When the
// query param `destroy=true` is set AND the row has a cloud_resource_id,
// the handler first calls Provisioner.Destroy to terminate the cloud
// instance — best-effort; failures there don't block the row delete
// (operator gets the destroy error in the response body and can retry
// via the cloud console).
//
// Cleanup-gap from PR #129: also removes the matching nodes row when
// the provision linked one. Without this an operator who deletes a
// managed-deploy would still see a ghost node in the inventory.
//
// Idempotent: deleting a missing id returns 204.
func NodeProvisionDeleteHandler(store *db.Store, registry *cpprovision.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		row, err := store.NodeProvision(id)
		if err != nil {
			// Already-gone is success; the operator's intent ("make
			// this row not exist") matches the post-state.
			w.WriteHeader(http.StatusNoContent)
			return
		}

		var destroyErr string
		destroy := r.URL.Query().Get("destroy") == "true"
		if destroy && row.CloudResourceID.Valid && row.CloudResourceID.String != "" {
			prov, perr := registry.Get(row.Cloud)
			if perr != nil {
				destroyErr = "no provisioner for " + row.Cloud + ": " + perr.Error()
			} else if !row.CredentialID.Valid {
				destroyErr = "credential gone — destroy via cloud console: " + row.CloudResourceID.String
			} else {
				mk, mkErr := store.MasterKeyFromMeta()
				if mkErr != nil {
					destroyErr = "master key: " + mkErr.Error()
				} else {
					credBytes, derr := store.DecryptCloudCredential(row.CredentialID.Int64, mk)
					if derr != nil {
						destroyErr = "decrypt credential: " + derr.Error()
					} else if dErr := prov.Destroy(r.Context(), row.CloudResourceID.String, row.Region, credBytes); dErr != nil {
						destroyErr = dErr.Error()
					}
				}
			}
		}

		// Cleanup-gap from PR #129: drop the linked nodes row first so
		// the inventory matches operator intent. Best-effort — a stale
		// FK is not a reason to fail the provision-row delete.
		if row.NodeID.Valid && row.NodeID.Int64 != 0 {
			_ = store.DeleteNode(row.NodeID.Int64)
		}

		// Drop the one-time package blob from the bucket. Mirrors the
		// cp_provision destroy path (deleteCPProvisionBootstrapBlob).
		// Best-effort: a Stale Object delete is harmless, and we'd
		// rather a failed S3 round-trip not block the row delete.
		if row.TransportConfigID != 0 {
			if err := deleteNodeProvisionPackageBlob(r.Context(), store, row.TransportConfigID, row.ID); err != nil {
				log.Printf("node_provisions delete %d: package blob cleanup: %v", row.ID, err)
			}
		}

		if err := store.DeleteNodeProvision(id); err != nil {
			http.Error(w, "delete: "+err.Error(), http.StatusInternalServerError)
			return
		}

		audit.Emit(r, store, db.AuditEntry{
			Action: "node_provision.delete",
			Target: fmt.Sprintf("node_provision:%d", id),
			Metadata: map[string]any{
				"display_name":      row.DisplayName,
				"cloud":             row.Cloud,
				"status":            string(row.Status),
				"destroy_attempted": destroy,
				"destroy_error":     destroyErr,
			},
		})

		// 204 when the row is gone and (if requested) destroy
		// succeeded; 200 + body when destroy failed so the operator
		// sees the cloud-side error inline. Same shape as
		// CPProvisionDeleteHandler so the UI can branch identically.
		if destroyErr != "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"deleted":        true,
				"destroy_error":  destroyErr,
				"cloud_resource": row.CloudResourceID.String,
			})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// deleteNodeProvisionPackageBlob removes the one-time
// `provisions/nodes/<id>/package.tar.gz` object the worker
// uploaded for cloud-init to fetch. Mirrors
// deleteCPProvisionBootstrapBlob in cp_provision.go.
//
// Best-effort: missing transport_config or unreachable bucket
// surfaces as a logged error; the destroy-row path proceeds.
func deleteNodeProvisionPackageBlob(ctx context.Context, store *db.Store, transportConfigID, provisionID int64) error {
	tc, err := store.GetTransportConfig(transportConfigID)
	if err != nil {
		return fmt.Errorf("transport_config: %w", err)
	}
	if !tc.AccessKey.Valid || !tc.SecretKey.Valid {
		return errors.New("transport_config has no access keys")
	}
	blob, err := s3blob.New(ctx, s3blob.Config{
		Endpoint:  tc.Endpoint,
		Region:    tc.Region.String,
		Bucket:    tc.Bucket,
		AccessKey: tc.AccessKey.String,
		SecretKey: tc.SecretKey.String,
		UseSSL:    tc.UseSSL,
	})
	if err != nil {
		return fmt.Errorf("s3 client: %w", err)
	}
	key := fmt.Sprintf("provisions/nodes/%d/package.tar.gz", provisionID)
	return blob.Delete(ctx, key)
}
