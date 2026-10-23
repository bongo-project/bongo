package deliver

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	bongomail "github.com/bongo-project/bongo/internal/mail"
	"github.com/bongo-project/bongo/internal/srs"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

var srsTestKey = []byte("0123456789abcdef0123456789abcdef")

// srsNow is a fixed instant whose day count is not a multiple of 32 or 1024.
var srsNow = time.Unix(20000*86400+3600, 0).UTC()

func srsCodecAt(maxAge int, at time.Time) *srs.Codec {
	return srs.NewWithClock(srsTestKey, maxAge, func() time.Time { return at })
}

type fakeUserDomains struct {
	domains map[string]string
	err     error
}

func (f fakeUserDomains) UserDomain(userID string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.domains[userID], nil
}

var hostedSet = map[string]bool{"hosted.example": true, "other.example": true}

func hosted(domain string) bool { return hostedSet[domain] }

// catchAllResolver sends every address on hosted.example that is not an exact
// mapping to a catch-all owner, as the store resolver would without its SRS
// guard.
type catchAllResolver struct{ mockResolver }

func (c catchAllResolver) ResolveRecipient(email string) (string, error) {
	if id, err := c.mockResolver.ResolveRecipient(email); err == nil {
		return id, nil
	}
	if strings.HasSuffix(email, "@hosted.example") {
		return "user-catchall", nil
	}
	return "", errors.New("not local")
}

func newSRSProc(t *testing.T, queuer *mockQueuer, applier *mockApplier) *DeliverProcessor {
	t.Helper()
	proc := NewDeliverProcessor(applier, catchAllResolver{mockResolver{mapping: map[string]string{
		"alice@hosted.example": "user-alice",
		"carol@other.example":  "user-carol",
	}}}, newTestLogger())
	proc.SetQueuer(queuer)
	proc.SetForwardTargets(allowAllTargets{})
	proc.SetHostedDomains(hosted)
	proc.SetSRS(srsCodecAt(17, srsNow))
	proc.SetUserDomains(fakeUserDomains{domains: map[string]string{
		"user-alice": "hosted.example",
		"user-carol": "other.example",
	}})
	return proc
}

func TestForwardGetsSRSSender(t *testing.T) {
	srs0 := regexp.MustCompile(`^SRS0=[a-z2-7]{8}=[a-z2-7]{2}=example\.org=bob@hosted\.example$`)
	raw := minimalMessage("bob@example.org", "alice@hosted.example", "Hello", "Hi!\r\n")

	for _, path := range []string{"forward address", "rule forward"} {
		t.Run(path, func(t *testing.T) {
			queuer := &mockQueuer{}
			proc := newSRSProc(t, queuer, &mockApplier{})
			env := &bongov1.QueueEnvelope{
				Id: "e1", Sender: "bob@example.org", Recipients: []string{"alice@hosted.example"},
				ReceivedAt: time.Now().Unix(), BlobHash: "blob1",
			}
			if path == "forward address" {
				proc.SetPreferences(&mockPrefs{prefs: map[string]*bongov1.UserPreferences{"user-alice": {ForwardAddress: "ext@dest.example"}}})
			} else {
				env.ForwardRequests = []*bongov1.ForwardRequest{{UserId: "user-alice", Target: "ext@dest.example"}}
			}
			if _, err := proc.Process(context.Background(), env, strings.NewReader(raw)); err != nil {
				t.Fatalf("Process: %v", err)
			}
			if len(queuer.messages) != 1 {
				t.Fatalf("forwards = %d, want 1", len(queuer.messages))
			}
			m := queuer.messages[0]
			if !srs0.MatchString(m.SenderEmail) {
				t.Fatalf("sender = %q, want SRS0 on hosted.example", m.SenderEmail)
			}
			if m.BlobHash != "blob1" {
				t.Errorf("blob = %q, want the unmodified original blob1 (header From untouched)", m.BlobHash)
			}
			if m.Origin != bongov1.OutboundOrigin_OUTBOUND_ORIGIN_FORWARD {
				t.Errorf("origin = %v, want FORWARD", m.Origin)
			}
			back, err := srsCodecAt(17, srsNow).Reverse(m.SenderEmail)
			if err != nil || back != "bob@example.org" {
				t.Errorf("Reverse = %q, %v; want bob@example.org", back, err)
			}
		})
	}
}

