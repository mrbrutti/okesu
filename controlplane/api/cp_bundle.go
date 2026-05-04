// CP bootstrap bundle generator.
//
// A "CP bundle" is a tar.gz an operator drops on a fresh host to
// spin up a new child CP that auto-registers with this parent. Three
// emission formats sharing the same envelope of files:
//
//   1. dockerfile-tarball — Dockerfile (multistage) + the okesu-cp
//      linux binary. Operator's host builds the image locally.
//      Works in air-gapped + dev setups; slowest first-boot.
//
//   2. compose-tarball — docker-compose.yml + the parent's running
//      okesu-cp Docker image as okesu-cp-image.tar (loaded with
//      `docker load -i`). Faster first-boot when the parent is
//      already running in a container; the operator never builds.
//
//   3. terraform — a self-contained Terraform module that wraps the
//      same effect (binary upload + systemd unit + env). For ops
//      teams that want IaC. Lands in Phase 21.4.
//
// All three carry a one-time bootstrap token, the parent's URL, and
// the env knobs the new CP needs to auto-register on first boot.
// The token is bcrypt-hashed in the parent's DB; on the wire (inside
// the .env) it's plaintext — the bundle is sensitive material the
// operator must transport carefully.

package api

import (
	"archive/tar"
	"compress/gzip"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/section9labs/okesu/controlplane/audit"
	"github.com/section9labs/okesu/controlplane/auth"
	"github.com/section9labs/okesu/controlplane/db"
)

// BundleFormat enumerates the supported emission shapes. The frontend
// modal exposes one radio per value; the bundle handler dispatches on
// the request body's format field.
type BundleFormat string

const (
	BundleFormatDockerfile  BundleFormat = "dockerfile-tarball"
	BundleFormatCompose     BundleFormat = "compose-tarball"
	BundleFormatTerraform   BundleFormat = "terraform"
	BundleFormatS3DeadDrop  BundleFormat = "s3-dead-drop"
)

// CPBundleConfig is what the parent CP needs to bake into a bundle
// beyond what the operator supplies. Built once at server boot from
// the config + filesystem state — the handler doesn't recompute on
// every request.
type CPBundleConfig struct {
	// ParentMgmtURL is what the child posts its bootstrap call to —
	// reachable from wherever the operator runs the bundle. Defaults
	// to cfg.MgmtPublicURL; operators behind weird NAT can override
	// per-bundle in the request body.
	ParentMgmtURL string

	// LinuxBinaryPath is the single-arch path to the okesu-cp linux
	// binary the dockerfile bundle ships. Empty disables dockerfile
	// mode and returns a 503-style error so the modal can surface it.
	// Used as a fallback when LinuxBinaryAmd64Path /
	// LinuxBinaryArm64Path are both empty.
	LinuxBinaryPath string

	// LinuxBinaryAmd64Path / LinuxBinaryArm64Path enable a multi-arch
	// bundle: when both are non-empty, writeS3DeadDropBundle (and the
	// dockerfile bundle path) ship both binaries plus a Dockerfile
	// that copies the right one via ARG TARGETARCH at build time on
	// the cloud VM. This is what lets an arm64 mac operator deploy
	// to amd64 OCI/EC2 shapes without rebuilding manually.
	LinuxBinaryAmd64Path string
	LinuxBinaryArm64Path string

	// LinuxImageTarPath is the path to a `docker save`-format tarball
	// of the okesu-cp image. Empty disables compose mode (operator
	// must use dockerfile or terraform).
	LinuxImageTarPath string

	// Version is baked into the bundle's README so ops know which CP
	// build their child is going to run. Empty falls back to "dev".
	Version string
}

