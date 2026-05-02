// GET /api/cloud-credentials/{id}/compartments
//
// Returns every compartment the credential can see, sorted by name.
// Used by the AddBucketWizard's compartment dropdown.
//
// For clouds without a compartment concept (AWS, MinIO) the handler
// returns an empty JSON array rather than an error — the UI can detect
// this and show a "no compartments — bucket lands in account default"
// message.
//
// Admin-gated at the route layer (same group as the other
// /api/cloud-credentials/* mutation routes).

package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/section9labs/okesu/controlplane/cpprovision"
	"github.com/section9labs/okesu/controlplane/db"
)

// ListCompartmentsForCredential returns every compartment the
// credential can see. Delegates to the provisioner registered for the
// credential's cloud kind; provisioners that don't implement
// cpprovision.CompartmentLister receive an empty-array 200.
func ListCompartmentsForCredential(store *db.Store, registry *cpprovision.BucketRegistry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad credential id", http.StatusBadRequest)
			return
		}
		cred, err := store.GetCloudCredential(id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		// Attempt to look up a provisioner for this cloud. If none is
		// registered we treat it like a cloud without compartments —
		// the credential may be for a cloud whose provisioner isn't
		// installed on this CP build.
		prov, provErr := registry.Get(cred.Cloud)
		if provErr != nil {
			// No registered provisioner → empty list, not an error.
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]cpprovision.Compartment{})
			return
		}

		lister, ok := prov.(cpprovision.CompartmentLister)
		if !ok {
			// Cloud doesn't have compartments — return empty list.
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]cpprovision.Compartment{})
			return
		}

		// Decrypt the payload — same pattern used by CloudCredentialTest.
		masterKey, err := store.MasterKeyFromMeta()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		payload, err := store.DecryptCloudCredential(id, masterKey)
		if err != nil {
			http.Error(w, "decrypt credential: "+err.Error(), http.StatusInternalServerError)
			return
		}

		region := r.URL.Query().Get("region")
		comparts, err := lister.ListCompartments(r.Context(), payload, region)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		// Always emit a JSON array, never null.
		if comparts == nil {
			comparts = []cpprovision.Compartment{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(comparts)
	}
}
