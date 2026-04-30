// HTTP API for the S3 dead-drop transport: CRUD on transport_configs,
// generate-and-download enrollment packages, list/revoke packages.
//
// Routes (mounted under the standard authenticated prefix):
//
//   GET    /api/transport-configs
//   POST   /api/transport-configs
//   GET    /api/transport-configs/{id}
//   PUT    /api/transport-configs/{id}
//   DELETE /api/transport-configs/{id}
//
//   GET    /api/enrollment-packages
//   POST   /api/enrollment-packages         → JSON metadata
//   GET    /api/enrollment-packages/{id}/download?format=tar.gz
//   POST   /api/enrollment-packages/{id}/revoke
//
// All routes require admin role (set in server.go's mount).

package api

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/section9labs/okesu/controlplane/auth"
	"github.com/section9labs/okesu/controlplane/db"
	"github.com/section9labs/okesu/controlplane/packaging"
)

// ───────────────────────── transport_configs ─────────────────────────

type transportConfigJSON struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
	// Bucket + Endpoint are what nodes embed in their bootstrap.json.
	// EndpointInternal, when set, is what the CP scanner dials —
	// useful when the CP can reach the bucket over a private VPC
	// endpoint that public clients can't resolve. NULL on the wire
	// (omitempty + nullable string) means "scanner falls back to
	// Endpoint".
	Bucket            string `json:"bucket"`
	Endpoint          string `json:"endpoint"`
	EndpointInternal  string `json:"endpoint_internal,omitempty"`
	Region            string `json:"region,omitempty"`
	UseSSL            bool   `json:"use_ssl"`
	AccessKey         string `json:"access_key,omitempty"`
	HasSecretKey      bool   `json:"has_secret_key"`
	HasFleetPubkey    bool   `json:"has_fleet_pubkey"`
	HasFleetPrivkey   bool   `json:"has_fleet_privkey"`
	ScannerIntervalMs int    `json:"scanner_interval_ms"`
	CPID              string `json:"cp_id,omitempty"`
	CreatedAt         string `json:"created_at,omitempty"`
	UpdatedAt         string `json:"updated_at,omitempty"`
}

