package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/mail"
)

func sendCompose(t *testing.T, env *composeSendAsEnv, userID, email string, to []string, cc []string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{
		"send_id": {sendID()},
		"to":      {strings.Join(to, ", ")},
		"cc":      {strings.Join(cc, ", ")},
		"subject": {"limit test"},
		"body":    {"hello"},
	}
	req := httptest.NewRequest(http.MethodPost, "/compose/send", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUserContext(req, userID, email)
	w := httptest.NewRecorder()
	env.handler.Send(w, req)
	return w
}

func externalAddrs(prefix string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("%s%d@external.example", prefix, i)
	}
	return out
}

func newLimitEnv(t *testing.T, userID, email string) *composeSendAsEnv {
	t.Helper()
	env := newComposeSendAsEnv(t)
	env.seedUser(t, userID, email)
	env.seedAddress(t, email, userID, "example.com", true, true)
	return env
}

// TestComposeRecipientLimitCountsRecipients: web compose spends the shared
// budget per external recipient, and a refused send writes nothing.
func TestComposeRecipientLimitCountsRecipients(t *testing.T) {
	userID, email := "user-rcpt-limit", "alice@example.com"
	env := newLimitEnv(t, userID, email)
	env.handler.SetRecipientLimiter(mail.NewRateLimiter(7))

	if w := sendCompose(t, env, userID, email, externalAddrs("a", 3), nil); w.Code != http.StatusSeeOther {
		t.Fatalf("send to 3 (under limit): %d %s", w.Code, w.Body.String())
	}
	pendingBefore, _ := env.qs.ListPending(50)
	sentBefore, _ := env.mailboxes.ListMessages(userID, "Sent")

	w := sendCompose(t, env, userID, email, externalAddrs("b", 3), externalAddrs("c", 2))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("send to 5 with 4 remaining: %d, want 429", w.Code)
	}
	pendingAfter, _ := env.qs.ListPending(50)
	sentAfter, _ := env.mailboxes.ListMessages(userID, "Sent")
	if len(pendingAfter) != len(pendingBefore) {
		t.Errorf("refused send enqueued %d messages", len(pendingAfter)-len(pendingBefore))
	}
	if len(sentAfter) != len(sentBefore) {
		t.Error("refused send left a Sent copy")
	}

	// The refusal spent nothing: exactly 4 units remain.
	if w := sendCompose(t, env, userID, email, externalAddrs("d", 4), nil); w.Code != http.StatusSeeOther {
		t.Fatalf("send to 4 with 4 remaining: %d %s", w.Code, w.Body.String())
	}
	if w := sendCompose(t, env, userID, email, externalAddrs("e", 1), nil); w.Code != http.StatusTooManyRequests {
		t.Fatalf("send with budget spent: %d, want 429", w.Code)
	}
}

func TestComposeMaxRecipientsPerMessage(t *testing.T) {
	userID, email := "user-rcpt-cap", "alice@example.com"
	env := newLimitEnv(t, userID, email)
	env.handler.SetMaxRecipients(7)

	if w := sendCompose(t, env, userID, email, externalAddrs("a", 4), externalAddrs("b", 3)); w.Code != http.StatusSeeOther {
		t.Fatalf("exactly 7 recipients: %d %s", w.Code, w.Body.String())
	}
	if w := sendCompose(t, env, userID, email, externalAddrs("c", 4), externalAddrs("d", 4)); w.Code != http.StatusBadRequest {
		t.Fatalf("8 recipients: %d, want 400", w.Code)
	}
}

// Compose must spend the budget under the userID key, the key SMTP uses.
func TestComposeRecipientLimitSharedUnderUserID(t *testing.T) {
	userID, email := "user-rcpt-shared", "alice@example.com"
	env := newLimitEnv(t, userID, email)
	rl := mail.NewRateLimiter(7)
	env.handler.SetRecipientLimiter(rl)

	if !rl.AllowN(userID, 6) { // as an SMTP submission would
		t.Fatal("seed AllowN refused")
	}
	if w := sendCompose(t, env, userID, email, externalAddrs("a", 2), nil); w.Code != http.StatusTooManyRequests {
		t.Fatalf("2 recipients with 1 remaining: %d, want 429", w.Code)
	}
	if w := sendCompose(t, env, userID, email, externalAddrs("b", 1), nil); w.Code != http.StatusSeeOther {
		t.Fatalf("1 recipient with 1 remaining: %d %s", w.Code, w.Body.String())
	}
	if got := rl.Remaining(userID); got != 0 {
		t.Fatalf("remaining under userID = %d, want 0", got)
	}
}

// The To+Cc cap counts local recipients too.
func TestComposeMaxRecipientsCountsLocal(t *testing.T) {
	userID, email := "user-rcpt-cap-mixed", "alice@example.com"
	env := newLimitEnv(t, userID, email)
	env.handler.SetMaxRecipients(7)

	to := append([]string{email, "alice@example.com"}, externalAddrs("x", 3)...)
	cc := externalAddrs("y", 3)
	if w := sendCompose(t, env, userID, email, to, cc); w.Code != http.StatusBadRequest {
		t.Fatalf("8 mixed local+external recipients: %d, want 400", w.Code)
	}
}

// With no cap configured (max_recipients = 0) compose falls back to 100, the
// SMTP default, rather than allowing unlimited recipients.
func TestComposeMaxRecipientsDefaultFallback(t *testing.T) {
	userID, email := "user-rcpt-default", "alice@example.com"
	env := newLimitEnv(t, userID, email)

	if w := sendCompose(t, env, userID, email, externalAddrs("a", 100), nil); w.Code != http.StatusSeeOther {
		t.Fatalf("exactly 100 recipients: %d %s", w.Code, w.Body.String())
	}
	if w := sendCompose(t, env, userID, email, externalAddrs("b", 101), nil); w.Code != http.StatusBadRequest {
		t.Fatalf("101 recipients: %d, want 400", w.Code)
	}
}
