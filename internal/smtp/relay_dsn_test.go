package smtp

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/textproto"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	gosmtp "github.com/emersion/go-smtp"
	"github.com/miekg/dns"

	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/mail"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// dsnBackend records what the relay put on the wire: the MAIL and RCPT
// options as the server parsed them, and how many MAIL commands arrived (a
// refused policy must leave this at zero).
type dsnBackend struct {
	mu       sync.Mutex
	mails    int
	mailOpts *gosmtp.MailOptions
	rcptOpts map[string]*gosmtp.RcptOptions
	rejects  map[string]int // recipient -> reply code
	data     chan struct{}
	rejectEC gosmtp.EnhancedCode // enhanced code on rejected RCPTs
}

func newDSNBackend() *dsnBackend {
	return &dsnBackend{rcptOpts: map[string]*gosmtp.RcptOptions{}, rejects: map[string]int{}, data: make(chan struct{}, 4)}
}

func (b *dsnBackend) NewSession(*gosmtp.Conn) (gosmtp.Session, error) {
	return &dsnSession{b: b}, nil
}

func (b *dsnBackend) mailCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.mails
}

type dsnSession struct{ b *dsnBackend }

func (s *dsnSession) Mail(_ string, o *gosmtp.MailOptions) error {
	s.b.mu.Lock()
	defer s.b.mu.Unlock()
	s.b.mails++
	cp := *o
	s.b.mailOpts = &cp
	return nil
}

func (s *dsnSession) Rcpt(to string, o *gosmtp.RcptOptions) error {
	s.b.mu.Lock()
	defer s.b.mu.Unlock()
	if code := s.b.rejects[to]; code != 0 {
		return &gosmtp.SMTPError{Code: code, EnhancedCode: s.b.rejectEC, Message: "no"}
	}
	cp := *o
	s.b.rcptOpts[to] = &cp
	return nil
}

func (s *dsnSession) Data(r io.Reader) error {
	_, _ = io.Copy(io.Discard, r)
	s.b.data <- struct{}{}
	return nil
}
func (s *dsnSession) Reset()        {}
func (s *dsnSession) Logout() error { return nil }

type dsnServerOpts struct {
	dsn     bool
	maxSize int64
	cert    *tls.Certificate
}

