package smtp

import (
	"crypto/tls"
	"os"
	"path/filepath"
	"testing"

	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/security"
)

func TestNewServerServesCertThroughHolder(t *testing.T) {
	dir := t.TempDir()
	certPEM, keyPEM, err := security.GenerateSelfSigned([]string{"localhost"})
	if err != nil {
		t.Fatal(err)
	}
	certFile, keyFile := filepath.Join(dir, "c.pem"), filepath.Join(dir, "k.pem")
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		files      bool
		wantHolder bool
	}{
		{"cert files", true, true},
		{"no cert files", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.SMTPConfig{Port: 2525, SubmissionPort: 2587, SubmissionsPort: 2465}
			if tt.files {
				cfg.TLS.CertFile, cfg.TLS.KeyFile = certFile, keyFile
			}
			srv := NewServer(cfg, "", NewBackend(nil, nil, testLogger()), true, testLogger())
			if got := srv.CertHolder() != nil; got != tt.wantHolder {
				t.Fatalf("CertHolder non-nil = %v, want %v", got, tt.wantHolder)
			}
			if !tt.wantHolder {
				return
			}
			want, _ := srv.CertHolder().GetCertificate(nil)
			configs := map[string]*tls.Config{
				"mx":          srv.mx.TLSConfig,
				"submission":  srv.submission.TLSConfig,
				"submissions": srv.submissionsTLS,
			}
			for name, c := range configs {
				if c == nil || c.GetCertificate == nil || len(c.Certificates) != 0 {
					t.Errorf("%s must serve through GetCertificate only", name)
					continue
				}
				got, err := c.GetCertificate(nil)
				if err != nil || got != want {
					t.Errorf("%s does not share the holder's certificate (%v)", name, err)
				}
			}
		})
	}
}

// A configured but absent certificate (first boot) gets a self-signed fallback
// holder that picks the file up on Reload; an existing but invalid one keeps
// the old behaviour: warn and run without TLS.
func TestNewServerAbsentCertFallsBackAndReloads(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "c.pem"), filepath.Join(dir, "k.pem")
	cfg := &config.SMTPConfig{Port: 2525, SubmissionPort: 2587, SubmissionsPort: 2465}
	cfg.TLS.CertFile, cfg.TLS.KeyFile = certFile, keyFile

	srv := NewServer(cfg, "", NewBackend(nil, nil, testLogger()), false, testLogger())
	h := srv.CertHolder()
	if h == nil || !h.FileBacked() {
		t.Fatal("absent certificate must give a registered-for-reload fallback holder")
	}
	if srv.mx.TLSConfig == nil || srv.submission.TLSConfig == nil {
		t.Fatal("mx and submission must offer TLS through the fallback")
	}
	if srv.submissionsErr != nil || srv.submissionsTLS == nil {
		t.Fatalf("submissions must serve the fallback, err = %v", srv.submissionsErr)
	}
	before, _ := h.GetCertificate(nil)

	certPEM, keyPEM, err := security.GenerateSelfSigned([]string{"localhost"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if changed, err := h.Reload(); err != nil || !changed {
		t.Fatalf("Reload = %v, %v", changed, err)
	}
	after, _ := srv.mx.TLSConfig.GetCertificate(nil)
	if after == before {
		t.Fatal("mx did not pick up the certificate that appeared")
	}
}

func TestNewServerInvalidCertRunsWithoutTLS(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "c.pem"), filepath.Join(dir, "k.pem")
	for _, f := range []string{certFile, keyFile} {
		if err := os.WriteFile(f, []byte("junk"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &config.SMTPConfig{Port: 2525, SubmissionPort: 2587}
	cfg.TLS.CertFile, cfg.TLS.KeyFile = certFile, keyFile
	srv := NewServer(cfg, "", NewBackend(nil, nil, testLogger()), false, testLogger())
	if srv.CertHolder() != nil || srv.mx.TLSConfig != nil {
		t.Fatal("an existing invalid certificate must run without TLS, as before")
	}
}
