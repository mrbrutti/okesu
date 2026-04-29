// First-boot enrollment via the bucket. Called by `okesu enroll`
// after an operator-distributed package is unpacked. The flow:
//
//   1. Generate a UUID + ECDSA keypair for this node.
//   2. Build a CSR.
//   3. Upload registration/<uuid>.json to the CP's bucket prefix
//      with the supplied package signing cert + signature.
//   4. Poll registration-result/<uuid>.json until the CP responds.
//   5. Fetch the issued cert from cp/<cp-id>/nodes/<id>/cert.pem.
//   6. Persist node-id, key, cert, fingerprints to /etc/okesu/node-certs.
//
// All cryptography is standard library only — no external deps.

package s3transport

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/google/uuid"
)

// EnrollRequest is what an operator passes to Enroll() — typically
// loaded from /etc/okesu/package.json that the install script
// dropped on disk.
type EnrollRequest struct {
	Client *Client
	CPID   string

	// PackageID, PackageCertPEM, PackageKeyPEM are baked into the
	// package at generation time. The CP scanner uses the cert to
	// verify the request's signature, then re-verifies the cert
	// against the fleet trust anchor in the bucket.
	PackageID      int64
	PackageCertPEM string
	PackageKeyPEM  string

	// Hostname / agents-to-install — operator can override or let
	// the runtime discover from os.Hostname().
	Hostname        string
	AgentsRequested []string

	// CertDir is where cert.pem + node.key + node-uuid get written.
	CertDir string
}

// EnrollResult holds the durable identity the node uses from now on.
type EnrollResult struct {
	NodeID   int64
	NodeUUID string
	CertPEM  string
	KeyPEM   string
}

// Enroll runs the full first-boot flow. Idempotent — if a node-id
// is already cached at <CertDir>/node-id, returns the cached value
// without re-enrolling.
func Enroll(ctx context.Context, req EnrollRequest) (EnrollResult, error) {
	if req.Client == nil || req.CPID == "" {
		return EnrollResult{}, errors.New("enroll: client + cp_id required")
	}
	if req.PackageCertPEM == "" || req.PackageKeyPEM == "" {
		return EnrollResult{}, errors.New("enroll: package cert + key required")
	}
	if req.CertDir == "" {
		req.CertDir = "/etc/okesu/node-certs"
	}
	if err := os.MkdirAll(req.CertDir, 0o755); err != nil {
		return EnrollResult{}, fmt.Errorf("mkdir %s: %w", req.CertDir, err)
	}

	if cached, ok := readCachedIdentity(req.CertDir); ok {
		return cached, nil
	}

	if req.Hostname == "" {
		host, _ := os.Hostname()
		req.Hostname = host
	}

	// Generate ECDSA P-256 keypair + CSR.
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return EnrollResult{}, fmt.Errorf("ecdsa: %w", err)
	}
	nodeUUID := uuid.NewString()
	csrTpl := x509.CertificateRequest{
		Subject: pkix.Name{CommonName: nodeUUID},
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &csrTpl, priv)
	if err != nil {
		return EnrollResult{}, fmt.Errorf("csr: %w", err)
	}
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})

	// Build the request and sign it with the package private key
	// (parsed from the PEM material we got at install time).
	pkgKey, err := parsePackageKey(req.PackageKeyPEM)
	if err != nil {
		return EnrollResult{}, fmt.Errorf("parse package key: %w", err)
	}

	rr := RegistrationRequest{
		NodeUUID:        nodeUUID,
		Hostname:        req.Hostname,
		OS:              runtime.GOOS,
		Arch:            runtime.GOARCH,
		CSRPEM:          string(csrPEM),
		PackageID:       req.PackageID,
		AgentsRequested: req.AgentsRequested,
		SignedAt:        time.Now().UTC(),
		PackageCertPEM:  req.PackageCertPEM,
	}

	// Canonical JSON for signing — marshal without the Signature
	// field; deterministic field ordering (Go's json package sorts
	// keys for us implicitly via struct order; explicit empty
	// signature ensures the field's absence rather than presence).
	signBody, err := json.Marshal(rr)
	if err != nil {
		return EnrollResult{}, fmt.Errorf("marshal: %w", err)
	}
	sum := sha256.Sum256(signBody)
	sigDER, err := ecdsa.SignASN1(rand.Reader, pkgKey, sum[:])
	if err != nil {
		return EnrollResult{}, fmt.Errorf("sign: %w", err)
	}
	rr.Signature = base64.StdEncoding.EncodeToString(sigDER)
	finalBody, err := json.Marshal(rr)
	if err != nil {
		return EnrollResult{}, fmt.Errorf("marshal final: %w", err)
	}

	regKey := CPPrefix(req.CPID) + PathRegistrationDir + nodeUUID + ".json"
	if err := req.Client.Put(ctx, regKey, finalBody, "application/json"); err != nil {
		return EnrollResult{}, fmt.Errorf("upload registration: %w", err)
	}

	// Wait for the CP to respond. Poll cap covers the case where the
	// scanner's interval is high; in normal operation this completes
	// within ~30s.
	resultKey := CPPrefix(req.CPID) + PathRegistrationResultDir + nodeUUID + ".json"
	deadline := time.Now().Add(10 * time.Minute)
	for {
		body, err := req.Client.GetBytes(ctx, resultKey)
		if err == nil {
			var res RegistrationResult
			if err := json.Unmarshal(body, &res); err != nil {
				return EnrollResult{}, fmt.Errorf("decode result: %w", err)
			}
			if res.Error != "" {
				return EnrollResult{}, fmt.Errorf("cp rejected enrollment: %s", res.Error)
			}
			// Pull the issued cert + persist identity.
			certBytes, err := req.Client.GetBytes(ctx, res.CertPath)
			if err != nil {
				return EnrollResult{}, fmt.Errorf("fetch issued cert: %w", err)
			}
			ident := EnrollResult{
				NodeID:   res.NodeID,
				NodeUUID: nodeUUID,
				CertPEM:  string(certBytes),
				KeyPEM:   pemEncodeECKey(priv),
			}
			if err := writeCachedIdentity(req.CertDir, ident); err != nil {
				return EnrollResult{}, fmt.Errorf("persist identity: %w", err)
			}
			return ident, nil
		}
		if err != ErrNotFound {
			return EnrollResult{}, fmt.Errorf("poll registration result: %w", err)
		}
		if time.Now().After(deadline) {
			return EnrollResult{}, errors.New("enrollment timed out — CP scanner did not respond within 10 minutes")
		}
		select {
		case <-ctx.Done():
			return EnrollResult{}, ctx.Err()
		case <-time.After(15 * time.Second):
		}
	}
}

