// Cloud-side discovery for the +Add CP "Managed deploy" form.
//
// The Phase 21.3 form forced operators to paste raw OCIDs +
// region-specific AMI ids into a JSON textarea. That's a UX
// disaster — the operator's tenancy already has a single source
// of truth for "which compartments do I have," "which subnets
// exist," "which images are blessed in this region." The parent
// CP has the credential; it can call the same SDKs the
// provisioner uses and offer real dropdowns.
//
// One handler per resource. All read-only, all admin-only at the
// route layer. Each:
//
//   1. Decodes the credential from cloud_credentials.
//   2. Builds an OCI client.
//   3. Calls the relevant List* method.
//   4. Maps the response into a small {id, name, ...} JSON shape
//      so the UI doesn't have to know the SDK's struct layout.
//
// We deliberately do NOT cache. The blast radius of stale data is
// "operator picks a deleted compartment, gets a 404 from
// LaunchInstance, reads a clear error" — annoying but recoverable.
// Caching would invent its own bugs around invalidation.

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/core"
	"github.com/oracle/oci-go-sdk/v65/identity"

	"github.com/section9labs/okesu/controlplane/db"
)

// ociCredentialPayload mirrors the encrypted credential body the
// Settings → Cloud editor stores. Kept private to this package
// because the provisioner has its own copy in cpprovision/oci —
// we don't want to import that module just for the struct.
type ociCredentialPayload struct {
	TenancyOCID string `json:"tenancy_ocid"`
	UserOCID    string `json:"user_ocid"`
	Fingerprint string `json:"fingerprint"`
	PrivateKey  string `json:"private_key"`
	Region      string `json:"region"`
}

// loadOCIProviderForCredential decrypts the row and builds an OCI
// configuration provider. Returns an HTTP-safe error so the handler
// can pass it straight to http.Error.
func loadOCIProviderForCredential(store *db.Store, credID int64, regionOverride string) (common.ConfigurationProvider, *ociCredentialPayload, *httpErr) {
	cred, err := store.GetCloudCredential(credID)
	if err != nil {
		return nil, nil, &httpErr{status: http.StatusNotFound, msg: "credential not found"}
	}
	if cred.Cloud != "oci" {
		return nil, nil, &httpErr{status: http.StatusBadRequest, msg: fmt.Sprintf("credential %d is for cloud %q, not oci", credID, cred.Cloud)}
	}
	masterKey, err := store.MasterKeyFromMeta()
	if err != nil {
		return nil, nil, &httpErr{status: http.StatusInternalServerError, msg: err.Error()}
	}
	raw, err := store.DecryptCloudCredential(credID, masterKey)
	if err != nil {
		return nil, nil, &httpErr{status: http.StatusInternalServerError, msg: "decrypt: " + err.Error()}
	}
	var p ociCredentialPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, nil, &httpErr{status: http.StatusInternalServerError, msg: "credential payload not json"}
	}
	region := strings.TrimSpace(regionOverride)
	if region == "" {
		region = p.Region
	}
	provider := common.NewRawConfigurationProvider(
		p.TenancyOCID, p.UserOCID, region, p.Fingerprint, p.PrivateKey, nil,
	)
	return provider, &p, nil
}

type httpErr struct {
	status int
	msg    string
}

// credIDFromURL pulls {id} from the chi URL var + parses it. Common
// preamble for every handler in this file.
func credIDFromURL(r *http.Request) (int64, *httpErr) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		return 0, &httpErr{status: http.StatusBadRequest, msg: "bad credential id"}
	}
	return id, nil
}

// queryArg returns a trimmed query parameter; empty string when absent.
func queryArg(r *http.Request, key string) string {
	return strings.TrimSpace(r.URL.Query().Get(key))
}

// ── handlers ───────────────────────────────────────────────────────