// cpBundleReq is the JSON body shape for POST /api/federation/cp-bundle.
type cpBundleReq struct {
	DisplayName string       `json:"display_name"`
	Region      string       `json:"region"`
	Format      BundleFormat `json:"format"`
	// Cloud is required for Format == terraform (the rendered module
	// is provider-specific). Ignored for the Docker formats.
	Cloud       string `json:"cloud,omitempty"`
	ParentURL   string `json:"parent_url,omitempty"`    // override for the embedded bootstrap target
	ChildHost   string `json:"child_host,omitempty"`    // optional — operator can pre-set the child's hostname for the README
	ChildPort   int    `json:"child_port,omitempty"`    // optional — defaults to 8443
	MgmtPort    int    `json:"mgmt_port,omitempty"`     // optional — defaults to 8444
	WithAPIKeys bool   `json:"with_api_keys,omitempty"` // include parent's Fleet API keys in the .env (off by default)
	// TransportConfigID is required for Format == s3-dead-drop. The
	// parent reads this transport_config to get the bucket coords
	// it'll bake into the .env (so the child can publish), AND uses
	// the same row_id when registering the federation_peer (so its
	// s3reader uses the same bucket creds to read).
	TransportConfigID int64 `json:"transport_config_id,omitempty"`
}

// CPBundleHandler issues a bootstrap token, generates a tar.gz with
// the requested format, and streams it back. Admin-only — the token
// is sensitive enough that adding it to a viewer's surface would be
// a privilege bump.
//
// `cache` and `parentBaseURL` are only used for Format == terraform:
// the rendered module's cloud-init script fetches the actual Docker
// bundle from /api/federation/cp-bundle/download (same path the
// managed-deploy worker uses). They can be nil/empty for deployments
// that disable the Terraform format.
func CPBundleHandler(store *db.Store, cfg CPBundleConfig, cache *BundleCache, parentBaseURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req cpBundleReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		req.DisplayName = strings.TrimSpace(req.DisplayName)
		req.Region = strings.TrimSpace(req.Region)
		if req.DisplayName == "" || req.Region == "" {
			http.Error(w, "display_name and region are required", http.StatusBadRequest)
			return
		}
		if req.Format == "" {
			req.Format = BundleFormatDockerfile
		}
		if req.ChildPort == 0 {
			req.ChildPort = 8443
		}
		if req.MgmtPort == 0 {
			req.MgmtPort = 8444
		}

		parentURL := strings.TrimSpace(req.ParentURL)
		if parentURL == "" {
			parentURL = cfg.ParentMgmtURL
		}
		if parentURL == "" {
			http.Error(w, "parent CP has no public mgmt URL configured. Set --mgmt-public-url or pass parent_url in the request.", http.StatusBadRequest)
			return
		}

		// Format-specific feasibility guards. Surface a precise error
		// the frontend can show inline rather than a generic 500.
		switch req.Format {
		case BundleFormatDockerfile:
			if cfg.LinuxBinaryPath == "" {
				http.Error(w, "dockerfile bundle: parent has no linux daemon binary configured (--daemon-binary)", http.StatusServiceUnavailable)
				return
			}
		case BundleFormatCompose:
			if cfg.LinuxImageTarPath == "" {
				http.Error(w, "compose bundle: parent has no okesu-cp image tarball available. Use dockerfile-tarball, or run the parent in Docker with the image tag exposed.", http.StatusServiceUnavailable)
				return
			}
		case BundleFormatTerraform:
			req.Cloud = strings.ToLower(strings.TrimSpace(req.Cloud))
			if !isSupportedTerraformCloud(req.Cloud) {
				http.Error(w, "terraform bundle: cloud must be one of: oci, aws", http.StatusBadRequest)
				return
			}
			if cache == nil || strings.TrimSpace(parentBaseURL) == "" {
				http.Error(w, "terraform bundle: parent CP missing public URL or bundle cache (server config error)", http.StatusServiceUnavailable)
				return
			}
			if cfg.LinuxBinaryPath == "" && cfg.LinuxImageTarPath == "" {
				http.Error(w, "terraform bundle: parent has no daemon binary or image tarball — the rendered cloud-init has nothing to fetch. Configure --daemon-binary or run the parent in Docker.", http.StatusServiceUnavailable)
				return
			}
		case BundleFormatS3DeadDrop:
			if cfg.LinuxBinaryPath == "" {
				http.Error(w, "s3-dead-drop bundle: parent has no linux daemon binary configured (--daemon-binary)", http.StatusServiceUnavailable)
				return
			}
			if req.TransportConfigID == 0 {
				http.Error(w, "s3-dead-drop bundle: transport_config_id is required (the bucket the child publishes to + the parent reads from)", http.StatusBadRequest)
				return
			}
		default:
			http.Error(w, fmt.Sprintf("unsupported format %q", req.Format), http.StatusBadRequest)
			return
		}

		// Mint the bootstrap token. This is the only place the
		// plaintext is in memory — it goes straight into the tarball
		// and we never log it.
		var userID int64
		var userEmail string
		if u := auth.UserFromContext(r.Context()); u != nil {
			userID = u.ID
			userEmail = u.Email
		}
		plaintext, tokenID, err := store.IssueCPBootstrapToken(req.DisplayName, req.Region, parentURL, userID, userEmail)
		if err != nil {
			http.Error(w, "issue token: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Random freshman secrets for the new CP. The admin password
		// + webhook secret + session HMAC seed are all baked into the
		// bundle's .env so the operator doesn't have to make them up.
		adminPassword, _ := randomHex(16)
		webhookSecret, _ := randomHex(32)
		sessionKey, _ := randomHex(32)

		bundle := bundleVars{
			DisplayName:    req.DisplayName,
			Region:         req.Region,
			ParentURL:      parentURL,
			BootstrapToken: plaintext,
			AdminPassword:  adminPassword,
			WebhookSecret:  webhookSecret,
			SessionKey:     sessionKey,
			ChildHost:      req.ChildHost,
			ChildPort:      req.ChildPort,
			MgmtPort:       req.MgmtPort,
			Version:        nonEmpty(cfg.Version, "dev"),
			IssuedAt:       time.Now().UTC().Format(time.RFC3339),
		}

		audit.Emit(r, store, db.AuditEntry{
			Action: "federation.cp_bundle.issue",
			Target: fmt.Sprintf("cp_bootstrap_token:%d", tokenID),
			Metadata: map[string]any{
				"display_name": req.DisplayName,
				"region":       req.Region,
				"format":       string(req.Format),
				"parent_url":   parentURL,
			},
		})

		filename := fmt.Sprintf("okesu-cp-%s-%s.tar.gz",
			slugify(req.DisplayName),
			time.Now().UTC().Format("20060102-150405"))
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)

		switch req.Format {
		case BundleFormatDockerfile:
			err = writeDockerfileBundle(w, bundle, cfg.LinuxBinaryPath)
		case BundleFormatCompose:
			err = writeComposeBundle(w, bundle, cfg.LinuxImageTarPath)
		case BundleFormatTerraform:
			err = writeTerraformBundle(w, bundle, req.Cloud, cfg, cache, parentBaseURL, tokenID)
		case BundleFormatS3DeadDrop:
			// Direct-download path: the operator is downloading the
			// tar.gz themselves and will drop it onto a VM they manage.
			// No need to also upload to the bucket — that's only
			// required for the managed-deploy worker (cp_provision).
			_, err = writeS3DeadDropBundle(w, store, bundle, req.TransportConfigID, cfg)
		}
		if err != nil {
			// Tarball stream may have started — best we can do is log.
			_ = err
		}
	}
}

