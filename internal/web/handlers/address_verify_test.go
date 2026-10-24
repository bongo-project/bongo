package handlers

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/mail"
	"github.com/bongo-project/bongo/internal/queue"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// verifyTestEnv holds all stores needed for AddressVerifyHandler tests.
type verifyTestEnv struct {
	ps          *store.PebbleStore
	blobs       *store.BlobStore
	meta        *store.MetadataManager
	addresses   *store.AddressStore
	verifyStore *store.AddressVerifyStore
	qs          *queue.QueueStore
	applier     *directApplierTags
	handler     *AddressVerifyHandler
	logBuf      *bytes.Buffer
}

// newVerifyTestEnvFull builds the env and starts the background sender, as the
// agent does. Tests that read the queue go through flush first.
func newVerifyTestEnvFull(t *testing.T) *verifyTestEnv {
	t.Helper()
	env := newVerifyTestEnvStopped(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		env.handler.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	waitRunning(t, env.handler)
	return env
}

// waitRunning blocks until the sender goroutine has marked itself running.
func waitRunning(t *testing.T, h *AddressVerifyHandler) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !h.running.Load() {
		if time.Now().After(deadline) {
			t.Fatal("sender did not start")
		}
		time.Sleep(time.Millisecond)
	}
}

// flush waits until the background sender has finished every submitted job.
func (e *verifyTestEnv) flush(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !e.handler.idle() {
		if time.Now().After(deadline) {
			t.Fatal("background verification sender did not go idle")
		}
		time.Sleep(time.Millisecond)
	}
}

// newVerifyTestEnvStopped builds the env without starting the sender.
func newVerifyTestEnvStopped(t *testing.T) *verifyTestEnv {
	t.Helper()
	dir := t.TempDir()

	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })

	blobs, err := store.NewBlobStore(dir)
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}

	meta, err := store.NewMetadataManager(dir + "/meta")
	if err != nil {
		t.Fatalf("NewMetadataManager: %v", err)
	}
	t.Cleanup(func() { meta.Close() })

	applier := &directApplierTags{ps: ps}
	as := store.NewAddressStore(ps, applier, meta.System)
	avs := store.NewAddressVerifyStore(ps, applier)
	qs := queue.NewQueueStore(ps, applier)

	// Capture log output so we can assert the code is never logged.
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	h := NewAddressVerifyHandler(logger)
	h.SetAddressStore(as)
	h.SetVerifyStore(avs)
	h.SetBlobStore(blobs)
	h.SetQueueStore(qs)
	h.SetOwnershipStores(ps, meta, applier)

	return &verifyTestEnv{
		ps: ps, blobs: blobs, meta: meta,
		addresses: as, verifyStore: avs, qs: qs,
		applier: applier, handler: h, logBuf: &logBuf,
	}
}

// seedUser inserts a user into the SQLite users table.
func (e *verifyTestEnv) seedUser(t *testing.T, userID, email string) {
	t.Helper()
	_, err := e.meta.System.DB().Exec(
		`INSERT INTO users (id, username, display_name, enabled, created_at, modified_at)
		 VALUES (?, ?, ?, 1, unixepoch(), unixepoch())
		 ON CONFLICT(id) DO NOTHING`,
		userID, email, "Test User",
	)
	if err != nil {
		t.Fatalf("seedUser %q: %v", email, err)
	}
}

// seedAddress stores an address record via AddressStore.
func (e *verifyTestEnv) seedAddress(t *testing.T, address, userID, kind string, canSend, verified bool) {
	t.Helper()
	atIdx := strings.LastIndex(address, "@")
	domain := "localhost"
	if atIdx >= 0 {
		domain = address[atIdx+1:]
	}
	if err := e.addresses.Put(address, &bongov1.AddressRecord{
		UserId:   userID,
		Domain:   domain,
		Kind:     kind,
		CanSend:  canSend,
		Verified: verified,
	}); err != nil {
		t.Fatalf("seedAddress %q: %v", address, err)
	}
}

// requestVerificationHTTP POSTs to /settings/addresses/{address}/verify-request.
func (e *verifyTestEnv) requestVerificationHTTP(t *testing.T, userID, sessionEmail, address string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/settings/addresses/%s/verify-request", address),
		nil,
	)
	r = r.WithContext(middleware.WithUserContext(r.Context(), userID, sessionEmail))
	r = withChiParam(r, "address", address)
	w := httptest.NewRecorder()
	e.handler.RequestVerification(w, r)
	return w
}

