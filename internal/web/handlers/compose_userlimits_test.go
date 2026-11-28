package handlers

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/mail"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func limitsReader(l *bongov1.SendLimits) func(string) (*bongov1.SendLimits, error) {
	return func(string) (*bongov1.SendLimits, error) { return l, nil }
}

// Web compose gains messages per minute; the per-user value overrides the
// global one in both directions.
func TestComposeMessagesPerMinute(t *testing.T) {
	tests := []struct {
		name    string
		global  int
		perUser uint32
		allowed int
	}{
		{"global", 2, 0, 2},
		{"per user below global", 3, 1, 1},
		{"per user above global", 1, 3, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			userID, email := "user-mpm-"+tt.name, "alice@example.com"
			env := newLimitEnv(t, userID, email)
			env.handler.SetMessageLimiter(mail.NewRateLimiterWindow(tt.global, time.Minute))
			env.handler.SetSendLimitsReader(limitsReader(&bongov1.SendLimits{MessagesPerMinute: tt.perUser}))

			for i := range tt.allowed {
				if w := sendCompose(t, env, userID, email, externalAddrs("m", 1), nil); w.Code != http.StatusSeeOther {
					t.Fatalf("send %d: %d %s", i+1, w.Code, w.Body.String())
				}
			}
			if w := sendCompose(t, env, userID, email, externalAddrs("n", 1), nil); w.Code != http.StatusTooManyRequests {
				t.Fatalf("send over the limit: %d, want 429", w.Code)
			}
		})
	}
}

// A refused send spends nothing on the recipient budget, and the SMTP path
// shares the message limiter instance.
func TestComposeMessageLimiterSharedWithSMTPKey(t *testing.T) {
	userID, email := "user-mpm-shared", "alice@example.com"
	env := newLimitEnv(t, userID, email)
	rl := mail.NewRateLimiterWindow(1, time.Minute)
	rcpt := mail.NewRateLimiter(10)
	env.handler.SetMessageLimiter(rl)
	env.handler.SetRecipientLimiter(rcpt)
	if !rl.Allow(userID) { // as an SMTP submission would
		t.Fatal("seed refused")
	}
	if w := sendCompose(t, env, userID, email, externalAddrs("a", 2), nil); w.Code != http.StatusTooManyRequests {
		t.Fatalf("send after SMTP spent the minute: %d, want 429", w.Code)
	}
	if got := rcpt.Remaining(userID); got != 10 {
		t.Fatalf("recipient budget remaining = %d, want 10 (nothing spent)", got)
	}
}

func TestComposePerUserRecipientsPerHour(t *testing.T) {
	userID, email := "user-rph", "alice@example.com"
	env := newLimitEnv(t, userID, email)
	env.handler.SetRecipientLimiter(mail.NewRateLimiter(3))
	env.handler.SetSendLimitsReader(limitsReader(&bongov1.SendLimits{RecipientsPerHour: 5}))

	if w := sendCompose(t, env, userID, email, externalAddrs("a", 5), nil); w.Code != http.StatusSeeOther {
		t.Fatalf("5 recipients with a per-user budget of 5 (global 3): %d %s", w.Code, w.Body.String())
	}
	if w := sendCompose(t, env, userID, email, externalAddrs("b", 1), nil); w.Code != http.StatusTooManyRequests {
		t.Fatalf("budget spent: %d, want 429", w.Code)
	}
}

func TestComposePerUserMaxRecipients(t *testing.T) {
	tests := []struct {
		name    string
		global  int
		perUser uint32
		n       int
		want    int
	}{
		{"above global accepted", 3, 6, 5, http.StatusSeeOther},
		{"above per-user refused", 3, 6, 7, http.StatusBadRequest},
		{"below global refused", 5, 2, 3, http.StatusBadRequest},
		{"zero means global", 4, 0, 4, http.StatusSeeOther},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			userID, email := "user-max-"+tt.name, "alice@example.com"
			env := newLimitEnv(t, userID, email)
			env.handler.SetMaxRecipients(tt.global)
			env.handler.SetSendLimitsReader(limitsReader(&bongov1.SendLimits{MaxRecipients: tt.perUser}))
			if w := sendCompose(t, env, userID, email, externalAddrs("r", tt.n), nil); w.Code != tt.want {
				t.Fatalf("%d recipients: %d, want %d (%s)", tt.n, w.Code, tt.want, w.Body.String())
			}
			if got := env.handler.recipientLimitFor(userID); got != map[bool]int{true: int(tt.perUser), false: tt.global}[tt.perUser > 0] {
				t.Errorf("recipientLimitFor = %d", got)
			}
		})
	}
}

// When the limits cannot be read the send is refused with 503 and nothing is
// charged or written.
func TestComposeSendFailsClosedWhenLimitsUnreadable(t *testing.T) {
	userID, email := "user-limits-err", "alice@example.com"
	env := newLimitEnv(t, userID, email)
	rl := mail.NewRateLimiterWindow(5, time.Minute)
	env.handler.SetMessageLimiter(rl)
	env.handler.SetSendLimitsReader(func(string) (*bongov1.SendLimits, error) { return nil, errors.New("pebble down") })

	if w := sendCompose(t, env, userID, email, externalAddrs("a", 1), nil); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d, want 503", w.Code)
	}
	if got := rl.RemainingLimit(userID, 5); got != 5 {
		t.Fatalf("limiter charged on a refused send: remaining %d", got)
	}
	if pending, _ := env.qs.ListPending(50); len(pending) != 0 {
		t.Fatalf("%d messages queued", len(pending))
	}
}
