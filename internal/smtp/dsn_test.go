package smtp

import (
	"net"
	"net/textproto"
	"strings"
	"testing"

	gosmtp "github.com/emersion/go-smtp"

	"github.com/bongo-project/bongo/internal/mail"
)

// Every listener must advertise DSN, or go-smtp answers RET, ENVID, NOTIFY and
// ORCPT with 504. The 465 listener is built separately from the other two and is
// easy to miss.
func TestEHLOAdvertisesDSNOnAllListeners(t *testing.T) {
	srv, _ := submissionsServer(t, nil)
	tests := []struct {
		name string
		dial func(t *testing.T) *gosmtp.Client
	}{
		{"mx", func(t *testing.T) *gosmtp.Client {
			c, err := gosmtp.Dial(srv.MXAddr())
			if err != nil {
				t.Fatal(err)
			}
			return c
		}},
		{"587", func(t *testing.T) *gosmtp.Client {
			c, err := gosmtp.Dial(srv.SubmissionAddr())
			if err != nil {
				t.Fatal(err)
			}
			return c
		}},
		{"465", func(t *testing.T) *gosmtp.Client {
			c, err := gosmtp.DialTLS(srv.SubmissionsAddr(), tlsInsecure)
			if err != nil {
				t.Fatal(err)
			}
			return c
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := tt.dial(t)
			t.Cleanup(func() { _ = c.Close() })
			if err := c.Hello("client.example"); err != nil {
				t.Fatal(err)
			}
			if ok, _ := c.Extension("DSN"); !ok {
				t.Error("EHLO does not advertise DSN")
			}
		})
	}
}

// The options a client sends reach the local-delivery metadata, per recipient
// and in recipient order.
func TestMXCapturesDSNParameters(t *testing.T) {
	srv, b := submissionsServer(t, nil)
	b.resolver = &fakeResolver{local: map[string]bool{"bob@example.com": true, "carol@example.com": true}}
	fd := b.deliverer.(*fakeSplitDeliverer)

	c, err := gosmtp.Dial(srv.MXAddr())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if err := c.Hello("client.example"); err != nil {
		t.Fatal(err)
	}
	if err := c.Mail("sender@example.org", &gosmtp.MailOptions{Return: gosmtp.DSNReturnFull, EnvelopeID: "env 42+x"}); err != nil {
		t.Fatalf("MAIL: %v", err)
	}
	if err := c.Rcpt("bob@example.com", &gosmtp.RcptOptions{
		Notify:                []gosmtp.DSNNotify{gosmtp.DSNNotifySuccess, gosmtp.DSNNotifyDelayed},
		OriginalRecipientType: gosmtp.DSNAddressTypeRFC822,
		OriginalRecipient:     "orig+1@example.net",
	}); err != nil {
		t.Fatalf("RCPT bob: %v", err)
	}
	if err := c.Rcpt("carol@example.com", nil); err != nil {
		t.Fatalf("RCPT carol: %v", err)
	}
	w, err := c.Data()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte(authedMsg))
	if err := w.Close(); err != nil {
		t.Fatalf("DATA: %v", err)
	}

	if len(fd.localMetas) != 1 {
		t.Fatalf("local deliveries = %d, want 1", len(fd.localMetas))
	}
	m := fd.localMetas[0]
	if m.DSNRet != "FULL" || m.DSNEnvID != "env 42+x" {
		t.Errorf("RET/ENVID = %q/%q, want FULL / %q", m.DSNRet, m.DSNEnvID, "env 42+x")
	}
	want := []RcptDSN{
		{Notify: mail.NotifySuccess | mail.NotifyDelay, ORCPTType: "rfc822", ORCPT: "orig+1@example.net"},
		{},
	}
	if len(m.DSNRcpts) != len(want) {
		t.Fatalf("DSNRcpts = %+v, want %+v", m.DSNRcpts, want)
	}
	for i := range want {
		if m.DSNRcpts[i] != want[i] {
			t.Errorf("DSNRcpts[%d] = %+v, want %+v", i, m.DSNRcpts[i], want[i])
		}
	}
}