func isSupportedTerraformCloud(c string) bool {
	switch c {
	case "oci", "aws":
		return true
	}
	return false
}

// bundleVars carries everything the bundle's templates need. Filled
// once per request before any template is rendered.
type bundleVars struct {
	DisplayName    string
	Region         string
	ParentURL      string
	BootstrapToken string
	AdminPassword  string
	WebhookSecret  string
	SessionKey     string
	ChildHost      string
	ChildPort      int
	MgmtPort       int
	Version        string
	IssuedAt       string
}

// writeDockerfileBundle emits a tar.gz with the linux binary, a
// multistage Dockerfile that copies it in, a docker-compose.yml that
// builds + runs it, the .env, and a README. Operator runs:
//
//	tar -xzf okesu-cp-*.tar.gz && cd okesu-cp-* && docker compose up -d
func writeDockerfileBundle(w io.Writer, b bundleVars, binaryPath string) error {
	binaryBytes, err := os.ReadFile(binaryPath)
	if err != nil {
		return fmt.Errorf("read parent binary: %w", err)
	}
	gz := gzip.NewWriter(w)
	defer gz.Close()
	t := tar.NewWriter(gz)
	defer t.Close()

	root := bundleRootDir(b)
	files := []bundleFile{
		{name: "Dockerfile", mode: 0o644, content: dockerfileTemplate(b)},
		{name: "docker-compose.yml", mode: 0o644, content: composeTemplateForDockerfile(b)},
		{name: ".env", mode: 0o600, content: envTemplate(b)},
		{name: "README.md", mode: 0o644, content: readmeTemplate(b, BundleFormatDockerfile)},
	}
	for _, f := range files {
		if err := writeTarFile(t, root+"/"+f.name, []byte(f.content), f.mode); err != nil {
			return err
		}
	}
	if err := writeTarFile(t, root+"/okesu-cp-binary", binaryBytes, 0o755); err != nil {
		return err
	}
	return nil
}

