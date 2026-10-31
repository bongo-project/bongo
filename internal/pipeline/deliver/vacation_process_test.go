package deliver

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/mail"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	bongomail "github.com/bongo-project/bongo/internal/mail"
	"github.com/bongo-project/bongo/internal/observability"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// vacApplier records commands and keeps vacation tracking PUTs so the same
// object serves as the tracker. failVacation makes writes to a tracking key
// fail.
type vacApplier struct {
	mu           sync.Mutex
	commands     []*bongov1.RaftCommand
	kv           map[string][]byte
	failVacation bool
	// getDelay slows Get so concurrent readers all see the state from before
	// the first writer's record.
	getDelay time.Duration
	// staleReads makes Get miss every record, as a follower that has not yet
	// applied a just-committed PUT would.
	staleReads bool
}

func (a *vacApplier) ApplyCommand(cmd *bongov1.RaftCommand, _ time.Duration) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if strings.Contains(string(cmd.Key), "/vacation/sent/") {
		if a.failVacation {
			return errors.New("raft unavailable")
		}
		if a.kv == nil {
			a.kv = map[string][]byte{}
		}
		if cmd.Op == bongov1.Op_OP_PUT {
			a.kv[string(cmd.Key)] = cmd.Value
		} else {
			delete(a.kv, string(cmd.Key))
		}
	}
	a.commands = append(a.commands, cmd)
	return nil
}

func (a *vacApplier) Get(key []byte) ([]byte, error) {
	if a.getDelay > 0 {
		time.Sleep(a.getDelay)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.staleReads {
		return nil, store.ErrNotFound
	}
	if v, ok := a.kv[string(key)]; ok {
		return v, nil
	}
	return nil, store.ErrNotFound
}

func (a *vacApplier) trackingKeys() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.kv)
}

type vacAccounts map[string]string

func (m vacAccounts) ResolveAccount(email string) (string, error) {
	if id, ok := m[strings.ToLower(email)]; ok {
		return id, nil
	}
	return "", fmt.Errorf("no account %s", email)
}

type vacProfiles map[string]string

func (m vacProfiles) PrimaryEmail(id string) (string, error) {
	if e, ok := m[id]; ok {
		return e, nil
	}
	return "", errors.New("no profile")
}

type vacAuthorizer map[string]bool // "uid|addr"

func (m vacAuthorizer) CanSendAs(uid, addr string) (bool, error) {
	return m[uid+"|"+strings.ToLower(addr)], nil
}

type localMail struct {
	from string
	rcpt []string
	raw  []byte
}

type fakeLocal struct {
	mu   sync.Mutex
	got  []localMail
	fail error
}

func (f *fakeLocal) Deliver(from string, rcpt []string, r io.Reader) error {
	raw, _ := io.ReadAll(r)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return f.fail
	}
	f.got = append(f.got, localMail{from, rcpt, raw})
	return nil
}

type vacHarness struct {
	t       *testing.T
	proc    *DeliverProcessor
	applier *vacApplier
	queue   *mockQueuer
	local   *fakeLocal
	blobs   *store.BlobStore
	prefs   *mockPrefs
	now     time.Time
	limiter *bongomail.RateLimiter
	authz   vacAuthorizer
}

func newVacHarness(t *testing.T) *vacHarness {
	t.Helper()
	bs, err := store.NewBlobStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := &vacHarness{
		t:       t,
		applier: &vacApplier{},
		queue:   &mockQueuer{},
		local:   &fakeLocal{},
		blobs:   bs,
		now:     time.Unix(1_800_000_000, 0),
		limiter: bongomail.NewRateLimiter(store.VacationRepliesPerHour),
		authz:   vacAuthorizer{},
		prefs: &mockPrefs{prefs: map[string]*bongov1.UserPreferences{
			"user-alice": {VacationEnabled: true, VacationBody: "I am away.\nBack soon.", ForwardKeepCopy: true},
		}},
	}
	resolver := &mockResolver{mapping: map[string]string{
		"alice@example.com": "user-alice", "alias@example.com": "user-alice", "carol@example.com": "user-carol",
	}}
	h.proc = NewDeliverProcessor(h.applier, resolver, newTestLogger())
	h.proc.SetPreferences(h.prefs)
	h.proc.SetQueuer(h.queue)
	h.proc.SetHostedDomains(func(d string) bool { return d == "example.com" })
	h.proc.SetSenderAuthorizer(h.authz)
	h.proc.SetVacation(VacationConfig{
		Accounts: vacAccounts{"alice@example.com": "user-alice", "alias@example.com": "user-alice", "carol@example.com": "user-carol"},
		Profiles: vacProfiles{"user-alice": "alice@example.com", "user-carol": "carol@example.com"},
		Tracker:  h.applier,
		Blobs:    bs,
		Local:    h.local,
		Limiter:  h.limiter,
		Now:      func() time.Time { return h.now },
	})
	return h
}

