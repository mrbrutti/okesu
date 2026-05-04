// Cloud-init script rendering for managed CP deploys.
//
// Every cloud's Launch() call passes the rendered script as user_data
// (OCI metadata.user_data, AWS UserData, GCP startup-script, etc.).
// The script:
//
//   1. Installs Docker if missing (apt-get install on Debian-family,
//      dnf on RHEL-family — both are common base AMIs in clouds).
//   2. curl-downloads the parent CP's bundle from a one-time URL,
//      authenticated by a download token the parent minted alongside
//      the bootstrap token.
//   3. Untars + `docker compose up -d`.
//   4. The okesu-cp inside the container detects the bootstrap token
//      env, calls /api/v1/cp/bootstrap on the parent, and joins the
//      federation. The parent's worker observes peer_id getting
//      stamped on the cp_provisions row and flips status → ready.
//
// The script is opinionated: it assumes systemd + apt-or-dnf on the
// AMI/image. Operators on Alpine / arch / source-based distros will
// need to adapt by picking a different image OCID / AMI id.

package cpprovision

import (
	"bytes"
	"text/template"
)

// CloudInitVars are the parameters the rendered cloud-init script
// inlines. Bundle URL + token are the moving parts; the rest is
// metadata for the on-host log so an operator SSH'ing in can tell
// which provision the VM belongs to.
type CloudInitVars struct {
	DisplayName    string
	Region         string
	BundleURL      string // URL the new instance fetches the tar.gz from
	BundleToken    string // Bearer token authenticating that fetch (one-time)
	BundleFilename string // suggested local filename, e.g. okesu-cp-foo.tar.gz
	ProvisionID    int64  // cp_provisions.id, for grep-ability in logs
}

// RenderCloudInit returns the bash script to drop in user_data.
// Output is plain text (no MIME multipart), matching every cloud's
// "execute as bash" default for cloud-init.
func RenderCloudInit(v CloudInitVars) (string, error) {
	var buf bytes.Buffer
	if err := cloudInitTemplate.Execute(&buf, v); err != nil {
		return "", err
	}
	return buf.String(), nil
}

var cloudInitTemplate = template.Must(template.New("cloud-init").Parse(cloudInitScript))

// The bash deliberately avoids any non-coreutils dependencies until
// it's installed Docker — `apt-get update` first, then everything
// else. Errors abort with a non-zero exit so cloud-init reports
// failure to the operator's SSH-in debug session.
const cloudInitScript = `#!/usr/bin/env bash
set -euo pipefail
exec > >(tee -a /var/log/okesu-bootstrap.log) 2>&1

echo "==> okesu-cp child bootstrap (provision #{{.ProvisionID}}, {{.DisplayName}}, {{.Region}})"
echo "==> $(date -u +%FT%TZ)"

# ── 1. Install Docker (apt or dnf) ──────────────────────────────────
if ! command -v docker >/dev/null 2>&1; then
    if command -v apt-get >/dev/null 2>&1; then
        export DEBIAN_FRONTEND=noninteractive
        apt-get update -y
        apt-get install -y ca-certificates curl gnupg lsb-release
        install -m 0755 -d /etc/apt/keyrings
        curl -fsSL https://download.docker.com/linux/debian/gpg \
            | gpg --dearmor -o /etc/apt/keyrings/docker.gpg
        chmod a+r /etc/apt/keyrings/docker.gpg
        codename="$(lsb_release -cs)"
        # Docker's debian repo doesn't carry every Ubuntu codename verbatim
        # — fall back on the most recent LTS when needed.
        echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/debian ${codename} stable" \
            > /etc/apt/sources.list.d/docker.list
        apt-get update -y
        apt-get install -y docker-ce docker-ce-cli containerd.io docker-compose-plugin
    elif command -v dnf >/dev/null 2>&1; then
        dnf install -y dnf-plugins-core
        dnf config-manager --add-repo https://download.docker.com/linux/centos/docker-ce.repo
        dnf install -y docker-ce docker-ce-cli containerd.io docker-compose-plugin
    else
        echo "no apt-get or dnf — cannot install Docker. Use a Debian or RHEL family image." >&2
        exit 1
    fi
    systemctl enable --now docker
fi

# ── 2. Download + extract the bundle ────────────────────────────────
WORKDIR=/opt/okesu-cp
mkdir -p "$WORKDIR"
cd "$WORKDIR"
echo "==> fetching bundle from {{.BundleURL}}"
curl -fSL --retry 6 --retry-delay 5 \
     {{if .BundleToken}}-H "Authorization: Bearer {{.BundleToken}}" \
     {{end}}-o "{{.BundleFilename}}" \
     "{{.BundleURL}}"
tar -xzf "{{.BundleFilename}}"
# Bundle root dir is the only directory the tar contains — descend.
cd "$(find . -mindepth 1 -maxdepth 1 -type d | head -n 1)"

# ── 3. Run ──────────────────────────────────────────────────────────
echo "==> docker compose up -d"
docker compose up -d
echo "==> bootstrap script done. The CP container should now POST to its parent's /api/v1/cp/bootstrap."
`
