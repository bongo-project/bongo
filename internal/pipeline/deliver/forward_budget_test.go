package deliver

import (
	"context"
	"strings"
	"testing"
	"time"

	bongomail "github.com/bongo-project/bongo/internal/mail"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const (
	originLocal   = bongov1.OutboundOrigin_OUTBOUND_ORIGIN_LOCAL
	originForward = bongov1.OutboundOrigin_OUTBOUND_ORIGIN_FORWARD
)

// newBudgetProc builds a processor where users B and C (both on hosted.example)
// each forward to external targets and author A may send as a@hosted.example.
func newBudgetProc(t *testing.T, queuer *mockQueuer, limiter ForwardLimiter) *DeliverProcessor {
	t.Helper()
	proc := NewDeliverProcessor(&mockApplier{}, &mockResolver{mapping: map[string]string{
		"b@hosted.example":     "user-b",
		"c@hosted.example":     "user-c",
		"carol@other.example":  "user-carol",
		"alice@hosted.example": "user-alice",
	}}, newTestLogger())
	proc.SetQueuer(queuer)
	proc.SetForwardTargets(allowAllTargets{})
	proc.SetHostedDomains(hosted)
	proc.SetSRS(srsCodecAt(17, srsNow))
	proc.SetUserDomains(fakeUserDomains{domains: map[string]string{
		"user-b": "hosted.example", "user-c": "hosted.example", "user-alice": "hosted.example",
	}})
	proc.SetSenderAuthorizer(&fakeAuthorizer{allow: map[string]bool{"user-a|a@hosted.example": true}})
	if limiter != nil {
		proc.SetAuthorRecipientLimiter(limiter)
	}
	return proc
}

func budgetEnv(authUser string, requests ...*bongov1.ForwardRequest) *bongov1.QueueEnvelope {
	return &bongov1.QueueEnvelope{
		Id: "env-budget", Sender: "a@hosted.example",
		Recipients: []string{"b@hosted.example", "c@hosted.example"},
		ReceivedAt: time.Now().Unix(), BlobHash: "blob-budget", AuthUserId: authUser,
		ForwardRequests: requests,
	}
}

func fwdReq(user, target string) *bongov1.ForwardRequest {
	return &bongov1.ForwardRequest{UserId: user, Target: target}
}

func budgetRaw() string {
	return minimalMessage("a@hosted.example", "b@hosted.example", "Hello", "Hi!\r\n")
}

// targetsByOrigin flattens the queued forwards into origin -> targets.
func targetsByOrigin(q *mockQueuer) map[bongov1.OutboundOrigin][]string {
	out := map[bongov1.OutboundOrigin][]string{}
	for _, m := range q.messages {
		out[m.Origin] = append(out[m.Origin], m.Recipients...)
	}
	return out
}

func spend(l *bongomail.RateLimiter, key string, n int) {
	for range n {
		l.Allow(key)
	}
}

func TestLocalForwardChargesAuthorBudget(t *testing.T) {
	requests := []*bongov1.ForwardRequest{
		fwdReq("user-b", "b1@dest.example"), fwdReq("user-b", "b2@dest.example"),
		fwdReq("user-c", "c1@dest.example"), fwdReq("user-c", "c2@dest.example"),
	}

	t.Run("budget of three splits three LOCAL and one FORWARD", func(t *testing.T) {
		limiter := bongomail.NewRateLimiter(5)
		spend(limiter, "user-a", 2)
		queuer := &mockQueuer{}
		proc := newBudgetProc(t, queuer, limiter)
		if _, err := proc.Process(context.Background(), budgetEnv("user-a", requests...), strings.NewReader(budgetRaw())); err != nil {
			t.Fatalf("Process: %v", err)
		}
		got := targetsByOrigin(queuer)
		if len(got[originLocal]) != 3 || len(got[originForward]) != 1 {
			t.Fatalf("LOCAL=%v FORWARD=%v, want 3 LOCAL and 1 FORWARD", got[originLocal], got[originForward])
		}
		if got[originForward][0] != "c2@dest.example" {
			t.Errorf("FORWARD target = %q, want the last in decision order (c2)", got[originForward][0])
		}
		for _, m := range queuer.messages {
			switch m.Origin {
			case originLocal:
				if m.SenderEmail != "a@hosted.example" {
					t.Errorf("LOCAL sender = %q, want the author", m.SenderEmail)
				}
			case originForward:
				if !strings.HasPrefix(m.SenderEmail, "SRS0=") || !strings.HasSuffix(m.SenderEmail, "@hosted.example") {
					t.Errorf("FORWARD sender = %q, want SRS0 on hosted.example", m.SenderEmail)
				}
				if !m.SenderAuthenticated {
					t.Error("fallback FORWARD of an authenticated author must be marked authenticated")
				}
			}
		}
		if limiter.Allow("user-a") {
			t.Error("author budget must be exhausted after the forwards")
		}
	})

	t.Run("budget to spare keeps every target LOCAL", func(t *testing.T) {
		limiter := bongomail.NewRateLimiter(10)
		queuer := &mockQueuer{}
		proc := newBudgetProc(t, queuer, limiter)
		if _, err := proc.Process(context.Background(), budgetEnv("user-a", requests...), strings.NewReader(budgetRaw())); err != nil {
			t.Fatalf("Process: %v", err)
		}
		got := targetsByOrigin(queuer)
		if len(got[originLocal]) != 4 || len(got[originForward]) != 0 {
			t.Fatalf("LOCAL=%v FORWARD=%v, want all LOCAL", got[originLocal], got[originForward])
		}
		if r := limiter.Remaining("user-a"); r != 6 {
			t.Errorf("remaining = %d, want 6 (four charged)", r)
		}
	})

	t.Run("a target forwarded by two users is charged once", func(t *testing.T) {
		limiter := bongomail.NewRateLimiter(10)
		queuer := &mockQueuer{}
		proc := newBudgetProc(t, queuer, limiter)
		env := budgetEnv("user-a", fwdReq("user-b", "same@dest.example"), fwdReq("user-c", "same@dest.example"))
		if _, err := proc.Process(context.Background(), env, strings.NewReader(budgetRaw())); err != nil {
			t.Fatalf("Process: %v", err)
		}
		if r := limiter.Remaining("user-a"); r != 9 {
			t.Errorf("remaining = %d, want 9 (one distinct target)", r)
		}
	})

	t.Run("hosted targets are not charged", func(t *testing.T) {
		limiter := bongomail.NewRateLimiter(10)
		queuer := &mockQueuer{}
		proc := newBudgetProc(t, queuer, limiter)
		env := budgetEnv("user-a", fwdReq("user-b", "carol@other.example"))
		if _, err := proc.Process(context.Background(), env, strings.NewReader(budgetRaw())); err != nil {
			t.Fatalf("Process: %v", err)
		}
		if r := limiter.Remaining("user-a"); r != 10 {
			t.Errorf("remaining = %d, want 10 (hosted target is not an external recipient)", r)
		}
	})

	t.Run("no AuthUserId charges nothing", func(t *testing.T) {
		limiter := bongomail.NewRateLimiter(5)
		spend(limiter, "user-a", 2)
		queuer := &mockQueuer{}
		proc := newBudgetProc(t, queuer, limiter)
		env := budgetEnv("", requests...)
		env.SpfResult = "pass"
		if _, err := proc.Process(context.Background(), env, strings.NewReader(budgetRaw())); err != nil {
			t.Fatalf("Process: %v", err)
		}
		if r := limiter.Remaining("user-a"); r != 3 {
			t.Errorf("remaining = %d, want 3 (unchanged)", r)
		}
		got := targetsByOrigin(queuer)
		if len(got[originLocal]) != 0 || len(got[originForward]) != 4 {
			t.Errorf("LOCAL=%v FORWARD=%v, want all FORWARD", got[originLocal], got[originForward])
		}
	})

	t.Run("exhausted budget sends every target as FORWARD", func(t *testing.T) {
		limiter := bongomail.NewRateLimiter(2)
		spend(limiter, "user-a", 2)
		queuer := &mockQueuer{}
		proc := newBudgetProc(t, queuer, limiter)
		if _, err := proc.Process(context.Background(), budgetEnv("user-a", requests...), strings.NewReader(budgetRaw())); err != nil {
			t.Fatalf("Process: %v", err)
		}
		got := targetsByOrigin(queuer)
		if len(got[originLocal]) != 0 || len(got[originForward]) != 4 {
			t.Errorf("LOCAL=%v FORWARD=%v, want all FORWARD", got[originLocal], got[originForward])
		}
	})
}