// writeComposeBundle emits a tar.gz with the parent's saved
// okesu-cp image, a docker-compose.yml that loads + runs it, the
// .env, and a README. Operator runs:
//
//	tar -xzf okesu-cp-*.tar.gz && cd okesu-cp-* && docker load -i okesu-cp-image.tar && docker compose up -d
//
// (The compose file's `image:` line points at the same tag that
// `docker load` produces, so up -d picks it up without re-pulling.)
func writeComposeBundle(w io.Writer, b bundleVars, imageTarPath string) error {
	imageBytes, err := os.ReadFile(imageTarPath)
	if err != nil {
		return fmt.Errorf("read image tarball: %w", err)
	}
	gz := gzip.NewWriter(w)
	defer gz.Close()
	t := tar.NewWriter(gz)
	defer t.Close()

	root := bundleRootDir(b)
	files := []bundleFile{
		{name: "docker-compose.yml", mode: 0o644, content: composeTemplateForImageLoad(b)},
		{name: ".env", mode: 0o600, content: envTemplate(b)},
		{name: "README.md", mode: 0o644, content: readmeTemplate(b, BundleFormatCompose)},
	}
	for _, f := range files {
		if err := writeTarFile(t, root+"/"+f.name, []byte(f.content), f.mode); err != nil {
			return err
		}
	}
	if err := writeTarFile(t, root+"/okesu-cp-image.tar", imageBytes, 0o644); err != nil {
		return err
	}
	return nil
}

type bundleFile struct {
	name    string
	mode    int64
	content string
}

func writeTarFile(t *tar.Writer, name string, content []byte, mode int64) error {
	hdr := &tar.Header{
		Name:    name,
		Mode:    mode,
		Size:    int64(len(content)),
		ModTime: time.Now().UTC(),
	}
	if err := t.WriteHeader(hdr); err != nil {
		return err
	}
	if _, err := t.Write(content); err != nil {
		return err
	}
	return nil
}

func bundleRootDir(b bundleVars) string {
	return "okesu-cp-" + slugify(b.DisplayName)
}

// ── templates ──────────────────────────────────────────────────────