func startDSNServer(t *testing.T, b *dsnBackend, o dsnServerOpts) net.Listener {
	t.Helper()
	srv := gosmtp.NewServer(b)
	srv.Domain = "localhost"
	srv.EnableDSN = o.dsn
	srv.MaxMessageBytes = o.maxSize
	if o.cert != nil {
		srv.TLSConfig = &tls.Config{Certificates: []tls.Certificate{*o.cert}, MinVersion: tls.VersionTLS12}
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	return l
}

var dsnRaw = []byte("From: a@example.com\r\nTo: x@remote.test\r\nSubject: t\r\n\r\nhello\r\n")

func dsnMessage() *bongov1.OutboundMessage {
	return &bongov1.OutboundMessage{
		SenderEmail: "a@example.com",
		Recipients:  []string{"ok@remote.test", "never@remote.test", "plain@remote.test"},
		DsnRet:      "FULL",
		DsnEnvid:    "env-123",
		RecipientDsn: []*bongov1.RecipientDSN{
			{Address: "ok@remote.test", Notify: mail.NotifySuccess | mail.NotifyFailure, OrcptType: "rfc822", Orcpt: "orig@example.org"},
			{Address: "never@remote.test", Notify: mail.NotifyNever},
		},
	}
}

func smartRelay(addr string) *Relay {
	return NewRelay(&config.SMTPRelay{Enabled: true, Host: addr}, "mail.example.test", slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestDeliverOutboundPassesDSNWhenAdvertised(t *testing.T) {
	b := newDSNBackend()
	l := startDSNServer(t, b, dsnServerOpts{dsn: true, maxSize: 1 << 20})
	r := smartRelay(l.Addr().String())

	perm, relayed, err := r.DeliverOutbound(context.Background(), dsnMessage(), dsnRaw)
	if err != nil || len(perm) != 0 {
		t.Fatalf("DeliverOutbound: perm=%v err=%v", perm, err)
	}
	if len(relayed) != 0 {
		t.Errorf("relayedNoDSN = %v, want none: the remote supports DSN", relayed)
	}
	if b.mailOpts.Return != gosmtp.DSNReturnFull || b.mailOpts.EnvelopeID != "env-123" {
		t.Errorf("MAIL options = %+v, want RET=FULL ENVID=env-123", b.mailOpts)
	}
	if b.mailOpts.Size != int64(len(dsnRaw)) {
		t.Errorf("SIZE = %d, want %d", b.mailOpts.Size, len(dsnRaw))
	}
	ok := b.rcptOpts["ok@remote.test"]
	if ok == nil || !slices.Equal(ok.Notify, []gosmtp.DSNNotify{gosmtp.DSNNotifySuccess, gosmtp.DSNNotifyFailure}) ||
		ok.OriginalRecipientType != gosmtp.DSNAddressTypeRFC822 || ok.OriginalRecipient != "orig@example.org" {
		t.Errorf("ok@ RCPT options = %+v", ok)
	}
	if n := b.rcptOpts["never@remote.test"]; n == nil || !slices.Equal(n.Notify, []gosmtp.DSNNotify{gosmtp.DSNNotifyNever}) {
		t.Errorf("never@ RCPT options = %+v", n)
	}
	if p := b.rcptOpts["plain@remote.test"]; p == nil || len(p.Notify) != 0 || p.OriginalRecipient != "" {
		t.Errorf("plain@ RCPT options = %+v, want none", p)
	}
}

// rawServer is a minimal SMTP server that advertises exactly the extensions it
// is given and records every MAIL and RCPT line. go-smtp's own server always
// advertises SIZE, so it cannot stand in for a remote without it.
type rawServer struct {
	mu    sync.Mutex
	lines []string
}

func (s *rawServer) captured() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.lines)
}

func startRawServer(t *testing.T, exts ...string) (*rawServer, net.Listener) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	s := &rawServer{}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go s.serve(c, exts)
		}
	}()
	return s, l
}

func (s *rawServer) serve(c net.Conn, exts []string) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	tp := textproto.NewConn(c)
	_ = tp.PrintfLine("220 localhost ready")
	inData := false
	for {
		line, err := tp.ReadLine()
		if err != nil {
			return
		}
		if inData {
			if line == "." {
				inData = false
				_ = tp.PrintfLine("250 queued")
			}
			continue
		}
		up := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(up, "EHLO"):
			all := append([]string{"localhost"}, exts...)
			for i, e := range all {
				sep := "-"
				if i == len(all)-1 {
					sep = " "
				}
				_ = tp.PrintfLine("250%s%s", sep, e)
			}
		case strings.HasPrefix(up, "MAIL"), strings.HasPrefix(up, "RCPT"):
			s.mu.Lock()
			s.lines = append(s.lines, line)
			s.mu.Unlock()
			_ = tp.PrintfLine("250 ok")
		case strings.HasPrefix(up, "DATA"):
			inData = true
			_ = tp.PrintfLine("354 go")
		case strings.HasPrefix(up, "QUIT"):
			_ = tp.PrintfLine("221 bye")
			return
		default:
			_ = tp.PrintfLine("250 ok")
		}
	}
}