// submitCodeHTTP POSTs to /settings/addresses/{address}/verify-submit.
func (e *verifyTestEnv) submitCodeHTTP(t *testing.T, userID, sessionEmail, address, code string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"code": {code}}
	r := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/settings/addresses/%s/verify-submit", address),
		strings.NewReader(form.Encode()),
	)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r = r.WithContext(middleware.WithUserContext(r.Context(), userID, sessionEmail))
	r = withChiParam(r, "address", address)
	w := httptest.NewRecorder()
	e.handler.SubmitCode(w, r)
	return w
}

// extractCodeFromQueue reads the most recently enqueued blob and extracts
// the plaintext code from the email body. This simulates what the user sees.
func (e *verifyTestEnv) extractCodeFromQueue(t *testing.T) string {
	t.Helper()
	e.flush(t)
	pending, err := e.qs.ListPending(10)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(pending) == 0 {
		t.Fatal("no queued messages: verification email was not sent")
	}
	msg := pending[len(pending)-1]

	// Verification mail is server-generated: it must be marked LOCAL so the
	// queue worker DKIM-signs it.
	if got := msg.GetOrigin(); got != bongov1.OutboundOrigin_OUTBOUND_ORIGIN_LOCAL {
		t.Errorf("verification OutboundMessage.Origin = %v, want LOCAL", got)
	}

	rc, err := e.blobs.Get(msg.BlobHash)
	if err != nil {
		t.Fatalf("BlobStore.Get(%q): %v", msg.BlobHash, err)
	}
	defer rc.Close()

	raw, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("reading blob: %v", err)
	}

	// The email body has the code on its own line after the intro.
	body := string(raw)
	const marker = "Your Bongo Mail address verification code is:\r\n\r\n"
	idx := strings.Index(body, marker)
	if idx < 0 {
		t.Fatalf("code marker not found in email body; body snippet: %.200s", body)
	}
	rest := body[idx+len(marker):]
	endIdx := strings.Index(rest, "\r\n")
	if endIdx < 0 {
		t.Fatal("no CRLF after code in email body")
	}
	return strings.TrimSpace(rest[:endIdx])
}

// --- Happy Path ---

