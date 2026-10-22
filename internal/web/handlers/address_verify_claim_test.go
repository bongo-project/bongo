package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/auth"
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
	rec, ok, _ := env.addresses.Get("me@external.example")
	if !ok || rec.UserId != "userA" || rec.Kind != "identity" || rec.Verified {
		t.Errorf("record = %+v ok=%v", rec, ok)
	}
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
