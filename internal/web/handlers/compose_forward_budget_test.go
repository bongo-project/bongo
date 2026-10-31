package handlers

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/mail"
	"github.com/bongo-project/bongo/internal/pipeline/deliver"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

type budgetResolver map[string]string

func (r budgetResolver) ResolveRecipient(email string) (string, error) {
	if id, ok := r[email]; ok {
		return id, nil
	}
	return "", http.ErrNoLocation
}

func (r budgetResolver) ResolveAccount(email string) (string, error) {
	return r.ResolveRecipient(email)
}

type budgetApplier struct{}

func (budgetApplier) ApplyCommand(*bongov1.RaftCommand, time.Duration) error { return nil }

type budgetQueuer struct{ n int }

func (q *budgetQueuer) Enqueue(*bongov1.OutboundMessage) error { q.n++; return nil }

type budgetTargets struct{}

func (budgetTargets) Allowed(string, string) (bool, error) { return true, nil }

type budgetAuthz struct{}

func (budgetAuthz) CanSendAs(string, string) (bool, error) { return true, nil }

// TestComposeRefusedAfterLocalForwardSpendsBudget: the deliver stage and web
// compose share one limiter, so external forwards signed as the author use up
// the budget a later compose send needs.
func TestComposeRefusedAfterLocalForwardSpendsBudget(t *testing.T) {
	userID, email := "user-fwd-budget", "alice@example.com"
	env := newLimitEnv(t, userID, email)
	limiter := mail.NewRateLimiter(2)
	env.handler.SetRecipientLimiter(limiter)

	queuer := &budgetQueuer{}
	proc := deliver.NewDeliverProcessor(budgetApplier{}, budgetResolver{"b@hosted.example": "user-b"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	proc.SetQueuer(queuer)
	proc.SetForwardTargets(budgetTargets{})
	proc.SetSenderAuthorizer(budgetAuthz{})
	proc.SetAuthorRecipientLimiter(limiter)

	raw := "From: alice@example.com\r\nTo: b@hosted.example\r\nSubject: hi\r\nMessage-ID: <1@example.com>\r\nDate: Mon, 01 Jan 2024 12:00:00 +0000\r\n\r\nbody\r\n"
	msg := &bongov1.QueueEnvelope{
		Id: "env-fwd-budget", Sender: email, Recipients: []string{"b@hosted.example"},
		ReceivedAt: time.Now().Unix(), BlobHash: "blob", AuthUserId: userID,
		ForwardRequests: []*bongov1.ForwardRequest{
			{UserId: "user-b", Target: "x1@dest.example"},
			{UserId: "user-b", Target: "x2@dest.example"},
		},
	}
	if _, err := proc.Process(context.Background(), msg, strings.NewReader(raw)); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if queuer.n == 0 {
		t.Fatal("no forward was queued")
	}
	if w := sendCompose(t, env, userID, email, externalAddrs("a", 1), nil); w.Code != http.StatusTooManyRequests {
		t.Fatalf("compose after the forwards spent the budget: %d, want 429", w.Code)
	}
}