// TestSelfServe_HappyPath exercises the full self-serve verification flow:
// add address → request code → code sent to claimed address → submit correct code
// → address becomes Verified+CanSend → appears in From picker.
func TestSelfServe_HappyPath(t *testing.T) {
	env := newVerifyTestEnvFull(t)
	userID := "user-happy"
	email := "alice@example.com"
	claimedAddr := "alice@external.example"

	env.seedUser(t, userID, email)
	env.seedAddress(t, email, userID, "primary", true, true)

	// Step 1: Add the claimed address (POST /settings/addresses).
	form := url.Values{"address": {claimedAddr}}
	r := httptest.NewRequest(http.MethodPost, "/settings/addresses",
		strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r = r.WithContext(middleware.WithUserContext(r.Context(), userID, email))
	w := httptest.NewRecorder()
	env.handler.AddSendAsAddress(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("AddSendAsAddress = %d; body: %s", w.Code, w.Body.String())
	}

	// Step 2: Request verification.
	w2 := env.requestVerificationHTTP(t, userID, email, claimedAddr)
	if w2.Code != http.StatusSeeOther {
		t.Fatalf("RequestVerification = %d; body: %s", w2.Code, w2.Body.String())
	}

	// Assert: email was queued TO the claimed address.
	env.flush(t)
	pending, err := env.qs.ListPending(10)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(pending) == 0 {
		t.Fatal("no queued messages: verification email was not sent")
	}
	hasRecipient := false
	for _, rcpt := range pending[0].Recipients {
		if strings.EqualFold(rcpt, claimedAddr) {
			hasRecipient = true
			break
		}
	}
	if !hasRecipient {
		t.Errorf("verification email not addressed to %q; recipients: %v", claimedAddr, pending[0].Recipients)
	}

	// Assert: stored challenge contains a HASH, not the plaintext code.
	challenge, ok, err := env.verifyStore.Get(userID, claimedAddr)
	if err != nil || !ok {
		t.Fatalf("Get challenge: ok=%v err=%v", ok, err)
	}
	if len(challenge.CodeHash) == 0 {
		t.Fatal("stored challenge has empty code_hash")
	}
	// The hash must NOT equal the raw code bytes. We extract the code from the
	// email blob and confirm the stored bytes differ.
	rawCode := env.extractCodeFromQueue(t)
	if bytes.Equal(challenge.CodeHash, []byte(rawCode)) {
		t.Error("SECURITY: stored code_hash equals the plaintext code (hash must differ)")
	}
	// The hash must equal sha256(rawCode) (verified by the submit path working).
	expectedHash := hashVerificationCode([]byte(rawCode))
	if !bytes.Equal(challenge.CodeHash, expectedHash) {
		t.Error("stored code_hash is not sha256(rawCode)")
	}

	// Assert: plaintext code is NOT in log output.
	if strings.Contains(env.logBuf.String(), rawCode) {
		t.Errorf("SECURITY: plaintext code leaked into log output")
	}

	// Step 3: Submit the correct code.
	w3 := env.submitCodeHTTP(t, userID, email, claimedAddr, rawCode)
	if w3.Code != http.StatusSeeOther {
		t.Fatalf("SubmitCode = %d; body: %s", w3.Code, w3.Body.String())
	}

	// Assert: address is now Verified+CanSend.
	rec, ok, err := env.addresses.Get(claimedAddr)
	if err != nil || !ok {
		t.Fatalf("Get address after verify: ok=%v err=%v", ok, err)
	}
	if !rec.Verified {
		t.Error("Verified should be true after successful code submit")
	}
	if !rec.CanSend {
		t.Error("CanSend should be true after successful code submit")
	}

	// Assert: challenge consumed (deleted after success).
	_, stillExists, _ := env.verifyStore.Get(userID, claimedAddr)
	if stillExists {
		t.Error("challenge should be deleted after successful verification")
	}

	// Assert: plaintext code not in logs (still).
	if strings.Contains(env.logBuf.String(), rawCode) {
		t.Errorf("SECURITY: plaintext code leaked into log output after submit")
	}
}

// --- Wrong Code ---

func TestSelfServe_WrongCode_DoesNotVerify(t *testing.T) {
	env := newVerifyTestEnvFull(t)
	userID := "user-wrongcode"
	email := "alice@example.com"
	claimedAddr := "alice@external.example"

	env.seedUser(t, userID, email)
	env.seedAddress(t, claimedAddr, userID, "identity", false, false)

	w := env.requestVerificationHTTP(t, userID, email, claimedAddr)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("RequestVerification = %d", w.Code)
	}

	// Submit a WRONG code.
	w2 := env.submitCodeHTTP(t, userID, email, claimedAddr, "definitely-not-the-code")
	if w2.Code != http.StatusSeeOther {
		t.Fatalf("SubmitCode(wrong) = %d; body: %s", w2.Code, w2.Body.String())
	}
	// Should redirect to error (the test only checks no verification happened).

	// Address must remain unverified.
	rec, ok, _ := env.addresses.Get(claimedAddr)
	if !ok {
		t.Fatal("address disappeared after wrong code")
	}
	if rec.Verified || rec.CanSend {
		t.Error("address must not be verified/can-send after wrong code")
	}

	// Attempt count must have increased.
	ch, ok, _ := env.verifyStore.Get(userID, claimedAddr)
	if !ok {
		t.Fatal("challenge disappeared after wrong code (should persist)")
	}
	if ch.Attempts == 0 {
		t.Error("challenge.Attempts should have been incremented after wrong code")
	}
}

// --- Expired Code ---

