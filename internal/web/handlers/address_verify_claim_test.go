package handlers

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/mail"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func (e *verifyTestEnv) postAdd(t *testing.T, userID, sessionEmail, address string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"address": {address}}
	r := httptest.NewRequest(http.MethodPost, "/settings/addresses", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r = r.WithContext(middleware.WithUserContext(r.Context(), userID, sessionEmail))
	w := httptest.NewRecorder()
	e.handler.AddSendAsAddress(w, r)
	return w
}

func (e *verifyTestEnv) putKey(t *testing.T, key, val string) {
	t.Helper()
	if err := e.applier.ApplyCommand(&bongov1.RaftCommand{
		Op: bongov1.Op_OP_PUT, Key: []byte(key), Value: []byte(val),
	}, time.Second); err != nil {
		t.Fatalf("putKey %s: %v", key, err)
	}
}

func (e *verifyTestEnv) assertNoRecord(t *testing.T, address string) {
	t.Helper()
	if rec, ok, err := e.addresses.Get(address); err != nil || ok {
		t.Errorf("record for %s: ok=%v err=%v rec=%v, want none", address, ok, err, rec)
	}
	var n int
	if err := e.meta.System.DB().QueryRow(`SELECT count(*) FROM addresses WHERE address = ?`, address).Scan(&n); err != nil || n != 0 {
		t.Errorf("addresses rows for %s = %d (err %v), want 0", address, n, err)
	}
}

func (e *verifyTestEnv) getList(t *testing.T, userID, sessionEmail string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/settings/addresses", nil)
	r = r.WithContext(middleware.WithUserContext(r.Context(), userID, sessionEmail))
	w := httptest.NewRecorder()
	e.handler.ShowAddresses(w, r)
	return w
}

func (e *verifyTestEnv) assertClaim(t *testing.T, userID, address string) *bongov1.AddressVerification {
	t.Helper()
	ch, ok, err := e.verifyStore.Get(userID, address)
	if err != nil || !ok {
		t.Fatalf("claim for (%s, %s): ok=%v err=%v, want present", userID, address, ok, err)
	}
	return ch
}

func (e *verifyTestEnv) assertNoClaim(t *testing.T, userID, address string) {
	t.Helper()
	if _, ok, err := e.verifyStore.Get(userID, address); err != nil || ok {
		t.Errorf("claim for (%s, %s): ok=%v err=%v, want none", userID, address, ok, err)
	}
}

func (e *verifyTestEnv) queued(t *testing.T) int {
	t.Helper()
	e.flush(t)
	pending, err := e.qs.ListPending(50)
	if err != nil {
		t.Fatal(err)
	}
	return len(pending)
}

func (e *verifyTestEnv) rawRecord(t *testing.T, address string) []byte {
	t.Helper()
	raw, err := e.ps.Get([]byte("/system/address/" + address))
	if err != nil {
		t.Fatalf("raw record %s: %v", address, err)
	}
	return raw
}

const claimAddr = "claim@ext.example"

// US-002 (a): a self-serve add writes a per-user claim and no identity record.
func TestSelfServe_AddWritesClaimNotRecord(t *testing.T) {
	env := newVerifyTestEnvFull(t)
	env.seedUser(t, "userA", "a@example.com")
	env.seedAddress(t, "a@example.com", "userA", "primary", true, true)

	w := env.postAdd(t, "userA", "a@example.com", claimAddr)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", w.Code)
	}
	env.assertNoRecord(t, claimAddr)
	ch := env.assertClaim(t, "userA", claimAddr)
	if len(ch.CodeHash) != 0 || ch.ExpiresAt != 0 {
		t.Errorf("claim = %+v, want a bare claim without code or expiry", ch)
	}
}

// US-002 (b): the claim is listed, not send-capable and still offers a request.
func TestSelfServe_ClaimIsListed(t *testing.T) {
	env := newVerifyTestEnvFull(t)
	env.seedUser(t, "userA", "a@example.com")
	env.postAdd(t, "userA", "a@example.com", claimAddr)

	body := env.getList(t, "userA", "a@example.com").Body.String()
	if !strings.Contains(body, claimAddr) {
		t.Fatalf("list does not show %s", claimAddr)
	}
	if strings.Contains(body, "badge-active") {
		t.Error("claim is shown as send-capable")
	}
	if !strings.Contains(body, "verify-request") {
		t.Error("claim has no request-code action, so the user is stranded")
	}
}

