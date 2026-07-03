package security

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"strings"
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

func TestLoadCert(t *testing.T) {
	dir := t.TempDir()
	certFile := filepath.Join(dir, "cert.pem")
	keyFile := filepath.Join(dir, "key.pem")

	// Generate a real cert pair for the "valid" cases.
	certPEM, keyPEM, err := GenerateSelfSigned([]string{"localhost"})
	if err != nil {
		t.Fatalf("GenerateSelfSigned: %v", err)
	}

	tests := []struct {
		name      string
		setup     func()
		certFile  string
		keyFile   string
		wantErr   bool
		errSubstr string
	}{
		{
			name: "valid cert and key loads successfully",
			setup: func() {
				if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
					t.Fatal(err)
				}
			},
			certFile: certFile,
			keyFile:  keyFile,
			wantErr:  false,
		},
		{
			name:      "missing cert file returns error",
			setup:     func() {},
			certFile:  filepath.Join(dir, "nonexistent-cert.pem"),
			keyFile:   filepath.Join(dir, "nonexistent-key.pem"),
			wantErr:   true,
			errSubstr: "loading TLS key pair",
		},
		{
			name: "corrupt cert file returns error",
			setup: func() {
				if err := os.WriteFile(certFile, []byte("not a cert"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
					t.Fatal(err)
				}
			},
			certFile:  certFile,
			keyFile:   keyFile,
			wantErr:   true,
			errSubstr: "loading TLS key pair",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Reset the files before each sub-test.
			_ = os.Remove(certFile)
			_ = os.Remove(keyFile)
			tc.setup()

			cfg, err := LoadCert(tc.certFile, tc.keyFile)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tc.errSubstr != "" && !strings.Contains(err.Error(), tc.errSubstr) {
					t.Errorf("error %q does not contain %q", err.Error(), tc.errSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(cfg.Certificates) != 1 {
				t.Errorf("certificates count = %d, want 1", len(cfg.Certificates))
			}
			if cfg.MinVersion != tls.VersionTLS12 {
				t.Error("MinVersion not set to TLS 1.2")
			}
		})
	}
}

func TestSelfSignedTLSConfig(t *testing.T) {
	tests := []struct {
		name  string
		hosts []string
	}{
		{"dns only", []string{"localhost", "bongo.example.com"}},
		{"ip only", []string{"127.0.0.1"}},
		{"mixed", []string{"localhost", "127.0.0.1"}},
		{"empty hosts", []string{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := SelfSignedTLSConfig(tc.hosts)
			if err != nil {
				t.Fatalf("SelfSignedTLSConfig(%v): %v", tc.hosts, err)
			}
			if len(cfg.Certificates) != 1 {
				t.Errorf("certificates count = %d, want 1", len(cfg.Certificates))
			}
			if cfg.MinVersion != tls.VersionTLS12 {
				t.Error("MinVersion not set to TLS 1.2")
			}
		})
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