func toTransportConfigJSON(c db.TransportConfig) transportConfigJSON {
	return transportConfigJSON{
		ID:                c.ID,
		Name:              c.Name,
		Kind:              c.Kind,
		Bucket:            c.Bucket,
		Endpoint:          c.Endpoint,
		EndpointInternal:  c.EndpointInternal.String,
		Region:            c.Region.String,
		UseSSL:            c.UseSSL,
		AccessKey:         c.AccessKey.String,
		HasSecretKey:      c.SecretKey.Valid && c.SecretKey.String != "",
		HasFleetPubkey:    c.FleetPubkeyPEM.Valid && c.FleetPubkeyPEM.String != "",
		HasFleetPrivkey:   c.FleetPrivkeyPEM.Valid && c.FleetPrivkeyPEM.String != "",
		ScannerIntervalMs: c.ScannerIntervalMs,
		CPID:              c.CPID.String,
		CreatedAt:         c.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		UpdatedAt:         c.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
}

type transportConfigReq struct {
	Name              string `json:"name"`
	Kind              string `json:"kind"`
	Bucket            string `json:"bucket"`
	Endpoint          string `json:"endpoint"`
	EndpointInternal  string `json:"endpoint_internal,omitempty"`    // empty = scanner falls back to Endpoint
	Region            string `json:"region,omitempty"`
	UseSSL            bool   `json:"use_ssl"`
	AccessKey         string `json:"access_key,omitempty"`
	SecretKey         string `json:"secret_key,omitempty"`           // omit on PUT to keep current
	GenerateFleetKeys bool   `json:"generate_fleet_keys,omitempty"`  // POST/PUT: mint a fresh fleet keypair
	ScannerIntervalMs int    `json:"scanner_interval_ms,omitempty"`
	CPID              string `json:"cp_id,omitempty"`
}

// TransportConfigsList — GET /api/transport-configs
func TransportConfigsList(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := store.ListTransportConfigs()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := make([]transportConfigJSON, 0, len(rows))
		for _, c := range rows {
			out = append(out, toTransportConfigJSON(c))
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// TransportConfigDetail — GET /api/transport-configs/{id}
func TransportConfigDetail(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		c, err := store.GetTransportConfig(id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, toTransportConfigJSON(c))
	}
}

// TransportConfigCreate — POST /api/transport-configs
func TransportConfigCreate(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req transportConfigReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if req.Kind == "" {
			req.Kind = "s3"
		}
		if req.ScannerIntervalMs == 0 {
			req.ScannerIntervalMs = 10000
		}
		c := db.TransportConfig{
			Name:              req.Name,
			Kind:              req.Kind,
			Bucket:            req.Bucket,
			Endpoint:          req.Endpoint,
			EndpointInternal:  sql.NullString{String: req.EndpointInternal, Valid: req.EndpointInternal != ""},
			Region:            sql.NullString{String: req.Region, Valid: req.Region != ""},
			UseSSL:            req.UseSSL,
			AccessKey:         sql.NullString{String: req.AccessKey, Valid: req.AccessKey != ""},
			SecretKey:         sql.NullString{String: req.SecretKey, Valid: req.SecretKey != ""},
			ScannerIntervalMs: req.ScannerIntervalMs,
			CPID:              sql.NullString{String: req.CPID, Valid: req.CPID != ""},
		}
		if req.GenerateFleetKeys {
			pubPEM, privPEM, err := generateFleetKeypair()
			if err != nil {
				http.Error(w, "fleet keys: "+err.Error(), http.StatusInternalServerError)
				return
			}
			c.FleetPubkeyPEM = sql.NullString{String: pubPEM, Valid: true}
			c.FleetPrivkeyPEM = sql.NullString{String: privPEM, Valid: true}
		}
		id, err := store.CreateTransportConfig(c)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		c.ID = id
		writeJSON(w, http.StatusCreated, toTransportConfigJSON(c))
	}
}

// TransportConfigUpdate — PUT /api/transport-configs/{id}
func TransportConfigUpdate(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		existing, err := store.GetTransportConfig(id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		var req transportConfigReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		// Preserve unchanged secrets when the operator submits empty
		// strings (form pattern: leave blank to keep current).
		if req.SecretKey != "" {
			existing.SecretKey = sql.NullString{String: req.SecretKey, Valid: true}
		}
		if req.GenerateFleetKeys {
			pubPEM, privPEM, err := generateFleetKeypair()
			if err != nil {
				http.Error(w, "fleet keys: "+err.Error(), http.StatusInternalServerError)
				return
			}
			existing.FleetPubkeyPEM = sql.NullString{String: pubPEM, Valid: true}
			existing.FleetPrivkeyPEM = sql.NullString{String: privPEM, Valid: true}
		}
		existing.Name = req.Name
		existing.Kind = req.Kind
		existing.Bucket = req.Bucket
		existing.Endpoint = req.Endpoint
		// Setting EndpointInternal to "" on update is meaningful — it
		// clears the override so the scanner falls back to Endpoint.
		// We don't preserve the previous value when the operator sends
		// an empty string, only when the field is missing entirely.
		existing.EndpointInternal = sql.NullString{String: req.EndpointInternal, Valid: req.EndpointInternal != ""}
		existing.Region = sql.NullString{String: req.Region, Valid: req.Region != ""}
		existing.UseSSL = req.UseSSL
		existing.AccessKey = sql.NullString{String: req.AccessKey, Valid: req.AccessKey != ""}
		if req.ScannerIntervalMs > 0 {
			existing.ScannerIntervalMs = req.ScannerIntervalMs
		}
		if req.CPID != "" {
			existing.CPID = sql.NullString{String: req.CPID, Valid: true}
		}
		if err := store.UpdateTransportConfig(existing); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, toTransportConfigJSON(existing))
	}
}

// transportConfigPatchReq is the wire shape for PATCH. Only fields
// that are safe to edit post-creation appear here. Identity fields
// (bucket, endpoint, access_key, secret_key, region,
// cloud_credential_id) are intentionally absent — changing them
// would silently break enrollment packages and federated peers
// tied to the old identity. Operators who need a different bucket
// delete + re-add. Key rotation is deferred to a dedicated wizard.
type transportConfigPatchReq struct {
	Name              *string `json:"name,omitempty"`
	ScannerIntervalMs *int    `json:"scanner_interval_ms,omitempty"`
}

