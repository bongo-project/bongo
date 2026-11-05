package handlers

import (
	"bytes"
	"encoding/base32"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/security"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const (
	devicesURL = "/settings/security/devices"
	onURL      = "/settings/security/require-app-passwords/on"
	offURL     = "/settings/security/require-app-passwords/off"
)

// failApplier makes profile writes fail on demand, under a working applier.
type failApplier struct {
	pebbleApplier
	fail bool
}

func (f *failApplier) ApplyCommand(cmd *bongov1.RaftCommand, d time.Duration) error {
	if f.fail {
		return errors.New("raft unavailable")
	}
	return f.pebbleApplier.ApplyCommand(cmd, d)
}

func (e *secEnv) post(t *testing.T, fn http.HandlerFunc, user, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	return e.do(t, fn, user, http.MethodPost, path, form, false)
}

func (e *secEnv) flashOf(w *httptest.ResponseRecorder) string {
	u, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		return ""
	}
	return strings.Join(u.Query()["flash"], ",")
}

// enrol stores user as a 2FA account and returns its recovery codes, one per
// re-authentication.
func (e *secEnv) enrol(t *testing.T, user string) []string {
	t.Helper()
	return putProfileCodes(t, e.ps, user, true)
}

func (e *secEnv) mutate(t *testing.T, user string, f func(p *bongov1.UserProfile)) {
	t.Helper()
	p := e.profile(t, user)
	f(p)
	raw, err := proto.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.ps.Set([]byte("/user/"+user+"/profile"), raw); err != nil {
		t.Fatal(err)
	}
}

func createForm(label, password, code string) url.Values {
	f := url.Values{"label": {label}, "password": {password}}
	if code != "" {
		f.Set("totp_code", code)
	}
	return f
}

// FR-16: every create route needs the account password.
func TestCreateNeedsAccountPassword(t *testing.T) {
	e := newSecEnv(t)
	own := plaintextIn(t, e.create(t, secUserA, "control").Body.String())

	routes := map[string]struct {
		fn   http.HandlerFunc
		path string
		back string
	}{
		"plain form": {e.h.CreateAppPassword, "/settings/security/app-passwords", "/settings/security"},
		"add device": {e.h.CreateDevice, devicesURL, devicesURL},
	}
	for name, rt := range routes {
		for pname, pw := range map[string]string{
			"wrong":                      "wrong",
			"empty":                      "",
			"one of their app passwords": own,
		} {
			t.Run(name+"/"+pname, func(t *testing.T) {
				before := e.count(t, secUserA)
				w := e.post(t, rt.fn, secUserA, rt.path, createForm("x", pw, ""))
				if w.Code != http.StatusSeeOther {
					t.Fatalf("status = %d, want 303", w.Code)
				}
				loc := w.Header().Get("Location")
				if !strings.HasPrefix(loc, rt.back+"?flash=security-invalid-password") {
					t.Errorf("Location = %q", loc)
				}
				if e.count(t, secUserA) != before || strings.Contains(w.Body.String(), "app-password-new") {
					t.Error("a wrong password created a credential")
				}
			})
		}
		// Control: the right password goes through the same route.
		before := e.count(t, secUserA)
		if w := e.post(t, rt.fn, secUserA, rt.path, createForm("ok "+name, secPassword, "")); w.Code != http.StatusOK || e.count(t, secUserA) != before+1 {
			t.Errorf("%s control: status %d", name, w.Code)
		}
	}
}

// Wrong passwords on a create count against the shared throttle, and a
// throttled client does not reach the credential store.
func TestCreateIsThrottled(t *testing.T) {
	e := newSecEnv(t)
	rl := middleware.NewLoginRateLimiter(3, time.Minute)
	e.h.SetRateLimiter(rl)
	for range 3 {
		e.create(t, secUserA, "x") // right password: must not count
	}
	if e.count(t, secUserA) != 3 {
		t.Fatalf("setup: %d credentials", e.count(t, secUserA))
	}
	for range 3 {
		e.post(t, e.h.CreateAppPassword, secUserA, "/settings/security/app-passwords", createForm("x", "wrong", ""))
	}
	w := e.create(t, secUserA, "blocked")
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("after 3 failures the right password got %d, want 429", w.Code)
	}
	if e.count(t, secUserA) != 3 {
		t.Error("a throttled create stored a credential")
	}
}

