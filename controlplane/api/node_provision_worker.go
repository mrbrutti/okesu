// Package api — managed-node provisioner worker.
//
// State machine: queued → starting → cloud_init_running (after we
// hand the rendered user_data to the cloud SDK) → bootstrap_pending
// (after the VM is RUNNING and we're waiting for the new node to
// publish via S3). The s3scanner advances bootstrap_pending → ready
// when it first sees a registration from a node tied to this provision.
//
// The worker runs in a goroutine started from
// NodeProvisionCreateHandler. We don't reconcile in-flight rows on
// CP boot — if the parent crashed mid-launch the operator sees a
// stuck row and can retry or destroy from the UI.
//
// Mirrors cp_provision_worker.go in shape; the node-side worker is
// strictly simpler because there's no separate bundle-token /
// /api/v1/cp/bootstrap callback. The new node registers via the
// transport_config's bucket using the cert+key minted into the
// enrollment_packages row, exactly like an operator-generated
// enrollment package would.

package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/section9labs/okesu/controlplane/adapters/s3blob"
	"github.com/section9labs/okesu/controlplane/cpprovision"
	"github.com/section9labs/okesu/controlplane/db"
	"github.com/section9labs/okesu/controlplane/packaging"
)

// NodeProvisionWorkerConfig is the worker's dependency bag, built
// once at server boot from cfg + filesystem state. The handler
// receives a copy and can leave Store nil to disable goroutine
// kicking under tests.
type NodeProvisionWorkerConfig struct {
	Store    *db.Store
	Registry *cpprovision.Registry

	// BinaryResolver returns the daemon binary bytes for a given
	// "<os>-<arch>" target (e.g. "linux-amd64", "linux-arm64"). The
	// same shape as EnrollmentPackageDownload's binResolver — wired
	// from server.go so the worker's tarball matches the manual S3
	// tab's tarball exactly. nil in tests; the worker fails fast
	// with a clear log line in that case.
	BinaryResolver func(target string) ([]byte, error)

	// PackageBuilder is the seam tests use to swap out
	// packaging.Build for a fast in-memory stub. Production code
	// leaves this nil; the worker falls back to packaging.Build.
	PackageBuilder func(packaging.BuildRequest) (packaging.BuildResult, error)

	// BlobUploader is the seam tests use to swap out
	// s3blob.New + Put + PresignedGetURL with a small fake. Returns
	// the presigned URL the cloud-init script will curl. Production
	// code leaves this nil; the worker falls back to the real S3
	// path.
	BlobUploader func(ctx context.Context, tc db.TransportConfig, key string, body []byte) (presignedURL string, err error)
}

