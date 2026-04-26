package controlplane

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

// CA bundles a CA certificate with its signing key. Used to:
//   - sign the mgmt-plane server certificate (so daemons can verify it via ca.crt)
//   - issue client certificates for individual agents (CN = agent name)
type CA struct {
	Cert     *x509.Certificate
	CertPEM  []byte
	Key      *ecdsa.PrivateKey
	CertPath string
	KeyPath  string
}

// EnsureCA loads (or creates) the mTLS CA. If both cfg.CACertFile and
// cfg.CAKeyFile are set, those are used. Otherwise files at <dbDir>/ca.crt
// and <dbDir>/ca.key are loaded; if absent, a new self-signed CA is generated.
func EnsureCA(cfg Config) (*CA, error) {
	certPath := cfg.CACertFile
	keyPath := cfg.CAKeyFile
	if certPath == "" || keyPath == "" {
		dir := filepath.Dir(cfg.DBPath)
		certPath = filepath.Join(dir, "ca.crt")
		keyPath = filepath.Join(dir, "ca.key")
	}

	// Try to load existing CA.
	if cert, key, err := loadCertAndKey(certPath, keyPath); err == nil {
		certPEM, _ := os.ReadFile(certPath)
		return &CA{Cert: cert, CertPEM: certPEM, Key: key, CertPath: certPath, KeyPath: keyPath}, nil
	}

	// Generate new CA.
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("ca key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("serial: %w", err)
	}

	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "Okesu CP CA",
			Organization: []string{"Okesu"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		return nil, fmt.Errorf("create ca cert: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(certPath, certPEM, 0644); err != nil {
		return nil, fmt.Errorf("write ca cert: %w", err)
	}

	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return nil, fmt.Errorf("marshal ca key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		return nil, fmt.Errorf("write ca key: %w", err)
	}

	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("parse ca cert: %w", err)
	}
	return &CA{Cert: cert, CertPEM: certPEM, Key: priv, CertPath: certPath, KeyPath: keyPath}, nil
}

// IssueServerCert generates an mTLS-server certificate signed by the CA.
// The cert is valid for the given DNS names (e.g., "localhost", "okesu-cp.example.com").
// Returns PEM-encoded cert and key bytes.
func (ca *CA) IssueServerCert(dnsNames []string) (certPEM, keyPEM []byte, err error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, err
	}
	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "okesu-cp-mgmt"},
		DNSNames:     dnsNames,
		NotBefore:    time.Now().Add(-1 * time.Hour),
		NotAfter:     time.Now().Add(2 * 365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, ca.Cert, &priv.PublicKey, ca.Key)
	if err != nil {
		return nil, nil, err
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return nil, nil, err
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}

// IssueClientCert generates a client certificate with CN = agentName,
// signed by the CA. Used by `okesu-cp issue-cert`.
func (ca *CA) IssueClientCert(agentName string) (certPEM, keyPEM []byte, err error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, err
	}
	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   agentName,
			Organization: []string{"Okesu Agents"},
		},
		NotBefore:   time.Now().Add(-1 * time.Hour),
		NotAfter:    time.Now().Add(2 * 365 * 24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, ca.Cert, &priv.PublicKey, ca.Key)
	if err != nil {
		return nil, nil, err
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return nil, nil, err
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}

// loadCertAndKey reads a PEM cert+key pair from disk.
func loadCertAndKey(certPath, keyPath string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	certBytes, err := os.ReadFile(certPath)
	if err != nil {
		return nil, nil, err
	}
	block, _ := pem.Decode(certBytes)
	if block == nil {
		return nil, nil, fmt.Errorf("invalid cert PEM in %s", certPath)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, nil, err
	}

	keyBytes, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, nil, err
	}
	keyBlock, _ := pem.Decode(keyBytes)
	if keyBlock == nil {
		return nil, nil, fmt.Errorf("invalid key PEM in %s", keyPath)
	}
	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, nil, err
	}
	return cert, key, nil
}