func TestCreateWith2FA(t *testing.T) {
	e := newSecEnv(t)
	codes := e.enrol(t, secUserA)
	post := func(label, pw, code string) *httptest.ResponseRecorder {
		return e.post(t, e.h.CreateAppPassword, secUserA, "/settings/security/app-passwords", createForm(label, pw, code))
	}

	// No code, wrong code: refused, nothing created, nothing spent.
	for name, code := range map[string]string{"missing": "", "wrong": "000000"} {
		w := post("x", secPassword, code)
		if w.Code != http.StatusSeeOther || !strings.Contains(w.Header().Get("Location"), "flash=security-invalid-auth-code") {
			t.Errorf("%s code: %d %q", name, w.Code, w.Header().Get("Location"))
		}
	}
	if e.count(t, secUserA) != 0 || len(e.profile(t, secUserA).RecoveryCodes) != len(codes) {
		t.Fatal("a refused create changed state")
	}

	// A bad label with a valid code leaves the code unspent.
	if w := post("", secPassword, codes[0]); w.Code != http.StatusBadRequest {
		t.Fatalf("bad label: %d", w.Code)
	}
	if len(e.profile(t, secUserA).RecoveryCodes) != len(codes) {
		t.Error("a bad label spent the recovery code")
	}

	// The valid create spends the code in the same request.
	if w := post("phone", secPassword, codes[0]); w.Code != http.StatusOK {
		t.Fatalf("good create: %d", w.Code)
	}
	if n := len(e.profile(t, secUserA).RecoveryCodes); n != len(codes)-1 {
		t.Errorf("recovery codes left = %d, want %d", n, len(codes)-1)
	}
	// Replaying it is refused (control: it worked a moment ago).
	if w := post("again", secPassword, codes[0]); w.Code != http.StatusSeeOther || e.count(t, secUserA) != 1 {
		t.Errorf("replayed code: %d, %d credentials", w.Code, e.count(t, secUserA))
	}
}

// If the factor cannot be recorded the credential is withdrawn and the user is
// not shown a password.
func TestCreateRevokesWhenSecondFactorNotRecorded(t *testing.T) {
	e := newSecEnv(t)
	codes := e.enrol(t, secUserA)
	fa := &failApplier{pebbleApplier: pebbleApplier{e.ps}, fail: true}
	e.h.applier = fa

	w := e.post(t, e.h.CreateDevice, secUserA, devicesURL, createForm("x", secPassword, codes[0]))
	if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "app-password-new") {
		t.Errorf("status %d, body reveals a password", w.Code)
	}
	if e.count(t, secUserA) != 0 {
		t.Error("the credential survived an unrecorded second factor")
	}
	// The code was never spent, so the user can retry with it.
	fa.fail = false
	if w := e.post(t, e.h.CreateDevice, secUserA, devicesURL, createForm("x", secPassword, codes[0])); w.Code != http.StatusOK {
		t.Errorf("retry with the same code: %d", w.Code)
	}
}

// FR-17 through both routes, which share one create path.
func TestCreateLimit(t *testing.T) {
	e := newSecEnv(t)
	for i := range 10 {
		route := e.h.CreateAppPassword
		path := "/settings/security/app-passwords"
		if i%2 == 1 {
			route, path = e.h.CreateDevice, devicesURL
		}
		if w := e.post(t, route, secUserA, path, createForm("d", secPassword, "")); w.Code != http.StatusOK {
			t.Fatalf("create %d: %d", i+1, w.Code)
		}
	}
	for name, rt := range map[string]struct {
		fn   http.HandlerFunc
		path string
	}{"plain": {e.h.CreateAppPassword, "/settings/security/app-passwords"}, "device": {e.h.CreateDevice, devicesURL}} {
		w := e.post(t, rt.fn, secUserA, rt.path, createForm("eleventh", secPassword, ""))
		if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "limit of 10 app passwords") {
			t.Errorf("%s: status %d", name, w.Code)
		}
		if strings.Contains(w.Body.String(), "app-password-new") {
			t.Errorf("%s: the refused create revealed a password", name)
		}
	}
	if e.count(t, secUserA) != 10 {
		t.Fatalf("%d credentials", e.count(t, secUserA))
	}
	// The limit is per user.
	if w := e.create(t, secUserB, "bob"); w.Code != http.StatusOK {
		t.Errorf("other user: %d", w.Code)
	}
	// A refused 2FA create leaves the code unspent.
	codes := e.enrol(t, secUserA)
	if w := e.post(t, e.h.CreateAppPassword, secUserA, "/settings/security/app-passwords", createForm("x", secPassword, codes[0])); w.Code != http.StatusConflict {
		t.Fatalf("2FA at the limit: %d", w.Code)
	}
	if len(e.profile(t, secUserA).RecoveryCodes) != len(codes) {
		t.Error("a create refused by the limit spent the recovery code")
	}
}

