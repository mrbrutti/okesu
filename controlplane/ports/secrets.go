package ports

import "context"

// Secrets stores opaque sensitive values — webhook HMAC secret, OIDC
// client secret, anthropic / openai API keys, daimon-deploy SSH keys
// (when we add stored deploy creds), session-signing key, etc.
//
// Adapters: oci-vault (OCI Vault — managed KMS + secret store), env
// (environment variables; current behaviour kept for dev), aws-kms /
// gcp-secrets-manager (future).
//
// Names follow a "<system>/<purpose>" convention so an OCI Vault folder
// hierarchy maps naturally:
//
//	cp/webhook-secret
//	cp/session-key
//	cp/oidc/client-secret
//	llm/anthropic
//	llm/openai
//
// The interface deliberately models read-only-from-the-CP semantics for
// the common case. Some adapters will support write (rotate/store),
// which is exposed via the optional SecretsWriter.
type Secrets interface {
	// Get returns the value for `name`. Returns ErrNotFound when the
	// secret doesn't exist.
	Get(ctx context.Context, name string) ([]byte, error)
}

// SecretsWriter is implemented by adapters that support writing. The CP
// type-asserts to this interface for rotate/store operations and
// degrades gracefully when the adapter is read-only.
type SecretsWriter interface {
	// Put stores or rotates a secret. Adapters that version secrets
	// (OCI Vault, AWS Secrets Manager) MAY return the new version ID
	// in the returned metadata.
	Put(ctx context.Context, name string, value []byte) error
}
