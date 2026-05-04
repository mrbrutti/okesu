// Bucket provisioning HTTP handlers — surface the BucketRegistry to
// the Settings → Add Bucket wizard.

package api

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/section9labs/okesu/controlplane/cpprovision"
	"github.com/section9labs/okesu/controlplane/db"
)

// bucketProviderJSON is the wire shape returned by
// GET /api/buckets/cloud-providers — same projection as Add-CP uses.
type bucketProviderJSON struct {
	ID          int64  `json:"id"`
	Cloud       string `json:"cloud"`
	DisplayName string `json:"display_name"`
	Region      string `json:"region,omitempty"`
}

// BucketCloudProviders returns cloud_credentials filtered to the
// kinds for which we have a BucketProvisioner registered.
func BucketCloudProviders(store *db.Store, registry *cpprovision.BucketRegistry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		supported := registry.Clouds()
		creds, err := store.ListCloudCredentials("")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := make([]bucketProviderJSON, 0)
		for _, c := range creds {
			if !contains(supported, c.Cloud) {
				continue
			}
			region := ""
			if c.Region.Valid {
				region = c.Region.String
			}
			out = append(out, bucketProviderJSON{
				ID:          c.ID,
				Cloud:       c.Cloud,
				DisplayName: c.Name,
				Region:      region,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// BucketDiscover lists existing buckets in the credential's account.
func BucketDiscover(store *db.Store, registry *cpprovision.BucketRegistry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := r.URL.Query().Get("cloud_credential_id")
		region := r.URL.Query().Get("region")
		compartmentID := r.URL.Query().Get("compartment_id")
		credID, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || credID == 0 {
			http.Error(w, "missing or bad cloud_credential_id", http.StatusBadRequest)
			return
		}
		cred, err := store.GetCloudCredential(credID)
		if err != nil {
			http.Error(w, "cloud credential not found", http.StatusNotFound)
			return
		}
		provisioner, err := registry.Get(cred.Cloud)
		if err != nil {
			http.Error(w, fmt.Sprintf("no bucket provisioner for cloud %q", cred.Cloud), http.StatusBadRequest)
			return
		}
		masterKey, err := store.MasterKeyFromMeta()
		if err != nil {
			http.Error(w, "master key: "+err.Error(), http.StatusInternalServerError)
			return
		}
		payload, err := store.DecryptCloudCredential(credID, masterKey)
		if err != nil {
			http.Error(w, "decrypt credential: "+err.Error(), http.StatusInternalServerError)
			return
		}
		buckets, err := provisioner.ListBuckets(r.Context(), payload, region, compartmentID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		if buckets == nil {
			buckets = []cpprovision.BucketInfo{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(buckets)
	}
}

// bucketProvisionReq is the wire shape of POST /api/buckets/provision.
type bucketProvisionReq struct {
	CloudCredentialID int64  `json:"cloud_credential_id"`
	Region            string `json:"region"`
	BucketName        string `json:"bucket_name"`
	Mode              string `json:"mode"` // "discover" | "create"
	DisplayName       string `json:"display_name"`
	GenerateFleetKeys bool   `json:"generate_fleet_keys"`
	ScannerIntervalMs int    `json:"scanner_interval_ms"`
	// CompartmentID overrides the credential's default OCI compartment
	// for ListBuckets / EnsureBucket. Empty string falls back to the
	// credential's stored compartment, then the tenancy root. Ignored
	// by AWS and MinIO provisioners.
	CompartmentID string `json:"compartment_id,omitempty"`
}

// BucketProvision either discovers an existing bucket and writes a
// transport_configs row, or creates a new bucket via SDK and writes
// the row. Returns the new transport_configs row.
func BucketProvision(store *db.Store, registry *cpprovision.BucketRegistry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req bucketProvisionReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if req.CloudCredentialID == 0 || req.BucketName == "" || req.DisplayName == "" {
			http.Error(w, "missing required fields", http.StatusBadRequest)
			return
		}
		switch req.Mode {
		case "discover", "create":
		default:
			http.Error(w, "mode must be 'discover' or 'create'", http.StatusBadRequest)
			return
		}

		cred, err := store.GetCloudCredential(req.CloudCredentialID)
		if err != nil {
			http.Error(w, "cloud credential not found", http.StatusNotFound)
			return
		}
		provisioner, err := registry.Get(cred.Cloud)
		if err != nil {
			http.Error(w, "no bucket provisioner for cloud "+cred.Cloud, http.StatusBadRequest)
			return
		}
		masterKey, err := store.MasterKeyFromMeta()
		if err != nil {
			http.Error(w, "master key: "+err.Error(), http.StatusInternalServerError)
			return
		}
		payload, err := store.DecryptCloudCredential(req.CloudCredentialID, masterKey)
		if err != nil {
			http.Error(w, "decrypt credential: "+err.Error(), http.StatusInternalServerError)
			return
		}

		var info *cpprovision.BucketInfo
		switch req.Mode {
		case "discover":
			list, err := provisioner.ListBuckets(r.Context(), payload, req.Region, req.CompartmentID)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			for i := range list {
				if list[i].Name == req.BucketName {
					info = &list[i]
					break
				}
			}
			if info == nil {
				http.Error(w, fmt.Sprintf("bucket %q not found in account", req.BucketName), http.StatusNotFound)
				return
			}
		case "create":
			info, err = provisioner.EnsureBucket(r.Context(), payload, req.BucketName, req.Region, req.CompartmentID)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
		}

		access, secret, err := provisioner.BucketAccessKeys(r.Context(), payload)
		if err != nil {
			http.Error(w, "obtain access keys: "+err.Error(), http.StatusBadGateway)
			return
		}

		// minio-go's New(endpoint, ...) builds the URL itself from the
		// scheme implied by UseSSL, so a fully-qualified URL stored
		// here gets concatenated into "https://https://host…" and the
		// validator rejects it as "fully qualified paths". Strip the
		// scheme on write and let UseSSL drive the secure flag.
		endpointHost, secure := splitS3Endpoint(info.Endpoint)
		tc := db.TransportConfig{
			Name:              strings.TrimSpace(req.DisplayName),
			Kind:              "s3",
			Bucket:            info.Name,
			Endpoint:          endpointHost,
			Region:            bucketsNullableString(info.Region),
			UseSSL:            secure,
			AccessKey:         bucketsNullableString(access),
			SecretKey:         bucketsNullableString(secret),
			ScannerIntervalMs: req.ScannerIntervalMs,
		}
		if req.GenerateFleetKeys {
			pubPEM, privPEM, err := generateFleetKeypair()
			if err != nil {
				http.Error(w, "fleet keys: "+err.Error(), http.StatusInternalServerError)
				return
			}
			tc.FleetPubkeyPEM = bucketsNullableString(pubPEM)
			tc.FleetPrivkeyPEM = bucketsNullableString(privPEM)
		}
		// Random unique CP id to satisfy the schema's expectation.
		tc.CPID = bucketsNullableString(randomCPID())
		id, err := store.CreateTransportConfig(tc)
		if err != nil {
			http.Error(w, "create transport_config: "+err.Error(), http.StatusInternalServerError)
			return
		}
		tc.ID = id
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(tc)
	}
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

// bucketsNullableString is a local helper. The api package has no
// shared nullable-string utility today (the unexported one in db/findings.go
// returns `any`). Defining it here avoids cross-file coupling.
func bucketsNullableString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

// randomCPID returns a hex string suitable for transport_configs.cp_id.
// 16 bytes → 32 hex chars matches the existing format used elsewhere.
func randomCPID() string {
	var buf [16]byte
	_, _ = rand.Read(buf[:])
	return hex.EncodeToString(buf[:])
}

// splitS3Endpoint normalises an S3-style endpoint string into the
// host[:port] form that minio-go expects, plus a derived secure flag.
// The provisioners (e.g. ociS3Endpoint) emit a fully-qualified URL
// like "https://ns.compat.objectstorage.region.oraclecloud.com" because
// that's what an operator copy-pastes into a doc — but minio-go's
// New(endpoint, …) prepends its own scheme based on Options.Secure,
// so leaving the prefix in place produces "https://https://…", which
// the validator rejects with "Endpoint url cannot have fully qualified
// paths". A trailing path/slash is stripped too.
func splitS3Endpoint(in string) (host string, useSSL bool) {
	in = strings.TrimSpace(in)
	useSSL = true
	switch {
	case strings.HasPrefix(in, "https://"):
		in = strings.TrimPrefix(in, "https://")
		useSSL = true
	case strings.HasPrefix(in, "http://"):
		in = strings.TrimPrefix(in, "http://")
		useSSL = false
	}
	if i := strings.IndexByte(in, '/'); i >= 0 {
		in = in[:i]
	}
	return in, useSSL
}