var copyBtn = regexp.MustCompile(`</span>\s*<button[^>]*data-copy-previous`)

func TestAddDeviceOther(t *testing.T) {
	e := newSecEnv(t)
	e.h.SetClientSettings(newTestAutoconfig().ClientSettings)

	page := e.do(t, e.h.AddDevice, secUserA, http.MethodGet, devicesURL, nil, false).Body.String()
	if !strings.Contains(page, `action="/settings/security/devices"`) || !strings.Contains(page, "Other") {
		t.Error("the page does not offer Other")
	}
	if strings.Contains(page, "Apple") {
		t.Error("the page offers a choice that is not built")
	}
	if strings.Contains(page, "Finish") {
		t.Error("the Finish step shows outside enrolment")
	}

	w := e.post(t, e.h.CreateDevice, secUserA, devicesURL, createForm("Thunderbird", secPassword, ""))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if !strings.Contains(w.Header().Get("Cache-Control"), "no-store") || w.Header().Get("Pragma") != "no-cache" {
		t.Errorf("reveal is cacheable: %q", w.Header().Get("Cache-Control"))
	}
	body := w.Body.String()
	if !strings.Contains(body, `hx-history="false"`) {
		t.Error("reveal lacks hx-history=false")
	}
	pw := plaintextIn(t, body)
	for _, want := range []string{
		secUserA + "@example.com", "mail.example.com", "993", "465", "587", "SSL", "STARTTLS", "/caldav/", "/carddav/", "443",
		"IMAP", "SMTP", "CalDAV", "CardDAV",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("reveal lacks %q", want)
		}
	}
	// A copy button sits directly after every value, and every value has one.
	if n, m := len(copyBtn.FindAllString(body, -1)), strings.Count(body, `class="manual-key"`); n != m || n < 12 {
		t.Errorf("copy buttons %d, values %d", n, m)
	}

	// Shown once.
	again := e.do(t, e.h.AddDevice, secUserA, http.MethodGet, devicesURL, nil, false).Body.String()
	if strings.Contains(again, pw) || strings.Contains(again, `hx-history="false"`) {
		t.Error("the password is on the next page load")
	}

	// A kind this build does not have is refused, not treated as Other.
	f := createForm("x", secPassword, "")
	f.Set("kind", "apple")
	if w := e.post(t, e.h.CreateDevice, secUserA, devicesURL, f); w.Code != http.StatusBadRequest || e.count(t, secUserA) != 1 {
		t.Errorf("unknown kind: %d, %d credentials", w.Code, e.count(t, secUserA))
	}
}

// The setup step offers Finish only once there is a credential, and sends a
// wrong password back to itself.
func TestAddDeviceSetupStep(t *testing.T) {
	e := newSecEnv(t)
	e.enrol(t, secUserA)
	get := func() string {
		return e.do(t, e.h.AddDevice, secUserA, http.MethodGet, devicesSetupPath, nil, false).Body.String()
	}
	page := get()
	if strings.Contains(page, onURL) || !strings.Contains(page, "Skip for now") || !strings.Contains(page, "Create at least one app password") {
		t.Error("setup page with no credential")
	}

	f := createForm("x", "wrong", "123456")
	f.Set("setup", "1")
	w := e.post(t, e.h.CreateDevice, secUserA, devicesURL, f)
	if loc := w.Header().Get("Location"); !strings.HasPrefix(loc, devicesSetupPath+"&flash=security-invalid-password") {
		t.Errorf("wrong password returned to %q", loc)
	}

	if _, _, err := e.apps.Create(secUserA, "phone"); err != nil {
		t.Fatal(err)
	}
	page = get()
	if !strings.Contains(page, onURL) || !strings.Contains(page, "Finish") || !strings.Contains(page, `name="setup" value="1"`) {
		t.Error("setup page with a credential lacks Finish")
	}
}

