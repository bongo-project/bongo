package agent

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"log/slog"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/acmecert"
	"github.com/bongo-project/bongo/internal/acmecert/acmetest"
	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/netx"
	"github.com/bongo-project/bongo/internal/security"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// acmeTestAgent starts a single-node agent with ACME on, talking to ca. The
// dev role keeps the test self-contained (in-memory Raft); Validate, which
// refuses ACME outside the server role, is not part of NewAgent.
func acmeTestAgent(t *testing.T, ca *acmetest.Server, logs *syncBuf) *Agent {
	t.Helper()
	cfg := config.Defaults()
	isolatePorts(t, cfg)
	cfg.Server.Role = "dev"
	cfg.SMTP.Enabled = true
	cfg.IMAP.Enabled = true
	cfg.POP3.Enabled = true
	cfg.Web.TLS.ACME = &config.ACMEConfig{
		Enabled:      true,
		Domains:      []string{"mail.example.com"},
		DirectoryURL: ca.DirectoryURL(),
	}
	a, err := NewAgent(cfg, slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	// Dev mode forces plain HTTP; a real node (server role) would not.
	a.Config.Web.PlainHTTP = false
	a.acmeTest = &acmeTestHooks{
		client:    ca.Client(),
		settleSet: true,
		first:     50 * time.Millisecond,
		tick:      200 * time.Millisecond,
		syncEvery: 100 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	t.Cleanup(func() {
		if t.Failed() {
			for _, l := range strings.Split(logs.String(), "\n") {
				if strings.Contains(l, "acme") || strings.Contains(l, "web") || strings.Contains(l, "ERROR") {
					t.Log(l)
				}
			}
		}
	})
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			t.Error("agent did not stop")
		}
	})
	return a
}

// serialAt dials addr with implicit TLS and returns the leaf serial in hex.
func serialAt(t *testing.T, addr string) string {
	t.Helper()
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp", addr, &tls.Config{
		ServerName:         "mail.example.com",
		InsecureSkipVerify: true, // #nosec G402 -- reading the served serial only
		MinVersion:         tls.VersionTLS12,
	})
	if err != nil {
		return ""
	}
	defer conn.Close()
	return conn.ConnectionState().PeerCertificates[0].SerialNumber.Text(16)
}