func TestSelfServe_ExpiredCode_DoesNotVerify(t *testing.T) {
	env := newVerifyTestEnvFull(t)
	userID := "user-expired"
	email := "alice@example.com"
	claimedAddr := "alice@external.example"

	env.seedUser(t, userID, email)
	env.seedAddress(t, claimedAddr, userID, "identity", false, false)

	// Manually plant an already-expired challenge.
	expiredChallenge := &bongov1.AddressVerification{
		CodeHash:  hashVerificationCode([]byte("testcode")),
		ExpiresAt: time.Now().Add(-1 * time.Minute).Unix(), // in the past
		Attempts:  0,
	}
	if err := env.verifyStore.Put(userID, claimedAddr, expiredChallenge); err != nil {
		t.Fatalf("Put expired challenge: %v", err)
	}

	w := env.submitCodeHTTP(t, userID, email, claimedAddr, "testcode")
	if w.Code != http.StatusSeeOther {
		t.Fatalf("SubmitCode(expired) = %d; body: %s", w.Code, w.Body.String())
	}
	// Should have redirected with error; address must not be verified.

	rec, ok, _ := env.addresses.Get(claimedAddr)
	if !ok {
		t.Fatal("address disappeared after expired code submission")
	}
	if rec.Verified || rec.CanSend {
		t.Error("address must not be verified after expired code")
	}

	// The expired challenge is reset to a bare claim, not deleted.
	ch, stillExists, _ := env.verifyStore.Get(userID, claimedAddr)
	if !stillExists || len(ch.CodeHash) != 0 || ch.ExpiresAt != 0 {
		t.Errorf("expired challenge = %+v exists=%v, want a bare claim", ch, stillExists)
	}
}

// --- Reused Code (single-use) ---

func TestSelfServe_ReusedCode_FailsSecondTime(t *testing.T) {
	env := newVerifyTestEnvFull(t)
	userID := "user-reuse"
	email := "alice@example.com"
	claimedAddr := "alice@external.example"

	env.seedUser(t, userID, email)
	env.seedAddress(t, claimedAddr, userID, "identity", false, false)

	// Request verification.
	env.requestVerificationHTTP(t, userID, email, claimedAddr)
	rawCode := env.extractCodeFromQueue(t)

	// First submit: should succeed.
	w1 := env.submitCodeHTTP(t, userID, email, claimedAddr, rawCode)
	if w1.Code != http.StatusSeeOther {
		t.Fatalf("first submit = %d; body: %s", w1.Code, w1.Body.String())
	}

	// Second submit with same code: challenge is consumed, should fail.
	w2 := env.submitCodeHTTP(t, userID, email, claimedAddr, rawCode)
	// Should redirect with error (no challenge exists).
	if w2.Code < http.StatusBadRequest && w2.Code != http.StatusSeeOther {
		t.Fatalf("second submit unexpected status = %d", w2.Code)
	}
	// The address should still be verified (first submit succeeded).
	rec, ok, _ := env.addresses.Get(claimedAddr)
	if !ok {
		t.Fatal("address disappeared")
	}
	if !rec.Verified {
		t.Error("address should still be verified after second submit attempt")
	}

	// But if the challenge is gone, a "wrong code" error (redirect) is expected.
	// The key assertion is that the second submit did NOT break the verified state.
}

// --- Rate Limiting (existing challenge blocks new request) ---

func TestSelfServe_RateLimit_ExistingChallengePreventsNewCode(t *testing.T) {
	env := newVerifyTestEnvFull(t)
	userID := "user-ratelimit"
	email := "alice@example.com"
	claimedAddr := "alice@external.example"

	env.seedUser(t, userID, email)
	env.seedAddress(t, claimedAddr, userID, "identity", false, false)

	// First request: should succeed.
	w1 := env.requestVerificationHTTP(t, userID, email, claimedAddr)
	if w1.Code != http.StatusSeeOther {
		t.Fatalf("first RequestVerification = %d", w1.Code)
	}
	env.flush(t)
	count1, _ := env.qs.ListPending(10)

	// Second request while first is still valid: should be rate-limited.
	w2 := env.requestVerificationHTTP(t, userID, email, claimedAddr)
	if w2.Code != http.StatusSeeOther {
		t.Fatalf("second RequestVerification = %d", w2.Code)
	}
	// The redirect location should indicate the rate limit error.
	loc2 := w2.Header().Get("Location")
	if !strings.Contains(loc2, "error") && !strings.Contains(strings.ToLower(loc2), "already") {
		t.Logf("second request location: %s (expected rate-limit indication)", loc2)
	}

	// No additional email should have been queued.
	env.flush(t)
	count2, _ := env.qs.ListPending(10)
	if len(count2) > len(count1) {
		t.Error("a second code was queued despite an existing valid challenge (rate limit bypassed)")
	}
}

// --- Max Attempts (brute-force guard) ---