// send runs one message through Process and returns the error.
func (h *vacHarness) send(env *bongov1.QueueEnvelope, raw string) error {
	h.t.Helper()
	if env.Id == "" {
		env.Id = fmt.Sprintf("env-%d", time.Now().UnixNano())
	}
	if env.ReceivedAt == 0 {
		env.ReceivedAt = h.now.Unix()
	}
	env.BlobHash = "h"
	_, err := h.proc.Process(context.Background(), env, strings.NewReader(raw))
	return err
}

func extMsg(sender string, extra ...string) (*bongov1.QueueEnvelope, string) {
	env := &bongov1.QueueEnvelope{Sender: sender, Recipients: []string{"alice@example.com"}, SpfResult: "pass"}
	raw := "From: " + sender + "\r\nTo: alice@example.com\r\nSubject: Lunch?\r\nMessage-ID: <orig-1@example.net>\r\n" +
		"Date: Mon, 01 Jan 2024 12:00:00 +0000\r\n" + strings.Join(extra, "") + "\r\nHello\r\n"
	return env, raw
}

func (h *vacHarness) autoReplies() []*bongov1.OutboundMessage {
	var out []*bongov1.OutboundMessage
	for _, m := range h.queue.messages {
		if m.Origin == bongov1.OutboundOrigin_OUTBOUND_ORIGIN_AUTOREPLY {
			out = append(out, m)
		}
	}
	return out
}

func (h *vacHarness) blobText(hash string) string {
	h.t.Helper()
	rc, err := h.blobs.Get(hash)
	if err != nil {
		h.t.Fatalf("blob %s: %v", hash, err)
	}
	defer rc.Close()
	raw, err := io.ReadAll(rc)
	if err != nil {
		h.t.Fatalf("read blob %s: %v", hash, err)
	}
	return string(raw)
}

func counter(result string) float64 {
	return testutil.ToFloat64(observability.VacationRepliesTotal.WithLabelValues(result))
}

