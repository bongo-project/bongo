package agent

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/security"
)

func certTestLogger() (*slog.Logger, *lockedBuffer) {
	buf := &lockedBuffer{}
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})), buf
}

// writeSelfSigned writes a fresh pair and gives both files a distinct mtime,
// because filesystem granularity can hide quick rewrites.
func writeSelfSigned(t *testing.T, certFile, keyFile string, mtime time.Time) {
	t.Helper()
	c, k, err := security.GenerateSelfSigned([]string{"localhost"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certFile, c, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, k, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{certFile, keyFile} {
		if err := os.Chtimes(f, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
}

func serialOf(t *testing.T, h *security.CertHolder) string {
	t.Helper()
	c, err := h.GetCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	return c.Leaf.SerialNumber.String()
}

func TestCertReloaderPoll(t *testing.T) {
	dir := t.TempDir()
	cf, kf := filepath.Join(dir, "c.pem"), filepath.Join(dir, "k.pem")
	base := time.Now().Add(-time.Hour)
	writeSelfSigned(t, cf, kf, base)
	h, err := security.LoadCertHolder("test", cf, kf)
	if err != nil {
		t.Fatal(err)
	}
	logger, buf := certTestLogger()
	var cr certReloader
	cr.register(h)
	cr.register(h) // duplicate ignored
	cr.register(nil)
	if len(cr.holders) != 1 {
		t.Fatalf("holders = %d, want 1", len(cr.holders))
	}
	first := serialOf(t, h)

	cr.pollOnce(logger)
	if strings.Contains(buf.String(), "tls:") {
		t.Fatalf("unchanged files logged: %s", buf.String())
	}

	// A bad change logs once across two polls and keeps the old certificate.
	if err := os.WriteFile(cf, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(cf, base.Add(time.Minute), base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	cr.pollOnce(logger)
	cr.pollOnce(logger)
	if n := strings.Count(buf.String(), "certificate reload failed"); n != 1 {
		t.Fatalf("failure logged %d times, want 1: %s", n, buf.String())
	}
	if !strings.Contains(buf.String(), "c.pem") || strings.Contains(buf.String(), "PRIVATE KEY") || strings.Contains(buf.String(), "BEGIN") {
		t.Fatalf("log must name the file and carry no PEM: %s", buf.String())
	}
	if serialOf(t, h) != first {
		t.Fatal("bad pair replaced the certificate")
	}

	// Fixing the files reloads on the next poll.
	writeSelfSigned(t, cf, kf, base.Add(2*time.Minute))
	cr.pollOnce(logger)
	if serialOf(t, h) == first {
		t.Fatal("fixed pair was not loaded")
	}
	if !strings.Contains(buf.String(), "tls: certificate reloaded") {
		t.Fatalf("no reload log: %s", buf.String())
	}
}

func TestCertReloaderRunStopsOnCancel(t *testing.T) {
	logger, _ := certTestLogger()
	var cr certReloader
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { cr.run(ctx, logger, 5*time.Millisecond); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not stop")
	}
}

func TestMailListenerTLSFallbackPicksUpFile(t *testing.T) {
	dir := t.TempDir()
	cf, kf := filepath.Join(dir, "c.pem"), filepath.Join(dir, "k.pem")
	logger, _ := certTestLogger()
	cfg := &config.Config{}
	cfg.Server.Role = "server"
	a := &Agent{Config: cfg, logger: logger}

	tlsCfg, err := a.mailListenerTLS("imap/pop3", cf, kf, []string{"localhost"})
	if err != nil {
		t.Fatal(err)
	}
	if tlsCfg.GetCertificate == nil || len(tlsCfg.Certificates) != 0 {
		t.Fatal("config must serve through GetCertificate")
	}
	// POP3's call shares the holder.
	if _, err := a.mailListenerTLS("imap/pop3", cf, kf, nil); err != nil {
		t.Fatal(err)
	}
	if len(a.certs.holders) != 1 {
		t.Fatalf("holders = %d, want 1", len(a.certs.holders))
	}
	fallback := serialOf(t, a.mailCert)

	writeSelfSigned(t, cf, kf, time.Now())
	a.certs.pollOnce(logger)
	if serialOf(t, a.mailCert) == fallback {
		t.Fatal("holder did not pick up the file that appeared")
	}
	got, err := tlsCfg.GetCertificate(nil)
	if err != nil || got.Leaf.SerialNumber.String() != serialOf(t, a.mailCert) {
		t.Fatal("earlier config does not see the swap")
	}
}

func TestMailListenerTLSExistingBadFileFails(t *testing.T) {
	dir := t.TempDir()
	cf, kf := filepath.Join(dir, "c.pem"), filepath.Join(dir, "k.pem")
	for _, f := range []string{cf, kf} {
		if err := os.WriteFile(f, []byte("junk"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	logger, _ := certTestLogger()
	cfg := &config.Config{}
	cfg.Server.Role = "server"
	a := &Agent{Config: cfg, logger: logger}
	if _, err := a.mailListenerTLS("imap/pop3", cf, kf, nil); err == nil {
		t.Fatal("expected a hard error for an existing, corrupt certificate")
	}
}

func TestAgentReloadWithoutConfigFileReloadsCerts(t *testing.T) {
	dir := t.TempDir()
	cf, kf := filepath.Join(dir, "c.pem"), filepath.Join(dir, "k.pem")
	writeSelfSigned(t, cf, kf, time.Now().Add(-time.Hour))
	h, err := security.LoadCertHolder("test", cf, kf)
	if err != nil {
		t.Fatal(err)
	}
	logger, _ := certTestLogger()
	a := &Agent{logger: logger}
	a.certs.register(h)
	first := serialOf(t, h)

	writeSelfSigned(t, cf, kf, time.Now())
	if err := a.Reload(context.Background()); err != nil {
		t.Fatalf("Reload = %v", err)
	}
	if serialOf(t, h) == first {
		t.Fatal("SIGHUP did not reload the certificate")
	}
}

// The web listener serves through the reloadable holder: a swapped file reaches
// the served certificate after a reload, with no new config built.
func TestWebTLSConfigServesThroughHolder(t *testing.T) {
	dir := t.TempDir()
	cf, kf := filepath.Join(dir, "c.pem"), filepath.Join(dir, "k.pem")
	writeSelfSigned(t, cf, kf, time.Now().Add(-time.Hour))
	logger, _ := certTestLogger()
	cfg := &config.Config{}
	cfg.Server.Role = "server"
	cfg.Web.TLS.CertFile, cfg.Web.TLS.KeyFile = cf, kf
	a := &Agent{Config: cfg, logger: logger}

	tlsCfg, err := a.webTLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	if tlsCfg == nil || tlsCfg.GetCertificate == nil || len(tlsCfg.Certificates) != 0 {
		t.Fatal("web config must serve through GetCertificate only")
	}
	if len(a.certs.holders) != 1 {
		t.Fatalf("web holder not registered for reload: %d holders", len(a.certs.holders))
	}
	first, err := tlsCfg.GetCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}

	writeSelfSigned(t, cf, kf, time.Now())
	a.certs.reloadAll(logger)
	second, err := tlsCfg.GetCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.Leaf.SerialNumber.Cmp(second.Leaf.SerialNumber) == 0 {
		t.Fatal("web listener still serves the old certificate after reload")
	}
}

func TestWebTLSConfigPlainHTTPAndMissingFile(t *testing.T) {
	logger, _ := certTestLogger()
	cfg := &config.Config{}
	cfg.Server.Role = "server"
	cfg.Web.PlainHTTP = true
	a := &Agent{Config: cfg, logger: logger}
	if c, err := a.webTLSConfig(); c != nil || err != nil {
		t.Fatalf("plain_http = %v, %v; want nil, nil", c, err)
	}
	cfg.Web.PlainHTTP = false
	cfg.Web.TLS.CertFile = filepath.Join(t.TempDir(), "absent.pem")
	cfg.Web.TLS.KeyFile = filepath.Join(t.TempDir(), "absent-key.pem")
	if _, err := a.webTLSConfig(); err == nil {
		t.Fatal("a configured web certificate that cannot load must fail startup")
	}
}

// In the agent role Shutdown stops the cert-reload worker and waits for it.
func TestShutdownAgentStopsCertReloadWorker(t *testing.T) {
	logger, _ := certTestLogger()
	cfg := &config.Config{}
	cfg.Server.Role = "agent"
	a := &Agent{Config: cfg, logger: logger}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	certsCtx, certsCancel := context.WithCancel(ctx)
	a.certsCancel = certsCancel
	a.goWorker("cert-reload", func() { a.certs.run(certsCtx, a.logger, time.Hour) })

	if err := a.shutdownAgent(); err != nil {
		t.Fatal(err)
	}
	if live := a.liveWorkers(); len(live) != 0 {
		t.Fatalf("workers still live after shutdown: %v", live)
	}
}