// readCachedIdentity returns the persisted identity from a previous
// successful enrollment if present. Empty/missing files yield ok=false.
func readCachedIdentity(dir string) (EnrollResult, bool) {
	idBytes, err := os.ReadFile(filepath.Join(dir, "node-id"))
	if err != nil {
		return EnrollResult{}, false
	}
	uuidBytes, err := os.ReadFile(filepath.Join(dir, "node-uuid"))
	if err != nil {
		return EnrollResult{}, false
	}
	cert, err := os.ReadFile(filepath.Join(dir, "client.crt"))
	if err != nil {
		return EnrollResult{}, false
	}
	key, err := os.ReadFile(filepath.Join(dir, "client.key"))
	if err != nil {
		return EnrollResult{}, false
	}
	var nodeID int64
	if _, err := fmt.Sscanf(string(idBytes), "%d", &nodeID); err != nil {
		return EnrollResult{}, false
	}
	return EnrollResult{
		NodeID:   nodeID,
		NodeUUID: string(uuidBytes),
		CertPEM:  string(cert),
		KeyPEM:   string(key),
	}, true
}

func writeCachedIdentity(dir string, r EnrollResult) error {
	files := []struct {
		name string
		body string
		mode os.FileMode
	}{
		{"node-id", fmt.Sprintf("%d\n", r.NodeID), 0o644},
		{"node-uuid", r.NodeUUID + "\n", 0o644},
		{"client.crt", r.CertPEM, 0o644},
		{"client.key", r.KeyPEM, 0o600},
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f.name), []byte(f.body), f.mode); err != nil {
			return fmt.Errorf("write %s: %w", f.name, err)
		}
	}
	return nil
}

func parsePackageKey(pemStr string) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, errors.New("no PEM block")
	}
	switch block.Type {
	case "EC PRIVATE KEY":
		return x509.ParseECPrivateKey(block.Bytes)
	case "PRIVATE KEY":
		any, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		ek, ok := any.(*ecdsa.PrivateKey)
		if !ok {
			return nil, errors.New("PKCS#8 key is not ECDSA")
		}
		return ek, nil
	}
	return nil, fmt.Errorf("unsupported PEM block %q", block.Type)
}

func pemEncodeECKey(k *ecdsa.PrivateKey) string {
	der, err := x509.MarshalECPrivateKey(k)
	if err != nil {
		return ""
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}))
}