func TestVacationReplyToExternalSender(t *testing.T) {
	h := newVacHarness(t)
	env, raw := extMsg("bob@example.net")
	sent := counter("sent")
	if err := h.send(env, raw); err != nil {
		t.Fatal(err)
	}
	replies := h.autoReplies()
	if len(replies) != 1 {
		t.Fatalf("replies = %d, want 1", len(replies))
	}
	r := replies[0]
	if r.SenderEmail != "" || r.SenderId != "user-alice" || len(r.Recipients) != 1 || r.Recipients[0] != "bob@example.net" {
		t.Fatalf("queued reply = %+v", r)
	}
	if counter("sent") != sent+1 {
		t.Error("sent metric not incremented")
	}
	text := h.blobText(r.BlobHash)
	msg, err := mail.ReadMessage(strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	for hdr, want := range map[string]string{
		"From": "<alice@example.com>", "To": "<bob@example.net>", "Subject": "Auto: Lunch?",
		"Auto-Submitted": "auto-replied", "In-Reply-To": "<orig-1@example.net>", "References": "<orig-1@example.net>",
		"Content-Type": "text/plain; charset=utf-8",
	} {
		if got := msg.Header.Get(hdr); got != want {
			t.Errorf("%s = %q, want %q", hdr, got, want)
		}
	}
	if id := msg.Header.Get("Message-Id"); !strings.HasSuffix(id, "@example.com>") || id == "<orig-1@example.net>" {
		t.Errorf("Message-ID = %q, want a fresh id on the From domain", id)
	}
	body, _ := io.ReadAll(msg.Body)
	if !strings.Contains(string(body), "I am away.\r\nBack soon.") {
		t.Errorf("body = %q", body)
	}
	// The record is written, with the time of the reply.
	val, err := h.applier.Get(store.VacationSentKey("user-alice", "bob@example.net"))
	if err != nil {
		t.Fatalf("tracking record: %v", err)
	}
	if at, err := store.DecodeVacationSent(val); err != nil || !at.Equal(h.now) {
		t.Errorf("record = %v, %v", at, err)
	}
	// Delivery itself is unchanged.
	if n := len(decodeDeliveries(t, &mockApplier{commands: h.applier.commands})); n != 1 {
		t.Errorf("deliveries = %d, want 1", n)
	}
}

func TestVacationConfiguredSubject(t *testing.T) {
	h := newVacHarness(t)
	h.prefs.prefs["user-alice"].VacationSubject = "Out of office"
	env, raw := extMsg("bob@example.net")
	if err := h.send(env, raw); err != nil {
		t.Fatal(err)
	}
	msg, _ := mail.ReadMessage(strings.NewReader(h.blobText(h.autoReplies()[0].BlobHash)))
	if got := msg.Header.Get("Subject"); got != "Out of office" {
		t.Errorf("Subject = %q", got)
	}
}

func TestVacationInterval(t *testing.T) {
	h := newVacHarness(t)
	h.prefs.prefs["user-alice"].VacationIntervalDays = 3
	env, raw := extMsg("bob@example.net")
	if err := h.send(env, raw); err != nil {
		t.Fatal(err)
	}
	// One second short of the interval: still suppressed.
	h.now = h.now.Add(3*24*time.Hour - time.Second)
	env, raw = extMsg("Bob@Example.NET")
	if err := h.send(env, raw); err != nil {
		t.Fatal(err)
	}
	if n := len(h.autoReplies()); n != 1 {
		t.Fatalf("replies inside the interval = %d, want 1", n)
	}
	// A different sender is unaffected.
	env, raw = extMsg("dave@example.net")
	if err := h.send(env, raw); err != nil {
		t.Fatal(err)
	}
	if n := len(h.autoReplies()); n != 2 {
		t.Fatalf("replies after another sender = %d, want 2", n)
	}
	// At the interval the sender is answered again.
	h.now = h.now.Add(time.Second)
	env, raw = extMsg("bob@example.net")
	if err := h.send(env, raw); err != nil {
		t.Fatal(err)
	}
	if n := len(h.autoReplies()); n != 3 {
		t.Fatalf("replies after the interval = %d, want 3", n)
	}
}

func TestVacationDefaultIntervalIsSevenDays(t *testing.T) {
	h := newVacHarness(t) // VacationIntervalDays is 0
	env, raw := extMsg("bob@example.net")
	_ = h.send(env, raw)
	h.now = h.now.Add(6*24*time.Hour + 23*time.Hour)
	env, raw = extMsg("bob@example.net")
	_ = h.send(env, raw)
	if n := len(h.autoReplies()); n != 1 {
		t.Fatalf("replies at 6d23h = %d, want 1", n)
	}
	h.now = h.now.Add(time.Hour)
	env, raw = extMsg("bob@example.net")
	_ = h.send(env, raw)
	if n := len(h.autoReplies()); n != 2 {
		t.Fatalf("replies at 7d = %d, want 2", n)
	}
}

func TestVacationTrackingWriteFailureSendsNothing(t *testing.T) {
	h := newVacHarness(t)
	h.applier.failVacation = true
	failed := counter("track_failed")
	env, raw := extMsg("bob@example.net")
	if err := h.send(env, raw); err != nil {
		t.Fatalf("a vacation failure must not fail delivery: %v", err)
	}
	if n := len(h.autoReplies()); n != 0 {
		t.Fatalf("replies = %d, want 0 (fail closed)", n)
	}
	if counter("track_failed") != failed+1 {
		t.Error("track_failed metric not incremented")
	}
	if n := len(decodeDeliveries(t, &mockApplier{commands: h.applier.commands})); n != 1 {
		t.Errorf("message not delivered: %d", n)
	}
}

func TestVacationEnqueueFailureAfterRecordIsLogged(t *testing.T) {
	h := newVacHarness(t)
	h.queue.err = errors.New("queue down")
	var logs bytes.Buffer
	h.proc.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	lost := counter("enqueue_failed")
	env, raw := extMsg("bob@example.net")
	if err := h.send(env, raw); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if h.applier.trackingKeys() != 1 {
		t.Error("record must be written before the reply is queued")
	}
	if counter("enqueue_failed") != lost+1 {
		t.Error("enqueue_failed metric not incremented")
	}
	if !strings.Contains(logs.String(), "vacation reply lost") {
		t.Errorf("failure not logged:\n%s", logs.String())
	}
	// The record stays, so the retry of the same sender is suppressed.
	h.queue.err = nil
	env, raw = extMsg("bob@example.net")
	_ = h.send(env, raw)
	if n := len(h.autoReplies()); n != 0 {
		t.Errorf("replies after a lost reply = %d, want 0", n)
	}
}

func TestVacationHourlyLimit(t *testing.T) {
	h := newVacHarness(t)
	h.limiter.SetClock(func() time.Time { return h.now })
	limited := counter("rate_limited")
	for i := range store.VacationRepliesPerHour {
		env, raw := extMsg(fmt.Sprintf("s%d@example.net", i))
		if err := h.send(env, raw); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(h.autoReplies()); n != store.VacationRepliesPerHour {
		t.Fatalf("replies = %d, want %d", n, store.VacationRepliesPerHour)
	}
	env, raw := extMsg("overflow@example.net")
	if err := h.send(env, raw); err != nil {
		t.Fatal(err)
	}
	if n := len(h.autoReplies()); n != store.VacationRepliesPerHour {
		t.Fatalf("the 101st reply was sent: %d", n)
	}
	if counter("rate_limited") != limited+1 {
		t.Error("rate_limited metric not incremented")
	}
	if _, err := h.applier.Get(store.VacationSentKey("user-alice", "overflow@example.net")); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a capped reply left a tracking record: %v", err)
	}
	// Once the hour passes the capped sender is answered.
	h.now = h.now.Add(time.Hour + time.Second)
	env, raw = extMsg("overflow@example.net")
	_ = h.send(env, raw)
	if n := len(h.autoReplies()); n != store.VacationRepliesPerHour+1 {
		t.Errorf("replies after the hour = %d", n)
	}
}

func TestVacationHeaderInjection(t *testing.T) {
	h := newVacHarness(t)
	h.prefs.prefs["user-alice"].VacationSubject = "Away\r\nBcc: attacker@evil.test"
	env, raw := extMsg("bob@example.net")
	// The Message-ID is folded onto a second header line, and the Subject
	// carries CRLF inside an encoded word.
	raw = strings.Replace(raw, "Message-ID: <orig-1@example.net>", "Message-ID: <orig-1@example.net>\r\n X-Evil: yes", 1)
	if err := h.send(env, raw); err != nil {
		t.Fatal(err)
	}
	text := h.blobText(h.autoReplies()[0].BlobHash)
	headerBlock, _, _ := strings.Cut(text, "\r\n\r\n")
	for _, bad := range []string{"\nBcc:", "\nX-Evil", "\n X-Evil"} {
		if strings.Contains(headerBlock, bad) {
			t.Errorf("injected %q in:\n%s", bad, headerBlock)
		}
	}
	if strings.Contains(headerBlock, "In-Reply-To") {
		t.Errorf("a malformed Message-ID was echoed:\n%s", headerBlock)
	}
	// The configured subject arrives with its line break removed, not merely
	// hidden inside an encoded word.
	if m, err := mail.ReadMessage(strings.NewReader(text)); err != nil {
		t.Fatal(err)
	} else if dec, _ := new(mime.WordDecoder).DecodeHeader(m.Header.Get("Subject")); dec != "AwayBcc: attacker@evil.test" {
		t.Errorf("configured subject = %q", dec)
	}

	// Original subject with CRLF hidden in an encoded word, with the
	// configured subject cleared.
	h2 := newVacHarness(t)
	env, raw = extMsg("bob@example.net")
	raw = strings.Replace(raw, "Subject: Lunch?", "Subject: =?utf-8?q?Hi=0D=0ABcc:_attacker@evil.test?=", 1)
	if err := h2.send(env, raw); err != nil {
		t.Fatal(err)
	}
	text = h2.blobText(h2.autoReplies()[0].BlobHash)
	headerBlock, _, _ = strings.Cut(text, "\r\n\r\n")
	if strings.Contains(headerBlock, "\nBcc:") || strings.Contains(headerBlock, "\r\nBcc:") {
		t.Errorf("subject injected a header:\n%s", headerBlock)
	}
	msg, _ := mail.ReadMessage(strings.NewReader(text))
	if got := msg.Header.Get("Subject"); got != "Auto: HiBcc: attacker@evil.test" {
		t.Errorf("Subject = %q", got)
	}
}

func TestVacationNonASCIIOriginalSubject(t *testing.T) {
	h := newVacHarness(t)
	env, raw := extMsg("bob@example.net")
	raw = strings.Replace(raw, "Subject: Lunch?", "Subject: =?utf-8?q?Gr=C3=BC=C3=9Fe?=", 1)
	if err := h.send(env, raw); err != nil {
		t.Fatal(err)
	}
	text := h.blobText(h.autoReplies()[0].BlobHash)
	headerBlock, _, _ := strings.Cut(text, "\r\n\r\n")
	for i := 0; i < len(headerBlock); i++ {
		if headerBlock[i] >= 0x80 {
			t.Fatalf("raw non-ASCII in header block:\n%s", headerBlock)
		}
	}
	msg, _ := mail.ReadMessage(strings.NewReader(text))
	dec, _ := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if dec != "Auto: Grüße" {
		t.Errorf("decoded subject = %q", dec)
	}
}

func TestVacationFromSelection(t *testing.T) {
	tests := []struct {
		name     string
		rcpt     string
		identity bool
		want     string
	}{
		{"alias without send-as uses the primary", "alias@example.com", false, "<alice@example.com>"},
		{"alias with send-as is used", "alias@example.com", true, "<alias@example.com>"},
		{"primary", "alice@example.com", false, "<alice@example.com>"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newVacHarness(t)
			if tc.identity {
				h.authz["user-alice|"+tc.rcpt] = true
			}
			env, raw := extMsg("bob@example.net")
			env.Recipients = []string{tc.rcpt}
			raw = strings.Replace(raw, "To: alice@example.com", "To: "+tc.rcpt, 1)
			if err := h.send(env, raw); err != nil {
				t.Fatal(err)
			}
			replies := h.autoReplies()
			if len(replies) != 1 {
				t.Fatalf("replies = %d", len(replies))
			}
			msg, _ := mail.ReadMessage(strings.NewReader(h.blobText(replies[0].BlobHash)))
			if got := msg.Header.Get("From"); got != tc.want {
				t.Errorf("From = %q, want %q", got, tc.want)
			}
		})
	}
}