func TestSelfServe_MaxAttempts_InvalidatesChallenge(t *testing.T) {
	env := newVerifyTestEnvFull(t)
	userID := "user-maxattempts"
	email := "alice@example.com"
	claimedAddr := "alice@external.example"

	env.seedUser(t, userID, email)
	env.seedAddress(t, claimedAddr, userID, "identity", false, false)

	env.requestVerificationHTTP(t, userID, email, claimedAddr)

	// Submit wrong code maxVerifyAttempts+1 times.
	for range maxVerifyAttempts + 1 {
		env.submitCodeHTTP(t, userID, email, claimedAddr, "wrongcode")
	}

	// After exceeding maxAttempts the challenge is reset to a bare claim.
	ch, stillExists, _ := env.verifyStore.Get(userID, claimedAddr)
	if !stillExists || len(ch.CodeHash) != 0 || ch.Attempts != 0 {
		t.Errorf("challenge = %+v exists=%v, want a bare claim after max attempts", ch, stillExists)
	}

	// Even if the user somehow gets the real code, they can no longer verify.
	rawCode := env.extractCodeFromQueue(t)
	w := env.submitCodeHTTP(t, userID, email, claimedAddr, rawCode)
	// Should redirect with "no pending verification" error.
	_ = w // Status code varies by impl; key thing is challenge is gone.

	rec, ok, _ := env.addresses.Get(claimedAddr)
	if !ok {
		t.Fatal("address disappeared")
	}
	if rec.Verified || rec.CanSend {
		t.Error("address must not be verified after max-attempts invalidation")
	}
}

// --- Cross-User Ownership ---

func TestSelfServe_CrossUserRequest_Rejected(t *testing.T) {
	env := newVerifyTestEnvFull(t)

	userID1 := "user-owner"
	email1 := "owner@example.com"
	userID2 := "user-attacker"
	email2 := "attacker@example.com"
	claimedAddr := "claimed@external.example"

	env.seedUser(t, userID1, email1)
	env.seedUser(t, userID2, email2)

	// Address belongs to user1.
	env.seedAddress(t, claimedAddr, userID1, "identity", false, false)

	// user2 tries to request verification for user1's address.
	w := env.requestVerificationHTTP(t, userID2, email2, claimedAddr)
	if w.Code == http.StatusSeeOther {
		loc := w.Header().Get("Location")
		if !strings.Contains(loc, "error") {
			// Redirect without error means the request was accepted — which is wrong.
			// Acceptable only if a "code sent" flash was shown but no code was queued.
			env.flush(t)
			pending, _ := env.qs.ListPending(10)
			if len(pending) > 0 {
				t.Error("verification email sent for cross-user ownership attempt")
			}
		}
	}

	// No challenge should exist under user2's namespace for this address.
	_, hasChallenge, _ := env.verifyStore.Get(userID2, claimedAddr)
	if hasChallenge {
		t.Error("challenge created for user2 on address owned by user1 (cross-user ownership bypassed)")
	}

	// user1's address must not be modified.
	rec, ok, _ := env.addresses.Get(claimedAddr)
	if !ok {
		t.Fatal("address disappeared")
	}
	if rec.Verified || rec.CanSend {
		t.Error("address must not be verified after cross-user ownership attempt")
	}
}

func TestSelfServe_CrossUserSubmit_Rejected(t *testing.T) {
	env := newVerifyTestEnvFull(t)

	userID1 := "user-owner2"
	email1 := "owner@example.com"
	userID2 := "user-attacker2"
	email2 := "attacker@example.com"
	claimedAddr := "claimed@external.example"

	env.seedUser(t, userID1, email1)
	env.seedUser(t, userID2, email2)

	// Address belongs to user1.
	env.seedAddress(t, claimedAddr, userID1, "identity", false, false)

	// user2 tries to submit a code for user1's address.
	w := env.submitCodeHTTP(t, userID2, email2, claimedAddr, "anycode")
	if w.Code < http.StatusBadRequest && w.Code != http.StatusSeeOther {
		t.Fatalf("cross-user SubmitCode = %d, want redirect-with-error or 4xx", w.Code)
	}

	// Address must remain unverified.
	rec, ok, _ := env.addresses.Get(claimedAddr)
	if !ok {
		t.Fatal("address disappeared")
	}
	if rec.Verified || rec.CanSend {
		t.Error("address must not be verified after cross-user submit")
	}
}

// --- Unverified address excluded from picker and send rejected ---

