// CP enrollment bundle for the S3 dead-drop federation transport
// (Phase A.1).
//
// What's in the package the operator drops on a fresh VM:
//
//   okesu-cp-binary    — Linux build of okesu-cp (same one the
//                         dockerfile bundle ships)
//   Dockerfile         — multistage build that copies the binary
//   docker-compose.yml — runs okesu-cp with all the
//                         --federation-s3-publish-* + --cp-instance-id
//                         flags set so the publisher activates on
//                         first boot
//   .env               — the values for those flags + admin password
//                         + webhook secret + session HMAC key
//   README.md          — operator-facing instructions
//
// Pre-flight on the parent side, BEFORE the bundle leaves:
//
//   1. Mint a child instance UUID. The bundle bakes it into
//      OKESU_CP_INSTANCE_ID; on first boot the child's cp_meta
//      adopts that uuid via SeedCPInstanceID.
//   2. Compute the bucket prefix:
//        cp/<child-uuid>/outbound/<parent-uuid>/
//   3. Insert a federation_peers row with transport='s3_dead_drop'
//      pointing at that prefix + the supplied transport_config_id.
//      The parent's s3reader picks up the new row on its next tick
//      (within 30s) and starts polling the prefix. The child's first
//      publish is on its 0th tick (immediately after boot), so the
//      first introspect lands seconds after `docker compose up -d`.
//
// No bootstrap-token roundtrip — federation peers writing via S3
// don't need to "register" to the parent; the bucket prefix IS
// their registration. Auth is the bucket creds (the operator scoped
// the access key + secret to this bucket when they created the
// transport_config).

package api

import (
	"archive/tar"
	"compress/gzip"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/section9labs/okesu/controlplane/db"
)