// Mail sent to an address that merely routes to the user (a catch-all, here
// unknown@example.com resolving for delivery) is not "addressed to the user".
func TestVacationCatchAllRecipientDoesNotCount(t *testing.T) {
	h := newVacHarness(t)
	h.proc.resolver.(*mockResolver).mapping["unknown@example.com"] = "user-alice" // catch-all routing
	env, raw := extMsg("bob@example.net")
	env.Recipients = []string{"unknown@example.com"}
	raw = strings.Replace(raw, "To: alice@example.com", "To: unknown@example.com", 1)
	if err := h.send(env, raw); err != nil {
		t.Fatal(err)
	}
	if n := len(h.autoReplies()); n != 0 {
		t.Fatalf("replies = %d, want 0", n)
	}
	// Bcc'd mail (user in neither To nor Cc) is not answered either.
	env, raw = extMsg("bob@example.net")
	raw = strings.Replace(raw, "To: alice@example.com", "To: someone@else.test", 1)
	_ = h.send(env, raw)
	if n := len(h.autoReplies()); n != 0 {
		t.Fatalf("replies to a Bcc = %d, want 0", n)
	}
	// Cc counts.
	env, raw = extMsg("bob@example.net")
	raw = strings.Replace(raw, "To: alice@example.com", "To: someone@else.test\r\nCc: Alice <alice@example.com>", 1)
	_ = h.send(env, raw)
	if n := len(h.autoReplies()); n != 1 {
		t.Fatalf("replies to a Cc = %d, want 1", n)
	}
}

