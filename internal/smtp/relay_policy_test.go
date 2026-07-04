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
}

func (d *fakeSplitDeliverer) Deliver(from string, recipients []string, r io.Reader) error {
	return nil
}
func (d *fakeSplitDeliverer) StoreBlob(r io.Reader) (string, error) {
	_, _ = io.Copy(io.Discard, r)
	return "blobhash", nil
}
func (d *fakeSplitDeliverer) EnqueueLocal(from string, recipients []string, blobHash, spf, sourceIP string) error {
	d.localCalls = append(d.localCalls, recipients)
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
