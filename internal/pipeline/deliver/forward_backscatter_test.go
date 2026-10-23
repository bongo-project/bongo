package deliver

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/srs"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func forgedEnv(spf, dmarc string) *bongov1.QueueEnvelope {
	env := &bongov1.QueueEnvelope{
		Id: "env-forged", Sender: "mallory@forged.example", Recipients: []string{"alice@hosted.example"},
		ReceivedAt: time.Now().Unix(), BlobHash: "blob-forged", SpfResult: spf,
		ForwardRequests: []*bongov1.ForwardRequest{{UserId: "user-alice", Target: "ext@dest.example"}},
	}
	if dmarc != "" {
		env.Stages = []*bongov1.StageResult{{Stage: "antispam", DmarcResult: dmarc}}
	}
	return env
}

func forgedRaw() string {
	return minimalMessage("mallory@forged.example", "alice@hosted.example", "Hello", "Hi!\r\n")
}

func TestForwardOfUnauthenticatedSenderHasNoRelaySender(t *testing.T) {
	noRelay := srs.NoRelayLocal + "@hosted.example"
	tests := []struct {
		name          string
		spf, dmarc    string
		noSRS         bool
		wantAuth      bool
		wantNoRelay   bool
		wantSRSSender bool
	}{
		{name: "spf fail dmarc fail", spf: "fail", dmarc: "fail", wantNoRelay: true},
		{name: "spf fail dmarc fail without SRS configured", spf: "fail", dmarc: "fail", noSRS: true, wantNoRelay: true},
		{name: "no results at all", wantNoRelay: true},
		{name: "spf softfail dmarc none", spf: "softfail", dmarc: "none", wantNoRelay: true},
		{name: "spf pass", spf: "pass", wantAuth: true, wantSRSSender: true},
		{name: "spf pass mixed case", spf: "Pass", wantAuth: true, wantSRSSender: true},
		{name: "dmarc pass only", spf: "fail", dmarc: "pass", wantAuth: true, wantSRSSender: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queuer := &mockQueuer{}
			proc := newSRSProc(t, queuer, &mockApplier{})
			if tt.noSRS {
				proc.SetSRS(nil)
			}
			if _, err := proc.Process(context.Background(), forgedEnv(tt.spf, tt.dmarc), strings.NewReader(forgedRaw())); err != nil {
				t.Fatalf("Process: %v", err)
			}
			if len(queuer.messages) != 1 {
				t.Fatalf("forwards = %d, want 1", len(queuer.messages))
			}
			m := queuer.messages[0]
			if m.Origin != originForward {
				t.Errorf("origin = %v, want FORWARD", m.Origin)
			}
			if m.SenderAuthenticated != tt.wantAuth {
				t.Errorf("SenderAuthenticated = %v, want %v", m.SenderAuthenticated, tt.wantAuth)
			}
			if tt.wantNoRelay {
				if m.SenderEmail != noRelay {
					t.Errorf("sender = %q, want %q", m.SenderEmail, noRelay)
				}
				if strings.Contains(m.SenderEmail, "forged") || srs.IsSRS(strings.SplitN(m.SenderEmail, "@", 2)[0]) {
					t.Errorf("sender %q must not carry or encode the original sender", m.SenderEmail)
				}
				if m.SenderId != "user-alice" {
					t.Errorf("SenderId = %q, want the forwarding user", m.SenderId)
				}
			}
			if tt.wantSRSSender {
				if back, err := srsCodecAt(17, srsNow).Reverse(m.SenderEmail); err != nil || back != "mallory@forged.example" {
					t.Errorf("sender %q does not reverse to the original (%q, %v)", m.SenderEmail, back, err)
				}
			}
		})
	}
}