// Malformed or over-long values are refused with 501 before any recipient or
// body is accepted.
func TestMXRefusesBadDSNParameters(t *testing.T) {
	srv, _ := submissionsServer(t, nil)
	long := func(n int) string { return strings.Repeat("a", n) }

	tests := []struct {
		name string
		mail string // MAIL FROM line parameters
		rcpt string // RCPT TO line parameters; "" skips the RCPT step
		want int    // reply code of the offending command
	}{
		{"ENVID at limit", "ENVID=" + long(100), "", 250},
		{"ENVID over limit", "ENVID=" + long(101), "", 501},
		{"ENVID bad xtext", "ENVID=bad+zz", "", 501},
		{"RET unknown", "RET=SOME", "", 501},
		{"ORCPT at limit", "", "ORCPT=rfc822;" + long(500), 250},
		{"ORCPT over limit", "", "ORCPT=rfc822;" + long(501), 501},
		{"ORCPT bad type", "", "ORCPT=bogus;a@b", 501},
		{"ORCPT no type", "", "ORCPT=a@b.example", 501},
		{"ORCPT bad xtext", "", "ORCPT=rfc822;a+zz@b", 501},
		{"NOTIFY unknown", "", "NOTIFY=SOMETIMES", 501},
		{"NOTIFY NEVER combined", "", "NOTIFY=NEVER,FAILURE", 501},
		{"NOTIFY duplicated", "", "NOTIFY=DELAY,DELAY", 501},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, err := net.Dial("tcp", srv.MXAddr())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = conn.Close() })
			tc := textproto.NewConn(conn)
			expect := func(want int) {
				t.Helper()
				if _, _, err := tc.ReadResponse(want); err != nil {
					t.Fatalf("want %d: %v", want, err)
				}
			}
			expect(220)
			_ = tc.PrintfLine("EHLO client.example")
			expect(250)
			if tt.rcpt == "" {
				_ = tc.PrintfLine("MAIL FROM:<a@example.org> %s", tt.mail)
				expect(tt.want)
				return
			}
			_ = tc.PrintfLine("MAIL FROM:<a@example.org>")
			expect(250)
			_ = tc.PrintfLine("RCPT TO:<bob@example.com> %s", tt.rcpt)
			expect(tt.want)
		})
	}
}

// A UTF-8 ORCPT decodes to Unicode; control characters in it are refused.
func TestRcptDSNFromRefusesControlCharacters(t *testing.T) {
	tests := []struct {
		name    string
		orcpt   string
		wantErr bool
	}{
		{"plain", "user@example.net", false},
		{"non-ASCII letters", "jürgen@example.net", false},
		{"embedded CRLF", "a@b\r\nBcc: x@y", true},
		{"NUL", "a\x00@b", true},
		{"C1 control", "a\u0085@b", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := rcptDSNFrom(&gosmtp.RcptOptions{
				OriginalRecipientType: gosmtp.DSNAddressTypeUTF8,
				OriginalRecipient:     tt.orcpt,
			})
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && smtpCode(err) != 501 {
				t.Errorf("code = %d, want 501", smtpCode(err))
			}
		})
	}
}

// Over-long ENVID straight at Session.Mail, no network.
func TestSessionMailENVIDLimit(t *testing.T) {
	b, _, _ := testBackend()
	for _, tt := range []struct {
		n    int
		want int
	}{{100, 0}, {101, 501}} {
		s := &Session{backend: b, logger: b.logger}
		err := s.Mail("a@example.org", &gosmtp.MailOptions{EnvelopeID: strings.Repeat("e", tt.n)})
		if smtpCode(err) != tt.want {
			t.Errorf("ENVID len %d: code = %d (%v), want %d", tt.n, smtpCode(err), err, tt.want)
		}
	}
}

