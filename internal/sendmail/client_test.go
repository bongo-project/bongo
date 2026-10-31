package sendmail

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-sasl"
	gosmtp "github.com/emersion/go-smtp"
)

const (
	testUser = "alice@example.test"
	testPass = "hunter2-secret"
)

// fakeServer is a go-smtp server recording what the client sent and failing on
// demand at a chosen phase.
type fakeServer struct {
	mu           sync.Mutex
	authAttempts int
	from         string
	rcpts        []string
	data         string
	dataCalled   bool

	mailErr, dataErr, authErr error
	rcptErr                   map[string]error
}

func (f *fakeServer) NewSession(*gosmtp.Conn) (gosmtp.Session, error) { return &fakeSession{f}, nil }

type fakeSession struct{ f *fakeServer }

func (s *fakeSession) AuthMechanisms() []string { return []string{sasl.Plain} }
func (s *fakeSession) Auth(string) (sasl.Server, error) {
	return sasl.NewPlainServer(func(_, user, pass string) error {
		s.f.mu.Lock()
		defer s.f.mu.Unlock()
		s.f.authAttempts++
		if s.f.authErr != nil {
			return s.f.authErr
		}
		if user != testUser || pass != testPass {
			return &gosmtp.SMTPError{Code: 535, EnhancedCode: gosmtp.EnhancedCode{5, 7, 8}, Message: "Authentication credentials invalid"}
		}
		return nil
	}), nil
}
func (s *fakeSession) Reset()        {}
func (s *fakeSession) Logout() error { return nil }
func (s *fakeSession) Mail(from string, _ *gosmtp.MailOptions) error {
	s.f.mu.Lock()
	defer s.f.mu.Unlock()
	s.f.from = from
	return s.f.mailErr
}
func (s *fakeSession) Rcpt(to string, _ *gosmtp.RcptOptions) error {
	s.f.mu.Lock()
	defer s.f.mu.Unlock()
	if err := s.f.rcptErr[to]; err != nil {
		return err
	}
	s.f.rcpts = append(s.f.rcpts, to)
	return nil
}
func (s *fakeSession) Data(r io.Reader) error {
	b, _ := io.ReadAll(r)
	s.f.mu.Lock()
	defer s.f.mu.Unlock()
	s.f.dataCalled = true
	s.f.data = string(b)
	return s.f.dataErr
}

func (f *fakeServer) snapshot() fakeServer {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fakeServer{authAttempts: f.authAttempts, from: f.from, rcpts: append([]string(nil), f.rcpts...), data: f.data, dataCalled: f.dataCalled}
}

func testCert(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pool
}

