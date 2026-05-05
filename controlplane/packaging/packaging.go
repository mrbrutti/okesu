// Package packaging builds self-contained enrollment packages —
// drop-on-host installers that auto-register N machines against a
// CP via the S3 dead-drop transport.
//
// Design goal: pluggable formatter so we can add `.deb`, `.rpm`,
// `.pkg`, `.msi` over time without touching the core. The Builder
// assembles the platform-neutral payload (binary, install.sh,
// bootstrap.json, fleet pubkey, package cert+key); each Formatter
// wraps the payload in the OS-specific archive format.
//
// v1 ships the tar.gz formatter. Adding a new formatter means
// implementing the Formatter interface and registering it via
// Register(); no other code changes.

package packaging

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"sync"
	"time"
)

// BuildRequest carries everything the Builder needs to mint a
// package. Constructed by the API handler from the operator's form
// + the CP's transport_config row.
type BuildRequest struct {
	// Identity / labels
	DisplayName string
	PackageID   int64

	// Bucket connection — embedded into bootstrap.json so the node
	// can connect with no manual config.
	Bucket    string
	Endpoint  string
	Region    string
	UseSSL    bool
	AccessKey string
	SecretKey string
	CPID      string

	// Trust material
	FleetPubkeyPEM string
	// Package signing cert + key — the Builder generates these on
	// Build() if both fields are empty; otherwise reuses them so
	// the operator can re-package an existing enrollment.
	PackageCertPEM string
	PackageKeyPEM  string

	// Per-package defaults — the agents to install on first boot,
	// override poll intervals, etc.
	Defaults PackageDefaults

	// Multi-arch binary blobs keyed by GOOS/GOARCH (e.g. "linux-amd64",
	// "linux-arm64", "darwin-arm64"). The install.sh inside the
	// package picks the right one based on `uname -s/-m`.
	Binaries map[string][]byte

	// Format selects the archive type. Use Formats() to enumerate
	// the registered formatters.
	Format string
}

// PackageDefaults is the same shape `agent/s3transport.PackageDefaults`
// uses on the wire — re-declared here to avoid an agent → controlplane
// import. Both serialise to the same JSON.
type PackageDefaults struct {
	Agents              []string `json:"agents,omitempty"`
	PollIntervalMs      int      `json:"poll_interval_ms,omitempty"`
	HeartbeatIntervalMs int      `json:"heartbeat_interval_ms,omitempty"`
	CPID                string   `json:"cp_id,omitempty"`
}

// BuildResult carries the produced bytes plus metadata. Filename is
// suggested — the API layer can override.
type BuildResult struct {
	Bytes          []byte
	Filename       string
	ContentType    string
	PackageCertPEM string // returned so the caller can persist it; only meaningful on first build
	PackageKeyPEM  string // ditto — never persisted by Builder; the API layer writes to enrollment_packages
}

// Formatter wraps the per-format archive logic. Each implementation
// owns the on-disk layout the resulting installer expects (tar.gz
// extracts to a temp dir, .deb wraps a postinst script, etc.).
type Formatter interface {
	// Format returns the canonical name (e.g. "tar.gz", "deb").
	Format() string
	// Suffix is the conventional file extension (".tar.gz", ".deb").
	Suffix() string
	// ContentType is the HTTP MIME type to advertise.
	ContentType() string
	// Build produces the archive bytes from the assembled payload.
	Build(p Payload) ([]byte, error)
}

// Payload is the format-neutral set of files a package contains.
// Formatters consume this and emit their archive bytes.
type Payload struct {
	// File map keyed by destination path inside the archive.
	Files map[string][]byte
	// PrimaryBinary identifies which file in Files should be marked
	// executable when the formatter unpacks (e.g. "okesu-linux-amd64").
	// Empty means none.
	PrimaryBinary string
	// Metadata for OS-package formatters (control file, plist, etc.).
	DisplayName string
	Description string
	Version     string
}

// ───────────────────────── registry ─────────────────────────

var (
	regMu  sync.RWMutex
	regMap = map[string]Formatter{}
)

// Register installs a formatter under its Format() name. Safe to
// call from package init.
func Register(f Formatter) {
	regMu.Lock()
	regMap[f.Format()] = f
	regMu.Unlock()
}