// Relay recipients keep their own NOTIFY/ORCPT on the OutboundMessage, keyed by
// address; a recipient with no DSN parameters gets no record.
func TestSubmissionRelayCarriesDSN(t *testing.T) {
	b, _, relay := testBackend("alice@example.com")
	s := &Session{backend: b, logger: b.logger, submission: true, authed: true, userID: "uid-alice@example.com"}

	if err := s.Mail("alice@example.com", &gosmtp.MailOptions{Return: gosmtp.DSNReturnHeaders, EnvelopeID: "E1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Rcpt("one@example.net", &gosmtp.RcptOptions{
		Notify:                []gosmtp.DSNNotify{gosmtp.DSNNotifyNever},
		OriginalRecipientType: gosmtp.DSNAddressTypeUTF8,
		OriginalRecipient:     "jürgen@example.net",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Rcpt("two@example.net", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Data(strings.NewReader(authedMsg)); err != nil {
		t.Fatal(err)
	}
	if len(relay.enqueued) != 1 {
		t.Fatalf("enqueued = %d, want 1", len(relay.enqueued))
	}
	out := relay.enqueued[0]
	if out.DsnRet != "HDRS" || out.DsnEnvid != "E1" {
		t.Errorf("ret/envid = %q/%q", out.DsnRet, out.DsnEnvid)
	}
	if len(out.RecipientDsn) != 1 {
		t.Fatalf("recipient_dsn = %v, want only the recipient with parameters", out.RecipientDsn)
	}
	r := out.RecipientDsn[0]
	if r.Address != "one@example.net" || r.Notify != mail.NotifyNever || r.OrcptType != "utf-8" || r.Orcpt != "jürgen@example.net" {
		t.Errorf("record = %v", r)
	}
}

// One transaction's parameters must not leak into the next on the same
// connection.
func TestSessionResetClearsDSN(t *testing.T) {
	b, _, _ := testBackend("bob@example.com")
	s := &Session{backend: b, logger: b.logger}
	if err := s.Mail("a@example.org", &gosmtp.MailOptions{Return: gosmtp.DSNReturnFull, EnvelopeID: "E"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Rcpt("bob@example.com", &gosmtp.RcptOptions{Notify: []gosmtp.DSNNotify{gosmtp.DSNNotifySuccess}}); err != nil {
		t.Fatal(err)
	}
	s.Reset()
	if s.dsnRet != "" || s.dsnEnvID != "" || len(s.localDSN) != 0 || len(s.externalDSN) != 0 {
		t.Errorf("DSN state survived Reset: %q %q %v %v", s.dsnRet, s.dsnEnvID, s.localDSN, s.externalDSN)
	}

	// A second transaction with no options must carry none.
	if err := s.Mail("a@example.org", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Rcpt("bob@example.com", nil); err != nil {
		t.Fatal(err)
	}
	meta := s.inboundMeta("203.0.113.9")
	if meta.DSNRet != "" || meta.DSNEnvID != "" || len(meta.DSNRcpts) != 1 || meta.DSNRcpts[0] != (RcptDSN{}) {
		t.Errorf("second transaction meta = %+v", meta)
	}
}

// Every place Rcpt accepts a recipient keeps the DSN slice aligned.
func TestRcptAcceptPathsKeepDSNAligned(t *testing.T) {
	// No resolver: the dev/agent fallback.
	b, _, _ := testBackend()
	b.resolver = nil
	s := &Session{backend: b, logger: b.logger}
	if err := s.Rcpt("x@example.com", &gosmtp.RcptOptions{Notify: []gosmtp.DSNNotify{gosmtp.DSNNotifyDelayed}}); err != nil {
		t.Fatal(err)
	}
	if len(s.localRcpts) != 1 || len(s.localDSN) != 1 || s.localDSN[0].Notify != mail.NotifyDelay {
		t.Errorf("no-resolver path: %v %v", s.localRcpts, s.localDSN)
	}

	// Local and relay.
	b, _, _ = testBackend("bob@example.com")
	s = &Session{backend: b, logger: b.logger, authed: true, userID: "u"}
	if err := s.Rcpt("bob@example.com", &gosmtp.RcptOptions{Notify: []gosmtp.DSNNotify{gosmtp.DSNNotifySuccess}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Rcpt("out@example.net", &gosmtp.RcptOptions{Notify: []gosmtp.DSNNotify{gosmtp.DSNNotifyFailure}}); err != nil {
		t.Fatal(err)
	}
	if len(s.localDSN) != len(s.localRcpts) || len(s.externalDSN) != len(s.externalRcpts) ||
		s.localDSN[0].Notify != mail.NotifySuccess || s.externalDSN[0].Notify != mail.NotifyFailure {
		t.Errorf("local/relay path: %v %v %v %v", s.localRcpts, s.localDSN, s.externalRcpts, s.externalDSN)
	}
}
