package handlers

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/store"
)

// enable2FA gives userID a 2FA setup as the Security page leaves it, with
// the per-account app password switch on.
func enable2FA(t *testing.T, ps *store.PebbleStore, applier store.RaftApplier, userID string) *bongov1.UserProfile {
	t.Helper()
	p := loadTestProfile(t, ps, userID)
	p.TotpEnabled = true
	p.TotpSecret = []byte("0123456789abcdefghij")
	p.RecoveryCodes = []string{"$2a$hash-one", "$2a$hash-two"}
	p.TotpLastStep = 12345
	p.RequireAppPasswords = true
	saveTestProfile(t, applier, userID, p)
	return p
}

func loadTestProfile(t *testing.T, ps *store.PebbleStore, userID string) *bongov1.UserProfile {
	t.Helper()
	data, err := ps.Get([]byte("/user/" + userID + "/profile"))
	if err != nil {
		t.Fatalf("load profile: %v", err)
	}
	var p bongov1.UserProfile
	if err := proto.Unmarshal(data, &p); err != nil {
		t.Fatal(err)
	}
	return &p
}

func saveTestProfile(t *testing.T, applier store.RaftApplier, userID string, p *bongov1.UserProfile) {
	t.Helper()
	data, err := proto.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := applier.ApplyCommand(&bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: []byte("/user/" + userID + "/profile"), Value: data}, 0); err != nil {
		t.Fatal(err)
	}
}

// assertCleared checks the reset outcome against the profile before it.
func assertCleared(t *testing.T, before, after *bongov1.UserProfile) {
	t.Helper()
	if after.TotpEnabled || len(after.TotpSecret) != 0 || len(after.RecoveryCodes) != 0 {
		t.Errorf("2FA not cleared: enabled=%v secret=%d codes=%d", after.TotpEnabled, len(after.TotpSecret), len(after.RecoveryCodes))
	}
	if after.RequireAppPasswords {
		t.Error("require-app-passwords left on without 2FA")
	}
	if after.SessionGeneration != before.SessionGeneration+1 {
		t.Errorf("session generation %d, want %d", after.SessionGeneration, before.SessionGeneration+1)
	}
	if after.TotpLastStep != before.TotpLastStep {
		t.Errorf("TotpLastStep changed to %d", after.TotpLastStep)
	}
	if !bytes.Equal(after.PasswordHash, before.PasswordHash) || after.Email != before.Email || after.IsAdmin != before.IsAdmin {
		t.Error("reset changed fields outside two-factor")
	}
}

func (e *adminScopeTestEnv) reset2FA(t *testing.T, targetID string, asDomainAdmin bool) *httptest.ResponseRecorder {
	t.Helper()
	ctx := func(r *http.Request) *http.Request { return withGlobalAdmin(r, e.globalAdminID) }
	if asDomainAdmin {
		ctx = func(r *http.Request) *http.Request { return withDomainAdmin1(r, e.domainAdmin1ID) }
	}
	return postToHandler(t, e.h, e.h.ResetTwoFactor, http.MethodPost,
		"/admin/users/"+targetID+"/reset-2fa", nil, map[string]string{"userID": targetID}, ctx)
}

func TestAdminResetTwoFactor(t *testing.T) {
	e := newAdminScopeTestEnv(t)
	logs := &bytes.Buffer{}
	e.h.logger = slog.New(slog.NewTextHandler(logs, nil))
	before := enable2FA(t, e.ps, e.applier, e.d1UserID)

	w := e.reset2FA(t, e.d1UserID, false)
	if w.Code != http.StatusSeeOther || !strings.Contains(w.Header().Get("Location"), "flash=two-factor-reset") {
		t.Fatalf("status %d location %q", w.Code, w.Header().Get("Location"))
	}
	assertCleared(t, before, loadTestProfile(t, e.ps, e.d1UserID))

	out := logs.String()
	if !strings.Contains(out, "admin_id="+e.globalAdminID) || !strings.Contains(out, "user_id="+e.d1UserID) {
		t.Errorf("log lacks admin and user: %s", out)
	}
	if strings.Contains(out, "0123456789") || strings.Contains(out, "$2a$") {
		t.Errorf("log contains 2FA material: %s", out)
	}

	// A second reset finds nothing to do and changes nothing.
	again := loadTestProfile(t, e.ps, e.d1UserID)
	if w := e.reset2FA(t, e.d1UserID, false); w.Code != http.StatusConflict {
		t.Errorf("reset with 2FA off: status %d, want 409", w.Code)
	}
	if got := loadTestProfile(t, e.ps, e.d1UserID); got.SessionGeneration != again.SessionGeneration {
		t.Error("a refused reset bumped the session generation")
	}
}