func envTemplate(b bundleVars) string {
	var sb strings.Builder
	sb.WriteString("# okesu-cp child bootstrap env. Generated " + b.IssuedAt + ".\n")
	sb.WriteString("# DO NOT COMMIT — this file holds the bootstrap token + admin password.\n\n")
	sb.WriteString("OKESU_CP_DISPLAY_NAME=" + b.DisplayName + "\n")
	sb.WriteString("OKESU_CP_REGION=" + b.Region + "\n")
	sb.WriteString("OKESU_CP_BOOTSTRAP_TOKEN=" + b.BootstrapToken + "\n")
	sb.WriteString("OKESU_CP_PARENT_URL=" + b.ParentURL + "\n\n")
	sb.WriteString("# Generated secrets — change before pushing this CP into production.\n")
	sb.WriteString("OKESU_CP_ADMIN_PASSWORD=" + b.AdminPassword + "\n")
	sb.WriteString("OKESU_WEBHOOK_SECRET=" + b.WebhookSecret + "\n")
	sb.WriteString("OKESU_CP_SESSION_KEY=" + b.SessionKey + "\n\n")
	sb.WriteString("# UI + management plane ports inside the container — map to host below.\n")
	sb.WriteString(fmt.Sprintf("OKESU_CP_LISTEN=:%d\n", b.ChildPort))
	sb.WriteString(fmt.Sprintf("OKESU_CP_MGMT_LISTEN=:%d\n", b.MgmtPort))
	if b.ChildHost != "" {
		sb.WriteString(fmt.Sprintf("OKESU_CP_PUBLIC_HOST=%s\n", b.ChildHost))
	}
	return sb.String()
}

func dockerfileTemplate(b bundleVars) string {
	_ = b
	return `# Multistage build — base image stays small, our binary lands in /usr/local/bin.
FROM debian:bookworm-slim AS runtime
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*

COPY okesu-cp-binary /usr/local/bin/okesu-cp
RUN chmod +x /usr/local/bin/okesu-cp

# State volumes the child needs. Compose maps these to host paths so
# the DB + certs survive container restarts.
RUN mkdir -p /var/lib/okesu /etc/okesu /var/log/okesu

EXPOSE 8443 8444

ENTRYPOINT ["/usr/local/bin/okesu-cp"]
CMD ["serve"]
`
}

func composeTemplateForDockerfile(b bundleVars) string {
	return fmt.Sprintf(`# Generated by parent CP %s on %s.
# Spins up a child CP that auto-registers with the parent on first boot.
services:
  okesu-cp:
    build: .
    image: okesu-cp:bootstrap-%s
    container_name: okesu-cp-%s
    restart: unless-stopped
    env_file: .env
    ports:
      - "%d:%d"
      - "%d:%d"
    volumes:
      - ./data/db:/var/lib/okesu
      - ./data/etc:/etc/okesu
      - ./data/log:/var/log/okesu
    command:
      - "serve"
      - "--db=/var/lib/okesu/cp.db"
      - "--listen=:%d"
      - "--mgmt-listen=:%d"
`,
		b.ParentURL, b.IssuedAt,
		slugify(b.DisplayName), slugify(b.DisplayName),
		b.ChildPort, b.ChildPort,
		b.MgmtPort, b.MgmtPort,
		b.ChildPort, b.MgmtPort,
	)
}

func composeTemplateForImageLoad(b bundleVars) string {
	return fmt.Sprintf(`# Generated by parent CP %s on %s.
# Run `+"`docker load -i okesu-cp-image.tar`"+` once before `+"`docker compose up -d`"+` so
# the local daemon registers the image tag this compose references.
services:
  okesu-cp:
    image: okesu-cp:bootstrap
    container_name: okesu-cp-%s
    restart: unless-stopped
    env_file: .env
    ports:
      - "%d:%d"
      - "%d:%d"
    volumes:
      - ./data/db:/var/lib/okesu
      - ./data/etc:/etc/okesu
      - ./data/log:/var/log/okesu
    command:
      - "serve"
      - "--db=/var/lib/okesu/cp.db"
      - "--listen=:%d"
      - "--mgmt-listen=:%d"
`,
		b.ParentURL, b.IssuedAt,
		slugify(b.DisplayName),
		b.ChildPort, b.ChildPort,
		b.MgmtPort, b.MgmtPort,
		b.ChildPort, b.MgmtPort,
	)
}

