package smtp

import (
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	gosmtp "github.com/emersion/go-smtp"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/mail"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
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

	// Injected failures for the spool-failure tests. A message that cannot be
	// stored or enqueued must never be answered with 250.
	storeErr   error
	stored     int
	enqueueErr error
}

func (d *fakeSplitDeliverer) Deliver(from string, recipients []string, r io.Reader) error {
	return nil
}
func (d *fakeSplitDeliverer) StoreBlob(r io.Reader) (string, error) {
	_, _ = io.Copy(io.Discard, r)
	if d.storeErr != nil {
		return "", d.storeErr
	}
	d.stored++
	return "blobhash", nil
}
func (d *fakeSplitDeliverer) EnqueueLocal(from string, recipients []string, blobHash string, meta *InboundMeta) error {
	if d.enqueueErr != nil {
		return d.enqueueErr
	}
	d.localCalls = append(d.localCalls, recipients)
	// Make a copy of meta for verification
	if meta != nil {
		d.localMetas = append(d.localMetas, &InboundMeta{
			SPFResult: meta.SPFResult,
			SourceIP:  meta.SourceIP,
			Trusted:   meta.Trusted,

			AuthUserID: meta.AuthUserID,
		})
	} else {
		d.localMetas = append(d.localMetas, &InboundMeta{})
	}
	return nil
}

type fakeRelayQueue struct {
	enqueued   []*bongov1.OutboundMessage
	enqueueErr error
}

func (q *fakeRelayQueue) Enqueue(msg *bongov1.OutboundMessage) error {
	if q.enqueueErr != nil {
		return q.enqueueErr
	}
	q.enqueued = append(q.enqueued, msg)
	return nil
}

// authedMsg is a minimal message whose From matches the "uid-alice@example.com"
// session user used by the authenticated Data tests.
const authedMsg = "From: alice@example.com\r\nSubject: hi\r\n\r\nbody"

// fakePolicy allows a user to send as the address that fakeResolver maps to
// that user ("uid-"+address) plus any address in extra.
type fakePolicy struct{ extra map[string]bool }

func (p *fakePolicy) CanSendAs(userID, addr string) (bool, error) {
	return userID == "uid-"+addr || p.extra[addr], nil
}