func TestAdminResetTwoFactorScope(t *testing.T) {
	e := newAdminScopeTestEnv(t)
	out := map[string]string{"other domain": e.d2UserID, "global admin": e.globalAdminID}
	for _, id := range out {
		enable2FA(t, e.ps, e.applier, id)
	}
	for name, id := range out {
		if w := e.reset2FA(t, id, true); w.Code != http.StatusForbidden {
			t.Errorf("%s: status %d, want 403", name, w.Code)
		}
		if !loadTestProfile(t, e.ps, id).TotpEnabled {
			t.Errorf("%s: an out-of-scope reset turned 2FA off", name)
		}
	}
	// Control: the same domain admin may act in scope.
	before := enable2FA(t, e.ps, e.applier, e.d1UserID)
	if w := e.reset2FA(t, e.d1UserID, true); w.Code != http.StatusSeeOther {
		t.Fatalf("in scope: status %d", w.Code)
	}
	assertCleared(t, before, loadTestProfile(t, e.ps, e.d1UserID))
	// An administrator's own 2FA goes through the Security page's re-check.
	enable2FA(t, e.ps, e.applier, e.globalAdminID)
	if w := e.reset2FA(t, e.globalAdminID, false); w.Code != http.StatusForbidden {
		t.Errorf("own account: status %d, want 403", w.Code)
	}
	if !loadTestProfile(t, e.ps, e.globalAdminID).TotpEnabled {
		t.Error("an admin reset their own 2FA without re-authenticating")
	}
	if w := e.reset2FA(t, "no-such-user", false); w.Code != http.StatusNotFound {
		t.Errorf("unknown user: status %d, want 404", w.Code)
	}
}

func TestControlResetTwoFactor(t *testing.T) {
	e := newAdminScopeTestEnv(t)
	h := NewTwoFactorResetHandler(e.ps, e.applier, discardLogger())
	post := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.Reset(w, httptest.NewRequest(http.MethodPost, "/internal/user/reset-2fa", strings.NewReader(body)))
		return w
	}

	// A primary address that is not an account's own (an alias pointing at the
	// user) must not resolve: the command names an account by its address.
	if err := e.as.Put("alias1@"+testDomain1, &bongov1.AddressRecord{UserId: e.d1UserID, Domain: testDomain1, Kind: "alias"}); err != nil {
		t.Fatalf("add alias: %v", err)
	}
	before := enable2FA(t, e.ps, e.applier, e.d1UserID)
	for name, body := range map[string]string{
		"unknown": `{"email":"nobody@` + testDomain1 + `"}`,
		"alias":   `{"email":"alias1@` + testDomain1 + `"}`,
	} {
		if w := post(body); w.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", name, w.Code)
		}
	}
	if !loadTestProfile(t, e.ps, e.d1UserID).TotpEnabled {
		t.Fatal("a refused request turned 2FA off")
	}
	for name, body := range map[string]string{"empty": `{"email":""}`, "not json": `x`} {
		if w := post(body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, w.Code)
		}
	}

	// The primary address, in any case and with spaces, resolves.
	w := post(`{"email":"  USER1@` + strings.ToUpper(testDomain1) + ` "}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), e.d1UserID) {
		t.Fatalf("status %d body %q", w.Code, w.Body.String())
	}
	assertCleared(t, before, loadTestProfile(t, e.ps, e.d1UserID))

	if w := post(`{"email":"user1@` + testDomain1 + `"}`); w.Code != http.StatusConflict {
		t.Errorf("2FA already off: status %d, want 409", w.Code)
	}
}

func TestClearTwoFactorOff(t *testing.T) {
	p := &bongov1.UserProfile{SessionGeneration: 4, RequireAppPasswords: true}
	if clearTwoFactor(p) {
		t.Error("reported a reset for an account without 2FA")
	}
	if p.SessionGeneration != 4 || !p.RequireAppPasswords {
		t.Error("changed a profile without 2FA")
	}
}
