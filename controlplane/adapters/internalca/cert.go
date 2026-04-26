// Package internalca implements the CertManager port against the
// CP's built-in self-signed CA (controlplane.CA). This is the default
// for dev and small-fleet deployments. Larger deployments should use
// the oci-certificates adapter (managed CA + automated rotation).
//
// The wrapper is intentionally thin — it delegates everything to the
// existing CA struct. Future hardening (CRL, OCSP) goes into the CA
// directly so all adapters benefit; the interface stays small.
package internalca

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"time"

	"github.com/section9labs/okesu/controlplane/ports"
)

// underlying is the subset of *controlplane.CA we depend on. Defined
// locally so this adapter doesn't pull in the full controlplane package
// (which would create an import cycle with config / Server).
type underlying interface {
	IssueClientCert(agentName string) (certPEM, keyPEM []byte, err error)
	GetCAPEM() []byte
}

// Adapter wraps an internal CA to satisfy ports.CertManager.
type Adapter struct {
	ca underlying
}

// New wraps the given CA. The CA is whatever the CP creates via
// controlplane.EnsureCA; we keep it abstract so tests can substitute.
func New(ca underlying) *Adapter {
	return &Adapter{ca: ca}
}

// IssueClientCert calls the underlying CA. validUntil is currently
// ignored — the internal CA hardcodes a 10-year expiry — but the
// interface accepts it so callers using OCI Certificates / Vault can
// pass a real value.
func (a *Adapter) IssueClientCert(_ context.Context, subject string, validUntil time.Time) (ports.CertBundle, error) {
	_ = validUntil
	if subject == "" {
		return ports.CertBundle{}, fmt.Errorf("subject required")
	}
	cert, key, err := a.ca.IssueClientCert(subject)
	if err != nil {
		return ports.CertBundle{}, err
	}
	exp := certExpiry(cert)
	return ports.CertBundle{
		CertPEM:   cert,
		KeyPEM:    key,
		CACertPEM: a.ca.GetCAPEM(),
		ExpiresAt: exp,
	}, nil
}

// CACert returns the CA cert PEM.
func (a *Adapter) CACert(_ context.Context) ([]byte, error) {
	return a.ca.GetCAPEM(), nil
}

// Revoke is unsupported by the internal CA — there's no CRL/OCSP
// infrastructure. Callers gracefully degrade. To actually revoke, the
// operator deletes the agent row + reissues; the old cert will fail
// the agent-name lookup at the mgmt-plane handler.
func (a *Adapter) Revoke(_ context.Context, _ string) error {
	return ports.ErrNotSupported
}

// certExpiry parses the leaf cert and returns its NotAfter. Used to
// surface in operator UI ("expires in N days"). Best-effort — a parse
// failure returns the zero time, which the UI displays as "unknown".
func certExpiry(certPEM []byte) time.Time {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return time.Time{}
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return time.Time{}
	}
	return cert.NotAfter
}

// Compile-time assertion.
var _ ports.CertManager = (*Adapter)(nil)
