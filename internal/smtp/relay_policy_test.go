package smtp

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	gosmtp "github.com/emersion/go-smtp"

	"github.com/bongo-go/bongo-go/internal/auth"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// --- test doubles ---------------------------------------------------------

type fakeAuth struct {
	userID string
	err    error
}

func (f *fakeAuth) Authenticate(username, password string) (string, error) {
	return f.userID, f.err
}

// fakeResolver treats any address in `local` as a local recipient.
type fakeResolver struct{ local map[string]bool }

func (r *fakeResolver) ResolveRecipient(email string) (string, error) {
	email = strings.TrimPrefix(strings.TrimSuffix(strings.TrimSpace(email), ">"), "<")
	if r.local[email] {
		return "uid-" + email, nil
	}
	return "", io.EOF // any error => non-local
}

// fakeSplitDeliverer records local deliveries and satisfies splitDeliverer.
type fakeSplitDeliverer struct {
	localCalls [][]string
	localMetas []*InboundMeta
}

func (d *fakeSplitDeliverer) Deliver(from string, recipients []string, r io.Reader) error {
	return nil
}
func (d *fakeSplitDeliverer) StoreBlob(r io.Reader) (string, error) {
	_, _ = io.Copy(io.Discard, r)
	return "blobhash", nil
}
func (d *fakeSplitDeliverer) EnqueueLocal(from string, recipients []string, blobHash string, meta *InboundMeta) error {
	d.localCalls = append(d.localCalls, recipients)
	// Make a copy of meta for verification
	if meta != nil {
		d.localMetas = append(d.localMetas, &InboundMeta{
			SPFResult: meta.SPFResult,
			SourceIP:  meta.SourceIP,
			Trusted:   meta.Trusted,
		})
	} else {
		d.localMetas = append(d.localMetas, &InboundMeta{})
	}
	return nil
}

type fakeRelayQueue struct{ enqueued []*bongov1.OutboundMessage }

func (q *fakeRelayQueue) Enqueue(msg *bongov1.OutboundMessage) error {
	q.enqueued = append(q.enqueued, msg)
	return nil
}

