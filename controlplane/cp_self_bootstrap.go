// CP self-bootstrap on startup.
//
// When a fresh child CP is launched from a parent-issued bundle, its
// env carries OKESU_CP_BOOTSTRAP_TOKEN + OKESU_CP_PARENT_URL. On
// first boot — and only first boot — we POST to the parent's
// /api/v1/cp/bootstrap, receive a long-lived rotating federation
// token, and stash it in cp_meta. Subsequent restarts skip this
// path because cp_meta.federation_token_hash is already set.
//
// Failure modes (network down, parent rejects the token, etc.) are
// non-fatal: we log + continue with whatever cfg.FederationToken
// said. Operators can re-issue a bundle and overwrite the env to
// retry. We deliberately don't loop on retry here — bootstrap is a
// one-shot identity event, and a CP that boots without joining the
// federation can still be added manually via /api/federation/peers.

package controlplane

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/section9labs/okesu/controlplane/db"
)

// MaybeBootstrapFromEnv runs the bootstrap exchange iff the
// OKESU_CP_BOOTSTRAP_TOKEN env is set AND cp_meta has no existing
// federation token hash. Returns the federation token to use for the
// rest of this boot — empty string when bootstrap was skipped or
// failed (caller should fall back to cfg.FederationToken).
func MaybeBootstrapFromEnv(store *db.Store, childPublicURL string) (string, error) {
	token := strings.TrimSpace(os.Getenv("OKESU_CP_BOOTSTRAP_TOKEN"))
	if token == "" {
		return "", nil // not in bootstrap mode
	}

	// Already bootstrapped — cp_meta has a token hash from a previous
	// run. Skip the exchange to avoid burning a fresh token every
	// boot, and ignore the env. Operators rotating identity should
	// reset cp_meta.federation_token_hash explicitly.
	meta, err := store.CPMeta()
	if err != nil {
		return "", fmt.Errorf("read cp_meta: %w", err)
	}
	if meta.FederationTokenHash != "" {
		log.Printf("bootstrap: skipping — federation token already configured (cp_meta hash present)")
		return "", nil
	}

	parentURL := strings.TrimSpace(os.Getenv("OKESU_CP_PARENT_URL"))
	if parentURL == "" {
		return "", fmt.Errorf("OKESU_CP_BOOTSTRAP_TOKEN set but OKESU_CP_PARENT_URL is empty")
	}

	displayName := strings.TrimSpace(os.Getenv("OKESU_CP_DISPLAY_NAME"))
	region := strings.TrimSpace(os.Getenv("OKESU_CP_REGION"))

	body, _ := json.Marshal(map[string]string{
		"token":        token,
		"child_url":    childPublicURL,
		"region":       region,
		"display_name": displayName,
	})

	// Bundle-issued parents are usually self-signed for the lab —
	// InsecureSkipVerify mirrors the federation poller's defaults so
	// bootstrap works in setups where the operator hasn't set up DNS
	// + a real cert yet. Production deploys can lock this down via a
	// reverse proxy with a real cert.
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		},
	}
	url := strings.TrimRight(parentURL, "/") + "/api/v1/cp/bootstrap"
	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("bootstrap POST %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Read up to 512 bytes of the error body for the operator log.
		buf := make([]byte, 512)
		n, _ := resp.Body.Read(buf)
		return "", fmt.Errorf("bootstrap %s rejected (HTTP %d): %s", url, resp.StatusCode, string(buf[:n]))
	}
	var out struct {
		FederationToken string `json:"federation_token"`
		PeerID          int64  `json:"peer_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("bootstrap response decode: %w", err)
	}
	if out.FederationToken == "" {
		return "", fmt.Errorf("bootstrap %s returned empty federation_token", url)
	}
	log.Printf("bootstrap: registered with parent %s as peer #%d", parentURL, out.PeerID)
	return out.FederationToken, nil
}
