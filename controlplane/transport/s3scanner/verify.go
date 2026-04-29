// Signature + fingerprint helpers used during registration.
//
// Cert chain: fleet root (in transport_configs.fleet_pubkey_pem) →
// package signing cert (per enrollment_packages row, embedded in
// each registration request) → request signature (over canonical
// JSON of the request body, ECDSA-SHA256).
//
// Both validation steps must succeed before the scanner accepts an
// enrollment. The fleet-root check guards against a forged package
// cert; the per-request signature check guards against replay of
// somebody else's registration request.

package s3scanner

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/section9labs/okesu/agent/s3transport"
)

// verifyRequestSignature checks that rr.Signature is a valid ECDSA
// signature over the canonical JSON of rr (with the Signature field
// stripped) using the public key from pkgCert.
func verifyRequestSignature(rr s3transport.RegistrationRequest, pkgCert *x509.Certificate) error {
	pub, ok := pkgCert.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return errors.New("package cert public key is not ECDSA")
	}
	sig, err := base64.StdEncoding.DecodeString(rr.Signature)
	if err != nil {
		return fmt.Errorf("decode signature: %w", err)
	}
	// Re-marshal the request without the signature field — same
	// canonical form the agent uses to sign.
	clean := rr
	clean.Signature = ""
	body, err := json.Marshal(clean)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	sum := sha256.Sum256(body)
	if !ecdsa.VerifyASN1(pub, sum[:], sig) {
		return errors.New("signature does not match package cert")
	}
	return nil
}

// certFingerprint returns the SHA-256 of the DER certificate bytes
// in lowercase hex. Same shape the db package's certPEMFingerprint
// helper expects.
func certFingerprint(c *x509.Certificate) string {
	sum := sha256.Sum256(c.Raw)
	const hex = "0123456789abcdef"
	out := make([]byte, len(sum)*2)
	for i, b := range sum {
		out[i*2] = hex[b>>4]
		out[i*2+1] = hex[b&0x0f]
	}
	return string(out)
}