func testBackend(local ...string) (*Backend, *fakeSplitDeliverer, *fakeRelayQueue) {
	set := map[string]bool{}
	for _, l := range local {
		set[l] = true
	}
	deliverer := &fakeSplitDeliverer{}
	relay := &fakeRelayQueue{}
	b := &Backend{
		auth:         &fakeAuth{userID: "uid-1"},
		deliverer:    deliverer,
		resolver:     &fakeResolver{local: set},
		relayQueue:   relay,
		senderPolicy: &fakePolicy{},
		logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
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

// An unauthenticated sender may deliver to a local recipient but is refused
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

	if err := s.Data(strings.NewReader(authedMsg)); err != nil {
		t.Fatalf("Data failed: %v", err)
	}
	if len(deliverer.localCalls) != 1 || deliverer.localCalls[0][0] != "staff@example.com" {
		t.Fatalf("expected 1 local delivery to staff, got %v", deliverer.localCalls)
	}
}

// The submission port rejects MAIL FROM before authentication.
func TestSubmissionRequiresAuth(t *testing.T) {
	b, _, _ := testBackend("staff@example.com")
	s := &Session{backend: b, logger: b.logger, submission: true}

	if err := s.Mail("spoof@example.com", nil); smtpCode(err) != 530 {
		t.Fatalf("unauthenticated submission MAIL should be 530, got %v", err)
	}

	// After auth, MAIL is accepted.
	s.authed = true
	s.userID = "uid-me@example.com"
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

// An authenticated session may relay to external recipients, which are
// enqueued for outbound delivery rather than dropped.
func TestAuthenticatedRelayToExternal(t *testing.T) {
	b, deliverer, relay := testBackend("staff@example.com")
	s := &Session{backend: b, logger: b.logger, submission: true, authed: true, userID: "uid-alice@example.com"}

	if err := s.Mail("alice@example.com", nil); err != nil {
		t.Fatalf("MAIL failed: %v", err)
	}
	if err := s.Rcpt("staff@example.com", nil); err != nil {
		t.Fatalf("local rcpt failed: %v", err)
	}
	if err := s.Rcpt("friend@external.com", nil); err != nil {
		t.Fatalf("authed external rcpt should be accepted, got %v", err)
	}
	if err := s.Data(strings.NewReader(authedMsg)); err != nil {
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
	if out.SenderId != "uid-alice@example.com" || out.BlobHash != "blobhash" {
		t.Fatalf("unexpected outbound message: %+v", out)
	}
}

func TestSanitizeHeaderValue(t *testing.T) {
	if got := sanitizeHeaderValue("ok\r\nBcc: victim@x"); got != "okBcc: victim@x" {
		t.Fatalf("CRLF not stripped: %q", got)
	}
}

// The trust flag is set ONLY from the SMTP session's authed state. No header,
// envelope field, or client-supplied value can set it. A message from an
// unauthenticated MX connection arrives untrusted even if it claims to be
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

// An authenticated submission port message arrives trusted.
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

// TestOutboundRateLimitRejectsWith452 covers the wiring at the Data() guard,
// not the limiter. inbound_ratelimit_test.go proves inboundRateLimiter counts
// correctly and explicitly asserts submission is exempt from the INBOUND
// limiter -- so nothing referenced outboundRL or OutboundMessagesPerMinute at
// all. Deleting the outbound guard left the whole suite green while a single
// compromised account could relay without bound.
func TestOutboundRateLimitRejectsWith452(t *testing.T) {
	b, _, _ := testBackend("alice@example.com")
	b.outboundRL = newInboundRateLimiter(2)

	send := func() error {
		s := &Session{backend: b, logger: b.logger, submission: true, authed: true, userID: "uid-alice@example.com"}
		if err := s.Mail("alice@example.com", nil); err != nil {
			t.Fatalf("Mail: %v", err)
		}
		if err := s.Rcpt("outsider@example.net", nil); err != nil {
			t.Fatalf("Rcpt to external: %v", err)
		}
		return s.Data(strings.NewReader(authedMsg))
	}

	for i := 1; i <= 2; i++ {
		if err := send(); err != nil {
			t.Fatalf("message %d should be under the limit, got %v", i, err)
		}
	}

	err := send()
	if err == nil {
		t.Fatal("third message was accepted; the outbound rate limit did not fire")
	}
	if code := smtpCode(err); code != 452 {
		t.Errorf("status = %d, want 452 (transient, so a legitimate client retries)", code)
	}
}

// TestOutboundRateLimitDoesNotApplyToLocalOnly pins the guard's scope. It only
// counts sessions with EXTERNAL recipients: internal mail between colleagues
// must not be throttled by an anti-relay control. Without this, tightening the
// condition to cover every submission would look like a passing test.
func TestOutboundRateLimitDoesNotApplyToLocalOnly(t *testing.T) {
	b, _, _ := testBackend("alice@example.com", "bob@example.com")
	b.outboundRL = newInboundRateLimiter(1)

	for i := 1; i <= 5; i++ {
		s := &Session{backend: b, logger: b.logger, submission: true, authed: true, userID: "uid-alice@example.com"}
		if err := s.Mail("alice@example.com", nil); err != nil {
			t.Fatalf("Mail: %v", err)
		}
		if err := s.Rcpt("bob@example.com", nil); err != nil {
			t.Fatalf("Rcpt local: %v", err)
		}
		if err := s.Data(strings.NewReader(authedMsg)); err != nil {
			t.Fatalf("local-only message %d was rate limited: %v", i, err)
		}
	}
}

// TestDataRejectsWhenSpoolFails is the accept-then-lose guarantee: if the
// message cannot be stored or queued, DATA must return an error so the sending
// MTA keeps the message and retries.
//
// Returning nil here is the worst failure mode a mail server has. The peer sees
// 250, deletes its copy, and the mail is gone with no bounce and no trace --
// which is precisely why "log the error and continue" must never creep into
// this path.
func TestDataRejectsWhenSpoolFails(t *testing.T) {
	boom := errors.New("disk full")

	tests := []struct {
		name    string
		rcpt    string
		setup   func(*Backend, *fakeSplitDeliverer, *fakeRelayQueue)
		wantErr string
	}{
		{
			name:    "blob store fails",
			rcpt:    "staff@example.com",
			setup:   func(b *Backend, d *fakeSplitDeliverer, q *fakeRelayQueue) { d.storeErr = boom },
			wantErr: "disk full",
		},
		{
			name:    "local enqueue fails",
			rcpt:    "staff@example.com",
			setup:   func(b *Backend, d *fakeSplitDeliverer, q *fakeRelayQueue) { d.enqueueErr = boom },
			wantErr: "disk full",
		},
		{
			name:    "outbound relay enqueue fails",
			rcpt:    "outsider@example.net",
			setup:   func(b *Backend, d *fakeSplitDeliverer, q *fakeRelayQueue) { q.enqueueErr = boom },
			wantErr: "disk full",
		},
		{
			name:    "outbound relay unavailable",
			rcpt:    "outsider@example.net",
			setup:   func(b *Backend, d *fakeSplitDeliverer, q *fakeRelayQueue) { b.relayQueue = nil },
			wantErr: "disk full",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, deliverer, relay := testBackend("staff@example.com", "alice@example.com")
			tt.setup(b, deliverer, relay)

			s := &Session{
				backend: b, logger: b.logger,
				submission: true, authed: true, userID: "uid-alice@example.com",
			}
			if err := s.Mail("alice@example.com", nil); err != nil {
				t.Fatalf("Mail: %v", err)
			}
			if err := s.Rcpt(tt.rcpt, nil); err != nil {
				t.Fatalf("Rcpt: %v", err)
			}

			err := s.Data(strings.NewReader(authedMsg))
			if err == nil {
				t.Fatal("DATA returned 250 for a message that was never spooled: " +
					"the sender will delete its copy and the mail is lost")
			}
			// The failure is temporary and must not echo internal detail.
			if smtpCode(err) != 451 {
				t.Errorf("error = %v, want a temporary 451", err)
			}
			if strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q leaks internal detail %q", err, tt.wantErr)
			}
		})
	}
}

// recipientSession opens an authenticated submission session for alice with
// the given external recipients, returning the session and the first RCPT error.
func recipientSession(t *testing.T, b *Backend, rcpts ...string) (*Session, []error) {
	t.Helper()
	s := &Session{backend: b, logger: b.logger, submission: true, authed: true, userID: "uid-alice@example.com"}
	if err := s.Mail("alice@example.com", nil); err != nil {
		t.Fatalf("Mail: %v", err)
	}
	errs := make([]error, 0, len(rcpts))
	for _, r := range rcpts {
		errs = append(errs, s.Rcpt(r, nil))
	}
	return s, errs
}

// TestOutboundRecipientLimitCountsRecipients: the budget is spent per
// recipient, so one message to many recipients cannot count as one.
func TestOutboundRecipientLimitCountsRecipients(t *testing.T) {
	b, _, relay := testBackend("alice@example.com")
	b.recipientRL = mail.NewRateLimiter(7)

	// Session 1: 3 recipients, within budget.
	s1, errs := recipientSession(t, b, "a@example.net", "b@example.net", "c@example.net")
	for i, err := range errs {
		if err != nil {
			t.Fatalf("session 1 rcpt %d: %v", i, err)
		}
	}
	if err := s1.Data(strings.NewReader(authedMsg)); err != nil {
		t.Fatalf("session 1 data: %v", err)
	}

	// Session 2: 5 recipients but only 4 units remain; the 5th is refused at
	// RCPT with 452 4.5.3 and the accepted four are queued.
	s2, errs := recipientSession(t, b, "d@example.net", "e@example.net", "f@example.net", "g@example.net", "h@example.net")
	for i := 0; i < 4; i++ {
		if errs[i] != nil {
			t.Fatalf("session 2 rcpt %d should be accepted: %v", i, errs[i])
		}
	}
	var se *gosmtp.SMTPError
	if !errors.As(errs[4], &se) || se.Code != 452 || se.EnhancedCode != (gosmtp.EnhancedCode{4, 5, 3}) {
		t.Fatalf("5th rcpt = %v, want 452 4.5.3", errs[4])
	}
	if err := s2.Data(strings.NewReader(authedMsg)); err != nil {
		t.Fatalf("session 2 data: %v", err)
	}
	if len(relay.enqueued) != 2 {
		t.Fatalf("enqueued = %d messages, want 2", len(relay.enqueued))
	}
	if got := len(relay.enqueued[1].GetRecipients()); got != 4 {
		t.Errorf("session 2 queued %d recipients, want exactly 4", got)
	}

	// Session 3: budget is spent.
	_, errs = recipientSession(t, b, "i@example.net")
	if code := smtpCode(errs[0]); code != 452 {
		t.Errorf("session 3 rcpt code = %d, want 452", code)
	}
}

func TestOutboundRecipientLimitPositiveAndLocalControls(t *testing.T) {
	b, _, relay := testBackend("alice@example.com", "bob@example.com")
	b.recipientRL = mail.NewRateLimiter(7)

	// Positive control: under the limit.
	s, errs := recipientSession(t, b, "a@example.net", "b@example.net")
	for _, err := range errs {
		if err != nil {
			t.Fatalf("rcpt under limit refused: %v", err)
		}
	}
	if err := s.Data(strings.NewReader(authedMsg)); err != nil || len(relay.enqueued) != 1 {
		t.Fatalf("data under limit: err=%v enqueued=%d", err, len(relay.enqueued))
	}

	// Local recipients never spend or consult the budget.
	for i := 0; i < 20; i++ {
		s, errs := recipientSession(t, b, "bob@example.com")
		if errs[0] != nil {
			t.Fatalf("local rcpt %d refused: %v", i, errs[0])
		}
		if err := s.Data(strings.NewReader(authedMsg)); err != nil {
			t.Fatalf("local data %d refused: %v", i, err)
		}
	}
	if got := b.recipientRL.Remaining("uid-alice@example.com"); got != 5 {
		t.Errorf("remaining = %d, want 5 (local mail must not spend budget)", got)
	}
}

// TestOutboundRecipientLimitDataCommitAtomic: RCPT only peeks; a parallel
// session can drain the budget in between, so DATA must refuse the whole
// message and queue nothing.
func TestOutboundRecipientLimitDataCommitAtomic(t *testing.T) {
	b, deliverer, relay := testBackend("alice@example.com")
	b.recipientRL = mail.NewRateLimiter(7)

	s, errs := recipientSession(t, b, "a@example.net", "b@example.net", "c@example.net")
	for _, err := range errs {
		if err != nil {
			t.Fatalf("rcpt: %v", err)
		}
	}
	// A second session spends 5 units between RCPT and DATA.
	if !b.recipientRL.AllowN("uid-alice@example.com", 5) {
		t.Fatal("setup drain failed")
	}

	err := s.Data(strings.NewReader(authedMsg))
	if code := smtpCode(err); code != 452 {
		t.Fatalf("data err = %v, want 452", err)
	}
	if len(relay.enqueued) != 0 {
		t.Errorf("relay queue holds %d messages, want 0", len(relay.enqueued))
	}
	if len(deliverer.localCalls) != 0 {
		t.Errorf("deliverer saw %d local calls, want 0", len(deliverer.localCalls))
	}
}

// A message rejected for its header From must not burn recipient budget.
func TestOutboundRecipientLimitNotSpentOnRejectedFrom(t *testing.T) {
	b, _, _ := testBackend("alice@example.com")
	b.recipientRL = mail.NewRateLimiter(7)

	s, _ := recipientSession(t, b, "a@example.net", "b@example.net")
	err := s.Data(strings.NewReader("From: mallory@evil.test\r\nSubject: x\r\n\r\nbody"))
	if err == nil {
		t.Fatal("forged header From was accepted")
	}
	if got := b.recipientRL.Remaining("uid-alice@example.com"); got != 7 {
		t.Errorf("remaining = %d, want 7", got)
	}
}