// RunNodeProvisionWorker drives one node-provisions row through its
// state machine. Returns once the row is bootstrap_pending OR failed
// — never both. Idempotent on the failure paths (status=failed +
// error column populated).
func RunNodeProvisionWorker(ctx context.Context, cfg NodeProvisionWorkerConfig, id int64) {
	store := cfg.Store
	logf := func(format string, args ...any) {
		_ = store.AppendNodeProvisionLog(id, fmt.Sprintf(format, args...)+"\n")
	}
	failNow := func(msg string) {
		_ = store.SetNodeProvisionError(id, msg)
		logf("✗ %s", msg)
	}

	row, err := store.NodeProvision(id)
	if err != nil {
		// Row gone — nothing to do.
		return
	}
	logf("→ starting provision #%d (%s, %s)", row.ID, row.Cloud, row.Region)
	_ = store.UpdateNodeProvisionStatus(id, db.NodeProvisionStarting)

	// Sanity: the handler verified all of these but a row written to
	// disk could in principle be tampered with; re-validate.
	if !row.CredentialID.Valid || row.CredentialID.Int64 == 0 {
		failNow("row has no credential_id")
		return
	}
	if row.TransportConfigID == 0 {
		failNow("row has no transport_config_id")
		return
	}

	tc, err := store.GetTransportConfig(row.TransportConfigID)
	if err != nil {
		failNow("transport_config: " + err.Error())
		return
	}
	if !tc.AccessKey.Valid || !tc.SecretKey.Valid {
		failNow("transport_config has no access keys")
		return
	}
	if !tc.FleetPubkeyPEM.Valid || !tc.FleetPrivkeyPEM.Valid {
		failNow("transport_config has no fleet keypair (use POST /api/transport-configs with generate_fleet_keys, or PUT to add)")
		return
	}

	mk, err := store.MasterKeyFromMeta()
	if err != nil {
		failNow("master key: " + err.Error())
		return
	}
	credBytes, err := store.DecryptCloudCredential(row.CredentialID.Int64, mk)
	if err != nil {
		failNow("decrypt credential: " + err.Error())
		return
	}

	// 1. Mint a fresh enrollment_packages row tied to the chosen
	// transport_config — gives us a per-provision cert/key pair the
	// new node will sign its registration with. Inlined here rather
	// than promoted to the store package because the cert+key minting
	// pattern is small and already lives in
	// api/transport_configs.go::EnrollmentPackageCreate.
	pkg, certPEM, keyPEM, err := mintProvisionEnrollmentPackage(store, row, tc)
	if err != nil {
		failNow("mint enrollment_package: " + err.Error())
		return
	}
	logf("✓ minted enrollment_package %d", pkg.ID)

	// 2. Resolve binary blobs.
	if cfg.BinaryResolver == nil {
		failNow("parent has no daemon binary resolver configured (set --daemon-binaries-dir on the CP)")
		return
	}
	bins := map[string][]byte{}
	for _, target := range []string{"linux-amd64", "linux-arm64", "darwin-arm64", "darwin-amd64", "freebsd-amd64"} {
		body, rerr := cfg.BinaryResolver(target)
		if rerr != nil || len(body) == 0 {
			continue
		}
		bins[target] = body
	}
	if len(bins) == 0 {
		failNow("no daemon binaries available — upload via /api/deploy/binaries first")
		return
	}

	// 3. Build the tarball (same shape as EnrollmentPackageDownload).
	build := cfg.PackageBuilder
	if build == nil {
		build = packaging.Build
	}
	res, err := build(packaging.BuildRequest{
		DisplayName:    row.DisplayName,
		PackageID:      pkg.ID,
		Bucket:         tc.Bucket,
		Endpoint:       tc.Endpoint,
		Region:         tc.Region.String,
		UseSSL:         tc.UseSSL,
		AccessKey:      tc.AccessKey.String,
		SecretKey:      tc.SecretKey.String,
		CPID:           tc.CPID.String,
		FleetPubkeyPEM: tc.FleetPubkeyPEM.String,
		PackageCertPEM: certPEM,
		PackageKeyPEM:  keyPEM,
		Binaries:       bins,
		Format:         "tar.gz",
	})
	if err != nil {
		failNow("build package: " + err.Error())
		return
	}
	logf("✓ built package (%d bytes)", len(res.Bytes))

	// 4. Upload to bucket so cloud-init can curl via presigned URL.
	bundleKey := fmt.Sprintf("provisions/nodes/%d/package.tar.gz", id)
	uploader := cfg.BlobUploader
	if uploader == nil {
		uploader = defaultBlobUploader
	}
	presignedURL, err := uploader(ctx, tc, bundleKey, res.Bytes)
	if err != nil {
		failNow("upload package: " + err.Error())
		return
	}
	logf("✓ package uploaded to s3://%s/%s; presigned URL valid 1h", tc.Bucket, bundleKey)

	// 5. Render cloud-init.
	cloudInit := nodeManagedCloudInit(presignedURL, res.Filename)

	// 6. Decode cloud_params and Launch.
	var params map[string]any
	if row.CloudParamsJSON != "" {
		if err := json.Unmarshal([]byte(row.CloudParamsJSON), &params); err != nil {
			failNow("cloud_params decode: " + err.Error())
			return
		}
	}
	prov, err := cfg.Registry.Get(row.Cloud)
	if err != nil {
		failNow("provisioner not registered: " + row.Cloud)
		return
	}
	_ = store.UpdateNodeProvisionStatus(id, db.NodeProvisionCloudInitRunning)
	logf("→ calling Provisioner.Launch")

	launchRes, err := prov.Launch(ctx, cpprovision.LaunchRequest{
		DisplayName:       row.DisplayName,
		Region:            row.Region,
		CredentialPayload: credBytes,
		CloudParams:       params,
		CloudInitScript:   cloudInit,
	}, nodeProvisionLogger{provisionID: id, store: store})
	if err != nil {
		failNow("Launch: " + err.Error())
		return
	}
	logf("✓ instance running at %s", launchRes.PublicIP)
	_ = store.SetNodeProvisionCloudResource(id, launchRes.ResourceID, launchRes.ConsoleURL)
	if launchRes.ConsoleURL != "" {
		logf("  console: %s", launchRes.ConsoleURL)
	}
	_ = store.UpdateNodeProvisionStatus(id, db.NodeProvisionBootstrapPending)
	logf("→ awaiting node enrollment via S3")
}

// nodeProvisionLogger satisfies cpprovision.Logger by appending
// progress lines into the node_provisions.log column.
type nodeProvisionLogger struct {
	provisionID int64
	store       *db.Store
}

func (l nodeProvisionLogger) Logf(format string, args ...any) {
	_ = l.store.AppendNodeProvisionLog(l.provisionID, "  "+fmt.Sprintf(format, args...)+"\n")
}

