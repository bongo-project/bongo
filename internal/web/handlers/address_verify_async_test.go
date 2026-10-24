package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// fakeVerifyBlobs and fakeVerifyQueue count calls so a test can prove when the
// handler does and does not touch them.
type fakeVerifyBlobs struct {
	calls    atomic.Int64
	err      error
	mu       sync.Mutex
	payloads [][]byte
}

func (f *fakeVerifyBlobs) Put(data []byte) (string, error) {
	f.calls.Add(1)
	if f.err != nil {
		return "", f.err
	}
	f.mu.Lock()
	f.payloads = append(f.payloads, append([]byte(nil), data...))
	f.mu.Unlock()
	return fmt.Sprintf("hash-%d", f.calls.Load()), nil
}

type fakeVerifyQueue struct {
	calls atomic.Int64
	err   error
	mu    sync.Mutex
	msgs  []*bongov1.OutboundMessage
}

func (f *fakeVerifyQueue) Enqueue(m *bongov1.OutboundMessage) error {
	f.calls.Add(1)
	if f.err != nil {
		return f.err
	}
	f.mu.Lock()
	f.msgs = append(f.msgs, m)
	f.mu.Unlock()
	return nil
}

func (f *fakeVerifyQueue) recipients() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, m := range f.msgs {
		out = append(out, m.Recipients...)
	}
	return out
}

func (e *verifyTestEnv) useFakes() (*fakeVerifyBlobs, *fakeVerifyQueue) {
	b, q := &fakeVerifyBlobs{}, &fakeVerifyQueue{}
	e.handler.blobs = b
	e.handler.queueStore = q
	return b, q
}

func startRun(t *testing.T, h *AddressVerifyHandler) (cancel func(), done <-chan struct{}) {
	t.Helper()
	ctx, c := context.WithCancel(context.Background())
	d := make(chan struct{})
	go func() {
		defer close(d)
		h.Run(ctx)
	}()
	t.Cleanup(func() { c(); <-d })
	waitRunning(t, h)
	return c, d
}

func TestRequestVerification_SyncPathIdenticalTakenAndFree(t *testing.T) {
	env := newVerifyTestEnvStopped(t)
	blobs, queue := env.useFakes()
	const free, taken = "free@ext.example", "taken@ext.example"
	env.seedUser(t, "userA", "a@example.com")
	env.seedUser(t, "userB", "b@example.com")
	env.seedAddress(t, taken, "userA", "identity", true, true)
	env.postAdd(t, "userB", "b@example.com", free)
	env.postAdd(t, "userB", "b@example.com", taken)
	// Requests are accepted only while a sender runs; hold the jobs in the
	// buffer instead so the test can prove the request path itself is inert.
	env.handler.running.Store(true)

	var locs []string
	for _, addr := range []string{free, taken} {
		w := env.requestVerificationHTTP(t, "userB", "b@example.com", addr)
		if w.Code != http.StatusSeeOther {
			t.Fatalf("%s: status = %d", addr, w.Code)
		}
		locs = append(locs, w.Header().Get("Location"))
		ch, ok, err := env.verifyStore.Get("userB", addr)
		if err != nil || !ok || len(ch.CodeHash) == 0 {
			t.Fatalf("%s: challenge = %+v ok=%v err=%v, want a stored code hash", addr, ch, ok, err)
		}
	}
	if locs[0] != locs[1] {
		t.Errorf("Location differs: free %q, taken %q", locs[0], locs[1])
	}
	if b, q := blobs.calls.Load(), queue.calls.Load(); b != 0 || q != 0 {
		t.Fatalf("request path touched blob store %d times and queue %d times, want 0 and 0", b, q)
	}

	env.handler.running.Store(false)
	startRun(t, env.handler)
	env.flush(t)
	if got := queue.recipients(); len(got) != 1 || got[0] != free {
		t.Errorf("enqueued recipients = %v, want only %q", got, free)
	}
	if got := blobs.calls.Load(); got != 1 {
		t.Errorf("blob writes = %d, want 1 (free address only)", got)
	}
}

