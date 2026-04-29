// Worker that drives a managed CP provision through its state
// machine. The CPProvisionCreate API hands a row off to RunWorker;
// the worker:
//
//  1. Loads the row + the linked cloud_credentials + cp_bootstrap_token.
//  2. Generates the bundle bytes (same logic as the manual download
//     endpoint) and stores them in an in-memory cache keyed by
//     bundle_token_id. The cloud-init script in the launched VM
//     fetches the bundle from this cache via /api/federation/cp-bundle/download.
//  3. Renders the cloud-init script with the bundle URL + Bearer token.
//  4. Calls Provisioner.Launch — long-running, returns once the VM is
//     RUNNING. State machine: queued → starting → cloud_init_running.
//  5. Updates the row with cloud_resource_id + console URL.
//  6. Worker returns. The state machine flips to bootstrap_pending
//     until the new CP calls /api/v1/cp/bootstrap; the bootstrap
//     handler then sets peer_id + status=ready (see
//     advanceCPProvisionOnBootstrap below).
//
// The worker runs in a goroutine started from CPProvisionCreate. We
// don't restart in-flight provisions on CP boot — if the parent
// crashed mid-launch, the operator sees a stuck cp_provisions row
// and can retry / destroy from the Federation page (Phase 21.5+).

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/section9labs/okesu/controlplane/cpprovision"
	"github.com/section9labs/okesu/controlplane/db"
)

// BundleCache is an in-memory store of generated bundle bytes keyed
// by bootstrap-token id. Populated by RunCPProvisionWorker before
// it calls Provisioner.Launch; drained when the cloud-init script
// fetches via /api/federation/cp-bundle/download. The cache eviction
// is opportunistic — we delete the entry when the bootstrap exchange
// burns the token (so a failed-then-retried provision can re-fetch
// during its 24h token TTL).
type BundleCache struct {
	mu    sync.Mutex
	items map[int64]bundleCacheEntry
}

type bundleCacheEntry struct {
	bytes      []byte
	filename   string
	uploadedAt time.Time
}

func NewBundleCache() *BundleCache {
	return &BundleCache{items: map[int64]bundleCacheEntry{}}
}

func (c *BundleCache) Put(tokenID int64, filename string, raw []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[tokenID] = bundleCacheEntry{
		bytes:      raw,
		filename:   filename,
		uploadedAt: time.Now(),
	}
}

func (c *BundleCache) Get(tokenID int64) ([]byte, string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.items[tokenID]
	if !ok {
		return nil, "", false
	}
	return v.bytes, v.filename, true
}

func (c *BundleCache) Evict(tokenID int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.items, tokenID)
}

// CPProvisionWorkerConfig bundles the dependencies the worker needs
// to do its job. Built once at server boot.
type CPProvisionWorkerConfig struct {
	Store         *db.Store
	Registry      *cpprovision.Registry
	Cache         *BundleCache
	Bundle        CPBundleConfig
	ParentBaseURL string // public UI URL (https://...:8443) — base for the bundle download URL the cloud-init fetches from
}