// transcript records everything B can observe for one request.
func transcript(w *httptest.ResponseRecorder) string {
	return fmt.Sprintf("%d|%s|%s", w.Code, w.Header().Get("Location"), w.Body.String())
}

// bSequence runs the same user-B interaction and returns what B saw.
func bSequence(t *testing.T, env *verifyTestEnv, limit int) []string {
	t.Helper()
	if limit > 0 {
		env.handler.SetLimiter(mail.NewRateLimiter(limit))
	}
	const uid, email = "userB", "b@example.com"
	var out []string
	add := func(a string) { out = append(out, transcript(env.postAdd(t, uid, email, a))) }
	list := func() { out = append(out, transcript(env.getList(t, uid, email))) }
	req := func(a string) { out = append(out, transcript(env.requestVerificationHTTP(t, uid, email, a))) }
	sub := func(a, code string) { out = append(out, transcript(env.submitCodeHTTP(t, uid, email, a, code))) }

	add(claimAddr)
	list()
	req(claimAddr)
	req(claimAddr) // second request: live challenge
	sub(claimAddr, "wrong-code")
	list()
	// A second address behind the per-user cap.
	add("second@ext.example")
	req("second@ext.example")
	list()
	return out
}

// US-002 (c): nothing B can observe depends on whether A holds the address,
// including the repeat request and the limiter.
func TestSelfServe_ClaimResponsesIdenticalWhetherOrNotTaken(t *testing.T) {
	for _, limit := range []int{0, 1} {
		t.Run(fmt.Sprintf("limit=%d", limit), func(t *testing.T) {
			taken := newVerifyTestEnvFull(t)
			taken.seedUser(t, "userA", "a@example.com")
			taken.seedUser(t, "userB", "b@example.com")
			taken.seedAddress(t, claimAddr, "userA", "identity", true, true)

			free := newVerifyTestEnvFull(t)
			free.seedUser(t, "userA", "a@example.com")
			free.seedUser(t, "userB", "b@example.com")

			got, want := bSequence(t, taken, limit), bSequence(t, free, limit)
			if len(got) != len(want) {
				t.Fatalf("transcripts differ in length")
			}
			for i := range got {
				if got[i] != want[i] {
					t.Errorf("step %d differs between taken and free:\ntaken: %s\nfree:  %s", i, got[i], want[i])
				}
			}
			taken.assertClaim(t, "userB", claimAddr)
			free.assertClaim(t, "userB", claimAddr)

			// The challenge looks the same too: a live, unused challenge.
			for name, env := range map[string]*verifyTestEnv{"taken": taken, "free": free} {
				ch := env.assertClaim(t, "userB", claimAddr)
				if len(ch.CodeHash) != sha256Len || ch.ExpiresAt <= time.Now().Unix() {
					t.Errorf("%s challenge = %+v, want a live hashed challenge", name, ch)
				}
			}

			// A's record is untouched by all of it.
			if rec, _, _ := taken.addresses.Get(claimAddr); rec.UserId != "userA" || !rec.Verified {
				t.Errorf("A's record changed: %+v", rec)
			}
			// Control: the free env really sent mail, so (d) below is not vacuous.
			if free.queued(t) == 0 {
				t.Error("free env queued no verification mail")
			}
		})
	}
}

const sha256Len = 32