func TestDeliverOutboundSendsNoDSNWithoutExtension(t *testing.T) {
	srv, l := startRawServer(t, "8BITMIME") // no DSN, no SIZE
	r := smartRelay(l.Addr().String())

	msg := dsnMessage()
	msg.RecipientDsn = append(msg.RecipientDsn, &bongov1.RecipientDSN{Address: "plain@remote.test", Notify: mail.NotifySuccess})
	perm, relayed, err := r.DeliverOutbound(context.Background(), msg, dsnRaw)
	if err != nil || len(perm) != 0 {
		t.Fatalf("DeliverOutbound: perm=%v err=%v", perm, err)
	}
	lines := srv.captured()
	if len(lines) != 4 {
		t.Fatalf("captured %d MAIL/RCPT lines, want 4: %q", len(lines), lines)
	}
	for _, line := range lines {
		for _, banned := range []string{"RET=", "ENVID=", "NOTIFY=", "ORCPT=", "SIZE="} {
			if strings.Contains(line, banned) {
				t.Errorf("%q sent to a server that advertises neither DSN nor SIZE", line)
			}
		}
	}
	// ok@ asked for SUCCESS and plain@ too; never@ did not. Only the SUCCESS
	// recipients can be reported as relayed.
	slices.Sort(relayed)
	if want := []string{"ok@remote.test", "plain@remote.test"}; !slices.Equal(relayed, want) {
		t.Errorf("relayedNoDSN = %v, want %v", relayed, want)
	}
}

func TestDeliverOutboundSizeOnlyWhenAdvertised(t *testing.T) {
	want := fmt.Sprintf("SIZE=%d", len(dsnRaw))
	tests := []struct {
		name string
		exts []string
		size bool
	}{
		{"SIZE advertised", []string{"SIZE 1000000"}, true},
		{"SIZE not advertised", []string{"8BITMIME"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, l := startRawServer(t, tt.exts...)
			if _, _, err := smartRelay(l.Addr().String()).DeliverOutbound(context.Background(), dsnMessage(), dsnRaw); err != nil {
				t.Fatalf("DeliverOutbound: %v", err)
			}
			var mailLine string
			for _, line := range srv.captured() {
				if strings.HasPrefix(strings.ToUpper(line), "MAIL") {
					mailLine = line
				}
			}
			if got := strings.Contains(mailLine, want); got != tt.size {
				t.Errorf("MAIL line %q: contains %s = %v, want %v", mailLine, want, got, tt.size)
			}
		})
	}
}

func TestDeliverOutboundRelayedSkipsRejectedAndNonSuccess(t *testing.T) {
	b := newDSNBackend()
	b.rejects["ok@remote.test"] = 550
	l := startDSNServer(t, b, dsnServerOpts{})
	r := smartRelay(l.Addr().String())

	msg := dsnMessage()
	msg.RecipientDsn[1].Notify = mail.NotifyNever | mail.NotifySuccess // NEVER wins
	perm, relayed, err := r.DeliverOutbound(context.Background(), msg, dsnRaw)
	if err != nil {
		t.Fatalf("DeliverOutbound: %v", err)
	}
	if !slices.Equal(perm, []string{"ok@remote.test"}) {
		t.Errorf("permanent fails = %v, want ok@remote.test", perm)
	}
	if len(relayed) != 0 {
		t.Errorf("relayedNoDSN = %v, want none (rejected rcpt, NEVER, no NOTIFY)", relayed)
	}
}