func TestVacationSuppressionThroughProcess(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(env *bongov1.QueueEnvelope, raw *string)
		wantReply bool
	}{
		{"baseline", func(*bongov1.QueueEnvelope, *string) {}, true},
		{"rule moved to Trash", func(e *bongov1.QueueEnvelope, _ *string) { e.TargetMailbox = "Trash" }, false},
		{"rule moved to Junk", func(e *bongov1.QueueEnvelope, _ *string) { e.TargetMailbox = "Junk" }, false},
		{"rule moved to another folder", func(e *bongov1.QueueEnvelope, _ *string) { e.TargetMailbox = "Work" }, true},
		{"copy to Trash, normal destination INBOX", func(e *bongov1.QueueEnvelope, _ *string) { e.CopyMailboxes = []string{"Trash"} }, true},
		{"deleted with a copy", func(e *bongov1.QueueEnvelope, _ *string) {
			e.DiscardNormal = true
			e.CopyMailboxes = []string{"Archive"}
		}, false},
		{"spam score at the tag threshold", func(e *bongov1.QueueEnvelope, _ *string) {
			e.Stages = []*bongov1.StageResult{{Stage: "antispam", Score: 0.5, Action: "pass"}}
		}, false},
		{"just below the tag threshold", func(e *bongov1.QueueEnvelope, _ *string) {
			e.Stages = []*bongov1.StageResult{{Stage: "antispam", Score: 0.49, Action: "pass"}}
		}, true},
		{"antispam tag action below the threshold", func(e *bongov1.QueueEnvelope, _ *string) {
			e.Stages = []*bongov1.StageResult{{Stage: "antispam", Score: 0.1, Action: "tag"}}
		}, false},
		{"quarantine released to INBOX", func(e *bongov1.QueueEnvelope, _ *string) {
			e.TargetMailbox = "INBOX"
			e.Stages = []*bongov1.StageResult{{Stage: "antispam", Score: 0.1, Action: "quarantine"}}
		}, false},
		{"SPF fail", func(e *bongov1.QueueEnvelope, _ *string) { e.SpfResult = "fail" }, false},
		{"SPF none", func(e *bongov1.QueueEnvelope, _ *string) { e.SpfResult = "none" }, false},
		{"SPF none but authenticated submitter", func(e *bongov1.QueueEnvelope, _ *string) {
			e.SpfResult = "none"
			e.AuthUserId = "someone"
		}, true},
		{"null sender", func(e *bongov1.QueueEnvelope, _ *string) { e.Sender = "" }, false},
		{"Auto-Submitted header", func(_ *bongov1.QueueEnvelope, r *string) {
			*r = "Auto-Submitted: auto-generated\r\n" + *r
		}, false},
		{"List-Id header", func(_ *bongov1.QueueEnvelope, r *string) { *r = "List-Id: <l.example.net>\r\n" + *r }, false},
		{"Precedence bulk", func(_ *bongov1.QueueEnvelope, r *string) { *r = "Precedence: bulk\r\n" + *r }, false},
		{"sender is the user's alias", func(e *bongov1.QueueEnvelope, _ *string) { e.Sender = "alias@example.com" }, false},
		{"sender is one of the user's send-as identities", func(e *bongov1.QueueEnvelope, _ *string) {
			e.Sender = "me@personal.test"
		}, false},
		{"sender on a hosted domain without an account", func(e *bongov1.QueueEnvelope, _ *string) {
			e.Sender = "ghost@example.com"
		}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newVacHarness(t)
			h.authz["user-alice|me@personal.test"] = true
			env, raw := extMsg("bob@example.net")
			tc.mutate(env, &raw)
			if err := h.send(env, raw); err != nil {
				t.Fatal(err)
			}
			if got := len(h.autoReplies()) == 1; got != tc.wantReply {
				t.Fatalf("reply = %v, want %v (queued %d)", got, tc.wantReply, len(h.queue.messages))
			}
			if !tc.wantReply && h.applier.trackingKeys() != 0 {
				t.Error("a suppressed reply left a tracking record")
			}
		})
	}
}