// In setup mode the reveal must still offer another device: each client needs
// its own credential before Finish breaks the ones that are not replaced.
func TestSetupRevealOffersAnotherDevice(t *testing.T) {
	e := newSecEnv(t)
	codes := e.enrol(t, secUserA)
	f := createForm("phone", secPassword, codes[0])
	f.Set("setup", "1")
	w := e.post(t, e.h.CreateDevice, secUserA, devicesURL, f)
	body := w.Body.String()
	if !strings.Contains(body, "app-password-new") {
		t.Fatalf("no reveal: %d", w.Code)
	}
	if !strings.Contains(body, `href="`+devicesSetupPath+`"`) || !strings.Contains(body, "Add another device") {
		t.Error("setup reveal has no link back to Add a device")
	}
	if !strings.Contains(body, onURL) {
		t.Error("setup reveal lost Finish after a good create")
	}
}

func TestDeviceRoutesRefuseAHeldSession(t *testing.T) {
	e := newSecEnv(t)
	if err := e.ps.Set([]byte("/system/settings/require_2fa"), []byte("1")); err != nil {
		t.Fatal(err)
	}
	held := func(fn http.HandlerFunc, method, path string, form url.Values) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r = withUserContext(r, secUserA, secUserA+"@example.com")
		w := httptest.NewRecorder()
		middleware.Require2FA(psKeys{e.ps})(fn).ServeHTTP(w, r)
		return w
	}
	for name, w := range map[string]*httptest.ResponseRecorder{
		"get devices":  held(e.h.AddDevice, http.MethodGet, devicesURL, nil),
		"post devices": held(e.h.CreateDevice, http.MethodPost, devicesURL, createForm("x", secPassword, "")),
		"turn on":      held(e.h.RequireAppPasswordsOn, http.MethodPost, onURL, nil),
	} {
		// Exactly the Security page: no failure flash, so no password was
		// compared and no factor or throttle budget was spent.
		if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/settings/security" {
			t.Errorf("%s: %d %q", name, w.Code, w.Header().Get("Location"))
		}
	}
	if e.count(t, secUserA) != 0 {
		t.Error("a held session created a credential")
	}
}

func TestRequireAppPasswordsOn(t *testing.T) {
	e := newSecEnv(t)
	on := func() *httptest.ResponseRecorder { return e.post(t, e.h.RequireAppPasswordsOn, secUserA, onURL, nil) }

	// Without 2FA it is refused, even with credentials.
	e.create(t, secUserA, "phone")
	if w := on(); !strings.Contains(e.flashOf(w), "require-app-passwords-no-2fa") || e.profile(t, secUserA).RequireAppPasswords {
		t.Errorf("no 2FA: %q", e.flashOf(w))
	}

	// With 2FA but no credential it is refused.
	e.enrol(t, secUserA)
	recs, _ := e.recs.List(secUserA)
	if err := e.recs.Delete(secUserA, recs[0].Id); err != nil {
		t.Fatal(err)
	}
	if w := on(); !strings.Contains(e.flashOf(w), "require-app-passwords-none") || e.profile(t, secUserA).RequireAppPasswords {
		t.Errorf("no credential: %q", e.flashOf(w))
	}

	// With both it turns on, and again is a no-op that still succeeds.
	if _, _, err := e.apps.Create(secUserA, "phone"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		w := on()
		if e.flashOf(w) != "require-app-passwords-on" || !e.profile(t, secUserA).RequireAppPasswords {
			t.Fatalf("turn on: %q", e.flashOf(w))
		}
	}
	// The write failing is reported, not shown as success.
	e.mutate(t, secUserA, func(p *bongov1.UserProfile) { p.RequireAppPasswords = false })
	e.h.applier = &failApplier{pebbleApplier: pebbleApplier{e.ps}, fail: true}
	if w := on(); e.flashOf(w) != "require-app-passwords-failed" || e.profile(t, secUserA).RequireAppPasswords {
		t.Errorf("save failure: %q", e.flashOf(w))
	}
}

