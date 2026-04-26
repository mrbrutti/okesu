package ports

import (
	"context"
	"time"
)

// CertManager issues mTLS certs for daemons (one per agent) and nodes
// (tunnel client). Centralising this behind an interface lets the CP
// graduate from the built-in self-signed CA to OCI Certificates / AWS
// PCA / HashiCorp Vault without touching the rest of the code base.
//
// Adapters: internalca (current — self-signed CA living on the CP host;
// fine for dev and small fleets), oci (OCI Certificates managed CA),
// vault (Vault PKI engine).
//
// Cert lifecycle
//
//   - Issue at deploy time, store the bundle on the node.
//   - Renew before expiry (the daemon's mgmt-plane poll can request a
//     renewal when its cert is within `renewal_window` of expiry).
//   - Revoke on node delete or compromise.
//
// All cert content (PEM) is opaque to the interface. The CP doesn't
// parse certs except to display issuance/expiry dates from the CA's
// metadata response.
type CertManager interface {
	// IssueClientCert mints a fresh client cert for the named subject
	// (the agent name or node name). The CN of the resulting cert MUST
	// equal `subject` — this is what mTLS-protected handlers match on.
	// Validity is adapter-specific but should not exceed `validUntil`.
	IssueClientCert(ctx context.Context, subject string, validUntil time.Time) (CertBundle, error)

	// CACert returns the CA's certificate (PEM). Daemons trust this for
	// verifying the CP's mgmt-plane TLS cert.
	CACert(ctx context.Context) ([]byte, error)

	// Revoke marks a cert as revoked. Optional — adapters that don't
	// support revocation lists return ErrNotSupported. Used on node
	// delete + on compromise events.
	Revoke(ctx context.Context, subject string) error
}

// CertBundle is what a CertManager.IssueClientCert returns.
type CertBundle struct {
	// CertPEM is the leaf certificate (subject=agent/node name).
	CertPEM []byte
	// KeyPEM is the private key matching CertPEM. Generated CA-side
	// because the CP needs to write it onto the node anyway.
	KeyPEM []byte
	// CACertPEM is the CA cert chain (for the daemon's truststore).
	CACertPEM []byte
	// ExpiresAt is when the cert stops being valid.
	ExpiresAt time.Time
}