func TestVacationFailsClosedWithoutDependencies(t *testing.T) {
	base := func(h *vacHarness) VacationConfig {
		return VacationConfig{Accounts: vacAccounts{}, Profiles: vacProfiles{}, Tracker: h.applier, Blobs: h.blobs,
			Local: h.local, Limiter: h.limiter}
	}
	for name, mutate := range map[string]func(c *VacationConfig){
		"accounts": func(c *VacationConfig) { c.Accounts = nil },
		"profiles": func(c *VacationConfig) { c.Profiles = nil },
		"tracker":  func(c *VacationConfig) { c.Tracker = nil },
		"blobs":    func(c *VacationConfig) { c.Blobs = nil },
		"local":    func(c *VacationConfig) { c.Local = nil },
		"limiter":  func(c *VacationConfig) { c.Limiter = nil },
	} {
		t.Run(name, func(t *testing.T) {
			h := newVacHarness(t)
			cfg := base(h)
			cfg.Accounts = vacAccounts{"alice@example.com": "user-alice"}
			cfg.Profiles = vacProfiles{"user-alice": "alice@example.com"}
			mutate(&cfg)
			h.proc.SetVacation(cfg)
			if h.proc.VacationEnabled() {
				t.Fatal("incomplete config reports enabled")
			}
			env, raw := extMsg("bob@example.net")
			if err := h.send(env, raw); err != nil {
				t.Fatal(err)
			}
			if n := len(h.autoReplies()); n != 0 {
				t.Errorf("replies = %d, want 0", n)
			}
		})
	}
	h := newVacHarness(t)
	h.proc.vacation = nil
	env, raw := extMsg("bob@example.net")
	_ = h.send(env, raw)
	if n := len(h.autoReplies()); n != 0 {
		t.Errorf("replies without SetVacation = %d", n)
	}
}