// RunCPProvisionWorker drives a single cp_provisions row through the
// launch phase. Caller passes ctx (typically context.Background() —
// the worker runs detached from the HTTP request's lifetime) so
// errors from a hung cloud API call don't fail the operator's POST.
func RunCPProvisionWorker(ctx context.Context, cfg CPProvisionWorkerConfig, provisionID int64) {
	row, err := cfg.Store.GetCPProvision(provisionID)
	if err != nil {
		return
	}

	logf := func(format string, args ...any) {
		line := fmt.Sprintf(format, args...) + "\n"
		_ = cfg.Store.AppendCPProvisionLog(provisionID, line)
	}
	failNow := func(msg string) {
		logf("✗ %s", msg)
		_ = cfg.Store.SetCPProvisionError(provisionID, msg)
	}

	if !row.CredentialID.Valid {
		failNow("no credential linked to this provision")
		return
	}
	if !row.BundleTokenID.Valid {
		failNow("no bootstrap token linked to this provision")
		return
	}

	prov, err := cfg.Registry.Get(row.Cloud)
	if err != nil {
		failNow("provisioner: " + err.Error())
		return
	}

	logf("→ starting provision #%d (%s, %s)", provisionID, row.Cloud, row.Region)
	_ = cfg.Store.UpdateCPProvisionStatus(provisionID, db.CPProvisionStarting)

	// Decrypt credential.
	masterKey, err := cfg.Store.MasterKeyFromMeta()
	if err != nil {
		failNow("master key: " + err.Error())
		return
	}
	credPayload, err := cfg.Store.DecryptCloudCredential(row.CredentialID.Int64, masterKey)
	if err != nil {
		failNow("decrypt credential: " + err.Error())
		return
	}

	// Generate the bundle. Same logic as the operator-facing
	// CPBundleHandler — we just write into a buffer instead of an
	// http.ResponseWriter so the worker can drop it in the cache.
	tokenRow, err := loadBootstrapToken(cfg.Store, row.BundleTokenID.Int64)
	if err != nil {
		failNow("load bootstrap token: " + err.Error())
		return
	}
	plaintext := tokenPlaintextFromContext(provisionID)
	if plaintext == "" {
		// We don't have the plaintext after the row is created.
		// The caller of RunCPProvisionWorker MUST stash it via
		// SetBootstrapTokenPlaintextForProvision before kicking the
		// worker, otherwise we can't render the .env. This is a
		// program bug, not a user error, so panic to surface it.
		failNow("internal: bootstrap token plaintext not stashed for this provision (program error)")
		return
	}
	adminPassword, _ := randomHex(16)
	webhookSecret, _ := randomHex(32)
	sessionKey, _ := randomHex(32)
	bv := bundleVars{
		DisplayName:    row.DisplayName,
		Region:         row.Region,
		ParentURL:      tokenRow.ParentURL,
		BootstrapToken: plaintext,
		AdminPassword:  adminPassword,
		WebhookSecret:  webhookSecret,
		SessionKey:     sessionKey,
		ChildPort:      8443,
		MgmtPort:       8444,
		Version:        nonEmpty(cfg.Bundle.Version, "dev"),
		IssuedAt:       time.Now().UTC().Format(time.RFC3339),
	}
	var buf bytes.Buffer
	if cfg.Bundle.LinuxBinaryPath != "" {
		if err := writeDockerfileBundle(&buf, bv, cfg.Bundle.LinuxBinaryPath); err != nil {
			failNow("build bundle: " + err.Error())
			return
		}
	} else if cfg.Bundle.LinuxImageTarPath != "" {
		if err := writeComposeBundle(&buf, bv, cfg.Bundle.LinuxImageTarPath); err != nil {
			failNow("build bundle: " + err.Error())
			return
		}
	} else {
		failNow("parent has no LinuxBinaryPath or LinuxImageTarPath configured — cannot build a bundle")
		return
	}
	bundleBytes := buf.Bytes()
	bundleFilename := fmt.Sprintf("okesu-cp-%s.tar.gz", slugify(row.DisplayName))
	cfg.Cache.Put(row.BundleTokenID.Int64, bundleFilename, bundleBytes)
	logf("✓ bundle generated (%d bytes), cached for download", len(bundleBytes))

	// Render cloud-init.
	bundleURL := strings.TrimRight(cfg.ParentBaseURL, "/") + "/api/federation/cp-bundle/download"
	cloudInit, err := cpprovision.RenderCloudInit(cpprovision.CloudInitVars{
		DisplayName:    row.DisplayName,
		Region:         row.Region,
		BundleURL:      bundleURL,
		BundleToken:    plaintext,
		BundleFilename: bundleFilename,
		ProvisionID:    provisionID,
	})
	if err != nil {
		failNow("render cloud-init: " + err.Error())
		return
	}

	// Decode cloud_params from the row.
	var cloudParams map[string]any
	if row.CloudParamsJSON != "" {
		_ = json.Unmarshal([]byte(row.CloudParamsJSON), &cloudParams)
	}

	logf("→ calling Provisioner.Launch")
	_ = cfg.Store.UpdateCPProvisionStatus(provisionID, db.CPProvisionCloudInitRunning)

	res, err := prov.Launch(ctx, cpprovision.LaunchRequest{
		DisplayName:       row.DisplayName,
		Region:            row.Region,
		CredentialPayload: credPayload,
		CloudParams:       cloudParams,
		CloudInitScript:   cloudInit,
	}, cpProvisionLogger{provisionID: provisionID, store: cfg.Store})
	if err != nil {
		failNow("Launch: " + err.Error())
		return
	}

	_ = cfg.Store.SetCPProvisionCloudResource(provisionID, res.ResourceID, res.ConsoleURL)
	logf("✓ instance running at %s", res.PublicIP)
	if res.ConsoleURL != "" {
		logf("  console: %s", res.ConsoleURL)
	}
	logf("→ awaiting child CP bootstrap call")
	_ = cfg.Store.UpdateCPProvisionStatus(provisionID, db.CPProvisionBootstrapPending)
	// Worker returns here. The bootstrap handler advances the row to
	// `ready` when the new CP calls /api/v1/cp/bootstrap with the
	// matching token (see advanceCPProvisionOnBootstrap).
}

