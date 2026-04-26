package sshdeploy

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net"

	"golang.org/x/crypto/ssh"
)

// HostKeyMismatchError is returned by the verification policy when the
// target's offered host key differs from the pinned fingerprint.
//
// Carries enough context that the API + UI can render a useful warning
// (which agent / which node / what was expected vs presented).
type HostKeyMismatchError struct {
	NodeName    string
	NodeID      int64
	Expected    string
	Got         string
	GotKeyType  string
	GotPubKey   string
}

func (e *HostKeyMismatchError) Error() string {
	return fmt.Sprintf(
		"host key mismatch for node %q (id=%d): expected %s, got %s — refusing to connect. "+
			"To re-establish trust after legitimate key rotation, an admin must clear the pinned key for this node.",
		e.NodeName, e.NodeID, e.Expected, e.Got)
}

// HostKeyPolicy is the abstraction over a TOFU + verify host-key flow.
// The CP wires up an implementation backed by the known_hosts table; tests
// can plug in a stub.
type HostKeyPolicy interface {
	// Lookup returns the pinned fingerprint for the node, or empty if none.
	Lookup() (fingerprint string, err error)
	// Pin records a newly-trusted fingerprint (TOFU on first connect).
	Pin(keyType, fingerprint, publicKey string) error
}

// VerifyingHostKeyCallback returns an ssh.HostKeyCallback that:
//
//   - Computes the SHA-256 fingerprint of the offered host key
//   - If the policy has no pin yet, records this one (TOFU)
//   - If the policy has a pin and it matches, accepts the connection
//   - If the policy has a pin and it differs, returns HostKeyMismatchError
//
// nodeName / nodeID are passed through into the error type so callers can
// render contextual UI without re-looking-up.
func VerifyingHostKeyCallback(policy HostKeyPolicy, nodeName string, nodeID int64) ssh.HostKeyCallback {
	return func(_ string, _ net.Addr, key ssh.PublicKey) error {
		fp := fingerprintSHA256(key)
		pubKey := string(ssh.MarshalAuthorizedKey(key))

		pinned, err := policy.Lookup()
		if err != nil {
			return fmt.Errorf("known_hosts lookup: %w", err)
		}
		if pinned == "" {
			// TOFU — first contact, accept and pin.
			if err := policy.Pin(key.Type(), fp, pubKey); err != nil {
				return fmt.Errorf("known_hosts pin: %w", err)
			}
			return nil
		}
		if pinned != fp {
			return &HostKeyMismatchError{
				NodeName:   nodeName,
				NodeID:     nodeID,
				Expected:   pinned,
				Got:        fp,
				GotKeyType: key.Type(),
				GotPubKey:  pubKey,
			}
		}
		return nil
	}
}

// fingerprintSHA256 returns the OpenSSH-style SHA256 fingerprint
// ("SHA256:<base64-no-padding>") for a public key.
func fingerprintSHA256(key ssh.PublicKey) string {
	sum := sha256.Sum256(key.Marshal())
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

// PinPolicyAdapter wraps simple closures into a HostKeyPolicy. Used by the
// CP to bind a node-id-specific lookup/pin to the generic callback.
type PinPolicyAdapter struct {
	LookupFn func() (string, error)
	PinFn    func(keyType, fingerprint, publicKey string) error
}

func (p PinPolicyAdapter) Lookup() (string, error) { return p.LookupFn() }
func (p PinPolicyAdapter) Pin(t, f, pk string) error {
	return p.PinFn(t, f, pk)
}