func testBackend(local ...string) (*Backend, *fakeSplitDeliverer, *fakeRelayQueue) {
	set := map[string]bool{}
	for _, l := range local {
		set[l] = true
	}
	deliverer := &fakeSplitDeliverer{}
	relay := &fakeRelayQueue{}
	b := &Backend{
		auth:       &fakeAuth{userID: "uid-1"},
		deliverer:  deliverer,
		resolver:   &fakeResolver{local: set},
		relayQueue: relay,
		perIP:      map[string]int{},
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	return b, deliverer, relay
}

func smtpCode(err error) int {
	if se, ok := err.(*gosmtp.SMTPError); ok {
		return se.Code
	}
	return 0
}

// --- tests ----------------------------------------------------------------

// B2: an unauthenticated sender may deliver to a local recipient but is refused
// relay to an external recipient.
func TestRcptUnauthenticatedRelayDenied(t *testing.T) {
	b, deliverer, _ := testBackend("staff@example.com")
	s := &Session{backend: b, logger: b.logger}

	if err := s.Rcpt("staff@example.com", nil); err != nil {
		t.Fatalf("local recipient should be accepted, got %v", err)
	}
	if err := s.Rcpt("outsider@evil.com", nil); smtpCode(err) != 550 {
		t.Fatalf("external recipient should be rejected 550, got %v", err)
	}

	if err := s.Data(strings.NewReader("subject: hi\r\n\r\nbody")); err != nil {
		t.Fatalf("Data failed: %v", err)
	}
	if len(deliverer.localCalls) != 1 || deliverer.localCalls[0][0] != "staff@example.com" {
		t.Fatalf("expected 1 local delivery to staff, got %v", deliverer.localCalls)
	}
}

// B1: the submission port rejects MAIL FROM before authentication.
func TestSubmissionRequiresAuth(t *testing.T) {
	b, _, _ := testBackend("staff@example.com")
	s := &Session{backend: b, logger: b.logger, submission: true}

	if err := s.Mail("spoof@example.com", nil); smtpCode(err) != 530 {
		t.Fatalf("unauthenticated submission MAIL should be 530, got %v", err)
	}

	// After auth, MAIL is accepted.
	s.authed = true
	if err := s.Mail("me@example.com", nil); err != nil {
		t.Fatalf("authenticated MAIL should be accepted, got %v", err)
	}
}

// Authenticated submission must still authorize the envelope sender. A valid
// user should not be able to submit mail as another local user or arbitrary
// third-party address.
func TestSubmissionRejectsUnauthorizedMailFrom(t *testing.T) {
	b, _, _ := testBackend("alice@example.com", "ceo@example.com")
	s := &Session{backend: b, logger: b.logger, submission: true, authed: true, userID: "uid-alice"}

	if err := s.Mail("ceo@example.com", nil); err == nil {
		t.Fatal("authenticated user was allowed to submit as a different local address")
	}
}

func TestSMTPAuthFailuresAreRateLimited(t *testing.T) {
	deliverer := &fakeSplitDeliverer{}
	b := &Backend{
		auth:      &fakeAuth{err: auth.ErrBadCredentials},
		deliverer: deliverer,
		perIP:     map[string]int{},
		logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	s := &Session{backend: b, logger: b.logger, submission: true}

	var err error
	for i := 0; i < 6; i++ {
		err = s.AuthPlain("alice@example.com", "wrong-password")
	}
	if smtpCode(err) == 0 {
		t.Fatalf("repeated SMTP AUTH failures were not converted to a throttling SMTP error; got %v", err)
	}
}

// B3: an authenticated session may relay to external recipients, which are
// enqueued for outbound delivery rather than dropped.
func TestAuthenticatedRelayToExternal(t *testing.T) {
	b, deliverer, relay := testBackend("staff@example.com")
	s := &Session{backend: b, logger: b.logger, submission: true, authed: true, userID: "uid-1"}

	if err := s.Mail("me@example.com", nil); err != nil {
		t.Fatalf("MAIL failed: %v", err)
	}
	if err := s.Rcpt("staff@example.com", nil); err != nil {
		t.Fatalf("local rcpt failed: %v", err)
	}
	if err := s.Rcpt("friend@external.com", nil); err != nil {
		t.Fatalf("authed external rcpt should be accepted, got %v", err)
	}
	if err := s.Data(strings.NewReader("subject: hi\r\n\r\nbody")); err != nil {
		t.Fatalf("Data failed: %v", err)
	}

	if len(deliverer.localCalls) != 1 {
		t.Fatalf("expected 1 local delivery, got %v", deliverer.localCalls)
	}
	if len(relay.enqueued) != 1 {
		t.Fatalf("expected 1 outbound relay enqueue, got %d", len(relay.enqueued))
	}
	out := relay.enqueued[0]
	if len(out.Recipients) != 1 || out.Recipients[0] != "friend@external.com" {
		t.Fatalf("expected external recipient in relay, got %v", out.Recipients)
	}
	if out.SenderId != "uid-1" || out.BlobHash != "blobhash" {
		t.Fatalf("unexpected outbound message: %+v", out)
	}
}

func TestSanitizeHeaderValue(t *testing.T) {
	if got := sanitizeHeaderValue("ok\r\nBcc: victim@x"); got != "okBcc: victim@x" {
		t.Fatalf("CRLF not stripped: %q", got)
	}
}

// US-001 AC5 & FR-2: The trust flag is set ONLY from the SMTP session's authed
// state. No header, envelope field, or client-supplied value can set it. A message
// from an unauthenticated MX connection arrives untrusted even if it claims to be
// trusted via headers that could plausibly be believed by careless downstream code.
func TestUnauthenticatedMXArrivesUntrusted(t *testing.T) {
	tests := []struct {
		name    string
		headers string
	}{
		{"no forged headers", ""},
		{"X-Fake-Trusted header", "X-Fake-Trusted: yes\r\n"},
		{"X-Trusted header", "X-Trusted: true\r\n"},
		{"forged Authentication-Results", "Authentication-Results: bongo; auth=pass\r\n"},
		{"forged Received with ESMTPA", "Received: from evil.example (evil.example [203.0.113.66])\r\n\tby bongo-go with ESMTPA id abc123;\r\n\tThu, 1 Jan 2026 00:00:00 +0000\r\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, deliverer, _ := testBackend("staff@example.com")
			// Unauthenticated session on MX port (submission = false, authed = false).
			s := &Session{backend: b, logger: b.logger, submission: false, authed: false}

			msg := tt.headers + "From: attacker@evil.com\r\nSubject: hi\r\n\r\nbody"

			if err := s.Mail("attacker@evil.com", nil); err != nil {
				t.Fatalf("Mail failed: %v", err)
			}
			if err := s.Rcpt("staff@example.com", nil); err != nil {
				t.Fatalf("Rcpt failed: %v", err)
			}
			if err := s.Data(strings.NewReader(msg)); err != nil {
				t.Fatalf("Data failed: %v", err)
			}

			if len(deliverer.localCalls) != 1 {
				t.Fatalf("expected 1 local delivery, got %d", len(deliverer.localCalls))
			}
			if len(deliverer.localMetas) != 1 {
				t.Fatalf("expected 1 metadata record, got %d", len(deliverer.localMetas))
			}

			meta := deliverer.localMetas[0]
			if meta.Trusted {
				t.Errorf("unauthenticated MX message with headers %q should not be trusted", tt.headers)
			}
		})
	}
}

// TestMXSessionCannotAuthenticate documents the structural reason MX-port
// sessions can never become trusted: AuthMechanisms returns nil whenever the
// session is not a submission session, so go-smtp never offers AUTH and
// s.authed can never become true. This is the invariant TestUnauthenticatedMXArrivesUntrusted
// relies on; asserting it directly means a future change to AuthMechanisms
// that silently makes MX sessions authenticatable is caught here too.
func TestMXSessionCannotAuthenticate(t *testing.T) {
	s := &Session{submission: false}
	if mechs := s.AuthMechanisms(); mechs != nil {
		t.Fatalf("AuthMechanisms() on a non-submission session = %v, want nil", mechs)
	}
}

// FR-2: An authenticated submission port message arrives trusted.
func TestAuthenticatedSubmissionArrivesTrusted(t *testing.T) {
	b, deliverer, _ := testBackend("alice@example.com", "bob@example.com")
	// Authenticated session on submission port with userID matching the sender domain
	s := &Session{
		backend:    b,
		logger:     b.logger,
		submission: true,
		authed:     true,
		userID:     "uid-alice@example.com",
	}

	msg := "From: alice@example.com\r\nTo: bob@example.com\r\nSubject: hi\r\n\r\nbody"

	if err := s.Mail("alice@example.com", nil); err != nil {
		t.Fatalf("Mail failed: %v", err)
	}
	if err := s.Rcpt("bob@example.com", nil); err != nil {
		t.Fatalf("Rcpt failed: %v", err)
	}
	if err := s.Data(strings.NewReader(msg)); err != nil {
		t.Fatalf("Data failed: %v", err)
	}

	if len(deliverer.localCalls) != 1 {
		t.Fatalf("expected 1 local delivery, got %d", len(deliverer.localCalls))
	}
	if len(deliverer.localMetas) != 1 {
		t.Fatalf("expected 1 metadata record, got %d", len(deliverer.localMetas))
	}

	meta := deliverer.localMetas[0]
	if !meta.Trusted {
		t.Error("authenticated submission message should be trusted")
	}
}