func TestDeliverOutboundDropsMalformedDSNValues(t *testing.T) {
	b := newDSNBackend()
	l := startDSNServer(t, b, dsnServerOpts{dsn: true})
	r := smartRelay(l.Addr().String())

	msg := &bongov1.OutboundMessage{
		SenderEmail: "a@example.com",
		Recipients:  []string{"a@remote.test", "b@remote.test", "c@remote.test"},
		DsnRet:      "SOMETHING",
		DsnEnvid:    "café",
		RecipientDsn: []*bongov1.RecipientDSN{
			{Address: "a@remote.test", Notify: mail.NotifyNever | mail.NotifySuccess | mail.NotifyFailure},
			{Address: "b@remote.test", Notify: mail.NotifyFailure, OrcptType: "x-unknown", Orcpt: "b@example.org"},
			{Address: "c@remote.test", Notify: mail.NotifyDelay, OrcptType: "rfc822", Orcpt: "jürgen@example.org"},
		},
	}
	perm, _, err := r.DeliverOutbound(context.Background(), msg, dsnRaw)
	if err != nil || len(perm) != 0 {
		t.Fatalf("a malformed stored parameter blocked delivery: perm=%v err=%v", perm, err)
	}
	if b.mailOpts.EnvelopeID != "" || b.mailOpts.Return != "" {
		t.Errorf("MAIL options = %+v, want malformed RET and ENVID dropped", b.mailOpts)
	}
	if a := b.rcptOpts["a@remote.test"]; a == nil || !slices.Equal(a.Notify, []gosmtp.DSNNotify{gosmtp.DSNNotifyNever}) {
		t.Errorf("a@ NOTIFY = %+v, want NEVER alone", a)
	}
	if o := b.rcptOpts["b@remote.test"]; o == nil || o.OriginalRecipient != "" {
		t.Errorf("b@ ORCPT = %+v, want unknown type dropped", o)
	}
	if o := b.rcptOpts["c@remote.test"]; o == nil || o.OriginalRecipient != "" {
		t.Errorf("c@ ORCPT = %+v, want non-ASCII rfc822 dropped", o)
	}
}

func TestIsPermanentErrorUsesReplyCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"550", &gosmtp.SMTPError{Code: 550, Message: "user unknown"}, true},
		{"wrapped 553", fmt.Errorf("RCPT: %w", &gosmtp.SMTPError{Code: 553}), true},
		{"450", &gosmtp.SMTPError{Code: 450, Message: "try later"}, false},
		{"421", &gosmtp.SMTPError{Code: 421}, false},
		{"client validation error", errors.New("smtp: Malformed ENVID parameter value"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isPermanentError(tt.err); got != tt.want {
				t.Errorf("isPermanentError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestDeliverOutboundTemporaryRcptErrorIsNotPermanent(t *testing.T) {
	b := newDSNBackend()
	b.rejects["ok@remote.test"] = 450
	l := startDSNServer(t, b, dsnServerOpts{})
	r := smartRelay(l.Addr().String())

	perm, _, err := r.DeliverOutbound(context.Background(), dsnMessage(), dsnRaw)
	if err == nil {
		t.Fatal("a 450 at RCPT must be a retryable error")
	}
	if len(perm) != 0 {
		t.Errorf("permanent fails = %v, want none", perm)
	}
}

// mxRelay returns a relay that resolves every domain to host "localhost" on
// the listener's port, so deliverViaMX (and so the TLS policy) runs against a
// local server.
func mxRelay(l net.Listener, cfg *config.SMTPRelay) *Relay {
	r := NewRelay(cfg, "mail.example.test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, port, _ := net.SplitHostPort(l.Addr().String())
	r.mxPort = port
	r.mxLookup = func(context.Context, string) ([]string, bool, error) { return []string{"localhost"}, true, nil }
	return r
}

func extMessage() *bongov1.OutboundMessage {
	return &bongov1.OutboundMessage{SenderEmail: "a@example.com", Recipients: []string{"bob@example.org"}}
}

// TestDeliverOutboundMTASTSEnforceRefusesPlaintext covers the MTA-STS path
// through the entry point the queue worker uses: an enforcing policy and a
// server that offers no STARTTLS must produce an error and no MAIL command.
func TestDeliverOutboundMTASTSEnforceRefusesPlaintext(t *testing.T) {
	b := newDSNBackend()
	l := startDSNServer(t, b, dsnServerOpts{dsn: true}) // no TLS configured
	r := mxRelay(l, &config.SMTPRelay{MTASTSEnabled: true})
	r.mtasts.lookupTXT = func(context.Context, string) ([]string, error) { return []string{"v=STSv1; id=1;"}, nil }
	r.mtasts.fetch = func(context.Context, string) (string, error) {
		return "version: STSv1\nmode: enforce\nmx: localhost\n", nil
	}

	_, _, err := r.DeliverOutbound(context.Background(), extMessage(), dsnRaw)
	if err == nil {
		t.Fatal("delivered over plaintext under an enforcing MTA-STS policy")
	}
	if !strings.Contains(err.Error(), "STARTTLS") {
		t.Errorf("error = %q, want it to name STARTTLS", err)
	}
	if n := b.mailCount(); n != 0 {
		t.Fatalf("server saw %d MAIL commands; nothing may be sent without TLS", n)
	}

	// Control: the same server accepts the message when no policy applies, so
	// the refusal above comes from the policy.
	r2 := mxRelay(l, &config.SMTPRelay{})
	if _, _, err := r2.DeliverOutbound(context.Background(), extMessage(), dsnRaw); err != nil {
		t.Fatalf("control delivery without policy failed: %v", err)
	}
	if b.mailCount() != 1 {
		t.Errorf("control: MAIL count = %d, want 1", b.mailCount())
	}
}

// TestDeliverOutboundDANEMismatchRefused covers DANE through the same entry
// point: a certificate matching no TLSA record is refused before any MAIL.
func TestDeliverOutboundDANEMismatchRefused(t *testing.T) {
	cert, leaf := selfSignedFor(t, "localhost")
	matching := &dns.TLSA{Usage: 3, Selector: 0, MatchingType: 1}
	if err := matching.Sign(3, 0, 1, leaf); err != nil {
		t.Fatalf("derive TLSA: %v", err)
	}
	mismatched := &dns.TLSA{Usage: 3, Selector: 0, MatchingType: 1, Certificate: strings.Repeat("ab", 32)}

	b := newDSNBackend()
	l := startDSNServer(t, b, dsnServerOpts{dsn: true, cert: &cert})

	r := mxRelay(l, &config.SMTPRelay{})
	r.tlsaLookup = func(context.Context, string) []*dns.TLSA { return []*dns.TLSA{mismatched} }
	_, _, err := r.DeliverOutbound(context.Background(), extMessage(), dsnRaw)
	if err == nil {
		t.Fatal("delivered to a server whose certificate matches no TLSA record")
	}
	if n := b.mailCount(); n != 0 {
		t.Fatalf("server saw %d MAIL commands after a failed DANE check", n)
	}

	// Control: with the matching record the same server is accepted.
	r.tlsaLookup = func(context.Context, string) []*dns.TLSA { return []*dns.TLSA{matching} }
	if _, _, err := r.DeliverOutbound(context.Background(), extMessage(), dsnRaw); err != nil {
		t.Fatalf("control delivery with matching TLSA failed: %v", err)
	}
	select {
	case <-b.data:
	case <-time.After(2 * time.Second):
		t.Fatal("control message not delivered")
	}
}

// The remote's RFC 3463 enhanced code must survive in the returned error:
// the delay and bounce reports parse it out of the stored reason.
func TestDeliverOutboundKeepsEnhancedStatusCode(t *testing.T) {
	b := newDSNBackend()
	b.rejects["ok@remote.test"] = 451
	b.rejectEC = gosmtp.EnhancedCode{4, 7, 1}
	l := startDSNServer(t, b, dsnServerOpts{dsn: true, maxSize: 1 << 20})
	r := smartRelay(l.Addr().String())

	_, _, err := r.DeliverOutbound(context.Background(), dsnMessage(), dsnRaw)
	if err == nil {
		t.Fatal("expected a temporary RCPT error")
	}
	if !strings.Contains(err.Error(), "451 4.7.1 no") {
		t.Fatalf("error %q lost the enhanced status code", err)
	}
	if isPermanentError(err) {
		t.Fatalf("451 must not be permanent: %v", err)
	}
}