func TestVacationDoesNotChangeForwarding(t *testing.T) {
	for _, keep := range []bool{true, false} {
		t.Run(fmt.Sprintf("keep copy %v", keep), func(t *testing.T) {
			h := newVacHarness(t)
			p := h.prefs.prefs["user-alice"]
			p.ForwardAddress, p.ForwardKeepCopy = "away@elsewhere.test", keep
			h.proc.SetForwardTargets(allowAll{})
			env, raw := extMsg("bob@example.net")
			if err := h.send(env, raw); err != nil {
				t.Fatal(err)
			}
			var forwards int
			for _, m := range h.queue.messages {
				if m.Origin == bongov1.OutboundOrigin_OUTBOUND_ORIGIN_FORWARD {
					forwards++
				}
			}
			if forwards != 1 {
				t.Errorf("forwards = %d, want 1", forwards)
			}
			// Forwarding without a copy is not a deletion: the user is still away.
			if n := len(h.autoReplies()); n != 1 {
				t.Errorf("replies = %d, want 1", n)
			}
			delivered := len(decodeDeliveries(t, &mockApplier{commands: h.applier.commands}))
			if want := map[bool]int{true: 1, false: 0}[keep]; delivered != want {
				t.Errorf("local deliveries = %d, want %d", delivered, want)
			}
		})
	}
}

type allowAll struct{}

func (allowAll) Allowed(string, string) (bool, error) { return true, nil }

func TestVacationReplyToLocalSenderIsSignedAndNeverLoops(t *testing.T) {
	h := newVacHarness(t)
	h.prefs.prefs["user-carol"] = &bongov1.UserPreferences{VacationEnabled: true, VacationBody: "Carol is away.", ForwardKeepCopy: true}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	reg := bongomail.NewDKIMRegistry()
	reg.Add("example.com", bongomail.NewDKIMSignerFromKey("sel", "example.com", key))
	cfg := VacationConfig{
		Accounts: vacAccounts{"alice@example.com": "user-alice", "carol@example.com": "user-carol"},
		Profiles: vacProfiles{"user-alice": "alice@example.com", "user-carol": "carol@example.com"},
		Tracker:  h.applier, Blobs: h.blobs, Local: h.local, Limiter: h.limiter, DKIM: reg,
		Now: func() time.Time { return h.now },
	}
	h.proc.SetVacation(cfg)

	// Carol, authenticated, writes to Alice, who is away.
	env, raw := extMsg("carol@example.com")
	env.AuthUserId = "user-carol"
	env.SpfResult = "none"
	if err := h.send(env, raw); err != nil {
		t.Fatal(err)
	}
	if len(h.autoReplies()) != 0 {
		t.Fatal("a local sender's reply went through the outbound queue")
	}
	if len(h.local.got) != 1 {
		t.Fatalf("local replies = %d, want 1", len(h.local.got))
	}
	lm := h.local.got[0]
	if lm.from != "" || len(lm.rcpt) != 1 || lm.rcpt[0] != "carol@example.com" {
		t.Fatalf("local delivery = %+v", lm)
	}
	if !bytes.Contains(lm.raw, []byte("DKIM-Signature:")) {
		t.Errorf("local reply is not DKIM-signed:\n%s", lm.raw)
	}

	// Feed the reply back in as Carol's inbound mail, with her vacation on:
	// the null sender and Auto-Submitted header stop the loop.
	env2 := &bongov1.QueueEnvelope{Sender: "", Recipients: []string{"carol@example.com"}, SpfResult: "none"}
	before := len(h.local.got) + len(h.queue.messages)
	if err := h.send(env2, string(lm.raw)); err != nil {
		t.Fatal(err)
	}
	if after := len(h.local.got) + len(h.queue.messages); after != before {
		t.Fatalf("the reply caused another message (%d -> %d)", before, after)
	}
	// Even with a made-up envelope sender, Auto-Submitted stops it.
	env3 := &bongov1.QueueEnvelope{Sender: "alice@example.com", Recipients: []string{"carol@example.com"}, SpfResult: "pass"}
	if err := h.send(env3, string(lm.raw)); err != nil {
		t.Fatal(err)
	}
	if after := len(h.local.got) + len(h.queue.messages); after != before {
		t.Fatalf("Auto-Submitted did not stop the loop (%d -> %d)", before, after)
	}
}

func TestVacationLocalDeliveryFailureIsCounted(t *testing.T) {
	h := newVacHarness(t)
	h.local.fail = errors.New("spool full")
	lost := counter("enqueue_failed")
	env, raw := extMsg("carol@example.com")
	env.AuthUserId = "user-carol"
	if err := h.send(env, raw); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if counter("enqueue_failed") != lost+1 {
		t.Error("enqueue_failed not incremented")
	}
}