func readmeTemplate(b bundleVars, fmt_ BundleFormat) string {
	var sb strings.Builder
	sb.WriteString("# okesu-cp child bundle — `" + b.DisplayName + "`\n\n")
	sb.WriteString("Generated by parent at `" + b.ParentURL + "` on " + b.IssuedAt + ".\n\n")
	sb.WriteString("## What this is\n\n")
	sb.WriteString("A self-contained Docker bundle that boots a new child Control Plane,\n")
	sb.WriteString("auto-registers with the parent on first start, and joins the federation\n")
	sb.WriteString("as `" + b.DisplayName + "` in region `" + b.Region + "`.\n\n")
	sb.WriteString("## Run\n\n")
	sb.WriteString("```sh\n")
	sb.WriteString("tar -xzf okesu-cp-*.tar.gz\n")
	sb.WriteString("cd " + bundleRootDir(b) + "\n")
	if fmt_ == BundleFormatCompose {
		sb.WriteString("docker load -i okesu-cp-image.tar      # one-time, registers the image\n")
	}
	sb.WriteString("docker compose up -d\n")
	sb.WriteString("```\n\n")
	sb.WriteString("On first boot the CP will:\n\n")
	sb.WriteString("1. Apply migrations to a fresh SQLite DB at `./data/db/cp.db`.\n")
	sb.WriteString("2. POST `OKESU_CP_BOOTSTRAP_TOKEN` to the parent's `/api/v1/cp/bootstrap`.\n")
	sb.WriteString("3. Receive a long-lived rotating federation token in response and store it.\n")
	sb.WriteString("4. Mark the bootstrap token used at the parent — it cannot be reused.\n")
	sb.WriteString("5. Begin serving the UI at `:" + fmt.Sprintf("%d", b.ChildPort) + "` and mgmt plane at `:" + fmt.Sprintf("%d", b.MgmtPort) + "`.\n\n")
	sb.WriteString("## Login\n\n")
	sb.WriteString("- URL:      `https://<your-host>:" + fmt.Sprintf("%d", b.ChildPort) + "/`\n")
	sb.WriteString("- Email:    `admin@local`\n")
	sb.WriteString("- Password: `" + b.AdminPassword + "` (from `.env`)\n\n")
	sb.WriteString("Change the admin password after the first login — `.env` is sensitive\n")
	sb.WriteString("and should not survive past the bootstrap.\n\n")
	sb.WriteString("## Security notes\n\n")
	sb.WriteString("- The bootstrap token in `.env` is **one-time use**, expires in 24h.\n")
	sb.WriteString("- Do not commit `.env` — it carries the bootstrap token + admin password.\n")
	sb.WriteString("- Once the CP has registered, you can safely delete `.env` (the env\n")
	sb.WriteString("  vars are loaded into the container at start; restart will require\n")
	sb.WriteString("  recreating it from your secrets store).\n")
	return sb.String()
}

// ── helpers ────────────────────────────────────────────────────────

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// slugify normalises a display name into something safe for filenames
// + container names. ASCII-only, lowercase, dashes for separators.
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var sb strings.Builder
	last := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'):
			sb.WriteByte(c)
			last = c
		default:
			if last != '-' && sb.Len() > 0 {
				sb.WriteByte('-')
				last = '-'
			}
		}
	}
	out := strings.Trim(sb.String(), "-")
	if out == "" {
		out = "child-cp"
	}
	return out
}

func nonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// Defensive: keep filepath imported for future use (terraform format
// will path-join its module files).
var _ = filepath.Join
