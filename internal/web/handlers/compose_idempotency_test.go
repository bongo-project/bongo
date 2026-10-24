package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bongo-project/bongo/internal/mail"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// sendID returns a fresh, well-formed compose send ID, as the form would carry.
func sendID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// postSendForm posts a compose form with the given send ID ("" omits the field).
func postSendForm(env *composeSendAsEnv, userID, email, id string, fields url.Values) *httptest.ResponseRecorder {
	form := url.Values{}
	for k, v := range fields {
		form[k] = v
	}
	if id != "" {
		form.Set("send_id", id)
	}
	req := httptest.NewRequest(http.MethodPost, "/compose/send", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUserContext(req, userID, email)
	w := httptest.NewRecorder()
	env.handler.Send(w, req)
	return w
}

func idemFields(body string) url.Values {
	return url.Values{
		"to":      {"bob@example.com, carol@example.com, ext@external.example"},
		"subject": {"idempotency"},
		"body":    {body},
	}
}

// assertOneCopyEach checks the exactly-once outcome of the three-part send in
// idemFields: one envelope per local recipient, one external queue entry and
// one Sent message.
func assertOneCopyEach(t *testing.T, env *composeSendAsEnv, uid string) {
	t.Helper()
	perRcpt := map[string]int{}
	ids := map[string]bool{}
	for _, e := range env.spool.envelopes() {
		perRcpt[e.Recipients[0]]++
		ids[e.Id] = true
	}
	if perRcpt["bob@example.com"] != 1 || perRcpt["carol@example.com"] != 1 || len(perRcpt) != 2 {
		t.Errorf("envelopes per recipient = %v, want one each", perRcpt)
	}
	if len(ids) != 2 {
		t.Errorf("distinct envelope ids = %d, want 2", len(ids))
	}
	if p, _ := env.qs.ListPending(50); len(p) != 1 {
		t.Errorf("pending queue entries = %d, want 1", len(p))
	}
	if sent, _ := env.mailboxes.ListMessages(uid, "Sent"); len(sent) != 1 {
		t.Errorf("Sent messages = %d, want 1", len(sent))
	}
}

func TestComposeRetryAfterPartialSpoolFailure(t *testing.T) {
	env, uid, email := newPipelineEnv(t)
	id := sendID()
	env.spool.failOn = 2

	if w := postSendForm(env, uid, email, id, idemFields("hello")); w.Code != http.StatusInternalServerError {
		t.Fatalf("first attempt status=%d want 500: %s", w.Code, w.Body.String())
	}
	if w := postSendForm(env, uid, email, id, idemFields("hello")); w.Code != http.StatusSeeOther {
		t.Fatalf("retry status=%d want 303: %s", w.Code, w.Body.String())
	}
	assertOneCopyEach(t, env, uid)

	// A further replay of the completed send changes nothing.
	if w := postSendForm(env, uid, email, id, idemFields("hello")); w.Code != http.StatusSeeOther {
		t.Fatalf("replay status=%d want 303", w.Code)
	}
	assertOneCopyEach(t, env, uid)
}

// failSentApplier fails the first mailbox delivery command, which in these
// tests is only ever the sender's Sent copy (local copies go via the spool).
type failSentApplier struct {
	store.RaftApplier
	mu     sync.Mutex
	failed bool
}

func (f *failSentApplier) ApplyCommand(cmd *bongov1.RaftCommand, d time.Duration) error {
	if store.IsMailboxSyntheticCommand(cmd.Key) {
		f.mu.Lock()
		first := !f.failed
		f.failed = true
		f.mu.Unlock()
		if first {
			return errors.New("injected raft failure")
		}
	}
	return f.RaftApplier.ApplyCommand(cmd, d)
}

func TestComposeRetryAfterSentCopyFailure(t *testing.T) {
	env, uid, email := newPipelineEnv(t)
	env.handler.mailboxes = store.NewMailboxStore(env.ps, &failSentApplier{RaftApplier: env.applier})
	id := sendID()

	if w := postSendForm(env, uid, email, id, idemFields("hello")); w.Code != http.StatusInternalServerError {
		t.Fatalf("first attempt status=%d want 500: %s", w.Code, w.Body.String())
	}
	if n := len(env.spool.envelopes()); n != 2 {
		t.Fatalf("envelopes after first attempt=%d want 2", n)
	}
	if w := postSendForm(env, uid, email, id, idemFields("hello")); w.Code != http.StatusSeeOther {
		t.Fatalf("retry status=%d want 303: %s", w.Code, w.Body.String())
	}
	assertOneCopyEach(t, env, uid)
}

func TestComposeNewSendIDIsNotSuppressed(t *testing.T) {
	env, uid, email := newPipelineEnv(t)
	for range 2 {
		if w := postSendForm(env, uid, email, sendID(), idemFields("same content")); w.Code != http.StatusSeeOther {
			t.Fatalf("status=%d: %s", w.Code, w.Body.String())
		}
	}
	if sent, _ := env.mailboxes.ListMessages(uid, "Sent"); len(sent) != 2 {
		t.Errorf("Sent=%d want 2", len(sent))
	}
	if p, _ := env.qs.ListPending(50); len(p) != 2 {
		t.Errorf("queue=%d want 2", len(p))
	}
	if n := len(env.spool.envelopes()); n != 4 {
		t.Errorf("envelopes=%d want 4", n)
	}
}

func TestComposeSameSendIDChangedContentConflicts(t *testing.T) {
	env, uid, email := newPipelineEnv(t)
	id := sendID()
	if w := postSendForm(env, uid, email, id, idemFields("first draft")); w.Code != http.StatusSeeOther {
		t.Fatalf("first status=%d: %s", w.Code, w.Body.String())
	}
	w := postSendForm(env, uid, email, id, idemFields("an edited second message"))
	if w.Code != http.StatusConflict {
		t.Fatalf("edited resubmit status=%d want 409", w.Code)
	}
	if !strings.Contains(w.Body.String(), "already started") {
		t.Errorf("409 body %q does not explain", w.Body.String())
	}
	assertOneCopyEach(t, env, uid)
}

func TestComposeSendIDRequiredAndValidated(t *testing.T) {
	bad := map[string]string{
		"missing":   "",
		"short":     strings.Repeat("a", 31),
		"long":      strings.Repeat("a", 33),
		"uppercase": strings.Repeat("A", 32),
		"non-hex":   strings.Repeat("g", 32),
		"slash":     strings.Repeat("a", 31) + "/",
		"dashed":    "0123456789abcdef-123456789abcdef"[:32],
	}
	for name, id := range bad {
		t.Run(name, func(t *testing.T) {
			env, uid, email := newPipelineEnv(t)
			// Valid in every other respect, so only the send ID can refuse it.
			w := postSendForm(env, uid, email, id, idemFields("hello"))
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status=%d want 400", w.Code)
			}
			if !strings.Contains(w.Body.String(), "reload") {
				t.Errorf("body %q does not tell the user to reload", w.Body.String())
			}
			if n := len(env.spool.envelopes()); n != 0 {
				t.Errorf("envelopes=%d want 0", n)
			}
			if p, _ := env.qs.ListPending(50); len(p) != 0 {
				t.Errorf("queue=%d want 0", len(p))
			}
			if sent, _ := env.mailboxes.ListMessages(uid, "Sent"); len(sent) != 0 {
				t.Errorf("Sent=%d want 0", len(sent))
			}
		})
	}
}