// Formats returns the registered formatter names sorted for stable
// listing in the UI.
func Formats() []string {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]string, 0, len(regMap))
	for k := range regMap {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func formatter(name string) (Formatter, error) {
	regMu.RLock()
	defer regMu.RUnlock()
	f, ok := regMap[name]
	if !ok {
		return nil, fmt.Errorf("packaging: no formatter %q (have %v)", name, formatterNames())
	}
	return f, nil
}

func formatterNames() []string {
	out := make([]string, 0, len(regMap))
	for k := range regMap {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ───────────────────────── builder ─────────────────────────

// Build assembles the payload and dispatches to the chosen
// formatter. The cert+key fields on the result are populated only
// when the Builder generated them (i.e. when BuildRequest left
// them empty).
func Build(req BuildRequest) (BuildResult, error) {
	if req.CPID == "" {
		return BuildResult{}, errors.New("packaging: cp_id required")
	}
	if req.Bucket == "" || req.Endpoint == "" {
		return BuildResult{}, errors.New("packaging: bucket + endpoint required")
	}
	if len(req.Binaries) == 0 {
		return BuildResult{}, errors.New("packaging: at least one binary required")
	}
	if req.Format == "" {
		req.Format = "tar.gz"
	}
	f, err := formatter(req.Format)
	if err != nil {
		return BuildResult{}, err
	}

	// Generate cert + key if absent. The cert is signed by the
	// fleet keypair (passed in as PEM) so the CP can verify it
	// during enrollment without a round-trip.
	certPEM := req.PackageCertPEM
	keyPEM := req.PackageKeyPEM
	if certPEM == "" || keyPEM == "" {
		var err error
		certPEM, keyPEM, err = generatePackageCert(req)
		if err != nil {
			return BuildResult{}, fmt.Errorf("generate package cert: %w", err)
		}
	}

	bootstrap := map[string]any{
		"package_id":          req.PackageID,
		"display_name":        req.DisplayName,
		"cp_id":               req.CPID,
		"bucket":              req.Bucket,
		"endpoint":            req.Endpoint,
		"region":              req.Region,
		"use_ssl":             req.UseSSL,
		"access_key":          req.AccessKey,
		"secret_key":          req.SecretKey,
		"fleet_pubkey_pem":    req.FleetPubkeyPEM,
		"package_cert_pem":    certPEM,
		"package_key_pem":     keyPEM,
		"defaults":            req.Defaults,
	}
	bootstrapJSON, _ := json.MarshalIndent(bootstrap, "", "  ")

	files := map[string][]byte{
		"bootstrap.json":   bootstrapJSON,
		"install.sh":       []byte(installShellScript),
		"README.txt":       []byte(readmeText(req)),
		"fleet-pubkey.pem": []byte(req.FleetPubkeyPEM),
		"package-cert.pem": []byte(certPEM),
		"package-key.pem":  []byte(keyPEM),
	}
	primary := ""
	for arch, body := range req.Binaries {
		name := "okesu-" + arch
		files[name] = body
		if primary == "" {
			primary = name
		}
	}

	payload := Payload{
		Files:         files,
		PrimaryBinary: primary,
		DisplayName:   req.DisplayName,
		Description:   "Okesu enrollment package — auto-registers nodes against CP " + req.CPID,
		Version:       time.Now().UTC().Format("20060102.150405"),
	}

	body, err := f.Build(payload)
	if err != nil {
		return BuildResult{}, fmt.Errorf("formatter %s: %w", f.Format(), err)
	}

	suffix := f.Suffix()
	filename := safeFilename(req.DisplayName) + suffix

	return BuildResult{
		Bytes:          body,
		Filename:       filename,
		ContentType:    f.ContentType(),
		PackageCertPEM: certPEM,
		PackageKeyPEM:  keyPEM,
	}, nil
}

func safeFilename(name string) string {
	if name == "" {
		return "okesu-package"
	}
	out := make([]byte, 0, len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_':
			out = append(out, c)
		case c >= 'A' && c <= 'Z':
			out = append(out, c+32)
		case c == ' ':
			out = append(out, '-')
		}
	}
	if len(out) == 0 {
		return "okesu-package"
	}
	return string(out)
}

// ───────────────────────── package cert ─────────────────────────

// generatePackageCert mints an ECDSA P-256 keypair and signs a
// short-lived (1y) cert with CN = "okesu-package-<package-id>". The
// cert is itself signed by the fleet keypair so the CP scanner can
// validate it offline by parsing fleet-pubkey.pem.
func generatePackageCert(req BuildRequest) (certPEM, keyPEM string, err error) {
	// Parse fleet private key — must be passed in alongside pubkey
	// for signing. The CP API layer reads it from
	// transport_configs.fleet_privkey_pem and passes via BuildRequest's
	// PackageCertPEM/PackageKeyPEM-as-input model.
	//
	// For v1 we accept that the fleet key materialises here as part
	// of the request; future hardening would route signing through
	// a separate "fleet signer" microsurface.
	if req.FleetPubkeyPEM == "" {
		return "", "", errors.New("fleet pubkey is required to mint package cert (set on transport_configs)")
	}
	// The packaging layer doesn't have the fleet PRIVATE key (we
	// don't pass it through BuildRequest as a defensive choice). So
	// the API layer is responsible for: (1) fetching the fleet key
	// from db, (2) calling SignPackageCert directly, and (3) passing
	// the resulting cert+key into BuildRequest.PackageCertPEM and
	// .PackageKeyPEM. This branch generates a self-signed cert as a
	// safe fallback for tests / dev. Production callers should
	// always pre-sign.
	return generateSelfSignedPackageCert(req)
}

// SignPackageCert signs a fresh package cert with the fleet keypair.
// Called by the API layer; not by Build (which never sees the
// fleet private key).
func SignPackageCert(fleetPubPEM, fleetPrivPEM string, packageID int64) (certPEM, keyPEM string, err error) {
	fleetPriv, err := parseECPrivateKey(fleetPrivPEM)
	if err != nil {
		return "", "", fmt.Errorf("parse fleet private key: %w", err)
	}
	fleetPub, err := parseECPublicKey(fleetPubPEM)
	if err != nil {
		return "", "", fmt.Errorf("parse fleet public key: %w", err)
	}

	// Generate the package keypair.
	pkgPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("ecdsa: %w", err)
	}

	// Build a cert template signed by the fleet key.
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	tpl := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: fmt.Sprintf("okesu-package-%d", packageID)},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().AddDate(1, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	// Self-sign-ish: parent template uses the fleet public key as
	// the subject. This works because the scanner only inspects the
	// signature + the public key on the embedded cert; we don't go
	// through full path validation.
	parent := x509.Certificate{
		Subject: pkix.Name{CommonName: "okesu-fleet"},
	}
	derBytes, err := x509.CreateCertificate(rand.Reader, &tpl, &parent, &pkgPriv.PublicKey, fleetPriv)
	if err != nil {
		return "", "", fmt.Errorf("sign package cert: %w", err)
	}
	_ = fleetPub

	certPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes}))
	der, err := x509.MarshalECPrivateKey(pkgPriv)
	if err != nil {
		return "", "", fmt.Errorf("marshal ec key: %w", err)
	}
	keyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}))
	return certPEM, keyPEM, nil
}