// The existing list-mail fixture and a spam message must never be answered,
// even when the user is in To and the sender is authenticated.
func TestVacationNoReplyForListAndSpamFixtures(t *testing.T) {
	list, err := os.ReadFile("../../../testdata/demo/mail/eml/07-list-message.eml")
	if err != nil {
		t.Fatal(err)
	}
	raw := strings.Replace(strings.ReplaceAll(string(list), "\r\n", "\n"), "To: gardeners@lists.example.org", "To: alice@example.com", 1)
	raw = strings.ReplaceAll(raw, "\n", "\r\n")
	if !strings.Contains(raw, "To: alice@example.com") {
		t.Fatal("fixture no longer has the expected To header")
	}
	t.Run("list mail", func(t *testing.T) {
		h := newVacHarness(t)
		env := &bongov1.QueueEnvelope{Sender: "sven@example.org", Recipients: []string{"alice@example.com"}, SpfResult: "pass"}
		if err := h.send(env, raw); err != nil {
			t.Fatal(err)
		}
		if n := len(h.autoReplies()); n != 0 {
			t.Fatalf("replies to list mail = %d", n)
		}
	})
	t.Run("spam", func(t *testing.T) {
		h := newVacHarness(t)
		env, msg := extMsg("bob@example.net")
		env.Stages = []*bongov1.StageResult{{Stage: "antispam", Score: 0.95, Action: "tag"}}
		if err := h.send(env, msg); err != nil {
			t.Fatal(err)
		}
		if n := len(h.autoReplies()); n != 0 {
			t.Fatalf("replies to spam = %d", n)
		}
	})
}

// Two messages from one sender handled by concurrent workers produce one
// reply: the check-then-write is serialised on this node.
func TestVacationConcurrentSameSenderRepliesOnce(t *testing.T) {
	h := newVacHarness(t)
	h.applier.getDelay = 30 * time.Millisecond
	q := &lockedQueue{}
	h.proc.SetQueuer(q)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			env, raw := extMsg("bob@example.net")
			env.Id = fmt.Sprintf("c-%d", i)
			if err := h.send(env, raw); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if n := q.count(); n != 1 {
		t.Fatalf("replies = %d, want 1", n)
	}
}

type lockedQueue struct {
	mu sync.Mutex
	n  int
}

func (q *lockedQueue) Enqueue(*bongov1.OutboundMessage) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.n++
	return nil
}

func (q *lockedQueue) count() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.n
}

// One message to two of the same user's addresses gets one reply, even when
// the tracking record written for the first is not yet readable.
func TestVacationOneReplyPerMessageForSeveralAddresses(t *testing.T) {
	h := newVacHarness(t)
	h.applier.staleReads = true
	env, raw := extMsg("bob@example.net")
	env.Recipients = []string{"alice@example.com", "alias@example.com"}
	raw = strings.Replace(raw, "To: alice@example.com", "To: alice@example.com, alias@example.com", 1)
	if err := h.send(env, raw); err != nil {
		t.Fatal(err)
	}
	if n := len(h.autoReplies()); n != 1 {
		t.Fatalf("replies = %d, want 1", n)
	}
}

// The vacation window is judged against the stage's clock, so a window that
// covers the injected time but not the wall clock still replies.
func TestVacationWindowUsesInjectedClock(t *testing.T) {
	h := newVacHarness(t)
	p := h.prefs.prefs["user-alice"]
	p.VacationStart = h.now.Add(-time.Hour).Unix()
	p.VacationEnd = h.now.Add(time.Hour).Unix()
	env, raw := extMsg("bob@example.net")
	if err := h.send(env, raw); err != nil {
		t.Fatal(err)
	}
	if n := len(h.autoReplies()); n != 1 {
		t.Fatalf("replies = %d, want 1", n)
	}
}

func TestVacationReplySubjectWithoutAnySubject(t *testing.T) {
	for _, tc := range []struct{ configured, original, want string }{
		{"", "", "Auto-reply"},
		{"", "   ", "Auto-reply"},
		{"", "Lunch?", "Auto: Lunch?"},
		{"Away", "", "Away"},
	} {
		if got := vacationReplySubject(tc.configured, tc.original); got != tc.want {
			t.Errorf("vacationReplySubject(%q, %q) = %q, want %q", tc.configured, tc.original, got, tc.want)
		}
	}
}