func TestComposeConcurrentSameSendID(t *testing.T) {
	env, uid, email := newPipelineEnv(t)
	id := sendID()
	var wg sync.WaitGroup
	codes := make([]int, 4)
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = postSendForm(env, uid, email, id, idemFields("double click")).Code
		}()
	}
	wg.Wait()
	for i, c := range codes {
		if c != http.StatusSeeOther {
			t.Errorf("request %d status=%d want 303", i, c)
		}
	}
	assertOneCopyEach(t, env, uid)
}

func TestComposeReplayDoesNotSpendRateBudget(t *testing.T) {
	env, uid, email := newPipelineEnv(t)
	env.handler.mailboxes = store.NewMailboxStore(env.ps, &failSentApplier{RaftApplier: env.applier})
	// Budget of exactly one send: the first attempt spends it, so a retry that
	// charged again would be refused with 429.
	env.handler.SetRecipientLimiter(mail.NewRateLimiter(1))
	id := sendID()
	if w := postSendForm(env, uid, email, id, idemFields("hello")); w.Code != http.StatusInternalServerError {
		t.Fatalf("first attempt status=%d want 500", w.Code)
	}
	if w := postSendForm(env, uid, email, id, idemFields("hello")); w.Code != http.StatusSeeOther {
		t.Fatalf("retry status=%d want 303 (budget must not be charged twice): %s", w.Code, w.Body.String())
	}
}