// US-002 (d): no verification mail for an address another account holds.
func TestSelfServe_RequestForTakenAddressQueuesNothing(t *testing.T) {
	cases := []struct {
		name string
		seed func(t *testing.T, e *verifyTestEnv)
	}{
		{"verified by another user", func(t *testing.T, e *verifyTestEnv) { e.seedAddress(t, claimAddr, "userA", "identity", true, true) }},
		{"another user's primary", func(t *testing.T, e *verifyTestEnv) { e.seedAddress(t, claimAddr, "userA", "primary", true, true) }},
		{"alias", func(t *testing.T, e *verifyTestEnv) { e.putKey(t, "/system/alias/"+claimAddr, "a@example.com") }},
		{"domain hosted after the claim", func(t *testing.T, e *verifyTestEnv) { e.putKey(t, "/system/domain/ext.example", "x") }},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			env := newVerifyTestEnvFull(t)
			env.seedUser(t, "userA", "a@example.com")
			env.seedUser(t, "userB", "b@example.com")
			env.postAdd(t, "userB", "b@example.com", claimAddr)
			tt.seed(t, env)

			w := env.requestVerificationHTTP(t, "userB", "b@example.com", claimAddr)
			if loc := w.Header().Get("Location"); loc != "/settings/addresses?flash=Verification+code+sent" {
				t.Errorf("Location = %q, want the normal success redirect", loc)
			}
			if n := env.queued(t); n != 0 {
				t.Errorf("%d messages queued, want 0", n)
			}
		})
	}
}

// US-002 (e): a correct code creates the verified record and removes the claim.
func TestSelfServe_CorrectCodeWritesRecordAndConsumesClaim(t *testing.T) {
	env := newVerifyTestEnvFull(t)
	env.seedUser(t, "userA", "a@example.com")
	env.postAdd(t, "userA", "a@example.com", claimAddr)
	env.requestVerificationHTTP(t, "userA", "a@example.com", claimAddr)
	code := env.extractCodeFromQueue(t)

	w := env.submitCodeHTTP(t, "userA", "a@example.com", claimAddr, code)
	if loc := w.Header().Get("Location"); loc != "/settings/addresses?flash=Address+verified" {
		t.Fatalf("Location = %q", loc)
	}
	rec, ok, err := env.addresses.Get(claimAddr)
	if err != nil || !ok {
		t.Fatalf("record: ok=%v err=%v", ok, err)
	}
	if rec.UserId != "userA" || !rec.Verified || !rec.CanSend || rec.Kind != "identity" || rec.Domain != "ext.example" {
		t.Errorf("record = %+v", rec)
	}
	env.assertNoClaim(t, "userA", claimAddr)
	if list, _ := env.addresses.ListByUser("userA"); !slices.Contains(list, claimAddr) {
		t.Errorf("sqlite list = %v, want the verified address", list)
	}
}

// US-002 (f): both users claim and request while the address is free; the
// first correct code wins and the second gets the generic failure.
func TestSelfServe_SecondCorrectCodeFailsGenerically(t *testing.T) {
	env := newVerifyTestEnvFull(t)
	env.seedUser(t, "userA", "a@example.com")
	env.seedUser(t, "userB", "b@example.com")

	env.postAdd(t, "userA", "a@example.com", claimAddr)
	env.requestVerificationHTTP(t, "userA", "a@example.com", claimAddr)
	codeA := env.extractCodeFromQueue(t)
	env.postAdd(t, "userB", "b@example.com", claimAddr)
	env.requestVerificationHTTP(t, "userB", "b@example.com", claimAddr)
	codeB := env.extractCodeFromQueue(t)
	if codeA == codeB {
		t.Fatal("codes collide, test cannot distinguish the users")
	}

	if w := env.submitCodeHTTP(t, "userA", "a@example.com", claimAddr, codeA); !strings.Contains(w.Header().Get("Location"), "flash=Address+verified") {
		t.Fatalf("A's submit Location = %q", w.Header().Get("Location"))
	}
	before := env.rawRecord(t, claimAddr)

	w := env.submitCodeHTTP(t, "userB", "b@example.com", claimAddr, codeB)
	if loc := w.Header().Get("Location"); !strings.HasPrefix(loc, "/settings/addresses?error=Verification+failed") {
		t.Errorf("B's Location = %q, want the generic verification failure", loc)
	}
	if !bytes.Equal(before, env.rawRecord(t, claimAddr)) {
		t.Error("A's record changed after B's correct code")
	}
	env.assertNoClaim(t, "userB", claimAddr)
}