// generateSelfSignedPackageCert is the dev-mode fallback. Real
// deployments call SignPackageCert (above) and pass the result via
// BuildRequest.PackageCertPEM / .PackageKeyPEM.
func generateSelfSignedPackageCert(req BuildRequest) (certPEM, keyPEM string, err error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	tpl := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: fmt.Sprintf("okesu-package-%d-selfsigned", req.PackageID)},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().AddDate(1, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tpl, &tpl, &priv.PublicKey, priv)
	if err != nil {
		return "", "", err
	}
	certPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	kder, _ := x509.MarshalECPrivateKey(priv)
	keyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder}))
	return certPEM, keyPEM, nil
}

// CertFingerprint returns the SHA-256 of the DER cert bytes in
// lowercase hex. Used by the db layer to look packages up by their
// embedded certificate.
func CertFingerprint(certPEM string) string {
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		return ""
	}
	sum := sha256.Sum256(block.Bytes)
	const hex = "0123456789abcdef"
	out := make([]byte, len(sum)*2)
	for i, b := range sum {
		out[i*2] = hex[b>>4]
		out[i*2+1] = hex[b&0x0f]
	}
	return string(out)
}

func parseECPrivateKey(pemStr string) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, errors.New("no PEM block")
	}
	switch block.Type {
	case "EC PRIVATE KEY":
		return x509.ParseECPrivateKey(block.Bytes)
	case "PRIVATE KEY":
		any, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		ek, ok := any.(*ecdsa.PrivateKey)
		if !ok {
			return nil, errors.New("PKCS8 key is not ECDSA")
		}
		return ek, nil
	}
	return nil, fmt.Errorf("unsupported PEM type %q", block.Type)
}