func TestRequestVerification_BackgroundSendFailureLoggedWithoutCode(t *testing.T) {
	env := newVerifyTestEnvStopped(t)
	blobs, queue := env.useFakes()
	queue.err = errors.New("queue unavailable")
	const addr = "free@ext.example"
	env.seedUser(t, "userB", "b@example.com")
	env.postAdd(t, "userB", "b@example.com", addr)
	startRun(t, env.handler)

	env.requestVerificationHTTP(t, "userB", "b@example.com", addr)
	env.flush(t)

	if queue.calls.Load() != 1 {
		t.Fatalf("enqueue calls = %d, want 1", queue.calls.Load())
	}
	logs := env.logBuf.String()
	if !strings.Contains(logs, "failed to enqueue") || !strings.Contains(logs, "user_id=userB") || !strings.Contains(logs, addr) {
		t.Errorf("log lacks failure context:\n%s", logs)
	}
	blobs.mu.Lock()
	payload := string(blobs.payloads[0])
	blobs.mu.Unlock()
	const marker = "verification code is:\r\n\r\n"
	i := strings.Index(payload, marker)
	if i < 0 {
		t.Fatalf("no code marker in payload: %.200s", payload)
	}
	code := strings.TrimSpace(strings.SplitN(payload[i+len(marker):], "\r\n", 2)[0])
	if len(code) < 16 {
		t.Fatalf("extracted code %q looks wrong", code)
	}
	if strings.Contains(logs, code) {
		t.Error("log contains the raw verification code")
	}
	ch := env.assertClaim(t, "userB", addr)
	if len(ch.CodeHash) != 0 || ch.ExpiresAt != 0 {
		t.Errorf("claim after failed send = %+v, want a bare claim", ch)
	}
}