// starttlsSerial connects to an SMTP listener, upgrades with STARTTLS and
// returns the leaf serial in hex, or "" on any failure.
func starttlsSerial(addr string) string {
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		return ""
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	c, err := smtp.NewClient(conn, "mail.example.com")
	if err != nil {
		return ""
	}
	defer func() { _ = c.Close() }()
	if err := c.StartTLS(&tls.Config{
		ServerName:         "mail.example.com",
		InsecureSkipVerify: true, // #nosec G402 -- reading the served serial only
		MinVersion:         tls.VersionTLS12,
	}); err != nil {
		return ""
	}
	st, ok := c.TLSConnectionState()
	if !ok || len(st.PeerCertificates) == 0 {
		return ""
	}
	return st.PeerCertificates[0].SerialNumber.Text(16)
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestACMEIssuesAndServesEveryListener(t *testing.T) {
	var webAddr string
	ca := acmetest.New(t, func(string) string { return webAddr })
	var logs syncBuf
	a := acmeTestAgent(t, ca, &logs)
	webAddr = net.JoinHostPort("127.0.0.1", strconv.Itoa(a.Config.Web.Port))

	st := acmeStore{a}
	eventually(t, "the certificate to be stored", func() bool {
		rec, _, _ := acmecert.LoadCertificate(st)
		return rec != nil
	})
	issued := ca.Issued()
	if len(issued) != 1 {
		t.Fatalf("CA issued %d certificates, want 1", len(issued))
	}
	want := issued[0].SerialNumber.Text(16)

	// Web, SMTP (implicit TLS submission), IMAP and POP3 all serve the new serial.
	addrs := map[string]string{
		"web":         webAddr,
		"submissions": net.JoinHostPort("127.0.0.1", strconv.Itoa(a.Config.SMTP.SubmissionsPort)),
		"imaps":       net.JoinHostPort("127.0.0.1", strconv.Itoa(a.Config.IMAP.PortTLS)),
		"pop3s":       net.JoinHostPort("127.0.0.1", strconv.Itoa(a.Config.POP3.PortTLS)),
	}
	for name, addr := range addrs {
		eventually(t, name+" to serve the issued certificate", func() bool { return serialAt(t, addr) == want })
	}

	// MX port: the certificate is offered through STARTTLS too.
	mx := net.JoinHostPort("127.0.0.1", strconv.Itoa(a.Config.SMTP.Port))
	eventually(t, "the MX STARTTLS handshake to serve the issued certificate", func() bool {
		return starttlsSerial(mx) == want
	})

	// The CA validated through the node's own web listener over TLS-ALPN-01.
	if got := ca.Validated(); len(got) != 1 || got[0] != "mail.example.com" {
		t.Errorf("validated = %v", got)
	}

	// The challenge entries are deleted again, and the keys are in Pebble.
	eventually(t, "the challenge to be deleted", func() bool {
		_, err := a.Store.Get([]byte(acmecert.ChallengeKey("mail.example.com")))
		return errors.Is(err, store.ErrNotFound)
	})
	for _, k := range []string{acmecert.CertKey, acmecert.AccountKey(ca.DirectoryURL())} {
		if _, err := a.Store.Get([]byte(k)); err != nil {
			t.Errorf("key %s not in Pebble: %v", k, err)
		}
	}

	// A second node fed the same Pebble picks the certificate up through its
	// sync pass: its holders start on a self-signed certificate.
	selfCfg, err := security.SelfSignedTLSConfig([]string{"mail.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	other := &acmeState{
		store: st,
		web:   security.NewStaticCertHolder("web", &selfCfg.Certificates[0]),
		smtp:  security.NewStaticCertHolder("smtp", &selfCfg.Certificates[0]),
		mail:  security.NewStaticCertHolder("imap/pop3", &selfCfg.Certificates[0]),
	}
	if other.serial() == want {
		t.Fatal("precondition: the second node must not serve the issued certificate yet")
	}
	a.syncACMEOnce(other)
	for name, h := range map[string]*security.CertHolder{"web": other.web, "smtp": other.smtp, "mail": other.mail} {
		if got := h.Current().Leaf.SerialNumber.Text(16); got != want {
			t.Errorf("second node %s serves %s, want %s", name, got, want)
		}
	}

	// Nothing secret reached the log.
	out := logs.String()
	if strings.Contains(out, "PRIVATE KEY") {
		t.Error("log contains a private key marker")
	}
	for _, k := range []string{acmecert.CertKey, acmecert.AccountKey(ca.DirectoryURL())} {
		raw, _ := a.Store.Get([]byte(k))
		var c bongov1.AcmeCertificate
		var acct bongov1.AcmeAccount
		var keyDER []byte
		if proto.Unmarshal(raw, &c) == nil && len(c.KeyDer) > 0 {
			keyDER = c.KeyDer
		} else if proto.Unmarshal(raw, &acct) == nil {
			keyDER = acct.KeyDer
		}
		if len(keyDER) == 0 {
			continue
		}
		if strings.Contains(out, base64.StdEncoding.EncodeToString(keyDER)) || strings.Contains(out, base64.RawURLEncoding.EncodeToString(keyDER)) {
			t.Errorf("log contains the key stored under %s", k)
		}
	}
	for _, tok := range ca.Tokens() {
		if strings.Contains(out, tok) {
			t.Error("log contains a challenge token")
		}
	}
}

func TestACMEFailureKeepsServingTheOldCertificate(t *testing.T) {
	var webAddr string
	ca := acmetest.New(t, func(string) string { return webAddr })
	ca.SetFailing(true)
	var logs syncBuf
	a := acmeTestAgent(t, ca, &logs)
	webAddr = net.JoinHostPort("127.0.0.1", strconv.Itoa(a.Config.Web.Port))

	eventually(t, "the failed validation", func() bool { return len(ca.Failures()) > 0 })
	eventually(t, "the failure to be logged", func() bool { return strings.Contains(logs.String(), "issuance failed") })

	if rec, _, _ := acmecert.LoadCertificate(acmeStore{a}); rec != nil {
		t.Error("a certificate was stored although validation failed")
	}
	if len(ca.Issued()) != 0 {
		t.Error("the CA issued a certificate")
	}
	// Still serving the self-signed fallback it started with.
	eventually(t, "the web listener to answer", func() bool { return serialAt(t, webAddr) != "" })
	if got, want := serialAt(t, webAddr), a.acmeSt.serial(); got != want {
		t.Errorf("web serves %s, holder says %s", got, want)
	}
}

func TestACMEOffByDefault(t *testing.T) {
	cfg := config.Defaults()
	isolatePorts(t, cfg)
	cfg.Server.Role = "dev"
	a, err := NewAgent(cfg, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Shutdown() }()
	if a.acme() != nil || a.acmeWebTLS() != nil || a.acmeStatusReader() != nil {
		t.Error("ACME wiring must be absent when the block is not enabled")
	}
}

// TestACMEClientRefusesPrivateNetworks reads the client the agent really
// builds when no test hook is set: the CA must not be reachable on a private
// address, and the production path has no hook.
func TestACMEClientRefusesPrivateNetworks(t *testing.T) {
	a := &Agent{}
	c := a.acmeHTTPClient()
	_, err := c.Get("https://127.0.0.1:443/directory")
	if !errors.Is(err, netx.ErrForbiddenAddress) {
		t.Fatalf("err = %v, want ErrForbiddenAddress", err)
	}
}

// TestACMEStatusWarnsOnUnloadableRecord: a stored record that does not load
// (garbage, or a key that does not match) must not read as issued.
func TestACMEStatusWarnsOnUnloadableRecord(t *testing.T) {
	cfg := config.Defaults()
	isolatePorts(t, cfg)
	cfg.Server.Role = "dev"
	cfg.Web.TLS.ACME = &config.ACMEConfig{Enabled: true, Domains: []string{"mail.example.com"}}
	a, err := NewAgent(cfg, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Shutdown() }()
	st := a.acme()
	if st == nil {
		t.Fatal("ACME state not built")
	}
	reader := acmeStatus{st: st}

	if _, issued, err := reader.ACMEExpiry(); issued || err != nil {
		t.Fatalf("empty store: issued=%v err=%v", issued, err)
	}

	// A record with a plausible NotAfter but no usable chain or key.
	raw, err := proto.Marshal(&bongov1.AcmeCertificate{
		ChainDer: [][]byte{{1, 2, 3}}, KeyDer: []byte{4}, NotAfter: time.Now().Add(60 * 24 * time.Hour).Unix(),
		Domains: []string{"mail.example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Set([]byte(acmecert.CertKey), raw); err != nil {
		t.Fatal(err)
	}
	if _, issued, err := reader.ACMEExpiry(); issued || err == nil {
		t.Errorf("unloadable record: issued=%v err=%v, want not issued with an error", issued, err)
	}
}