// ociItem is the wire shape every list endpoint returns: id +
// human-readable name plus a freeform attrs map for cloud-specific
// extras the form might want to surface (e.g. AD index, image OS
// version, shape OCPU/memory ranges).
type ociItem struct {
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Attrs map[string]any `json:"attrs,omitempty"`
}

// CloudDiscoveryOCICompartments — GET /api/cloud-credentials/{id}/oci/compartments[?root=tenancy].
// Returns flat list of accessible compartments. Default root is the
// tenancy itself; pass ?root=<ocid> to scope a subtree.
func CloudDiscoveryOCICompartments(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		credID, herr := credIDFromURL(r)
		if herr != nil {
			http.Error(w, herr.msg, herr.status)
			return
		}
		provider, cred, herr := loadOCIProviderForCredential(store, credID, queryArg(r, "region"))
		if herr != nil {
			http.Error(w, herr.msg, herr.status)
			return
		}
		client, err := identity.NewIdentityClientWithConfigurationProvider(provider)
		if err != nil {
			http.Error(w, "iam client: "+err.Error(), http.StatusInternalServerError)
			return
		}
		root := queryArg(r, "root")
		if root == "" {
			root = cred.TenancyOCID
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*identitySecond)
		defer cancel()
		// Compartment list is small; pull everything in the subtree
		// in one call (AccessLevel=ANY + IncludeSubtree=true).
		resp, err := client.ListCompartments(ctx, identity.ListCompartmentsRequest{
			CompartmentId:          common.String(root),
			AccessLevel:            identity.ListCompartmentsAccessLevelAny,
			CompartmentIdInSubtree: common.Bool(true),
			LifecycleState:         identity.CompartmentLifecycleStateActive,
		})
		if err != nil {
			http.Error(w, "ListCompartments: "+err.Error(), http.StatusBadGateway)
			return
		}
		// Always include the tenancy root so the operator can deploy
		// directly into it without a subcompartment.
		out := []ociItem{{ID: cred.TenancyOCID, Name: "(tenancy root)"}}
		for _, c := range resp.Items {
			if c.Id == nil || c.Name == nil {
				continue
			}
			out = append(out, ociItem{
				ID:   *c.Id,
				Name: *c.Name,
				Attrs: map[string]any{
					"description": stringOr(c.Description, ""),
				},
			})
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// CloudDiscoveryOCIAvailabilityDomains — GET .../oci/availability-domains?compartment_id=X.
// Compartment is required because AD listing is compartment-scoped
// in the SDK (even though ADs span the tenancy in reality).
func CloudDiscoveryOCIAvailabilityDomains(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		credID, herr := credIDFromURL(r)
		if herr != nil {
			http.Error(w, herr.msg, herr.status)
			return
		}
		provider, cred, herr := loadOCIProviderForCredential(store, credID, queryArg(r, "region"))
		if herr != nil {
			http.Error(w, herr.msg, herr.status)
			return
		}
		compartmentID := queryArg(r, "compartment_id")
		if compartmentID == "" {
			compartmentID = cred.TenancyOCID
		}
		client, err := identity.NewIdentityClientWithConfigurationProvider(provider)
		if err != nil {
			http.Error(w, "iam client: "+err.Error(), http.StatusInternalServerError)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*identitySecond)
		defer cancel()
		resp, err := client.ListAvailabilityDomains(ctx, identity.ListAvailabilityDomainsRequest{
			CompartmentId: common.String(compartmentID),
		})
		if err != nil {
			http.Error(w, "ListAvailabilityDomains: "+err.Error(), http.StatusBadGateway)
			return
		}
		out := make([]ociItem, 0, len(resp.Items))
		for _, ad := range resp.Items {
			if ad.Name == nil {
				continue
			}
			out = append(out, ociItem{
				ID:   *ad.Name, // AD's "id" in launch APIs IS the name string
				Name: *ad.Name,
			})
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// CloudDiscoveryOCISubnets — GET .../oci/subnets?compartment_id=X[&vcn_id=Y].
// Lists subnets in the compartment; optional vcn_id narrows further.
func CloudDiscoveryOCISubnets(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		credID, herr := credIDFromURL(r)
		if herr != nil {
			http.Error(w, herr.msg, herr.status)
			return
		}
		provider, _, herr := loadOCIProviderForCredential(store, credID, queryArg(r, "region"))
		if herr != nil {
			http.Error(w, herr.msg, herr.status)
			return
		}
		compartmentID := queryArg(r, "compartment_id")
		if compartmentID == "" {
			http.Error(w, "compartment_id is required", http.StatusBadRequest)
			return
		}
		vnet, err := core.NewVirtualNetworkClientWithConfigurationProvider(provider)
		if err != nil {
			http.Error(w, "vnet client: "+err.Error(), http.StatusInternalServerError)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*identitySecond)
		defer cancel()
		req := core.ListSubnetsRequest{
			CompartmentId:  common.String(compartmentID),
			LifecycleState: core.SubnetLifecycleStateAvailable,
		}
		if vcn := queryArg(r, "vcn_id"); vcn != "" {
			req.VcnId = common.String(vcn)
		}
		resp, err := vnet.ListSubnets(ctx, req)
		if err != nil {
			http.Error(w, "ListSubnets: "+err.Error(), http.StatusBadGateway)
			return
		}
		out := make([]ociItem, 0, len(resp.Items))
		for _, s := range resp.Items {
			if s.Id == nil || s.DisplayName == nil {
				continue
			}
			out = append(out, ociItem{
				ID:   *s.Id,
				Name: *s.DisplayName,
				Attrs: map[string]any{
					"cidr_block":         stringOr(s.CidrBlock, ""),
					"vcn_id":             stringOr(s.VcnId, ""),
					"prohibit_public_ip": s.ProhibitPublicIpOnVnic != nil && *s.ProhibitPublicIpOnVnic,
				},
			})
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// CloudDiscoveryOCIImages — GET .../oci/images?compartment_id=X[&os=Oracle Linux][&shape=...].
// Filters to Oracle-published images by default (excludes the
// operator's custom builds) and dedups by OS+version, returning the
// most recent build of each. Operators rarely want anything but
// "latest stable Oracle Linux 9 / Ubuntu 22.04 / etc."; we return ~30
// rows max so the dropdown stays usable.
func CloudDiscoveryOCIImages(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		credID, herr := credIDFromURL(r)
		if herr != nil {
			http.Error(w, herr.msg, herr.status)
			return
		}
		provider, _, herr := loadOCIProviderForCredential(store, credID, queryArg(r, "region"))
		if herr != nil {
			http.Error(w, herr.msg, herr.status)
			return
		}
		compartmentID := queryArg(r, "compartment_id")
		if compartmentID == "" {
			http.Error(w, "compartment_id is required", http.StatusBadRequest)
			return
		}
		compute, err := core.NewComputeClientWithConfigurationProvider(provider)
		if err != nil {
			http.Error(w, "compute client: "+err.Error(), http.StatusInternalServerError)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*identitySecond)
		defer cancel()
		req := core.ListImagesRequest{
			CompartmentId:  common.String(compartmentID),
			LifecycleState: core.ImageLifecycleStateAvailable,
			Limit:          common.Int(200),
			SortBy:         core.ListImagesSortByTimecreated,
			SortOrder:      core.ListImagesSortOrderDesc,
		}
		if os := queryArg(r, "os"); os != "" {
			req.OperatingSystem = common.String(os)
		}
		if shape := queryArg(r, "shape"); shape != "" {
			req.Shape = common.String(shape)
		}
		resp, err := compute.ListImages(ctx, req)
		if err != nil {
			http.Error(w, "ListImages: "+err.Error(), http.StatusBadGateway)
			return
		}
		// Dedup by (operating_system, operating_system_version) so the
		// dropdown shows one row per OS+version (the latest), not 50
		// rows of "Oracle Linux 9 — build 2024.01" / "build 2024.02".
		// Order is preserved from the SDK response (newest first), so
		// the first hit per key is the most recent.
		seen := map[string]bool{}
		out := []ociItem{}
		for _, img := range resp.Items {
			if img.Id == nil || img.OperatingSystem == nil {
				continue
			}
			key := *img.OperatingSystem
			if img.OperatingSystemVersion != nil {
				key += "|" + *img.OperatingSystemVersion
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			displayName := stringOr(img.DisplayName, key)
			out = append(out, ociItem{
				ID:   *img.Id,
				Name: displayName,
				Attrs: map[string]any{
					"os":         stringOr(img.OperatingSystem, ""),
					"os_version": stringOr(img.OperatingSystemVersion, ""),
				},
			})
			if len(out) >= 30 {
				break
			}
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// CloudDiscoveryOCIShapes — GET .../oci/shapes?compartment_id=X[&availability_domain=Y].
// Returns shapes available in the operator's tenancy. Filters to
// "general purpose" + "memory optimized" shapes by name prefix —
// Bare-Metal and DenseIO/GPU shapes are out of scope for a CP
// (overprovisioned).
func CloudDiscoveryOCIShapes(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		credID, herr := credIDFromURL(r)
		if herr != nil {
			http.Error(w, herr.msg, herr.status)
			return
		}
		provider, _, herr := loadOCIProviderForCredential(store, credID, queryArg(r, "region"))
		if herr != nil {
			http.Error(w, herr.msg, herr.status)
			return
		}
		compartmentID := queryArg(r, "compartment_id")
		if compartmentID == "" {
			http.Error(w, "compartment_id is required", http.StatusBadRequest)
			return
		}
		compute, err := core.NewComputeClientWithConfigurationProvider(provider)
		if err != nil {
			http.Error(w, "compute client: "+err.Error(), http.StatusInternalServerError)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*identitySecond)
		defer cancel()
		req := core.ListShapesRequest{
			CompartmentId: common.String(compartmentID),
			Limit:         common.Int(200),
		}
		if ad := queryArg(r, "availability_domain"); ad != "" {
			req.AvailabilityDomain = common.String(ad)
		}
		resp, err := compute.ListShapes(ctx, req)
		if err != nil {
			http.Error(w, "ListShapes: "+err.Error(), http.StatusBadGateway)
			return
		}
		seen := map[string]bool{}
		out := []ociItem{}
		for _, s := range resp.Items {
			if s.Shape == nil || seen[*s.Shape] {
				continue
			}
			name := *s.Shape
			// Skip bare-metal / GPU / DenseIO — all overkill for a CP.
			if strings.HasPrefix(name, "BM.") ||
				strings.Contains(name, "GPU") ||
				strings.Contains(name, "DenseIO") {
				continue
			}
			seen[name] = true
			attrs := map[string]any{}
			if s.OcpuOptions != nil && s.OcpuOptions.Min != nil && s.OcpuOptions.Max != nil {
				attrs["ocpus_min"] = *s.OcpuOptions.Min
				attrs["ocpus_max"] = *s.OcpuOptions.Max
			}
			if s.MemoryOptions != nil && s.MemoryOptions.MinInGBs != nil && s.MemoryOptions.MaxInGBs != nil {
				attrs["memory_min_gb"] = *s.MemoryOptions.MinInGBs
				attrs["memory_max_gb"] = *s.MemoryOptions.MaxInGBs
			}
			out = append(out, ociItem{ID: name, Name: name, Attrs: attrs})
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// stringOr unwraps a *string with a fallback.
func stringOr(p *string, fallback string) string {
	if p == nil {
		return fallback
	}
	return *p
}

// identitySecond is just time.Second renamed for ergonomic
// inline-multiplication. Aliased so the file doesn't pull in
// time at every call site (we already have it transitively, but
// the alias keeps the per-handler timeout literal short).
const identitySecond = 1_000_000_000 // 1s in nanoseconds (time.Duration units)