// TransportConfigPatch applies partial updates to a transport_config.
// Identity fields are not in transportConfigPatchReq so attempting
// to change them is a no-op (the JSON decoder drops them).
func TransportConfigPatch(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		existing, err := store.GetTransportConfig(id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		var patch transportConfigPatchReq
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if patch.Name != nil {
			existing.Name = strings.TrimSpace(*patch.Name)
		}
		if patch.ScannerIntervalMs != nil {
			existing.ScannerIntervalMs = *patch.ScannerIntervalMs
		}
		if err := store.UpdateTransportConfig(existing); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(existing)
	}
}

// transportConfigDeleteConflict is the body returned by DELETE when
// the row is referenced. Frontend renders an actionable list with
// deep-links to the relevant pages.
type transportConfigDeleteConflict struct {
	Message      string                       `json:"message"`
	ReferencedBy db.TransportConfigReferences `json:"referenced_by"`
}

// TransportConfigDelete removes a transport_config row, refusing
// with 409 + a referencing-resources list if any nodes or
// enrollment_packages reference it. (federation_peers and
// cp_provisions FKs use ON DELETE SET NULL; they're surfaced in the
// list as info but don't block.)
func TransportConfigDelete(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		refs, err := store.TransportConfigReferences(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if refs.HasBlockers() {
			conflict := transportConfigDeleteConflict{
				Message:      "transport_config is referenced by nodes or enrollment_packages; remove those dependencies first",
				ReferencedBy: refs,
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(conflict)
			return
		}
		if err := store.DeleteTransportConfig(id); err != nil {
			if errors.Is(err, db.ErrTransportConfigInUse) {
				// Race: refs query saw it clean but DELETE saw a new
				// dependency. Re-fetch and surface as 409.
				refs, _ := store.TransportConfigReferences(id)
				conflict := transportConfigDeleteConflict{
					Message:      "transport_config became referenced during deletion; retry after removing the new dependencies",
					ReferencedBy: refs,
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(conflict)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// ───────────────────────── enrollment packages ─────────────────────────

type enrollmentPackageJSON struct {
	ID                int64  `json:"id"`
	DisplayName       string `json:"display_name"`
	TransportConfigID int64  `json:"transport_config_id"`
	CPID              string `json:"cp_id"`
	CreatedAt         string `json:"created_at,omitempty"`
	RevokedAt         string `json:"revoked_at,omitempty"`
}

func toEnrollmentPackageJSON(p db.EnrollmentPackage) enrollmentPackageJSON {
	out := enrollmentPackageJSON{
		ID:                p.ID,
		DisplayName:       p.DisplayName,
		TransportConfigID: p.TransportConfigID,
		CPID:              p.CPID,
		CreatedAt:         p.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if p.RevokedAt.Valid {
		out.RevokedAt = p.RevokedAt.Time.UTC().Format("2006-01-02T15:04:05Z")
	}
	return out
}

type packageCreateReq struct {
	DisplayName       string                     `json:"display_name"`
	TransportConfigID int64                      `json:"transport_config_id"`
	Defaults          packaging.PackageDefaults  `json:"defaults"`
}

// EnrollmentPackagesList — GET /api/enrollment-packages
func EnrollmentPackagesList(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := store.ListEnrollmentPackages()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := make([]enrollmentPackageJSON, 0, len(rows))
		for _, p := range rows {
			out = append(out, toEnrollmentPackageJSON(p))
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// EnrollmentPackageCreate — POST /api/enrollment-packages
func EnrollmentPackageCreate(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req packageCreateReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		cfg, err := store.GetTransportConfig(req.TransportConfigID)
		if err != nil {
			http.Error(w, "transport config not found", http.StatusBadRequest)
			return
		}
		if !cfg.FleetPrivkeyPEM.Valid || cfg.FleetPrivkeyPEM.String == "" {
			http.Error(w, "transport config has no fleet keypair (use POST /api/transport-configs with generate_fleet_keys, or PUT to add)", http.StatusBadRequest)
			return
		}
		// Pre-allocate the row so we know the package_id at signing
		// time — the cert CN embeds it, and the DB scanner uses it
		// to look up the issuing package row.
		pkgRow := db.EnrollmentPackage{
			DisplayName:       req.DisplayName,
			TransportConfigID: cfg.ID,
			CPID:              cfg.CPID.String,
			DefaultsJSON: sql.NullString{
				String: marshalDefaults(req.Defaults),
				Valid:  true,
			},
		}
		if u := auth.UserFromContext(r.Context()); u != nil {
			pkgRow.CreatedBy = sql.NullInt64{Int64: u.ID, Valid: true}
		}
		// Sign cert + key against the fleet keypair, persist them,
		// then return the row metadata. Cert+key bytes are NOT
		// returned in the create response — operators download them
		// via the /download endpoint.
		certPEM, keyPEM, err := packaging.SignPackageCert(cfg.FleetPubkeyPEM.String, cfg.FleetPrivkeyPEM.String, 0)
		if err != nil {
			http.Error(w, "sign package: "+err.Error(), http.StatusInternalServerError)
			return
		}
		pkgRow.PackageCertPEM = certPEM
		pkgRow.PackageKeyPEM = keyPEM

		id, err := store.CreateEnrollmentPackage(pkgRow)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		pkgRow.ID = id
		writeJSON(w, http.StatusCreated, toEnrollmentPackageJSON(pkgRow))
	}
}

// EnrollmentPackageDownload — GET /api/enrollment-packages/{id}/download?format=tar.gz
//
// Builds the package on demand. The signing material was minted at
// Create time and stored on the row; here we just embed it into the
// chosen format. We intentionally don't cache built bytes — the
// binary set may change between calls (multi-arch deploys), and
// recomputing is cheap.
func EnrollmentPackageDownload(store *db.Store, binResolver func(string) ([]byte, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		pkg, err := store.GetEnrollmentPackage(id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		cfg, err := store.GetTransportConfig(pkg.TransportConfigID)
		if err != nil {
			http.Error(w, "transport config not found", http.StatusInternalServerError)
			return
		}
		format := r.URL.Query().Get("format")
		if format == "" {
			format = "tar.gz"
		}

		// Resolve binaries — use the existing daemon_binaries
		// resolver if supplied. Multi-arch is a future expansion
		// (the resolver currently returns one blob per call); for
		// now we ship just the host's native arch.
		bins := map[string][]byte{}
		for _, target := range []string{"linux-amd64", "linux-arm64", "darwin-arm64", "darwin-amd64", "freebsd-amd64"} {
			body, err := binResolver(target)
			if err != nil || len(body) == 0 {
				continue
			}
			bins[target] = body
		}
		if len(bins) == 0 {
			http.Error(w, "no daemon binaries available — upload via /api/daemon-binaries first", http.StatusFailedDependency)
			return
		}

		var defaults packaging.PackageDefaults
		if pkg.DefaultsJSON.Valid {
			_ = json.Unmarshal([]byte(pkg.DefaultsJSON.String), &defaults)
		}
		buildReq := packaging.BuildRequest{
			DisplayName:    pkg.DisplayName,
			PackageID:      pkg.ID,
			Bucket:         cfg.Bucket,
			Endpoint:       cfg.Endpoint,
			Region:         cfg.Region.String,
			UseSSL:         cfg.UseSSL,
			AccessKey:      cfg.AccessKey.String,
			SecretKey:      cfg.SecretKey.String,
			CPID:           cfg.CPID.String,
			FleetPubkeyPEM: cfg.FleetPubkeyPEM.String,
			PackageCertPEM: pkg.PackageCertPEM,
			PackageKeyPEM:  pkg.PackageKeyPEM,
			Defaults:       defaults,
			Binaries:       bins,
			Format:         format,
		}
		res, err := packaging.Build(buildReq)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", res.ContentType)
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, res.Filename))
		_, _ = w.Write(res.Bytes)
	}
}

// EnrollmentPackageRevoke — POST /api/enrollment-packages/{id}/revoke
func EnrollmentPackageRevoke(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		if err := store.RevokeEnrollmentPackage(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// generateFleetKeypair mints an ECDSA P-256 keypair for the fleet
// trust anchor. Public key is published (in the bucket + the
// generated package); private key never leaves the CP.
func generateFleetKeypair() (pubPEM, privPEM string, err error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		return "", "", err
	}
	privDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return "", "", err
	}
	pubPEM = string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))
	privPEM = string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privDER}))
	return pubPEM, privPEM, nil
}

func marshalDefaults(d packaging.PackageDefaults) string {
	b, _ := json.Marshal(d)
	return string(b)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