// writeS3DeadDropBundle emits a tar.gz with the linux binary +
// Dockerfile/compose/env wired for S3-dead-drop publishing. ALSO
// pre-registers the federation peer on the parent so the operator
// doesn't have to do a second curl after the bundle deploys.
func writeS3DeadDropBundle(w io.Writer, store *db.Store, b bundleVars, transportConfigID int64, binaryPath string) error {
	// 1. Get the parent's instance_id so we can compute the prefix.
	parentMeta, err := store.CPMeta()
	if err != nil {
		return fmt.Errorf("read parent cp_meta: %w", err)
	}

	// 2. Read the transport_config — bucket coords go inline into
	//    the .env so the child doesn't need a transport_config row
	//    on first boot.
	tc, err := store.GetTransportConfig(transportConfigID)
	if err != nil {
		return fmt.Errorf("transport_config %d: %w", transportConfigID, err)
	}

	// 3. Mint the child instance_id. UUID-shape so it slots into
	//    cp_meta.instance_id verbatim.
	childInstanceID, err := newUUID()
	if err != nil {
		return fmt.Errorf("mint child instance_id: %w", err)
	}

	// 4. Compute the prefix the child will publish to AND the
	//    parent will read from.
	prefix := fmt.Sprintf("cp/%s/outbound/%s/", childInstanceID, parentMeta.InstanceID)

	// 5. Pre-register the federation peer. The s3reader picks it up
	//    on its next tick (within 30s).
	if _, err := store.AddS3FederationPeer(b.DisplayName, prefix, transportConfigID, b.BootstrapToken); err != nil {
		return fmt.Errorf("register federation peer: %w", err)
	}

	// 6. Read the linux binary. (Same path the dockerfile bundle
	//    uses; the helper is shared.)
	binaryBytes, err := os.ReadFile(binaryPath)
	if err != nil {
		return fmt.Errorf("read parent binary: %w", err)
	}

	// 7. Pack the tarball.
	gz := gzip.NewWriter(w)
	defer gz.Close()
	t := tar.NewWriter(gz)
	defer t.Close()

	root := bundleRootDir(b)
	files := []bundleFile{
		{name: "Dockerfile", mode: 0o644, content: dockerfileTemplate(b)},
		{name: "docker-compose.yml", mode: 0o644, content: composeTemplateForS3DeadDrop(b)},
		{name: ".env", mode: 0o600, content: envTemplateForS3DeadDrop(b, childInstanceID, parentMeta.InstanceID, prefix, tc)},
		{name: "README.md", mode: 0o644, content: readmeTemplateForS3DeadDrop(b, childInstanceID, parentMeta.InstanceID, prefix, tc)},
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

// envTemplateForS3DeadDrop bakes the federation publisher's bucket
// coords + the pre-assigned instance_id into env vars that the
// docker-compose.yml passes through to okesu-cp.
func envTemplateForS3DeadDrop(b bundleVars, childInstanceID, parentInstanceID, prefix string, tc db.TransportConfig) string {
	useSSL := "true"
	if !tc.UseSSL {
		useSSL = "false"
	}
	var sb strings.Builder
	sb.WriteString("# okesu-cp child enrollment env (S3 dead-drop). Generated " + b.IssuedAt + ".\n")
	sb.WriteString("# DO NOT COMMIT — bucket access keys + admin password are in here.\n\n")
	sb.WriteString("# ── identity ────────────────────────────────────────────────\n")
	sb.WriteString("OKESU_CP_INSTANCE_ID=" + childInstanceID + "\n")
	sb.WriteString("OKESU_CP_DISPLAY_NAME=" + b.DisplayName + "\n")
	sb.WriteString("OKESU_CP_REGION=" + b.Region + "\n\n")
	sb.WriteString("# ── federation S3 publisher ─────────────────────────────────\n")
	sb.WriteString("# Parent instance id: " + parentInstanceID + "\n")
	sb.WriteString("OKESU_CP_FEDERATION_S3_PUBLISH_PREFIX=" + prefix + "\n")
	sb.WriteString("OKESU_CP_FEDERATION_S3_PUBLISH_BUCKET=" + tc.Bucket + "\n")
	sb.WriteString("OKESU_CP_FEDERATION_S3_PUBLISH_ENDPOINT=" + tc.Endpoint + "\n")
	sb.WriteString("OKESU_CP_FEDERATION_S3_PUBLISH_REGION=" + tc.Region.String + "\n")
	sb.WriteString("OKESU_CP_FEDERATION_S3_PUBLISH_USE_SSL=" + useSSL + "\n")
	sb.WriteString("OKESU_CP_FEDERATION_S3_PUBLISH_ACCESS_KEY=" + tc.AccessKey.String + "\n")
	sb.WriteString("OKESU_CP_FEDERATION_S3_PUBLISH_SECRET_KEY=" + tc.SecretKey.String + "\n\n")
	sb.WriteString("# ── child CP local secrets ──────────────────────────────────\n")
	sb.WriteString("OKESU_CP_ADMIN_PASSWORD=" + b.AdminPassword + "\n")
	sb.WriteString("OKESU_WEBHOOK_SECRET=" + b.WebhookSecret + "\n")
	sb.WriteString("OKESU_CP_SESSION_KEY=" + b.SessionKey + "\n\n")
	sb.WriteString("# ── ports inside container ──────────────────────────────────\n")
	sb.WriteString(fmt.Sprintf("OKESU_CP_LISTEN=:%d\n", b.ChildPort))
	sb.WriteString(fmt.Sprintf("OKESU_CP_MGMT_LISTEN=:%d\n", b.MgmtPort))
	return sb.String()
}

// composeTemplateForS3DeadDrop renders a compose file that runs
// okesu-cp with env_file pointing at the .env.
func composeTemplateForS3DeadDrop(b bundleVars) string {
	return fmt.Sprintf(`# Generated by parent CP %s on %s.
# S3-dead-drop federation: this child writes its introspect manifest
# to the bucket every 30s. The parent's s3reader picks it up — no
# inbound HTTPS path required between them.
services:
  okesu-cp:
    build: .
    image: okesu-cp:s3-deaddrop-%s
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
		b.DisplayName, b.IssuedAt,
		slugify(b.DisplayName), slugify(b.DisplayName),
		b.ChildPort, b.ChildPort,
		b.MgmtPort, b.MgmtPort,
		b.ChildPort, b.MgmtPort,
	)
}

// readmeTemplateForS3DeadDrop describes what the bundle does + how to
// run it. Aimed at an operator who's never deployed a child CP before.
func readmeTemplateForS3DeadDrop(b bundleVars, childInstanceID, parentInstanceID, prefix string, tc db.TransportConfig) string {
	var sb strings.Builder
	sb.WriteString("# okesu-cp child enrollment — `" + b.DisplayName + "` (S3 dead-drop)\n\n")
	sb.WriteString("Generated by parent at " + b.IssuedAt + ".\n\n")
	sb.WriteString("## What this is\n\n")
	sb.WriteString("A self-contained Docker bundle that boots a new child Control Plane\n")
	sb.WriteString("which **federates up to its parent over S3 dead-drop** instead of\n")
	sb.WriteString("inbound HTTPS. The child writes its introspect snapshot to a known\n")
	sb.WriteString("bucket prefix every 30s; the parent's `s3reader` polls the prefix\n")
	sb.WriteString("and surfaces this CP in its Federation page.\n\n")
	sb.WriteString("Use this when the parent has no public IP / lives behind NAT / is\n")
	sb.WriteString("on a developer's laptop — anywhere the child can't reach the parent\n")
	sb.WriteString("over HTTPS but both can talk to S3.\n\n")
	sb.WriteString("## Identity\n\n")
	sb.WriteString("- Child CP instance id: `" + childInstanceID + "` (pre-assigned)\n")
	sb.WriteString("- Parent CP instance id: `" + parentInstanceID + "`\n")
	sb.WriteString("- Bucket: `" + tc.Bucket + "` @ `" + tc.Endpoint + "`\n")
	sb.WriteString("- Prefix this child writes to: `" + prefix + "`\n\n")
	sb.WriteString("The parent already has a `federation_peers` row registered for the\n")
	sb.WriteString("above prefix — there's nothing else to configure on the parent side.\n\n")
	sb.WriteString("## Run\n\n")
	sb.WriteString("```sh\n")
	sb.WriteString("tar -xzf okesu-cp-*.tar.gz\n")
	sb.WriteString("cd " + bundleRootDir(b) + "\n")
	sb.WriteString("docker compose up -d\n")
	sb.WriteString("```\n\n")
	sb.WriteString("First introspect.json appears in the bucket within ~30s. Watch the\n")
	sb.WriteString("parent's Federation page — the new CP shows up as healthy as soon\n")
	sb.WriteString("as the parent's s3reader does its next tick.\n\n")
	sb.WriteString("## Login\n\n")
	sb.WriteString("- URL:      `https://<your-host>:" + fmt.Sprintf("%d", b.ChildPort) + "/`\n")
	sb.WriteString("- Email:    `admin@local`\n")
	sb.WriteString("- Password: `" + b.AdminPassword + "` (also in `.env`)\n\n")
	sb.WriteString("Change the admin password after the first login.\n\n")
	sb.WriteString("## Security notes\n\n")
	sb.WriteString("- `.env` carries the bucket's access + secret keys. Treat it like\n")
	sb.WriteString("  any other root credential — chmod 600, don't commit, scrub from\n")
	sb.WriteString("  CI logs.\n")
	sb.WriteString("- The bucket access keys are **scoped to this bucket** by the\n")
	sb.WriteString("  operator who minted the transport_config. Worst-case key leak\n")
	sb.WriteString("  exposes only the federation manifests, not the rest of the\n")
	sb.WriteString("  parent's tenancy.\n")
	sb.WriteString("- The child's instance_id is durable — re-launching with this same\n")
	sb.WriteString("  bundle keeps the same identity. To re-issue, generate a fresh\n")
	sb.WriteString("  bundle on the parent.\n")
	return sb.String()
}

// newUUID returns a random UUID-shape string in 8-4-4-4-12 form
// (v4-like, but we don't set the version/variant nibbles because
// nothing downstream cares — cp_meta.instance_id is opaque). Same
// approach as db.newInstanceID but exposed for the api package.
func newUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	hex := hex.EncodeToString(b)
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex[0:8], hex[8:12], hex[12:16], hex[16:20], hex[20:32]), nil
}

// _ keeps time imported for future expiry-stamping if/when we add it.
var _ = time.RFC3339
