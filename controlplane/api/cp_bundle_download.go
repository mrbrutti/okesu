// Bundle download endpoint — public on the UI port (no cookie /
// session auth) and authenticated by the bootstrap-token Bearer
// header in the request.
//
// Used by the cloud-init script the parent CP injects into the
// managed-deploy VM's user_data: that script runs `curl -H
// "Authorization: Bearer <token>" $BUNDLE_URL` to pull the bundle.
// The endpoint:
//
//   1. Looks the token up in cp_bootstrap_tokens (via Verify, which
//      also checks expiry / unused / hash).
//   2. Reads the cached bytes from the in-memory BundleCache keyed
//      by token id (populated by RunCPProvisionWorker).
//   3. Streams the tar.gz back as application/gzip with the same
//      Content-Disposition shape the operator-facing endpoint uses.
//
// Crucially, this endpoint does NOT mark the token used. The
// bootstrap exchange (POST /api/v1/cp/bootstrap) is what burns it,
// AFTER the new CP has already pulled the bundle.

package api

import (
	"net/http"
	"strings"

	"github.com/section9labs/okesu/controlplane/db"
)

// CPBundleDownloadHandler streams the cached bundle bytes to a
// caller presenting a valid bootstrap token. 401 on any auth failure
// (uniform message — leaking "valid token but no bundle cached" tells
// an attacker the token was real).
func CPBundleDownloadHandler(store *db.Store, cache *BundleCache) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(auth, prefix) {
			http.Error(w, "missing bearer token", http.StatusUnauthorized)
			return
		}
		token := strings.TrimSpace(auth[len(prefix):])
		bt, err := store.VerifyCPBootstrapToken(token)
		if err != nil {
			http.Error(w, "invalid bootstrap token", http.StatusUnauthorized)
			return
		}
		bytes, filename, ok := cache.Get(bt.ID)
		if !ok {
			// Token verified but no cached bundle — race condition
			// (parent CP restarted after the worker ran but before
			// the cloud-init fetched). Operator's recourse is to
			// destroy the half-launched VM and retry the provision.
			http.Error(w, "bundle not available — provision may have been re-issued, retry from the parent CP", http.StatusGone)
			return
		}
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
		_, _ = w.Write(bytes)
	}
}