// US-002 (g): a hosted domain is refused with the same reply and no claim.
func TestSelfServe_HostedDomainRefusedWithoutClaim(t *testing.T) {
	env := newVerifyTestEnvFull(t)
	env.seedUser(t, "userA", "a@example.com")
	env.putKey(t, "/system/domain/hosted.example", "x")

	hosted := env.postAdd(t, "userA", "a@example.com", "who@hosted.example")
	free := env.postAdd(t, "userA", "a@example.com", claimAddr)
	if hosted.Header().Get("Location") != free.Header().Get("Location") || hosted.Code != free.Code {
		t.Errorf("hosted reply %d %q differs from free reply %d %q",
			hosted.Code, hosted.Header().Get("Location"), free.Code, free.Header().Get("Location"))
	}
	env.assertNoClaim(t, "userA", "who@hosted.example")
	env.assertNoRecord(t, "who@hosted.example")
}

// An existing unverified identity record is an implicit claim for its owner.
func TestSelfServe_LegacyUnverifiedRecordCanStillBeVerified(t *testing.T) {
	env := newVerifyTestEnvFull(t)
	env.seedUser(t, "userA", "a@example.com")
	env.seedAddress(t, claimAddr, "userA", "identity", false, false)

	if body := env.getList(t, "userA", "a@example.com").Body.String(); !strings.Contains(body, claimAddr) {
		t.Error("legacy record no longer listed")
	}
	env.requestVerificationHTTP(t, "userA", "a@example.com", claimAddr)
	code := env.extractCodeFromQueue(t)
	env.submitCodeHTTP(t, "userA", "a@example.com", claimAddr, code)
	rec, _, _ := env.addresses.Get(claimAddr)
	if !rec.Verified || !rec.CanSend || rec.UserId != "userA" {
		t.Errorf("record = %+v, want verified", rec)
	}
	env.assertNoClaim(t, "userA", claimAddr)
}

// Without a claim or an own record there is nothing to request or submit.
func TestSelfServe_NoClaimNoRequestNoSubmit(t *testing.T) {
	env := newVerifyTestEnvFull(t)
	env.seedUser(t, "userB", "b@example.com")
	env.seedAddress(t, claimAddr, "userA", "identity", true, true)

	w := env.requestVerificationHTTP(t, "userB", "b@example.com", claimAddr)
	if loc := w.Header().Get("Location"); !strings.Contains(loc, "Address+not+found") {
		t.Errorf("request Location = %q", loc)
	}
	w = env.submitCodeHTTP(t, "userB", "b@example.com", claimAddr, "x")
	if loc := w.Header().Get("Location"); !strings.Contains(loc, "Address+not+found") {
		t.Errorf("submit Location = %q", loc)
	}
	env.assertNoClaim(t, "userB", claimAddr)
	if env.queued(t) != 0 {
		t.Error("mail queued without a claim")
	}
}

// A bare claim has no code to check.
func TestSelfServe_SubmitOnBareClaimKeepsClaim(t *testing.T) {
	env := newVerifyTestEnvFull(t)
	env.seedUser(t, "userA", "a@example.com")
	env.postAdd(t, "userA", "a@example.com", claimAddr)

	w := env.submitCodeHTTP(t, "userA", "a@example.com", claimAddr, "anything")
	if loc := w.Header().Get("Location"); !strings.Contains(loc, "No+pending+verification") {
		t.Errorf("Location = %q", loc)
	}
	env.assertClaim(t, "userA", claimAddr)
	env.assertNoRecord(t, claimAddr)
}