// cpProvisionLogger satisfies cpprovision.Logger by appending into
// the cp_provisions.log column.
type cpProvisionLogger struct {
	provisionID int64
	store       *db.Store
}

func (l cpProvisionLogger) Logf(format string, args ...any) {
	_ = l.store.AppendCPProvisionLog(l.provisionID, fmt.Sprintf(format, args...)+"\n")
}

// ── token plaintext stash ──────────────────────────────────────────
//
// The bootstrap-token plaintext is only available at the moment
// IssueCPBootstrapToken returns it; after that, the DB has only the
// hash. The worker needs the plaintext to inject into the bundle's
// .env. We stash it in this in-memory map at CPProvisionCreate time
// and the worker pulls it back out, then evicts.
//
// One per provision_id. Map is process-local, so a CP restart loses
// pending stashes — that's fine, the operator just retries the
// provision (which mints a new token).

var (
	tokenStashMu sync.Mutex
	tokenStash   = map[int64]string{}
)

func StashBootstrapTokenPlaintext(provisionID int64, plaintext string) {
	tokenStashMu.Lock()
	defer tokenStashMu.Unlock()
	tokenStash[provisionID] = plaintext
}

func tokenPlaintextFromContext(provisionID int64) string {
	tokenStashMu.Lock()
	defer tokenStashMu.Unlock()
	v := tokenStash[provisionID]
	delete(tokenStash, provisionID)
	return v
}

// loadBootstrapToken reads the cp_bootstrap_tokens row by id. The
// store package's existing helpers go by prefix; this is the path
// the worker uses to recover the parent_url + display_name + region
// without re-parsing the request.
func loadBootstrapToken(s *db.Store, id int64) (*db.CPBootstrapToken, error) {
	row := s.QueryRow(`
		SELECT id, token_prefix, token_hash, display_name, region, parent_url,
		       created_by_user_id, created_by_email, created_at, expires_at,
		       used_at, used_peer_id, used_from_url
		FROM cp_bootstrap_tokens
		WHERE id = ?
	`, id)
	var t db.CPBootstrapToken
	var hash string
	if err := row.Scan(&t.ID, &t.TokenPrefix, &hash, &t.DisplayName, &t.Region, &t.ParentURL,
		&t.CreatedByUser, &t.CreatedByEmail, &t.CreatedAt, &t.ExpiresAt,
		&t.UsedAt, &t.UsedPeerID, &t.UsedFromURL); err != nil {
		return nil, err
	}
	return &t, nil
}

// AdvanceCPProvisionOnBootstrap is called from CPBootstrapHandler
// when the bootstrap exchange succeeds for a token tied to a
// provision row. Sets peer_id + status=ready + evicts the bundle
// from the cache (its job is done — the new CP has it).
func AdvanceCPProvisionOnBootstrap(store *db.Store, cache *BundleCache, tokenID, peerID int64) {
	provision, err := store.FindCPProvisionByBundleToken(tokenID)
	if err != nil {
		// No matching provision — bootstrap was likely from the
		// manual "Generate bundle" path. Nothing to advance.
		return
	}
	_ = store.SetCPProvisionPeer(provision.ID, peerID)
	_ = store.UpdateCPProvisionStatus(provision.ID, db.CPProvisionReady)
	_ = store.AppendCPProvisionLog(provision.ID, "✓ child CP registered as peer #"+itoa(peerID)+"; provision ready\n")
	cache.Evict(tokenID)
}

func itoa(n int64) string { return fmt.Sprintf("%d", n) }