func TestForwardSRSNotApplied(t *testing.T) {
	raw := minimalMessage("a@hosted.example", "alice@hosted.example", "Hello", "Hi!\r\n")
	tests := []struct {
		name       string
		sender     string
		authUser   string
		allowLocal bool
		domains    fakeUserDomains
		hosted     func(string) bool
		want       string
	}{
		{"local origin keeps author sender", "a@hosted.example", "user-a", true, fakeUserDomains{domains: map[string]string{"user-alice": "hosted.example"}}, hosted, "a@hosted.example"},
		{"null sender stays null", "", "", false, fakeUserDomains{domains: map[string]string{"user-alice": "hosted.example"}}, hosted, ""},
		{"blank sender stays blank", "  ", "", false, fakeUserDomains{domains: map[string]string{"user-alice": "hosted.example"}}, hosted, "  "},
		{"lookup error keeps sender", "bob@example.org", "", false, fakeUserDomains{err: errors.New("boom")}, hosted, "bob@example.org"},
		{"empty domain keeps sender", "bob@example.org", "", false, fakeUserDomains{}, hosted, "bob@example.org"},
		{"unhosted domain keeps sender", "bob@example.org", "", false, fakeUserDomains{domains: map[string]string{"user-alice": "elsewhere.example"}}, hosted, "bob@example.org"},
		{"malformed sender keeps sender", "not an address", "", false, fakeUserDomains{domains: map[string]string{"user-alice": "hosted.example"}}, hosted, "not an address"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queuer := &mockQueuer{}
			proc := newSRSProc(t, queuer, &mockApplier{})
			proc.SetUserDomains(tt.domains)
			proc.SetHostedDomains(tt.hosted)
			if tt.allowLocal {
				proc.SetSenderAuthorizer(&fakeAuthorizer{allow: map[string]bool{"user-a|a@hosted.example": true}})
			}
			proc.SetPreferences(&mockPrefs{prefs: map[string]*bongov1.UserPreferences{"user-alice": {ForwardAddress: "ext@dest.example"}}})
			env := &bongov1.QueueEnvelope{
				Id: "e2", Sender: tt.sender, Recipients: []string{"alice@hosted.example"},
				ReceivedAt: time.Now().Unix(), BlobHash: "blob2", AuthUserId: tt.authUser,
			}
			if _, err := proc.Process(context.Background(), env, strings.NewReader(raw)); err != nil {
				t.Fatalf("Process: %v", err)
			}
			if len(queuer.messages) != 1 {
				t.Fatalf("forwards = %d, want 1", len(queuer.messages))
			}
			if got := queuer.messages[0].SenderEmail; got != tt.want {
				t.Errorf("sender = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestForwardSRSOneMessagePerDomain(t *testing.T) {
	queuer := &mockQueuer{}
	proc := newSRSProc(t, queuer, &mockApplier{})
	raw := minimalMessage("bob@example.org", "alice@hosted.example", "Hello", "Hi!\r\n")
	env := &bongov1.QueueEnvelope{
		Id: "e3", Sender: "bob@example.org", Recipients: []string{"alice@hosted.example", "carol@other.example"},
		ReceivedAt: time.Now().Unix(), BlobHash: "blob3",
		ForwardRequests: []*bongov1.ForwardRequest{
			{UserId: "user-alice", Target: "one@dest.example"},
			{UserId: "user-carol", Target: "two@dest.example"},
			{UserId: "user-alice", Target: "three@dest.example"},
		},
	}
	if _, err := proc.Process(context.Background(), env, strings.NewReader(raw)); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(queuer.messages) != 2 {
		t.Fatalf("forwards = %d, want 2 (one per forwarding domain)", len(queuer.messages))
	}
	byDomain := map[string][]string{}
	for _, m := range queuer.messages {
		at := strings.LastIndexByte(m.SenderEmail, '@')
		byDomain[m.SenderEmail[at+1:]] = m.Recipients
	}
	if got := byDomain["hosted.example"]; len(got) != 2 || got[0] != "one@dest.example" || got[1] != "three@dest.example" {
		t.Errorf("hosted.example recipients = %v", got)
	}
	if got := byDomain["other.example"]; len(got) != 1 || got[0] != "two@dest.example" {
		t.Errorf("other.example recipients = %v", got)
	}
}

func bounceEnv(sender, rcpt, mailbox string) *bongov1.QueueEnvelope {
	return &bongov1.QueueEnvelope{
		Id: "bounce", Sender: sender, Recipients: []string{rcpt},
		ReceivedAt: time.Now().Unix(), BlobHash: "dsnblob", TargetMailbox: mailbox,
	}
}

func TestInboundSRSBounce(t *testing.T) {
	issue := func(at time.Time, sender, domain string) string {
		a, err := srsCodecAt(17, at).Forward(sender, domain)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	valid := issue(srsNow, "bob@example.org", "hosted.example")
	tests := []struct {
		name    string
		sender  string
		rcpt    string
		mailbox string
		raw     string
		want    string // reversed address, or "" when nothing may be sent
	}{
		{"valid null-sender bounce", "", valid, "", "", "bob@example.org"},
		{"valid, lowercased by a hop", "", strings.ToLower(valid), "", "", "bob@example.org"},
		{"age 17 accepted", "", issue(srsNow.AddDate(0, 0, -17), "bob@example.org", "hosted.example"), "", "", "bob@example.org"},
		{"age 18 refused", "", issue(srsNow.AddDate(0, 0, -18), "bob@example.org", "hosted.example"), "", "", ""},
		{"future refused", "", issue(srsNow.AddDate(0, 0, 3), "bob@example.org", "hosted.example"), "", "", ""},
		{"non-null sender refused", "mallory@evil.example", valid, "", "", ""},
		{"tampered hash refused", "", flipHash(valid), "", "", ""},
		{"moved to another hosted domain refused", "", strings.Replace(valid, "@hosted.example", "@other.example", 1), "", "", ""},
		{"moved to an unhosted domain refused", "", strings.Replace(valid, "@hosted.example", "@elsewhere.example", 1), "", "", ""},
		{"spam-tagged bounce not relayed", "", valid, "Junk", "", ""},
		{"hop limit exceeded", "", valid, "", strings.Repeat("Received: from x by y; Mon, 01 Jan 2024 12:00:00 +0000\r\n", 26), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queuer := &mockQueuer{}
			applier := &mockApplier{}
			proc := newSRSProc(t, queuer, applier)
			raw := tt.raw + minimalMessage("MAILER-DAEMON <postmaster@dest.example>", "x@y.example", "Undelivered", "bounce\r\n")
			if _, err := proc.Process(context.Background(), bounceEnv(tt.sender, tt.rcpt, tt.mailbox), strings.NewReader(raw)); err != nil {
				t.Fatalf("Process: %v", err)
			}
			// Never delivered locally, not even to the catch-all owner.
			if len(applier.commands) != 0 {
				t.Fatalf("local deliveries = %d, want 0", len(applier.commands))
			}
			if tt.want == "" {
				if len(queuer.messages) != 0 {
					t.Fatalf("enqueued %+v, want nothing", queuer.messages)
				}
				return
			}
			if len(queuer.messages) != 1 {
				t.Fatalf("enqueued = %d, want 1", len(queuer.messages))
			}
			m := queuer.messages[0]
			if m.SenderEmail != "" || len(m.Recipients) != 1 || m.Recipients[0] != tt.want ||
				m.Origin != bongov1.OutboundOrigin_OUTBOUND_ORIGIN_FORWARD || m.BlobHash != "dsnblob" {
				t.Errorf("enqueued %+v, want null sender to %s as FORWARD of dsnblob", m, tt.want)
			}
		})
	}
}

// Without a codec an SRS-looking address on a hosted domain is still never
// resolved, so it cannot reach the catch-all owner.
func TestInboundSRSWithoutCodecDropped(t *testing.T) {
	queuer := &mockQueuer{}
	applier := &mockApplier{}
	proc := newSRSProc(t, queuer, applier)
	proc.SetSRS(nil)
	valid, _ := srsCodecAt(17, srsNow).Forward("bob@example.org", "hosted.example")
	raw := minimalMessage("MAILER-DAEMON <p@dest.example>", "x@y.example", "Undelivered", "bounce\r\n")
	if _, err := proc.Process(context.Background(), bounceEnv("", valid, ""), strings.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	if len(applier.commands) != 0 || len(queuer.messages) != 0 {
		t.Fatalf("deliveries=%d enqueued=%d, want none", len(applier.commands), len(queuer.messages))
	}
}

// Two SRS recipients that reverse to the same address relay once.
func TestInboundSRSBounceDedup(t *testing.T) {
	queuer := &mockQueuer{}
	proc := newSRSProc(t, queuer, &mockApplier{})
	a, _ := srsCodecAt(17, srsNow).Forward("bob@example.org", "hosted.example")
	env := bounceEnv("", a, "")
	env.Recipients = []string{a, strings.ToLower(a)}
	raw := minimalMessage("MAILER-DAEMON <p@dest.example>", "x@y.example", "Undelivered", "bounce\r\n")
	if _, err := proc.Process(context.Background(), env, strings.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	if len(queuer.messages) != 1 {
		t.Fatalf("enqueued = %d, want 1", len(queuer.messages))
	}
}

// bounceTo processes a null-sender bounce addressed to the SRS address issued
// for original, and returns how many messages the call queued.
func bounceTo(t *testing.T, proc *DeliverProcessor, queuer *mockQueuer, original string) int {
	t.Helper()
	addr, err := srsCodecAt(17, srsNow).Forward(original, "hosted.example")
	if err != nil {
		t.Fatal(err)
	}
	before := len(queuer.messages)
	raw := minimalMessage("MAILER-DAEMON <p@dest.example>", "x@y.example", "Undelivered", "bounce\r\n")
	if _, err := proc.Process(context.Background(), bounceEnv("", addr, ""), strings.NewReader(raw)); err != nil {
		t.Fatalf("Process: %v", err)
	}
	return len(queuer.messages) - before
}

func TestSRSBounceRateLimits(t *testing.T) {
	t.Run("per address", func(t *testing.T) {
		queuer := &mockQueuer{}
		proc := newSRSProc(t, queuer, &mockApplier{})
		proc.SetSRSBounceLimiters(bongomail.NewRateLimiter(3), nil)
		for i := 1; i <= 3; i++ {
			if got := bounceTo(t, proc, queuer, "bob@example.org"); got != 1 {
				t.Fatalf("bounce %d queued %d, want 1", i, got)
			}
		}
		if got := bounceTo(t, proc, queuer, "bob@example.org"); got != 0 {
			t.Fatalf("4th bounce to the same address queued %d, want 0", got)
		}
		// The key is case-insensitive on the reversed address.
		if got := bounceTo(t, proc, queuer, "BOB@example.org"); got != 0 {
			t.Errorf("bounce to differently cased address queued %d, want 0", got)
		}
		if got := bounceTo(t, proc, queuer, "carol@example.net"); got != 1 {
			t.Errorf("bounce to a different address queued %d, want 1", got)
		}
	})

	t.Run("global", func(t *testing.T) {
		queuer := &mockQueuer{}
		proc := newSRSProc(t, queuer, &mockApplier{})
		proc.SetSRSBounceLimiters(nil, bongomail.NewRateLimiter(4))
		for i, who := range []string{"a@example.org", "b@example.org", "c@example.org", "d@example.org"} {
			if got := bounceTo(t, proc, queuer, who); got != 1 {
				t.Fatalf("bounce %d queued %d, want 1", i+1, got)
			}
		}
		if got := bounceTo(t, proc, queuer, "e@example.org"); got != 0 {
			t.Fatalf("5th bounce queued %d, want 0 once the node cap is reached", got)
		}
	})

	t.Run("a refused address does not spend the node cap", func(t *testing.T) {
		queuer := &mockQueuer{}
		proc := newSRSProc(t, queuer, &mockApplier{})
		proc.SetSRSBounceLimiters(bongomail.NewRateLimiter(1), bongomail.NewRateLimiter(2))
		bounceTo(t, proc, queuer, "a@example.org")
		for range 5 {
			bounceTo(t, proc, queuer, "a@example.org")
		}
		if got := bounceTo(t, proc, queuer, "b@example.org"); got != 1 {
			t.Errorf("second distinct address queued %d, want 1", got)
		}
	})

	t.Run("zero disables", func(t *testing.T) {
		queuer := &mockQueuer{}
		proc := newSRSProc(t, queuer, &mockApplier{})
		proc.SetSRSBounceLimiters(bongomail.NewRateLimiter(0), bongomail.NewRateLimiter(0))
		for i := 1; i <= 7; i++ {
			if got := bounceTo(t, proc, queuer, "bob@example.org"); got != 1 {
				t.Fatalf("bounce %d queued %d, want 1 with limits disabled", i, got)
			}
		}
	})
}

// flipHash changes the first character of the 8-character hash so the address
// keeps its length and the HMAC comparison, not the length check, refuses it.
func flipHash(addr string) string {
	i := strings.Index(addr, "=") + 1
	b := []byte(addr)
	if b[i] == 'a' {
		b[i] = 'b'
	} else {
		b[i] = 'a'
	}
	return string(b)
}