// nodeManagedCloudInit returns the bash that runs as the OCI/AWS
// VM's user_data on first boot. It installs curl + tar from the
// distro repo (apt OR dnf, mirroring the CP cloud-init), curls the
// presigned package URL, untars, and runs install.sh — which in
// turn drops the binary at /usr/local/bin/okesu and writes a
// systemd unit when systemd is present (see
// packaging.installShellScript).
func nodeManagedCloudInit(bundleURL, filename string) string {
	// The bundleURL is an S3 SigV4 presigned GET — query params include
	// '&', '=', and '/' in the path. We use single-quote escaping
	// (shellSingleQuote) rather than Go's %q because Go's quoting
	// preserves $, backtick, and !, which bash double-quotes still
	// expand. Single quotes disable all shell metacharacter handling
	// inside, so any future input that leaks a $-shape can't escape
	// to RCE.
	//
	// The package-manager fall-through must explicitly fail on hosts
	// without dnf/apt-get; otherwise `set -euo pipefail` lets the
	// conditional exit 0 and we hit a confusing "curl: not found"
	// later. Mirrors cpprovision/cloudinit.go's pattern.
	return fmt.Sprintf(`#!/usr/bin/env bash
set -euo pipefail
exec > >(tee -a /var/log/okesu-bootstrap.log) 2>&1
echo "==> okesu node managed bootstrap"

if command -v dnf >/dev/null 2>&1; then
    dnf install -y curl tar
elif command -v apt-get >/dev/null 2>&1; then
    export DEBIAN_FRONTEND=noninteractive
    apt-get update -y
    apt-get install -y curl tar ca-certificates
else
    echo "no apt-get or dnf — cannot install curl/tar. Use a Debian or RHEL family image." >&2
    exit 1
fi

mkdir -p /opt/okesu-node
cd /opt/okesu-node
BUNDLE_URL=%s
BUNDLE_FILE=%s
curl -fSL --retry 6 --retry-delay 5 -o "$BUNDLE_FILE" "$BUNDLE_URL"
tar -xzf "$BUNDLE_FILE"
# The packaging tar.gz lays files at the root; if a future formatter
# changes that, descend into the single top-level dir.
if [ -f ./install.sh ]; then
    sudo ./install.sh
else
    cd "$(find . -mindepth 1 -maxdepth 1 -type d | head -1)"
    sudo ./install.sh
fi
echo "==> okesu node managed bootstrap done"
`, shellSingleQuote(bundleURL), shellSingleQuote(filename))
}

// shellSingleQuote wraps a string in shell single-quotes, with any
// embedded single-quote escaped as '\''. This disables ALL shell
// metacharacter expansion inside the quoted string — unlike double
// quotes (which still expand $, backtick, and !) or Go's fmt.%q
// (which only handles double-quote-relevant Go escapes).
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// defaultBlobUploader connects to the configured S3 endpoint, puts
// the bundle bytes at key, and returns a 1-hour presigned GET URL.
// Mirrors the s3_dead_drop branch of cp_provision_worker.
func defaultBlobUploader(ctx context.Context, tc db.TransportConfig, key string, body []byte) (string, error) {
	blob, err := s3blob.New(ctx, s3blob.Config{
		Endpoint:  tc.Endpoint,
		Region:    tc.Region.String,
		Bucket:    tc.Bucket,
		AccessKey: tc.AccessKey.String,
		SecretKey: tc.SecretKey.String,
		UseSSL:    tc.UseSSL,
	})
	if err != nil {
		return "", err
	}
	if err := blob.Put(ctx, key, bytes.NewReader(body), "application/gzip"); err != nil {
		return "", err
	}
	return blob.PresignedGetURL(ctx, key, time.Hour)
}

// mintProvisionEnrollmentPackage persists a fresh enrollment_packages
// row + its signing material so the new node can authenticate at
// registration. Replicates the inline minting flow in
// api/transport_configs.go::EnrollmentPackageCreate so the row this
// produces is indistinguishable from one an operator generated via
// the manual S3 tab.
func mintProvisionEnrollmentPackage(store *db.Store, row *db.NodeProvision, tc db.TransportConfig) (*db.EnrollmentPackage, string, string, error) {
	if !tc.FleetPubkeyPEM.Valid || tc.FleetPubkeyPEM.String == "" ||
		!tc.FleetPrivkeyPEM.Valid || tc.FleetPrivkeyPEM.String == "" {
		return nil, "", "", fmt.Errorf("transport_config has no fleet keypair")
	}

	displayName := strings.TrimSpace(row.DisplayName)
	if displayName == "" {
		displayName = fmt.Sprintf("provision-%d", row.ID)
	} else {
		displayName = fmt.Sprintf("%s (provision %d)", displayName, row.ID)
	}

	pkgRow := db.EnrollmentPackage{
		DisplayName:       displayName,
		TransportConfigID: tc.ID,
		CPID:              tc.CPID.String,
	}
	if row.CreatedByUserID.Valid {
		pkgRow.CreatedBy = sql.NullInt64{Int64: row.CreatedByUserID.Int64, Valid: true}
	}

	// Sign cert + key against the fleet keypair (same call
	// EnrollmentPackageCreate makes), persist them, return the
	// row metadata for the worker to embed in the build.
	certPEM, keyPEM, err := packaging.SignPackageCert(tc.FleetPubkeyPEM.String, tc.FleetPrivkeyPEM.String, 0)
	if err != nil {
		return nil, "", "", fmt.Errorf("sign package cert: %w", err)
	}
	pkgRow.PackageCertPEM = certPEM
	pkgRow.PackageKeyPEM = keyPEM

	id, err := store.CreateEnrollmentPackage(pkgRow)
	if err != nil {
		return nil, "", "", err
	}
	pkgRow.ID = id
	return &pkgRow, certPEM, keyPEM, nil
}