func TestAddressVerifySender_RunDrainsAndReturnsOnCancel(t *testing.T) {
	env := newVerifyTestEnvStopped(t)
	_, queue := env.useFakes()
	const n = 5
	for i := range n {
		if !env.handler.submit(verifyJob{userID: "u", address: fmt.Sprintf("a%d@ext.example", i), rawCode: []byte("c"), senderDomain: "example.com"}) {
			t.Fatalf("submit %d refused", i)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() { env.handler.Run(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	if got := queue.calls.Load(); got != n {
		t.Errorf("processed %d jobs, want all %d buffered", got, n)
	}
	if !env.handler.idle() {
		t.Error("handler not idle after drain")
	}

	// Without a consumer the buffer fills and submit refuses rather than blocks.
	h2 := newVerifyTestEnvStopped(t).handler
	accepted := 0
	for range verifyQueueSize + 10 {
		if h2.submit(verifyJob{}) {
			accepted++
		}
	}
	if accepted != verifyQueueSize {
		t.Errorf("accepted %d jobs, want %d", accepted, verifyQueueSize)
	}
}

func TestAddSendAsAddress_PendingClaimCap(t *testing.T) {
	env := newVerifyTestEnvFull(t)
	env.seedUser(t, "userA", "a@example.com")
	env.seedUser(t, "userB", "b@example.com")
	for i := range maxPendingSendAsClaims {
		env.postAdd(t, "userA", "a@example.com", fmt.Sprintf("c%d@ext.example", i))
	}
	if list, _ := env.verifyStore.ListByUser("userA"); len(list) != maxPendingSendAsClaims {
		t.Fatalf("seeded %d claims, want %d", len(list), maxPendingSendAsClaims)
	}

	const over = "over@ext.example"
	for _, locale := range []string{"en", "de", "es", "fr"} {
		form := url.Values{"address": {over}}
		r := httptest.NewRequest(http.MethodPost, "/settings/addresses", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r = r.WithContext(middleware.WithLocale(middleware.WithUserContext(r.Context(), "userA", "a@example.com"), locale))
		w := httptest.NewRecorder()
		env.handler.AddSendAsAddress(w, r)
		want := "/settings/addresses?error=" + url.QueryEscape(i18n.T(locale, "settings.sendAsTooManyPending"))
		if loc := w.Header().Get("Location"); loc != want {
			t.Errorf("%s: Location = %q, want %q", locale, loc, want)
		}
		env.assertNoClaim(t, "userA", over)
	}
	if en, de := i18n.T("en", "settings.sendAsTooManyPending"), i18n.T("de", "settings.sendAsTooManyPending"); en == "settings.sendAsTooManyPending" || en == de {
		t.Errorf("message not translated: en=%q de=%q", en, de)
	}

	// Another user is unaffected.
	env.postAdd(t, "userB", "b@example.com", over)
	env.assertClaim(t, "userB", over)

	// Re-adding an existing claim is not a new claim.
	w := env.postAdd(t, "userA", "a@example.com", "c0@ext.example")
	if loc := w.Header().Get("Location"); !strings.Contains(loc, "flash=") {
		t.Errorf("re-add Location = %q, want the normal reply", loc)
	}
}

func assertBare(t *testing.T, env *verifyTestEnv, userID, address string) {
	t.Helper()
	ch := env.assertClaim(t, userID, address)
	if len(ch.CodeHash) != 0 || ch.ExpiresAt != 0 || ch.Attempts != 0 {
		t.Errorf("claim %s = %+v, want a bare claim", address, ch)
	}
}

func TestRequestVerification_FailsClosedBeforeAnyWrite(t *testing.T) {
	const free, taken = "free@ext.example", "taken@ext.example"
	cases := []struct {
		name   string
		break_ func(e *verifyTestEnv)
	}{
		{"no ownership stores", func(e *verifyTestEnv) { e.handler.SetOwnershipStores(nil, nil, nil) }},
		{"no blob store", func(e *verifyTestEnv) { e.handler.blobs = nil }},
		{"no queue store", func(e *verifyTestEnv) { e.handler.queueStore = nil }},
		{"sender not running", func(e *verifyTestEnv) { e.handler.running.Store(false) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newVerifyTestEnvStopped(t)
			env.useFakes()
			env.seedUser(t, "userA", "a@example.com")
			env.seedUser(t, "userB", "b@example.com")
			env.seedAddress(t, taken, "userA", "identity", true, true)
			env.postAdd(t, "userB", "b@example.com", free)
			env.postAdd(t, "userB", "b@example.com", taken)
			env.handler.running.Store(true)
			tc.break_(env)

			for _, addr := range []string{free, taken} {
				w := env.requestVerificationHTTP(t, "userB", "b@example.com", addr)
				if w.Code != http.StatusInternalServerError {
					t.Errorf("%s: status = %d, want 500", addr, w.Code)
				}
				assertBare(t, env, "userB", addr)
			}
			if env.handler.pending.Load() != 0 {
				t.Errorf("pending jobs = %d, want 0", env.handler.pending.Load())
			}
		})
	}
}

func TestRequestVerification_FullBufferResetsClaim(t *testing.T) {
	env := newVerifyTestEnvStopped(t)
	env.useFakes()
	const addr = "free@ext.example"
	env.seedUser(t, "userB", "b@example.com")
	env.postAdd(t, "userB", "b@example.com", addr)
	env.handler.running.Store(true) // accepted, but nothing drains the buffer
	for i := range verifyQueueSize {
		if !env.handler.submit(verifyJob{userID: "x", address: fmt.Sprintf("f%d@ext.example", i)}) {
			t.Fatalf("filling buffer: submit %d refused", i)
		}
	}

	w := env.requestVerificationHTTP(t, "userB", "b@example.com", addr)
	if w.Code != http.StatusSeeOther || !strings.Contains(w.Header().Get("Location"), "flash=Verification+code+sent") {
		t.Errorf("reply = %d %q, want the normal success redirect", w.Code, w.Header().Get("Location"))
	}
	assertBare(t, env, "userB", addr)
	if logs := env.logBuf.String(); !strings.Contains(logs, "sender queue full") || !strings.Contains(logs, "level=WARN") {
		t.Errorf("no queue-full warning logged:\n%s", logs)
	}
	// The user can ask again once the buffer drains.
	startRun(t, env.handler)
	env.flush(t)
	if loc := env.requestVerificationHTTP(t, "userB", "b@example.com", addr).Header().Get("Location"); !strings.Contains(loc, "flash=Verification+code+sent") {
		t.Errorf("re-request Location = %q", loc)
	}
}

func TestAddressVerifySender_FailedOwnershipCheckResetsClaim(t *testing.T) {
	env := newVerifyTestEnvStopped(t)
	_, queue := env.useFakes()
	const addr = "free@ext.example"
	env.seedUser(t, "userB", "b@example.com")
	env.postAdd(t, "userB", "b@example.com", addr)
	env.handler.running.Store(true)
	if w := env.requestVerificationHTTP(t, "userB", "b@example.com", addr); w.Code != http.StatusSeeOther {
		t.Fatalf("request status = %d", w.Code)
	}
	if ch, _, _ := env.verifyStore.Get("userB", addr); len(ch.CodeHash) == 0 {
		t.Fatal("no challenge stored before the sender ran")
	}

	// Make the sender's ownership read fail.
	if err := env.meta.Close(); err != nil {
		t.Fatalf("closing metadata: %v", err)
	}
	if _, err := env.handler.addressUnavailable("userB", addr); err == nil {
		t.Skip("closing the metadata manager no longer makes the ownership check fail")
	}
	env.handler.running.Store(false)
	startRun(t, env.handler)
	env.flush(t)

	assertBare(t, env, "userB", addr)
	if queue.calls.Load() != 0 {
		t.Errorf("enqueue calls = %d, want 0", queue.calls.Load())
	}
	if logs := env.logBuf.String(); !strings.Contains(logs, "ownership check failed") {
		t.Errorf("failure not logged:\n%s", logs)
	}
}

// The agent cancels the sender only after the HTTP server has shut down, so a
// request accepted during the drain is still sent. Cancelling Run's context
// stands in for that late cancel: everything buffered is processed first, and
// afterwards requests fail closed.
func TestAddressVerifySender_DrainsInFlightSubmitThenFailsClosed(t *testing.T) {
	env := newVerifyTestEnvStopped(t)
	_, queue := env.useFakes()
	const addr = "free@ext.example"
	env.seedUser(t, "userB", "b@example.com")
	env.postAdd(t, "userB", "b@example.com", addr)
	cancel, done := startRun(t, env.handler)

	if w := env.requestVerificationHTTP(t, "userB", "b@example.com", addr); w.Code != http.StatusSeeOther {
		t.Fatalf("request status = %d", w.Code)
	}
	cancel()
	<-done
	if got := queue.recipients(); len(got) != 1 || got[0] != addr {
		t.Errorf("recipients after shutdown = %v, want the in-flight request sent", got)
	}
	if env.handler.running.Load() {
		t.Error("sender still marked running after Run returned")
	}
	if w := env.requestVerificationHTTP(t, "userB", "b@example.com", addr); w.Code != http.StatusInternalServerError {
		t.Errorf("request after sender stopped = %d, want 500", w.Code)
	}
}

func TestAddressVerifySender_BlobWriteFailureResetsClaim(t *testing.T) {
	env := newVerifyTestEnvStopped(t)
	blobs, queue := env.useFakes()
	blobs.err = errors.New("disk full")
	const addr = "free@ext.example"
	env.seedUser(t, "userB", "b@example.com")
	env.postAdd(t, "userB", "b@example.com", addr)
	startRun(t, env.handler)

	env.requestVerificationHTTP(t, "userB", "b@example.com", addr)
	env.flush(t)

	assertBare(t, env, "userB", addr)
	if queue.calls.Load() != 0 {
		t.Errorf("enqueue calls = %d, want 0", queue.calls.Load())
	}
	if !strings.Contains(env.logBuf.String(), "failed to store email blob") {
		t.Errorf("failure not logged:\n%s", env.logBuf.String())
	}
}