// Expiry and exhausted attempts reset the claim; they do not delete it.
func TestSelfServe_ExpiryAndAttemptsKeepClaimListed(t *testing.T) {
	t.Run("expired", func(t *testing.T) {
		env := newVerifyTestEnvFull(t)
		env.seedUser(t, "userA", "a@example.com")
		env.postAdd(t, "userA", "a@example.com", claimAddr)
		if err := env.verifyStore.Put("userA", claimAddr, &bongov1.AddressVerification{
			CodeHash: hashVerificationCode([]byte("c")), ExpiresAt: time.Now().Add(-time.Minute).Unix(),
		}); err != nil {
			t.Fatal(err)
		}
		w := env.submitCodeHTTP(t, "userA", "a@example.com", claimAddr, "c")
		if !strings.Contains(w.Header().Get("Location"), "expired") {
			t.Errorf("Location = %q", w.Header().Get("Location"))
		}
		assertResetClaim(t, env)
	})
	t.Run("attempts", func(t *testing.T) {
		env := newVerifyTestEnvFull(t)
		env.seedUser(t, "userA", "a@example.com")
		env.postAdd(t, "userA", "a@example.com", claimAddr)
		env.requestVerificationHTTP(t, "userA", "a@example.com", claimAddr)
		code := env.extractCodeFromQueue(t)
		for range maxVerifyAttempts + 1 {
			env.submitCodeHTTP(t, "userA", "a@example.com", claimAddr, "wrong")
		}
		assertResetClaim(t, env)
		env.submitCodeHTTP(t, "userA", "a@example.com", claimAddr, code)
		env.assertNoRecord(t, claimAddr)
	})
}

func assertResetClaim(t *testing.T, env *verifyTestEnv) {
	t.Helper()
	ch := env.assertClaim(t, "userA", claimAddr)
	if len(ch.CodeHash) != 0 || ch.ExpiresAt != 0 || ch.Attempts != 0 {
		t.Errorf("claim = %+v, want reset to a bare claim", ch)
	}
	if !strings.Contains(env.getList(t, "userA", "a@example.com").Body.String(), claimAddr) {
		t.Error("claim no longer listed")
	}
	// A fresh code can be requested again.
	if loc := env.requestVerificationHTTP(t, "userA", "a@example.com", claimAddr).Header().Get("Location"); !strings.Contains(loc, "flash=Verification+code+sent") {
		t.Errorf("re-request Location = %q", loc)
	}
}

func assertAlreadyOwned(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body %s", w.Code, w.Body.String())
	}
	// The reply is the same as for a successful add; the refusal shows in the
	// store (callers assert that), never in the message.
	if loc := w.Header().Get("Location"); strings.Contains(loc, "owned") || strings.Contains(loc, "error=") {
		t.Errorf("Location = %q, must not reveal ownership", loc)
	}
}