func TestRequireAppPasswordsOffNeedsReauthentication(t *testing.T) {
	e := newSecEnv(t)
	codes := e.enrol(t, secUserA)
	if _, _, err := e.apps.Create(secUserA, "phone"); err != nil {
		t.Fatal(err)
	}
	e.mutate(t, secUserA, func(p *bongov1.UserProfile) { p.RequireAppPasswords = true })
	off := func(pw, code string) *httptest.ResponseRecorder {
		f := url.Values{"password": {pw}}
		if code != "" {
			f.Set("totp_code", code)
		}
		return e.post(t, e.h.RequireAppPasswordsOff, secUserA, offURL, f)
	}

	for name, c := range map[string][2]string{
		"wrong password": {"wrong", codes[0]},
		"no code":        {secPassword, ""},
		"wrong code":     {secPassword, "000000"},
	} {
		if w := off(c[0], c[1]); w.Code != http.StatusSeeOther || !e.profile(t, secUserA).RequireAppPasswords {
			t.Errorf("%s turned the switch off (%d)", name, w.Code)
		}
	}
	if len(e.profile(t, secUserA).RecoveryCodes) != len(codes) {
		t.Fatal("a refused turn-off spent a code")
	}
	w := off(secPassword, codes[0])
	p := e.profile(t, secUserA)
	if e.flashOf(w) != "require-app-passwords-off" || p.RequireAppPasswords {
		t.Fatalf("turn off: %q", e.flashOf(w))
	}
	if len(p.RecoveryCodes) != len(codes)-1 || !p.TotpEnabled {
		t.Errorf("the code and the change did not land together: %d codes, totp=%v", len(p.RecoveryCodes), p.TotpEnabled)
	}
}

// An app password cannot reach the switch: the web accepts a session cookie
// only, so HTTP Basic with a real app password is sent to login.
func TestAppPasswordCannotReachTheSwitch(t *testing.T) {
	e := newSecEnv(t)
	e.enrol(t, secUserA)
	pw, _, err := e.apps.Create(secUserA, "phone")
	if err != nil {
		t.Fatal(err)
	}
	secret := []byte("secret")
	for name, fn := range map[string]http.HandlerFunc{"on": e.h.RequireAppPasswordsOn, "off": e.h.RequireAppPasswordsOff} {
		called := false
		h := middleware.RequireAuth(secret, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; fn(w, r) }))
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(url.Values{"password": {pw}}.Encode()))
		r.SetBasicAuth(secUserA+"@example.com", pw)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if called || w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login" {
			t.Errorf("%s: handler reached=%v, %d %q", name, called, w.Code, w.Header().Get("Location"))
		}
	}
	// And as the re-authentication password it is just a wrong password.
	e.mutate(t, secUserA, func(p *bongov1.UserProfile) { p.RequireAppPasswords = true })
	f := url.Values{"password": {pw}, "totp_code": {e.profile(t, secUserA).RecoveryCodes[0]}}
	e.post(t, e.h.RequireAppPasswordsOff, secUserA, offURL, f)
	if !e.profile(t, secUserA).RequireAppPasswords {
		t.Error("an app password turned the switch off")
	}
}

// Removing 2FA clears the switch in the same write, says so, and keeps the app
// passwords. The account is never left unable to turn the switch off.
func TestDisableTOTPClearsTheSwitch(t *testing.T) {
	for _, switchOn := range []bool{true, false} {
		e := newSecEnv(t)
		codes := e.enrol(t, secUserA)
		if _, _, err := e.apps.Create(secUserA, "phone"); err != nil {
			t.Fatal(err)
		}
		e.mutate(t, secUserA, func(p *bongov1.UserProfile) { p.RequireAppPasswords = switchOn })
		w := e.post(t, e.h.DisableTOTP, secUserA, "/settings/security/totp/disable", url.Values{"password": {secPassword}, "totp_code": {codes[1]}})
		p := e.profile(t, secUserA)
		if p.TotpEnabled || p.RequireAppPasswords {
			t.Errorf("switchOn=%v: totp=%v switch=%v after disabling 2FA", switchOn, p.TotpEnabled, p.RequireAppPasswords)
		}
		flash := e.flashOf(w)
		if got := strings.Contains(flash, "security-2fa-disabled-mail-again"); got != switchOn {
			t.Errorf("switchOn=%v: flash %q", switchOn, flash)
		}
		if !strings.Contains(flash, "security-2fa-disabled") || e.count(t, secUserA) != 1 {
			t.Errorf("switchOn=%v: flash %q, %d credentials", switchOn, flash, e.count(t, secUserA))
		}
	}
}