// startFake serves f. mode is "implicit" (TLS from the first byte), "starttls"
// or "plain" (no TLS at all, like dev's 587). It returns the port and the pool
// that trusts the server certificate.
func startFake(t *testing.T, f *fakeServer, mode string) (int, *x509.CertPool) {
	t.Helper()
	cert, pool := testCert(t)
	tlsCfg := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := gosmtp.NewServer(f)
	srv.Domain = "localhost"
	srv.ReadTimeout = 10 * time.Second
	srv.WriteTimeout = 10 * time.Second
	switch mode {
	case "implicit":
		ln = tls.NewListener(ln, tlsCfg)
	case "starttls":
		srv.TLSConfig = tlsCfg
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return ln.Addr().(*net.TCPAddr).Port, pool
}

func writeConf(t *testing.T, port int, tlsMode, password string) string {
	t.Helper()
	body := fmt.Sprintf("host = \"127.0.0.1\"\nport = %d\ntls = %q\nusername = %q\npassword = %q\n", port, tlsMode, testUser, password)
	p := filepath.Join(t.TempDir(), "sendmail.conf")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func runSendmail(t *testing.T, pool *x509.CertPool, stdin string, args ...string) (int, string) {
	t.Helper()
	var stderr bytes.Buffer
	code := RunWith(args, strings.NewReader(stdin), &stderr, Env{Now: func() time.Time { return testNow }, RootCAs: pool})
	return code, stderr.String()
}

const simpleMsg = "To: bob@example.net\nSubject: hi\n\nbody\n"

func TestSubmitDelivers(t *testing.T) {
	for _, mode := range []string{"implicit", "starttls"} {
		t.Run(mode, func(t *testing.T) {
			f := &fakeServer{}
			port, pool := startFake(t, f, mode)
			conf := writeConf(t, port, mode, testPass)
			msg := "To: bob@example.net\nCc: carol@example.net\nBcc: dave@example.net\nSubject: hi\n\n.leading dot\n..two\nlast\n"
			code, stderr := runSendmail(t, pool, msg, "--config", conf, "-t", "-i")
			if code != ExitOK {
				t.Fatalf("exit %d: %s", code, stderr)
			}
			got := f.snapshot()
			if got.from != testUser {
				t.Errorf("envelope sender = %q", got.from)
			}
			if strings.Join(got.rcpts, ",") != "bob@example.net,carol@example.net,dave@example.net" {
				t.Errorf("rcpts = %v", got.rcpts)
			}
			if strings.Contains(strings.ToLower(got.data), "bcc") || strings.Contains(got.data, "dave") {
				t.Errorf("Bcc reached the server:\n%s", got.data)
			}
			// The server un-stuffs what the client stuffed. Stuffing twice
			// would turn ".leading dot" into "..leading dot".
			if !strings.Contains(got.data, "\n.leading dot\n..two\nlast\n") && !strings.Contains(got.data, "\r\n.leading dot\r\n..two\r\nlast\r\n") {
				t.Errorf("body dot handling wrong:\n%q", got.data)
			}
			for _, h := range []string{"From: <" + testUser + ">", "Date: ", "Message-ID: <"} {
				if !strings.Contains(got.data, h) {
					t.Errorf("missing %q in\n%s", h, got.data)
				}
			}
		})
	}
}

func TestSubmitDotTerminatorWithoutI(t *testing.T) {
	f := &fakeServer{}
	port, pool := startFake(t, f, "implicit")
	code, stderr := runSendmail(t, pool, "To: b@example.net\n\nkeep\n.\ndrop\n", "--config", writeConf(t, port, "implicit", testPass), "-t")
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	d := f.snapshot().data
	if !strings.Contains(d, "keep") || strings.Contains(d, "drop") {
		t.Errorf("data = %q", d)
	}
}

// Debian and cronie style invocations with an unqualified sender and From.
func TestSubmitCronInvocations(t *testing.T) {
	tests := []struct {
		name string
		args []string
		from string
	}{
		{"debian", []string{"-FCronDaemon", "-i", "-B8BITMIME", "-oem", "-oi", "bob@example.net"}, "root (Cron Daemon)"},
		{"cronie", []string{"-f", "root", "-oi", "bob@example.net"}, "root (Cron Daemon)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeServer{}
			port, pool := startFake(t, f, "implicit")
			conf := writeConf(t, port, "implicit", testPass)
			code, stderr := runSendmail(t, pool, "From: "+tt.from+"\nTo: bob@example.net\nSubject: Cron\n\nout\n", append([]string{"--config", conf}, tt.args...)...)
			if code != ExitOK {
				t.Fatalf("exit %d: %s", code, stderr)
			}
			got := f.snapshot()
			if got.from != testUser {
				t.Errorf("envelope sender = %q, want the account", got.from)
			}
			if !strings.Contains(got.data, "From: \"Cron Daemon\" <"+testUser+">") {
				t.Errorf("From not rewritten:\n%s", got.data)
			}
		})
	}
}

func TestSubmitStartTLSRequired(t *testing.T) {
	f := &fakeServer{}
	port, pool := startFake(t, f, "plain")
	code, stderr := runSendmail(t, pool, simpleMsg, "--config", writeConf(t, port, "starttls", testPass), "-t")
	if code != ExitUnavailable {
		t.Fatalf("exit %d, want 69: %s", code, stderr)
	}
	got := f.snapshot()
	if got.authAttempts != 0 || got.dataCalled {
		t.Errorf("client went on without TLS: auth attempts %d, data %v", got.authAttempts, got.dataCalled)
	}
	if strings.Contains(stderr, testPass) {
		t.Error("stderr contains the password")
	}
}

func TestSubmitCertificateVerified(t *testing.T) {
	f := &fakeServer{}
	port, _ := startFake(t, f, "implicit")
	// No RootCAs: the self-signed certificate must not verify.
	code, stderr := runSendmail(t, nil, simpleMsg, "--config", writeConf(t, port, "implicit", testPass), "-t")
	if code != ExitUnavailable {
		t.Fatalf("exit %d, want 69: %s", code, stderr)
	}
	if got := f.snapshot(); got.authAttempts != 0 {
		t.Error("credentials were sent over an unverified connection")
	}
}

func TestSubmitInsecureSkipVerifyOnLoopback(t *testing.T) {
	f := &fakeServer{}
	port, _ := startFake(t, f, "implicit")
	conf := writeConf(t, port, "implicit", testPass)
	if err := os.WriteFile(conf, append(mustRead(t, conf), []byte("insecure_skip_verify = true\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, stderr := runSendmail(t, nil, simpleMsg, "--config", conf, "-t"); code != ExitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSubmitConnectionRefused(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	if code, _ := runSendmail(t, nil, simpleMsg, "--config", writeConf(t, port, "implicit", testPass), "-t"); code != ExitUnavailable {
		t.Fatalf("exit %d, want 69", code)
	}
}

func TestSubmitWrongPasswordNeverEchoed(t *testing.T) {
	f := &fakeServer{}
	port, pool := startFake(t, f, "implicit")
	const wrong = "wrong-pw-xyzzy"
	code, stderr := runSendmail(t, pool, simpleMsg, "--config", writeConf(t, port, "implicit", wrong), "-t", "-v")
	if code != ExitNoPerm {
		t.Fatalf("exit %d, want 77: %s", code, stderr)
	}
	if !strings.Contains(stderr, "535") || !strings.Contains(stderr, "Authentication credentials invalid") {
		t.Errorf("server reply missing from stderr: %q", stderr)
	}
	for _, secret := range []string{wrong, testPass} {
		if strings.Contains(stderr, secret) {
			t.Errorf("stderr contains the password: %q", stderr)
		}
	}
}

func TestSubmitExitCodesByPhase(t *testing.T) {
	smtpErr := func(code int, enh gosmtp.EnhancedCode, msg string) error {
		return &gosmtp.SMTPError{Code: code, EnhancedCode: enh, Message: msg}
	}
	tests := []struct {
		name     string
		setup    func(f *fakeServer)
		want     int
		wantText string
		dataSent bool
	}{
		{"auth 535", func(f *fakeServer) { f.authErr = smtpErr(535, gosmtp.EnhancedCode{5, 7, 8}, "nope") }, ExitNoPerm, "535", false},
		{"auth temporary", func(f *fakeServer) { f.authErr = smtpErr(454, gosmtp.EnhancedCode{4, 7, 0}, "try later") }, ExitTempFail, "454", false},
		{"send-as at MAIL", func(f *fakeServer) {
			f.mailErr = smtpErr(553, gosmtp.EnhancedCode{5, 7, 1}, "Sender address not owned by authenticated user")
		}, ExitNoPerm, "Sender address not owned by authenticated user", false},
		{"MAIL temporary", func(f *fakeServer) {
			f.mailErr = smtpErr(451, gosmtp.EnhancedCode{4, 3, 0}, "Sender policy temporarily unavailable")
		}, ExitTempFail, "451", false},
		{"MAIL 421", func(f *fakeServer) { f.mailErr = smtpErr(421, gosmtp.EnhancedCode{4, 3, 2}, "shutting down") }, ExitUnavailable, "421", false},
		{"MAIL size", func(f *fakeServer) { f.mailErr = smtpErr(552, gosmtp.EnhancedCode{5, 3, 4}, "too big") }, ExitDataErr, "552", false},
		{"RCPT relaying denied is 67 not 77", func(f *fakeServer) {
			f.rcptErr = map[string]error{"bob@example.net": smtpErr(550, gosmtp.EnhancedCode{5, 7, 1}, "Relaying denied")}
		}, ExitNoUser, "Relaying denied", false},
		{"RCPT unknown user", func(f *fakeServer) {
			f.rcptErr = map[string]error{"bob@example.net": smtpErr(550, gosmtp.EnhancedCode{5, 1, 1}, "Mailbox unavailable")}
		}, ExitNoUser, "bob@example.net", false},
		{"RCPT temporary", func(f *fakeServer) {
			f.rcptErr = map[string]error{"bob@example.net": smtpErr(451, gosmtp.EnhancedCode{4, 2, 0}, "later")}
		}, ExitTempFail, "451", false},
		{"DATA From not owned", func(f *fakeServer) {
			f.dataErr = smtpErr(550, gosmtp.EnhancedCode{5, 7, 1}, "From address not owned by authenticated user")
		}, ExitNoPerm, "From address not owned", true},
		{"DATA malformed From", func(f *fakeServer) {
			f.dataErr = smtpErr(550, gosmtp.EnhancedCode{5, 6, 0}, "exactly one From header is required")
		}, ExitDataErr, "exactly one From", true},
		{"DATA header too large", func(f *fakeServer) {
			f.dataErr = smtpErr(552, gosmtp.EnhancedCode{5, 3, 4}, "Message header too large")
		}, ExitDataErr, "header too large", true},
		{"DATA quota", func(f *fakeServer) { f.dataErr = smtpErr(552, gosmtp.EnhancedCode{5, 2, 2}, "Mailbox quota exceeded") }, ExitUnavailable, "quota", true},
		{"DATA temporary", func(f *fakeServer) { f.dataErr = smtpErr(451, gosmtp.EnhancedCode{4, 3, 0}, "try later") }, ExitTempFail, "451", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeServer{}
			tt.setup(f)
			port, pool := startFake(t, f, "implicit")
			code, stderr := runSendmail(t, pool, simpleMsg, "--config", writeConf(t, port, "implicit", testPass), "-t")
			if code != tt.want {
				t.Fatalf("exit %d, want %d: %s", code, tt.want, stderr)
			}
			if !strings.Contains(stderr, tt.wantText) {
				t.Errorf("stderr %q lacks %q", stderr, tt.wantText)
			}
			if strings.Contains(stderr, testPass) {
				t.Error("stderr contains the password")
			}
			if got := f.snapshot(); !tt.dataSent && got.dataCalled {
				t.Error("message data reached the server although an earlier phase failed")
			}
		})
	}
}

// One refused recipient abandons the whole transaction.
func TestSubmitRefusedRecipientSendsNothing(t *testing.T) {
	f := &fakeServer{rcptErr: map[string]error{
		"carol@example.net": &gosmtp.SMTPError{Code: 550, EnhancedCode: gosmtp.EnhancedCode{5, 1, 1}, Message: "Mailbox unavailable"},
	}}
	port, pool := startFake(t, f, "implicit")
	msg := "To: bob@example.net, carol@example.net\n\nb\n"
	code, stderr := runSendmail(t, pool, msg, "--config", writeConf(t, port, "implicit", testPass), "-t")
	if code != ExitNoUser {
		t.Fatalf("exit %d, want 67: %s", code, stderr)
	}
	if got := f.snapshot(); got.dataCalled {
		t.Error("data was sent although a recipient was refused")
	}
}

func TestRunConfigAndUsageFailures(t *testing.T) {
	dir := t.TempDir()
	wide := filepath.Join(dir, "wide.conf")
	if err := os.WriteFile(wide, []byte(goodConf), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(wide, 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		args []string
		want int
	}{
		{"world-readable config", []string{"--config", wide, "-t"}, ExitConfig},
		{"missing config", []string{"--config", filepath.Join(dir, "none"), "-t"}, ExitConfig},
		{"-bs", []string{"-bs"}, ExitUsage},
		{"unknown flag", []string{"-Z"}, ExitUsage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, stderr := runSendmail(t, nil, simpleMsg, tt.args...)
			if code != tt.want {
				t.Fatalf("exit %d, want %d: %s", code, tt.want, stderr)
			}
			if strings.Contains(stderr, "hunter2") {
				t.Error("stderr contains the password")
			}
		})
	}
}

func TestClassifyNonSMTPErrors(t *testing.T) {
	err := fmt.Errorf("connection reset")
	if got := classify(phaseConnect, err); got != ExitUnavailable {
		t.Errorf("connect: %d", got)
	}
	if got := classify(phaseData, err); got != ExitTempFail {
		t.Errorf("mid-session: %d", got)
	}
}