func TestComposeRenderCarriesFreshSendID(t *testing.T) {
	env, uid, email := newPipelineEnv(t)
	idRe := `name="send_id" value="`
	extract := func(body string) string {
		i := strings.Index(body, idRe)
		if i < 0 {
			return ""
		}
		rest := body[i+len(idRe):]
		return rest[:strings.Index(rest, `"`)]
	}
	valid := func(s string) bool {
		if len(s) != 32 {
			return false
		}
		for _, c := range s {
			if !strings.ContainsRune("0123456789abcdef", c) {
				return false
			}
		}
		return true
	}

	// Seed a message to reply to, forward and edit as a draft.
	meta := &bongov1.MailMeta{Guid: "g-render", MessageId: "<m@x>", Subject: "s", From: "bob@example.com", To: []string{email}}
	if _, err := env.mailboxes.DeliverMessage(uid, "INBOX", meta, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := env.mailboxes.DeliverMessage(uid, "Drafts", &bongov1.MailMeta{Guid: "g-draft", Subject: "d", To: []string{"bob@example.com"}}, 0); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		handler http.HandlerFunc
		params  map[string]string
	}{
		{"new", env.handler.NewMessage, nil},
		{"reply", env.handler.Reply, map[string]string{"mailbox": "INBOX", "uid": "1"}},
		{"forward", env.handler.Forward, map[string]string{"mailbox": "INBOX", "uid": "1"}},
		{"draft", env.handler.EditDraft, map[string]string{"mailbox": "Drafts", "uid": "1"}},
	}
	for _, c := range cases {
		for _, hx := range []bool{true, false} {
			name := c.name + "/full"
			if hx {
				name = c.name + "/hx"
			}
			t.Run(name, func(t *testing.T) {
				render := func() string {
					req := httptest.NewRequest(http.MethodGet, "/compose", nil)
					req = withUserContext(req, uid, email)
					rctx := chi.NewRouteContext()
					for k, v := range c.params {
						rctx.URLParams.Add(k, v)
					}
					req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
					if hx {
						req.Header.Set("HX-Request", "true")
					}
					w := httptest.NewRecorder()
					c.handler(w, req)
					if w.Code != http.StatusOK {
						t.Fatalf("status=%d: %s", w.Code, w.Body.String())
					}
					return extract(w.Body.String())
				}
				a, b := render(), render()
				if !valid(a) || !valid(b) {
					t.Fatalf("send_id %q / %q not 32 lowercase hex", a, b)
				}
				if a == b {
					t.Errorf("two renders share send_id %q", a)
				}
			})
		}
	}
}

// A crash between an object write and its marker leaves the object without a
// marker. The Sent copy and the external queue entry are found by their derived
// IDs, so a retry does not repeat them (local envelopes, which cannot be
// looked up, are repeated: at-least-once).
func TestComposeRetryWithoutMarkersFindsExistingObjects(t *testing.T) {
	env, uid, email := newPipelineEnv(t)
	id := sendID()
	if w := postSendForm(env, uid, email, id, idemFields("hello")); w.Code != http.StatusSeeOther {
		t.Fatalf("first status=%d: %s", w.Code, w.Body.String())
	}
	it, err := env.ps.PrefixScan(store.SendLogPrefix(uid))
	if err != nil {
		t.Fatal(err)
	}
	var keys [][]byte
	for it.Next() {
		keys = append(keys, append([]byte(nil), it.Key()...))
	}
	_ = it.Close()
	if len(keys) != 4 { // two local, external, sent
		t.Fatalf("markers = %d, want 4", len(keys))
	}
	for _, k := range keys {
		if err := env.ps.Delete(k); err != nil {
			t.Fatal(err)
		}
	}
	// The queue key embeds the enqueue second, so a duplicate enqueue in the
	// same second would overwrite instead of showing up as a second entry.
	time.Sleep(1100 * time.Millisecond)

	if w := postSendForm(env, uid, email, id, idemFields("hello")); w.Code != http.StatusSeeOther {
		t.Fatalf("retry status=%d: %s", w.Code, w.Body.String())
	}
	if p, _ := env.qs.ListPending(50); len(p) != 1 {
		t.Errorf("pending queue entries = %d, want 1", len(p))
	}
	if sent, _ := env.mailboxes.ListMessages(uid, "Sent"); len(sent) != 1 {
		t.Errorf("Sent messages = %d, want 1", len(sent))
	}
}