// The notice, the switch state and the warning before disabling 2FA.
func TestSecurityPageShowsTheSwitch(t *testing.T) {
	e := newSecEnv(t)
	page := func() string { return e.get(t, secUserA).Body.String() }

	if p := page(); strings.Contains(p, "require-app-passwords") || strings.Contains(p, "Mail clients") {
		t.Error("the switch is offered to an account without 2FA")
	}
	if !strings.Contains(page(), `name="revoke_app_passwords" value="1" checked`) {
		t.Error("Also revoke my app passwords is not ticked by default")
	}

	e.enrol(t, secUserA)
	p := page()
	if !strings.Contains(p, "still works in mail clients") || strings.Contains(p, onURL) || !strings.Contains(p, "Add a device first") {
		t.Error("2FA with no credential: notice should point at Add a device, with no turn-on form")
	}
	if _, _, err := e.apps.Create(secUserA, "phone"); err != nil {
		t.Fatal(err)
	}
	if p := page(); !strings.Contains(p, onURL) || strings.Contains(p, offURL) || strings.Contains(p, "refused in them") {
		t.Error("2FA with a credential and the switch off: want the turn-on form only")
	}
	e.mutate(t, secUserA, func(p *bongov1.UserProfile) { p.RequireAppPasswords = true })
	p = page()
	if !strings.Contains(p, offURL) || strings.Contains(p, onURL) || !strings.Contains(p, "work in mail clients again") {
		t.Error("switch on: want the turn-off form and the warning on the 2FA disable form")
	}

	// A flag with no 2FA (an earlier binary cleared 2FA) is not shown as on.
	e.mutate(t, secUserA, func(p *bongov1.UserProfile) { p.TotpEnabled = false })
	if p := page(); strings.Contains(p, offURL) || strings.Contains(p, "refused in them") {
		t.Error("a stale flag is shown as active")
	}
}

