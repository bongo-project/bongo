package security

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateSelfSigned(t *testing.T) {
	certPEM, keyPEM, err := GenerateSelfSigned([]string{"localhost", "127.0.0.1", "bongo.example.com"})
	if err != nil {
		t.Fatalf("GenerateSelfSigned: %v", err)
	}

	// Parse the certificate.
	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("failed to decode cert PEM")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}

	// Check SANs.
	if len(cert.DNSNames) != 2 {
		t.Errorf("DNS names = %v, want [localhost bongo.example.com]", cert.DNSNames)
	}
	if len(cert.IPAddresses) != 1 || !cert.IPAddresses[0].Equal(net.ParseIP("127.0.0.1")) {
		t.Errorf("IP addresses = %v, want [127.0.0.1]", cert.IPAddresses)
	}

	// Verify it's a valid key pair.
	_, err = tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("X509KeyPair: %v", err)
	}
}

func TestLoadOrGenerate_ExistingFiles(t *testing.T) {
	dir := t.TempDir()
	certFile := filepath.Join(dir, "cert.pem")
	keyFile := filepath.Join(dir, "key.pem")

	// Generate and write files.
	certPEM, keyPEM, err := GenerateSelfSigned([]string{"localhost"})
	if err != nil {
		t.Fatalf("GenerateSelfSigned: %v", err)
	}

	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}

	tlsCfg, err := LoadOrGenerate(certFile, keyFile, []string{"localhost"})
	if err != nil {
		t.Fatalf("LoadOrGenerate: %v", err)
	}

	if len(tlsCfg.Certificates) != 1 {
		t.Errorf("certificates count = %d, want 1", len(tlsCfg.Certificates))
	}
}

func TestLoadOrGenerate_GeneratesFallback(t *testing.T) {
	dir := t.TempDir()
	certFile := filepath.Join(dir, "nonexistent-cert.pem")
	keyFile := filepath.Join(dir, "nonexistent-key.pem")

	tlsCfg, err := LoadOrGenerate(certFile, keyFile, []string{"localhost", "127.0.0.1"})
	if err != nil {
		t.Fatalf("LoadOrGenerate: %v", err)
	}

	if len(tlsCfg.Certificates) != 1 {
		t.Errorf("certificates count = %d, want 1", len(tlsCfg.Certificates))
	}
	if tlsCfg.MinVersion != tls.VersionTLS12 {
		t.Error("MinVersion not set to TLS 1.2")
	}
}

func TestLoadCAPool(t *testing.T) {
	dir := t.TempDir()
	caFile := filepath.Join(dir, "ca.pem")

	// Generate a self-signed cert to act as CA.
	certPEM, _, err := GenerateSelfSigned([]string{"ca.example.com"})
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(caFile, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}

	pool, err := LoadCAPool(caFile)
	if err != nil {
		t.Fatalf("LoadCAPool: %v", err)
	}

	if pool == nil {
		t.Fatal("pool is nil")
	}
}

func TestLoadCAPool_InvalidFile(t *testing.T) {
	dir := t.TempDir()
	caFile := filepath.Join(dir, "bad-ca.pem")

	if err := os.WriteFile(caFile, []byte("not a cert"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := LoadCAPool(caFile)
	if err == nil {
		t.Error("expected error for invalid CA file")
	}
}