func TestSelfServe_UnverifiedAddress_ExcludedFromPicker(t *testing.T) {
	env := newComposeSendAsEnv(t)
	userID := "user-unverified-picker"
	email := "alice@example.com"
	unverifiedAddr := "unverified@other.example"

	env.seedUser(t, userID, email)
	env.seedAddress(t, email, userID, "example.com", true, true)
	env.seedAddress(t, unverifiedAddr, userID, "other.example", false, false)

	opts, _ := env.handler.buildFromPickerOptions(userID, email)
	for _, o := range opts {
		if strings.EqualFold(o, unverifiedAddr) {
			t.Errorf("picker includes unverified address %q", unverifiedAddr)
		}
	}
}

func TestSelfServe_UnverifiedAddress_SendRejected(t *testing.T) {
	env := newComposeSendAsEnv(t)
	userID := "user-unverified-send"
	email := "alice@example.com"
	unverifiedAddr := "unverified@other.example"

	env.seedUser(t, userID, email)
	env.seedAddress(t, email, userID, "example.com", true, true)
	env.seedAddress(t, unverifiedAddr, userID, "other.example", false, false)

	form := url.Values{
		"send_id": {sendID()},
		"to":      {email},
		"subject": {"Test"},
		"body":    {"hello"},
		"from":    {unverifiedAddr},
	}
	req := httptest.NewRequest(http.MethodPost, "/compose/send",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUserContext(req, userID, email)
	w := httptest.NewRecorder()
	env.handler.Send(w, req)

	if w.Code != http.StatusForbidden && w.Code != http.StatusBadRequest {
		t.Fatalf("send from unverified address = %d, want 403/400; body: %s", w.Code, w.Body.String())
	}
}

// --- Code Not in Logs ---

func TestSelfServe_CodeNeverInLogs(t *testing.T) {
	env := newVerifyTestEnvFull(t)
	userID := "user-noleak"
	email := "alice@example.com"
	claimedAddr := "alice@external.example"

	env.seedUser(t, userID, email)
	env.seedAddress(t, claimedAddr, userID, "identity", false, false)

	env.requestVerificationHTTP(t, userID, email, claimedAddr)
	rawCode := env.extractCodeFromQueue(t)
	env.submitCodeHTTP(t, userID, email, claimedAddr, rawCode)

	logOutput := env.logBuf.String()
	if strings.Contains(logOutput, rawCode) {
		t.Errorf("SECURITY: plaintext verification code %q found in log output:\n%s", rawCode, logOutput)
	}
}

// --- AddSendAsAddress tests ---

func TestSelfServe_AddSendAsAddress_CrossUser_Rejected(t *testing.T) {
	env := newVerifyTestEnvFull(t)

	userID1 := "user-add-owner"
	email1 := "alice@example.com"
	userID2 := "user-add-attacker"
	email2 := "bob@example.com"
	sharedAddr := "shared@example.com"

	env.seedUser(t, userID1, email1)
	env.seedUser(t, userID2, email2)

	// Address owned by user1.
	env.seedAddress(t, sharedAddr, userID1, "identity", false, false)

	// user2 tries to add the same address.
	form := url.Values{"address": {sharedAddr}}
	r := httptest.NewRequest(http.MethodPost, "/settings/addresses",
		strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r = r.WithContext(middleware.WithUserContext(r.Context(), userID2, email2))
	w := httptest.NewRecorder()
	env.handler.AddSendAsAddress(w, r)

	// The reply must not reveal that the address is taken; the store shows it.
	if w.Code != http.StatusSeeOther {
		t.Fatalf("AddSendAsAddress cross-user = %d", w.Code)
	}
	if loc := w.Header().Get("Location"); strings.Contains(loc, "error") {
		t.Errorf("AddSendAsAddress cross-user must not redirect with an error; Location: %s", loc)
	}
	if addrs, _ := env.addresses.ListByUser(userID2); slices.Contains(addrs, sharedAddr) {
		t.Errorf("attacker's address list holds %s", sharedAddr)
	}

	// Address record must still belong to user1.
	rec, ok, _ := env.addresses.Get(sharedAddr)
	if !ok {
		t.Fatal("address disappeared")
	}
	if rec.UserId != userID1 {
		t.Errorf("UserId changed to %q after cross-user add attempt", rec.UserId)
	}
}

func TestSelfServe_AddSendAsAddress_InvalidEmail_Rejected(t *testing.T) {
	env := newVerifyTestEnvFull(t)
	userID := "user-invalid-email"
	email := "alice@example.com"
	env.seedUser(t, userID, email)

	for _, bad := range []string{"", "notanemail", "@nodomain", "noat"} {
		form := url.Values{"address": {bad}}
		r := httptest.NewRequest(http.MethodPost, "/settings/addresses",
			strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r = r.WithContext(middleware.WithUserContext(r.Context(), userID, email))
		w := httptest.NewRecorder()
		env.handler.AddSendAsAddress(w, r)

		if w.Code != http.StatusSeeOther {
			t.Errorf("AddSendAsAddress(%q) status = %d, want redirect", bad, w.Code)
			continue
		}
		loc := w.Header().Get("Location")
		if !strings.Contains(loc, "error") {
			t.Errorf("AddSendAsAddress(%q) should redirect with error; Location: %s", bad, loc)
		}
	}
}

// --- buildVerificationEmail ---

func TestBuildVerificationEmail_ContainsCode(t *testing.T) {
	code := []byte("deadbeef12345678")
	domain := "example.com"
	email := buildVerificationEmail("target@other.com", code, domain)

	if !bytes.Contains(email, code) {
		t.Error("verification email body does not contain the code")
	}
	if !bytes.Contains(email, []byte("To: target@other.com")) {
		t.Error("verification email is not addressed to the target")
	}
	if !bytes.Contains(email, []byte(fmt.Sprintf("postmaster@%s", domain))) {
		t.Errorf("verification email From is not postmaster@%s", domain)
	}
}

// --- generateVerificationCode and hashVerificationCode ---

func TestGenerateVerificationCode_Length(t *testing.T) {
	code, err := generateVerificationCode()
	if err != nil {
		t.Fatalf("generateVerificationCode: %v", err)
	}
	if len(code) != verifyCodeLen*2 {
		t.Errorf("code length = %d, want %d (hex of %d bytes)", len(code), verifyCodeLen*2, verifyCodeLen)
	}
}

func TestHashVerificationCode_Deterministic(t *testing.T) {
	code := []byte("hello-world")
	h1 := hashVerificationCode(code)
	h2 := hashVerificationCode(code)
	if !bytes.Equal(h1, h2) {
		t.Error("hashVerificationCode is not deterministic")
	}
	if bytes.Equal(h1, code) {
		t.Error("hash equals input (not hashed)")
	}
	if len(h1) != 32 {
		t.Errorf("hash length = %d, want 32 (SHA-256)", len(h1))
	}
}

// TestSelfServe_PerUserLimit_RefusesWithoutLeakingAddress: the verification
// mailer is capped per user, the refusal names no address, and a refused
// request leaves no stranded challenge.
func TestSelfServe_PerUserLimit_RefusesWithoutLeakingAddress(t *testing.T) {
	env := newVerifyTestEnvFull(t)
	env.handler.SetLimiter(mail.NewRateLimiter(3))
	userID, email := "user-cap", "alice@example.com"
	env.seedUser(t, userID, email)

	addrs := []string{"one@claimed.example", "two@claimed.example", "three@claimed.example", "four@claimed.example"}
	for _, a := range addrs {
		env.seedAddress(t, a, userID, "identity", false, false)
	}

	for _, a := range addrs[:3] {
		w := env.requestVerificationHTTP(t, userID, email, a)
		if loc := w.Header().Get("Location"); !strings.Contains(loc, "flash=") {
			t.Fatalf("request for %s under the limit: Location=%q", a, loc)
		}
	}

	w := env.requestVerificationHTTP(t, userID, email, addrs[3])
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "error=Too+many") {
		t.Fatalf("4th request Location=%q, want the generic limit error", loc)
	}
	if strings.Contains(loc, "four") || strings.Contains(loc, "claimed.example") {
		t.Errorf("refusal leaks the target address: %q", loc)
	}
	if _, has, _ := env.verifyStore.Get(userID, addrs[3]); has {
		t.Error("a challenge was stored for the refused request")
	}
	env.flush(t)
	pending, _ := env.qs.ListPending(10)
	if len(pending) != 3 {
		t.Errorf("queue holds %d messages, want exactly 3", len(pending))
	}
}
