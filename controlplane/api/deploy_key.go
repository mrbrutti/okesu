package api

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/section9labs/okesu/controlplane/audit"
	"github.com/section9labs/okesu/controlplane/db"
	"github.com/section9labs/okesu/controlplane/ports"
)

// secretNameDeploySSHPrivateKey is the canonical name kept in sync with
// controlplane.SecretDeploySSHPrivateKey. Hard-coded here to avoid a
// circular import (controlplane imports api).
const secretNameDeploySSHPrivateKey = "deploy/ssh-private-key"

// deployKeyStatus is the wire shape for the GET endpoint. Never returns
// the key bytes — only the fingerprint + comment + presence flag.
type deployKeyStatus struct {
	Configured  bool   `json:"configured"`
	Fingerprint string `json:"fingerprint,omitempty"` // SHA256:base64 (OpenSSH style)
	KeyType     string `json:"key_type,omitempty"`    // ssh-ed25519, ssh-rsa, …
	Comment     string `json:"comment,omitempty"`     // anything after the public-key fields
	// AdapterWritable mirrors whether the configured Secrets adapter
	// supports Put. When false, the operator can read the current
	// fingerprint but the UI's [Save] button is disabled — they have
	// to rotate by editing the secret store directly.
	AdapterWritable bool `json:"adapter_writable"`
}

// DeployKeyGet returns the fingerprint of the stored deploy SSH key,
// or {configured:false} if none is set. Never returns the key bytes.
//
// GET /api/system/deploy-ssh-key  (admin only)
func DeployKeyGet(secrets ports.Secrets) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		out := deployKeyStatus{}
		_, writable := secrets.(ports.SecretsWriter)
		out.AdapterWritable = writable

		if secrets != nil {
			data, err := secrets.Get(r.Context(), secretNameDeploySSHPrivateKey)
			if err == nil && len(data) > 0 {
				out.Configured = true
				if fp, kt, comment, perr := publicKeyMetadataFromPrivate(data); perr == nil {
					out.Fingerprint = fp
					out.KeyType = kt
					out.Comment = comment
				}
				// Best-effort metadata only; don't fail the GET when we
				// can't derive the fingerprint (e.g. encrypted PEM).
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// DeployKeyPut stores a new SSH private key as the CP-wide default for
// SSH deploys. The body is the raw PEM-encoded key (or JSON
// {"private_key": "..."}); we accept both for ergonomics.
//
// Validates that the key parses as a valid OpenSSH private key before
// writing. Encrypted keys (passphrase-protected) are rejected here —
// the deploy code path doesn't know the passphrase ahead of time, so
// storing one would just fail every deploy. Operators with encrypted
// keys should decrypt locally and store the unencrypted version, OR
// continue pasting per-deploy.
//
// PUT /api/system/deploy-ssh-key  (admin only)
func DeployKeyPut(store *db.Store, secrets ports.Secrets) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writer, ok := secrets.(ports.SecretsWriter)
		if !ok {
			http.Error(w, "configured secrets adapter is read-only — rotate via the underlying store", http.StatusBadRequest)
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10)) // 64 KiB cap
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
		key := keyBytesFromBody(body)
		if len(key) == 0 {
			http.Error(w, "empty body", http.StatusBadRequest)
			return
		}

		// Parse to validate. Encrypted keys are rejected; non-PEM input
		// also fails here.
		if _, err := ssh.ParsePrivateKey(key); err != nil {
			http.Error(w, "invalid private key: "+err.Error(), http.StatusBadRequest)
			return
		}

		if err := writer.Put(r.Context(), secretNameDeploySSHPrivateKey, key); err != nil {
			if errors.Is(err, ports.ErrNotSupported) {
				http.Error(w, "secrets adapter does not support write", http.StatusBadRequest)
				return
			}
			http.Error(w, "store: "+err.Error(), http.StatusInternalServerError)
			return
		}

		fp, kt, comment, _ := publicKeyMetadataFromPrivate(key)

		audit.Emit(r, store, db.AuditEntry{
			Action: "system.deploy_ssh_key.set",
			Target: "system:deploy-ssh-key",
			Metadata: map[string]any{
				"fingerprint": fp,
				"key_type":    kt,
			},
		})

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(deployKeyStatus{
			Configured:      true,
			Fingerprint:     fp,
			KeyType:         kt,
			Comment:         comment,
			AdapterWritable: true,
		})
	}
}

// DeployKeyDelete removes the stored deploy SSH key. After this, deploys
// once again require a per-request private_key field.
//
// DELETE /api/system/deploy-ssh-key  (admin only)
func DeployKeyDelete(store *db.Store, secrets ports.Secrets) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writer, ok := secrets.(ports.SecretsWriter)
		if !ok {
			http.Error(w, "configured secrets adapter is read-only", http.StatusBadRequest)
			return
		}
		// Adapters expose write via Put; "delete" is "Put empty + filesystem-level remove".
		// envsecrets specifically rewrites with an empty file; OCI Vault
		// would soft-delete the secret version. For the smoke path we
		// just write zero bytes and let resolvePrivateKey treat empty
		// as "not configured".
		if err := writer.Put(r.Context(), secretNameDeploySSHPrivateKey, nil); err != nil {
			http.Error(w, "store: "+err.Error(), http.StatusInternalServerError)
			return
		}
		audit.Emit(r, store, db.AuditEntry{
			Action: "system.deploy_ssh_key.delete",
			Target: "system:deploy-ssh-key",
		})
		w.WriteHeader(http.StatusNoContent)
	}
}

// publicKeyMetadataFromPrivate derives the OpenSSH-style SHA256
// fingerprint, key type, and comment from a PEM-encoded private key.
func publicKeyMetadataFromPrivate(pemBytes []byte) (fingerprint, keyType, comment string, err error) {
	signer, err := ssh.ParsePrivateKey(pemBytes)
	if err != nil {
		return "", "", "", err
	}
	pub := signer.PublicKey()
	return ssh.FingerprintSHA256(pub), pub.Type(), commentFromPubKey(pub), nil
}

// commentFromPubKey extracts the trailing comment from `ssh.MarshalAuthorizedKey`.
// MarshalAuthorizedKey returns "<keytype> <base64> [comment]\n"; the comment
// is whatever follows the second space.
func commentFromPubKey(pub ssh.PublicKey) string {
	authorized := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))
	parts := strings.SplitN(authorized, " ", 3)
	if len(parts) < 3 {
		return ""
	}
	return parts[2]
}

// keyBytesFromBody accepts either a raw PEM body or a JSON object
// {"private_key": "..."}. Returns the trimmed PEM bytes.
func keyBytesFromBody(body []byte) []byte {
	trimmed := strings.TrimSpace(string(body))
	if strings.HasPrefix(trimmed, "{") {
		var w struct {
			PrivateKey string `json:"private_key"`
		}
		if err := json.Unmarshal([]byte(trimmed), &w); err == nil && w.PrivateKey != "" {
			return []byte(strings.TrimSpace(w.PrivateKey))
		}
	}
	return []byte(trimmed)
}

// Compile-time guard that we wired the right error helper.
var _ = fmt.Sprintf
var _ = sha256.New