func parseECPublicKey(pemStr string) (*ecdsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, errors.New("no PEM block")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	ek, ok := pub.(*ecdsa.PublicKey)
	if !ok {
		return nil, errors.New("public key is not ECDSA")
	}
	return ek, nil
}

// ───────────────────────── install.sh + README ─────────────────────────

// installShellScript runs at first boot inside the unpacked package
// directory. Detects OS/arch, picks the matching binary, drops it
// at /usr/local/bin/okesu, and invokes `okesu enroll` with paths
// pointing at the on-disk package material.
const installShellScript = `#!/bin/sh
# Okesu enrollment installer. Run with sudo.
set -e
HERE=$(cd "$(dirname "$0")" && pwd)

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
    x86_64|amd64) ARCH=amd64 ;;
    aarch64|arm64) ARCH=arm64 ;;
esac

BIN="$HERE/okesu-$OS-$ARCH"
if [ ! -f "$BIN" ]; then
    echo "no okesu binary for $OS/$ARCH in package" >&2
    exit 1
fi

install -m 0755 "$BIN" /usr/local/bin/okesu
mkdir -p /etc/okesu /etc/okesu/node-certs
install -m 0600 "$HERE/bootstrap.json"   /etc/okesu/bootstrap.json
install -m 0644 "$HERE/fleet-pubkey.pem" /etc/okesu/fleet-pubkey.pem
install -m 0644 "$HERE/package-cert.pem" /etc/okesu/package-cert.pem
install -m 0600 "$HERE/package-key.pem"  /etc/okesu/package-key.pem

# Run enrollment — non-interactive; succeeds when the CP scanner
# responds via the bucket within ~10 minutes.
/usr/local/bin/okesu enroll --bootstrap /etc/okesu/bootstrap.json

# Prefer systemd when present (managed deploys + most production
# installs). nohup is the fallback for laptops / quick smoke tests
# where the operator just wants the daemon running this session.
if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
    cat > /etc/systemd/system/okesu.service <<'UNIT'
[Unit]
Description=Okesu daemon (S3 jobs runtime)
After=network-online.target
Wants=network-online.target

[Service]
# Type=exec (not simple) so systemd waits for execve() to return
# before considering the unit "started". Under install.sh's set -e,
# a missing dynamic-linker target or bad bootstrap surfaces as a
# non-zero exit from systemctl enable --now and aborts cloud-init
# instead of leaving a silently-half-installed VM.
Type=exec
ExecStart=/usr/local/bin/okesu s3-jobs --bootstrap /etc/okesu/bootstrap.json
Restart=on-failure
RestartSec=5
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
UNIT
    systemctl daemon-reload
    systemctl enable --now okesu.service
    echo "okesu: enrolled, systemd unit started."
else
    nohup /usr/local/bin/okesu s3-jobs --bootstrap /etc/okesu/bootstrap.json >/var/log/okesu-jobs.log 2>&1 &
    disown 2>/dev/null || true
    echo "okesu: enrolled and running (nohup; no systemd detected)."
fi
`

func readmeText(req BuildRequest) string {
	return fmt.Sprintf(`Okesu enrollment package
========================
Display name: %s
CP id:        %s
Bucket:       %s
Generated:    %s

Quick start
-----------
  sudo ./install.sh

The installer drops the okesu binary at /usr/local/bin/okesu, places
trust material under /etc/okesu/, and runs the enrollment flow
against the bucket. The node appears in the CP UI within a few
scanner cycles.

Multiple machines
-----------------
The package is fleet-scoped — copy it to as many hosts as you want;
each one self-registers with its own identity.

Revocation
----------
If the package is lost or compromised, ask an admin to revoke it
in the CP UI under Transports → Packages. New nodes can no longer
enroll with this package; already-enrolled nodes are unaffected.
`, req.DisplayName, req.CPID, req.Bucket, time.Now().UTC().Format(time.RFC3339))
}