func TestForwardOfUnauthenticatedSenderGroupsPerUser(t *testing.T) {
	queuer := &mockQueuer{}
	proc := newSRSProc(t, queuer, &mockApplier{})
	env := forgedEnv("fail", "fail")
	env.Recipients = []string{"alice@hosted.example", "carol@other.example"}
	env.ForwardRequests = []*bongov1.ForwardRequest{
		{UserId: "user-alice", Target: "one@dest.example"},
		{UserId: "user-carol", Target: "two@dest.example"},
		{UserId: "user-alice", Target: "three@dest.example"},
	}
	if _, err := proc.Process(context.Background(), env, strings.NewReader(forgedRaw())); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(queuer.messages) != 2 {
		t.Fatalf("forwards = %d, want 2 (one per forwarding user)", len(queuer.messages))
	}
	byUser := map[string][]string{}
	for _, m := range queuer.messages {
		byUser[m.SenderId] = m.Recipients
	}
	if len(byUser["user-alice"]) != 2 || len(byUser["user-carol"]) != 1 {
		t.Errorf("recipients by user = %v", byUser)
	}
}

// TestForwardOfSameTargetByTwoUsersQueuedOnce: two users forwarding one message
// to the same target produce one copy, authenticated or not.
func TestForwardOfSameTargetByTwoUsersQueuedOnce(t *testing.T) {
	for _, spf := range []string{"fail", "pass"} {
		t.Run("spf "+spf, func(t *testing.T) {
			queuer := &mockQueuer{}
			proc := newSRSProc(t, queuer, &mockApplier{})
			env := forgedEnv(spf, "")
			env.Recipients = []string{"alice@hosted.example", "carol@other.example"}
			env.ForwardRequests = []*bongov1.ForwardRequest{
				{UserId: "user-alice", Target: "same@dest.example"},
				{UserId: "user-carol", Target: "same@dest.example"},
			}
			if _, err := proc.Process(context.Background(), env, strings.NewReader(forgedRaw())); err != nil {
				t.Fatalf("Process: %v", err)
			}
			n := 0
			for _, m := range queuer.messages {
				n += len(m.Recipients)
			}
			if len(queuer.messages) != 1 || n != 1 {
				t.Errorf("messages = %d carrying %d recipients, want 1 and 1", len(queuer.messages), n)
			}
		})
	}
}

// TestForwardOfUnauthenticatedSenderWithoutDomainUsesNullSender: when the
// forwarding user's domain cannot be established there is no no-relay address
// to use, so the forward must not keep the forged sender either.
func TestForwardOfUnauthenticatedSenderWithoutDomainUsesNullSender(t *testing.T) {
	queuer := &mockQueuer{}
	proc := newSRSProc(t, queuer, &mockApplier{})
	env := forgedEnv("fail", "fail")
	env.ForwardRequests = []*bongov1.ForwardRequest{{UserId: "user-nodomain", Target: "ext@dest.example"}}
	if _, err := proc.Process(context.Background(), env, strings.NewReader(forgedRaw())); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(queuer.messages) != 1 {
		t.Fatalf("forwards = %d, want 1", len(queuer.messages))
	}
	if m := queuer.messages[0]; m.SenderEmail != "" || m.SenderAuthenticated {
		t.Errorf("sender = %q authenticated = %v, want the null sender, unauthenticated", m.SenderEmail, m.SenderAuthenticated)
	}
}

// TestNoRelayAddressDropsBounces: the reserved sender accepts null-sender mail
// and discards it, even when a catch-all or a user would otherwise own it.
func TestNoRelayAddressDropsBounces(t *testing.T) {
	rcpt := srs.NoRelayLocal + "@hosted.example"
	raw := minimalMessage("MAILER-DAEMON <postmaster@dest.example>", rcpt, "Undelivered", "bounce\r\n")
	for _, name := range []string{"catch-all domain", "user holds the local part"} {
		t.Run(name, func(t *testing.T) {
			queuer := &mockQueuer{}
			applier := &mockApplier{}
			proc := newSRSProc(t, queuer, applier)
			if name != "catch-all domain" {
				proc = NewDeliverProcessor(applier, &mockResolver{mapping: map[string]string{rcpt: "user-squatter"}}, newTestLogger())
				proc.SetQueuer(queuer)
				proc.SetHostedDomains(hosted)
			}
			env := bounceEnv("", rcpt, "")
			if _, err := proc.Process(context.Background(), env, strings.NewReader(raw)); err != nil {
				t.Fatalf("Process: %v", err)
			}
			if len(queuer.messages) != 0 {
				t.Errorf("queued = %d, want 0", len(queuer.messages))
			}
			if len(applier.commands) != 0 {
				t.Errorf("local writes = %d, want 0", len(applier.commands))
			}
		})
	}
}