func TestSelfServe_AddSendAsAddress_PostBootAccount_Rejected(t *testing.T) {
	env := newVerifyTestEnvFull(t)
	env.seedUser(t, "userA", "a@example.com")
	env.seedAddress(t, "a@example.com", "userA", "primary", true, true)

	// B is created after boot through the real creation path.
	if _, err := auth.CreateUser(env.applier, env.meta, "b@example.com", "B", "secret123", false); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	// Simulate a pre-fix account: no address record at all.
	if err := env.addresses.Delete("b@example.com"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	assertAlreadyOwned(t, env.postAdd(t, "userA", "a@example.com", "b@example.com"))
	env.assertNoRecord(t, "b@example.com")
}

func TestSelfServe_AddSendAsAddress_TakenOrHosted_Rejected(t *testing.T) {
	tests := []struct {
		name    string
		address string
		seed    func(t *testing.T, e *verifyTestEnv)
	}{
		{"pebble alias key", "al@example.com", func(t *testing.T, e *verifyTestEnv) {
			e.putKey(t, "/system/alias/al@example.com", "a@example.com")
		}},
		{"sqlite aliases row", "al2@example.com", func(t *testing.T, e *verifyTestEnv) {
			if _, err := e.meta.System.DB().Exec(
				`INSERT INTO aliases (address, target_user_id, enabled, created_at) VALUES ('al2@example.com', 'userA', 1, 0)`); err != nil {
				t.Fatal(err)
			}
		}},
		{"identity of another user", "idn@elsewhere.example", func(t *testing.T, e *verifyTestEnv) {
			e.seedAddress(t, "idn@elsewhere.example", "userZ", "identity", false, false)
		}},
		{"hosted domain, unallocated name", "free@hosted.example", func(t *testing.T, e *verifyTestEnv) {
			e.putKey(t, "/system/domain/hosted.example", "x")
		}},
		{"catch-all domain", "any@catch.example", func(t *testing.T, e *verifyTestEnv) {
			e.putKey(t, "/system/domain/catch.example", "x")
			e.putKey(t, "/system/catchall/catch.example", "userZ")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newVerifyTestEnvFull(t)
			env.seedUser(t, "userA", "a@example.com")
			env.seedAddress(t, "a@example.com", "userA", "primary", true, true)
			tt.seed(t, env)

			w := env.postAdd(t, "userA", "a@example.com", tt.address)
			if w.Code != http.StatusSeeOther {
				t.Fatalf("status = %d, want 303", w.Code)
			}
			if loc := w.Header().Get("Location"); strings.Contains(loc, "error=") {
				t.Errorf("Location = %q, must not be an ownership-revealing error", loc)
			}
			rec, ok, _ := env.addresses.Get(tt.address)
			if ok && rec.UserId == "userA" {
				t.Errorf("record for %s was written for userA", tt.address)
			}
		})
	}
}

func TestSelfServe_AddSendAsAddress_ExternalAndOwn(t *testing.T) {
	env := newVerifyTestEnvFull(t)
	env.seedUser(t, "userA", "a@example.com")
	env.seedAddress(t, "a@example.com", "userA", "primary", true, true)

	for i := range 2 { // second call is the no-op for an address the caller already holds
		w := env.postAdd(t, "userA", "a@example.com", "me@external.example")
		if loc := w.Header().Get("Location"); !strings.HasPrefix(loc, "/settings/addresses?flash=") || strings.Contains(loc, "error=") {
			t.Fatalf("call %d: Location = %q", i, loc)
		}
	}
	env.assertNoRecord(t, "me@external.example")
	env.assertClaim(t, "userA", "me@external.example")

	// A live challenge survives a repeated add.
	env.requestVerificationHTTP(t, "userA", "a@example.com", "me@external.example")
	before := env.assertClaim(t, "userA", "me@external.example")
	env.postAdd(t, "userA", "a@example.com", "me@external.example")
	after := env.assertClaim(t, "userA", "me@external.example")
	if !bytes.Equal(before.CodeHash, after.CodeHash) || len(after.CodeHash) == 0 {
		t.Error("a repeated add overwrote the live challenge")
	}

	// An address the caller already holds verified gets no claim.
	env.postAdd(t, "userA", "a@example.com", "a@example.com")
	env.assertNoClaim(t, "userA", "a@example.com")
}

func TestSelfServe_AddSendAsAddress_NoOwnershipStores_FailsClosed(t *testing.T) {
	env := newVerifyTestEnvFull(t)
	env.handler.SetOwnershipStores(nil, nil, nil)
	w := env.postAdd(t, "userA", "a@example.com", "me@external.example")
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
	env.assertNoRecord(t, "me@external.example")
}

var _ store.RaftApplier = (*directApplierTags)(nil)

// US-003 at page level: once an account replaces A's unverified record, A's
// settings page no longer lists the address, even with a dangling challenge
// left by an old request-code that was never submitted.
func TestCreateUserReplacement_AddressVanishesFromReplacedOwnersPage(t *testing.T) {
	for _, withChallenge := range []bool{false, true} {
		t.Run(fmt.Sprintf("challenge=%v", withChallenge), func(t *testing.T) {
			env := newVerifyTestEnvFull(t)
			env.seedUser(t, "userA", "a@example.com")
			const addr = "squat@future.example"
			env.seedAddress(t, addr, "userA", "identity", false, false)
			if withChallenge {
				if err := env.verifyStore.Put("userA", addr, &bongov1.AddressVerification{
					CodeHash: []byte("x"), ExpiresAt: time.Now().Add(-time.Hour).Unix(),
				}); err != nil {
					t.Fatal(err)
				}
			}
			if body := env.getList(t, "userA", "a@example.com").Body.String(); !strings.Contains(body, addr) {
				t.Fatalf("precondition: page does not list %s", addr)
			}

			if _, err := auth.CreateUser(env.applier, env.meta, addr, "New", "secret123", false); err != nil {
				t.Fatalf("CreateUser: %v", err)
			}
			if body := env.getList(t, "userA", "a@example.com").Body.String(); strings.Contains(body, addr) {
				t.Error("A's settings page still lists the replaced address")
			}
			env.assertNoClaim(t, "userA", addr)
		})
	}
}