// totpFor returns a current code for the secret.
func totpFor(t *testing.T, secret []byte) string {
	t.Helper()
	code, err := totp.GenerateCode(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return code
}

// live is the stateFn the agent uses: the epoch of the stored profile.
func (e *secEnv) live(t *testing.T, user string) func(string) middleware.LiveSessionState {
	return func(string) middleware.LiveSessionState {
		p := e.profile(t, user)
		return middleware.LiveSessionState{Found: true, Epoch: security.SessionEpoch(p.PasswordHash, p.TotpSecret, p.TotpEnabled, p.SessionGeneration)}
	}
}

// Enrolment and removal change the session epoch. Without a fresh cookie the
// next click signs the calling device out, so the enrolment could never reach
// Add a device. Run through RequireAuth with a live epoch.
func TestTOTPChangesKeepTheCallingDeviceSignedIn(t *testing.T) {
	secret := []byte("secret")
	reach := func(e *secEnv, c *http.Cookie) bool {
		called := false
		h := middleware.RequireAuth(secret, e.live(t, secUserA))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
		r := httptest.NewRequest(http.MethodGet, "/settings/security", nil)
		r.AddCookie(c)
		h.ServeHTTP(httptest.NewRecorder(), r)
		return called
	}
	session := func(e *secEnv) *http.Cookie {
		p := e.profile(t, secUserA)
		return middleware.CreateSessionCookie(secret, secUserA, secUserA+"@example.com", false, false, false,
			security.SessionEpoch(p.PasswordHash, p.TotpSecret, p.TotpEnabled, p.SessionGeneration))
	}
	cookieOf := func(w *httptest.ResponseRecorder) *http.Cookie {
		for _, c := range w.Result().Cookies() {
			if c.Name == "bongo_session" {
				return c
			}
		}
		return nil
	}
	newEnv := func() *secEnv {
		e := newSecEnv(t)
		e.h = NewSecurityHandler(psKeys{e.ps}, pebbleApplier{e.ps}, nil, secret)
		e.h.SetAppPasswords(e.apps)
		return e
	}

	t.Run("confirm", func(t *testing.T) {
		e := newEnv()
		e.mutate(t, secUserA, func(p *bongov1.UserProfile) { p.RequireAppPasswords = true }) // stale flag
		old := session(e)
		if !reach(e, old) {
			t.Fatal("control: the session does not work before enrolment")
		}

		rec := httptest.NewRecorder()
		const b32 = "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"
		e.h.setTOTPPendingCookie(rec, b32)
		raw, _ := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(b32)
		r := httptest.NewRequest(http.MethodPost, "/settings/security/totp/confirm", strings.NewReader(url.Values{"code": {totpFor(t, raw)}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(rec.Result().Cookies()[0])
		r.AddCookie(old)
		r = withUserContext(r, secUserA, secUserA+"@example.com")
		w := httptest.NewRecorder()
		e.h.ConfirmTOTP(w, r)

		p := e.profile(t, secUserA)
		if !p.TotpEnabled || p.RequireAppPasswords {
			t.Fatalf("totp=%v, stale switch=%v", p.TotpEnabled, p.RequireAppPasswords)
		}
		if reach(e, old) {
			t.Error("the old cookie still works, so the epoch did not change and this test proves nothing")
		}
		fresh := cookieOf(w)
		if fresh == nil || !reach(e, fresh) {
			t.Fatal("the enrolling device is signed out by its own enrolment")
		}
		if !strings.Contains(w.Body.String(), devicesSetupPath) {
			t.Error("the recovery codes page does not continue to Add a device")
		}
	})

	t.Run("disable", func(t *testing.T) {
		e := newEnv()
		codes := e.enrol(t, secUserA)
		old := session(e)
		r := httptest.NewRequest(http.MethodPost, "/settings/security/totp/disable", strings.NewReader(url.Values{"password": {secPassword}, "totp_code": {codes[0]}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(old)
		r = withUserContext(r, secUserA, secUserA+"@example.com")
		w := httptest.NewRecorder()
		e.h.DisableTOTP(w, r)
		if reach(e, old) {
			t.Error("the old cookie still works")
		}
		if fresh := cookieOf(w); fresh == nil || !reach(e, fresh) {
			t.Error("disabling 2FA signs the device out before it can see the result")
		}
	})
}

func TestChangePasswordRevokesAppPasswordsOnRequest(t *testing.T) {
	change := func(e *secEnv, revoke string) *httptest.ResponseRecorder {
		f := url.Values{"current_password": {secPassword}, "new_password": {"a-new-password"}, "confirm_password": {"a-new-password"}}
		if revoke != "" {
			f.Set("revoke_app_passwords", revoke)
		}
		return e.post(t, e.h.ChangePassword, secUserA, "/settings/security/password", f)
	}
	seed := func(e *secEnv) {
		for range 3 {
			if _, _, err := e.apps.Create(secUserA, "d"); err != nil {
				t.Fatal(err)
			}
		}
	}

	for _, tt := range []struct {
		name, field string
		wantLeft    int
	}{
		{"ticked", "1", 0},
		{"unticked (field absent)", "", 3},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := newSecEnv(t)
			seed(e)
			w := change(e, tt.field)
			if e.flashOf(w) != "security-password-changed" {
				t.Errorf("flash = %q", e.flashOf(w))
			}
			if n := e.count(t, secUserA); n != tt.wantLeft {
				t.Errorf("%d app passwords left, want %d", n, tt.wantLeft)
			}
			if e.profile(t, secUserA).SessionGeneration != 1 {
				t.Error("the password did not change")
			}
		})
	}

	t.Run("nil service", func(t *testing.T) {
		e := newSecEnv(t)
		e.h.SetAppPasswords(nil)
		if w := change(e, "1"); e.flashOf(w) != "security-password-changed" {
			t.Errorf("flash = %q", e.flashOf(w))
		}
	})

	t.Run("switch on says to add the devices again", func(t *testing.T) {
		e := newSecEnv(t)
		codes := e.enrol(t, secUserA)
		seed(e)
		e.mutate(t, secUserA, func(p *bongov1.UserProfile) { p.RequireAppPasswords = true })
		f := url.Values{"current_password": {secPassword}, "new_password": {"a-new-password"}, "confirm_password": {"a-new-password"}, "revoke_app_passwords": {"1"}, "totp_code": {codes[0]}}
		w := e.post(t, e.h.ChangePassword, secUserA, "/settings/security/password", f)
		if e.flashOf(w) != "security-app-passwords-readd" {
			t.Errorf("flash = %q", e.flashOf(w))
		}
	})

	t.Run("revoke fails after the password changed", func(t *testing.T) {
		e := newSecEnv(t)
		seed(e)
		e.flaky.failDelete = true
		var logs bytes.Buffer
		old := slog.Default()
		slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
		t.Cleanup(func() { slog.SetDefault(old) })
		w := change(e, "1")
		if e.flashOf(w) != "security-app-passwords-revoke-incomplete" {
			t.Fatalf("flash = %q: the user is shown plain success", e.flashOf(w))
		}
		if e.profile(t, secUserA).SessionGeneration != 1 {
			t.Error("the password change was lost")
		}
		if l := logs.String(); !strings.Contains(l, "password changed but app passwords were not all revoked") || !strings.Contains(l, "revoked=0") {
			t.Errorf("the failure is not logged with the count: %s", l)
		}
		// The banner is an error, not green, and shows beside the policy banner.
		if err := e.ps.Set([]byte("/system/settings/require_2fa"), []byte("1")); err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodGet, w.Header().Get("Location"), nil)
		r = withUserContext(r, secUserA, secUserA+"@example.com")
		pw := httptest.NewRecorder()
		middleware.Require2FA(psKeys{e.ps})(http.HandlerFunc(e.h.SecuritySettings)).ServeHTTP(pw, r)
		body := pw.Body.String()
		if !strings.Contains(body, "flash-error") || !strings.Contains(body, "not every app password could be revoked") {
			t.Error("the failure banner is missing or not an error banner")
		}
		if !strings.Contains(body, "requires two-factor") {
			t.Error("the policy banner replaced the flash instead of sitting beside it")
		}
	})
}

// The switch survives every Security write that is not meant to clear it.
func TestSwitchSurvivesProfileWrites(t *testing.T) {
	writers := map[string]func(e *secEnv, codes []string){
		"change password": func(e *secEnv, codes []string) {
			e.post(t, e.h.ChangePassword, secUserA, "/settings/security/password", url.Values{
				"current_password": {secPassword}, "new_password": {"a-new-password"}, "confirm_password": {"a-new-password"}, "totp_code": {codes[0]},
			})
		},
		"create credential": func(e *secEnv, codes []string) {
			e.post(t, e.h.CreateAppPassword, secUserA, "/settings/security/app-passwords", createForm("x", secPassword, codes[0]))
		},
		"create via add device": func(e *secEnv, codes []string) {
			e.post(t, e.h.CreateDevice, secUserA, devicesURL, createForm("x", secPassword, codes[0]))
		},
		"turn on again": func(e *secEnv, _ []string) {
			e.post(t, e.h.RequireAppPasswordsOn, secUserA, onURL, nil)
		},
	}
	for name, write := range writers {
		t.Run(name, func(t *testing.T) {
			e := newSecEnv(t)
			codes := e.enrol(t, secUserA)
			if _, _, err := e.apps.Create(secUserA, "phone"); err != nil {
				t.Fatal(err)
			}
			e.mutate(t, secUserA, func(p *bongov1.UserProfile) { p.RequireAppPasswords = true })
			before := e.profile(t, secUserA)
			write(e, codes)
			after := e.profile(t, secUserA)
			if !after.RequireAppPasswords {
				t.Error("the write dropped the switch")
			}
			switch name {
			case "turn on again":
			case "change password":
				if bytes.Equal(after.PasswordHash, before.PasswordHash) {
					t.Error("the password did not change, so the check proves nothing")
				}
			default:
				if after.TotpLastStep == before.TotpLastStep && len(after.RecoveryCodes) == len(before.RecoveryCodes) {
					t.Error("the writer did not run, so the check proves nothing")
				}
			}
		})
	}
}

func TestFlashLevelWarnsWhenDevicesMustBeAddedAgain(t *testing.T) {
	tests := []struct {
		name string
		key  flashKey
		want string
	}{
		{"plain success", flashSecurityPasswordChanged, "success"},
		{"clients must be added again", flashSecurityAppPasswordsReadd, "warning"},
		{"failure", flashSecurityRevokeIncomplete, "error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, withFlash(securityPath, tt.key), nil)
			if got := flashLevel(r); got != tt.want {
				t.Errorf("flashLevel = %q, want %q", got, tt.want)
			}
		})
	}
}