// TestSPFPassBounceRelayedOnce is the positive control: a forward of
// authenticated mail keeps SRS, and a bounce to that address is relayed to the
// original sender exactly once.
func TestSPFPassBounceRelayedOnce(t *testing.T) {
	queuer := &mockQueuer{}
	proc := newSRSProc(t, queuer, &mockApplier{})
	if _, err := proc.Process(context.Background(), forgedEnv("pass", ""), strings.NewReader(forgedRaw())); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(queuer.messages) != 1 || !strings.HasPrefix(queuer.messages[0].SenderEmail, "SRS0=") {
		t.Fatalf("want one SRS0 forward, got %+v", queuer.messages)
	}
	srsAddr := queuer.messages[0].SenderEmail
	queuer.messages = nil

	raw := minimalMessage("MAILER-DAEMON <postmaster@dest.example>", srsAddr, "Undelivered", "bounce\r\n")
	if _, err := proc.Process(context.Background(), bounceEnv("", srsAddr, ""), strings.NewReader(raw)); err != nil {
		t.Fatalf("Process bounce: %v", err)
	}
	if len(queuer.messages) != 1 || len(queuer.messages[0].Recipients) != 1 || queuer.messages[0].Recipients[0] != "mallory@forged.example" {
		t.Fatalf("relayed bounces = %+v, want exactly one to mallory@forged.example", queuer.messages)
	}
}

func TestSenderAuthenticated(t *testing.T) {
	tests := []struct {
		name  string
		env   *bongov1.QueueEnvelope
		dmarc string
		want  bool
	}{
		{"nothing", &bongov1.QueueEnvelope{}, "", false},
		{"spf pass", &bongov1.QueueEnvelope{SpfResult: "pass"}, "", true},
		{"spf PASS", &bongov1.QueueEnvelope{SpfResult: "PASS"}, "", true},
		{"dmarc pass", &bongov1.QueueEnvelope{SpfResult: "fail"}, "pass", true},
		{"authenticated submission", &bongov1.QueueEnvelope{SpfResult: "softfail", AuthUserId: "u"}, "", true},
		{"fail and fail", &bongov1.QueueEnvelope{SpfResult: "fail"}, "fail", false},
	}
	for _, tt := range tests {
		if got := senderAuthenticated(tt.env, tt.dmarc); got != tt.want {
			t.Errorf("%s: senderAuthenticated = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// A forwarded bounce (null original sender) keeps the null sender whether or
// not the original was authenticated: RFC 5321 s6.1 forbids answering a null
// reverse-path, and a sink address in its place would invite replies.
func TestForwardOfNullSenderKeepsNullSender(t *testing.T) {
	for _, spf := range []string{"fail", "pass"} {
		t.Run("spf "+spf, func(t *testing.T) {
			queuer := &mockQueuer{}
			proc := newSRSProc(t, queuer, &mockApplier{})
			env := forgedEnv(spf, "")
			env.Sender = ""
			raw := minimalMessage("MAILER-DAEMON@forged.example", "alice@hosted.example", "Undelivered", "x\r\n")
			if _, err := proc.Process(context.Background(), env, strings.NewReader(raw)); err != nil {
				t.Fatalf("Process: %v", err)
			}
			if len(queuer.messages) != 1 {
				t.Fatalf("forwards = %d, want 1", len(queuer.messages))
			}
			if got := queuer.messages[0].SenderEmail; got != "" {
				t.Errorf("sender = %q, want the null sender", got)
			}
		})
	}
}
