package handlers

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/bcrypt"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// psKeys lets a PebbleStore stand in for the Agent as a profile source.
type psKeys struct{ ps *store.PebbleStore }

func (k psKeys) GetKey(key []byte) ([]byte, error) { return k.ps.Get(key) }

// flakyRecords injects persistence failures under a real record store.
type flakyRecords struct {
	auth.AppPasswordRecords
	failPut, failList, failDelete bool
	// failListOn fails only the nth List call (1-based), so a test can let
	// Create's own count succeed and break the page that follows it.
	failListOn, listCalls int
	// mu guards listCalls, which concurrent requests bump.
	mu sync.Mutex
}

var errInjected = errors.New("injected failure")

func (f *flakyRecords) Put(u string, r *bongov1.AppPassword) error {
	if f.failPut {
		return errInjected
	}
	return f.AppPasswordRecords.Put(u, r)
}

func (f *flakyRecords) List(u string) ([]*bongov1.AppPassword, error) {
	f.mu.Lock()
	f.listCalls++
	n := f.listCalls
	f.mu.Unlock()
	if f.failList || (f.failListOn != 0 && n == f.failListOn) {
		return nil, errInjected
	}
	return f.AppPasswordRecords.List(u)
}

func (f *flakyRecords) Delete(u, id string) error {
	if f.failDelete {
		return errInjected
	}
	return f.AppPasswordRecords.Delete(u, id)
}

type secEnv struct {
	h     *SecurityHandler
	apps  *auth.AppPasswords
	recs  *store.AppPasswordStore
	flaky *flakyRecords
	ps    *store.PebbleStore
	logs  *bytes.Buffer
}

const (
	secUserA = "u-alice"
	secUserB = "u-bob"
)

func newSecEnv(t *testing.T) *secEnv {
	t.Helper()
	ps, err := store.NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	recs := store.NewAppPasswordStore(ps, pebbleApplier{ps})
	flaky := &flakyRecords{AppPasswordRecords: recs}
	logs := &bytes.Buffer{}
	apps := auth.NewAppPasswords(flaky, slog.New(slog.NewTextHandler(logs, nil)))
	h := NewSecurityHandler(psKeys{ps}, pebbleApplier{ps}, nil, []byte("secret"))
	h.SetAppPasswords(apps)
	putProfile(t, ps, secUserA, false)
	putProfile(t, ps, secUserB, false)
	return &secEnv{h: h, apps: apps, recs: recs, flaky: flaky, ps: ps, logs: logs}
}

// secPassword is every test account's password.
const secPassword = "right"

var secHash = func() []byte {
	h, err := bcrypt.GenerateFromPassword([]byte(secPassword), bcrypt.MinCost)
	if err != nil {
		panic(err)
	}
	return h
}()

// putProfile stores an account whose password is secPassword. With totp it is
// enrolled and holds recovery codes, which putProfileCodes returns.
func putProfile(t *testing.T, ps *store.PebbleStore, id string, totp bool) {
	t.Helper()
	putProfileCodes(t, ps, id, totp)
}

func putProfileCodes(t *testing.T, ps *store.PebbleStore, id string, totp bool) []string {
	t.Helper()
	p := &bongov1.UserProfile{Email: id + "@example.com", PasswordHash: secHash, TotpEnabled: totp}
	var plain []string
	if totp {
		var hashed []string
		var err error
		plain, hashed, err = auth.GenerateRecoveryCodes(12)
		if err != nil {
			t.Fatal(err)
		}
		p.TotpSecret, p.RecoveryCodes = reauthTOTPSecret, hashed
	}
	raw, err := proto.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := ps.Set([]byte("/user/"+id+"/profile"), raw); err != nil {
		t.Fatal(err)
	}
	return plain
}

func (e *secEnv) profile(t *testing.T, id string) *bongov1.UserProfile {
	t.Helper()
	raw, err := e.ps.Get([]byte("/user/" + id + "/profile"))
	if err != nil {
		t.Fatal(err)
	}
	var p bongov1.UserProfile
	if err := proto.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	return &p
}

func (e *secEnv) do(t *testing.T, fn http.HandlerFunc, user, method, path string, form url.Values, hx bool) *httptest.ResponseRecorder {
	t.Helper()
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	r := httptest.NewRequest(method, path, body)
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if hx {
		r.Header.Set("HX-Request", "true")
	}
	r = withUserContext(r, user, user+"@example.com")
	w := httptest.NewRecorder()
	fn(w, r)
	return w
}

func (e *secEnv) create(t *testing.T, user, label string) *httptest.ResponseRecorder {
	return e.do(t, e.h.CreateAppPassword, user, http.MethodPost, "/settings/security/app-passwords", url.Values{"label": {label}, "password": {secPassword}}, false)
}

func (e *secEnv) revoke(t *testing.T, user, id string, hx bool) *httptest.ResponseRecorder {
	return e.do(t, e.h.RevokeAppPassword, user, http.MethodPost, "/settings/security/app-passwords/revoke", url.Values{"id": {id}}, hx)
}

func (e *secEnv) get(t *testing.T, user string) *httptest.ResponseRecorder {
	return e.do(t, e.h.SecuritySettings, user, http.MethodGet, "/settings/security", nil, false)
}

func (e *secEnv) count(t *testing.T, user string) int {
	t.Helper()
	l, err := e.recs.List(user)
	if err != nil {
		t.Fatal(err)
	}
	return len(l)
}

// plaintextIn pulls the one-time password out of a create response.
func plaintextIn(t *testing.T, body string) string {
	t.Helper()
	const marker = `id="app-password-new"><span>`
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("no reveal in body")
	}
	rest := body[i+len(marker):]
	return rest[:strings.Index(rest, "</span>")]
}

func TestCreateAppPasswordShowsPlaintextOnce(t *testing.T) {
	e := newSecEnv(t)
	w := e.create(t, secUserA, "iPhone Mail")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control = %q", cc)
	}
	if w.Header().Get("Pragma") != "no-cache" || w.Header().Get("Location") != "" {
		t.Errorf("Pragma/Location = %q/%q", w.Header().Get("Pragma"), w.Header().Get("Location"))
	}
	body := w.Body.String()
	pw := plaintextIn(t, body)
	if _, _, ok := auth.ParseAppPassword(pw); !ok {
		t.Fatalf("revealed value %q is not an app password", pw)
	}
	if !strings.Contains(body, `hx-history="false"`) {
		t.Error("create response lacks the hx-history marker")
	}

	// Not recoverable: the next page load, the log and the stored record.
	g := e.get(t, secUserA)
	gb := g.Body.String()
	if strings.Contains(gb, pw) || strings.Contains(gb, strings.ReplaceAll(pw, "-", "")) {
		t.Error("GET after create contains the plaintext")
	}
	if strings.Contains(gb, `hx-history="false"`) {
		t.Error("normal page carries the hx-history marker")
	}
	for _, want := range []string{"iPhone Mail", pw[:6] + "-******-******-******", "Never"} {
		if !strings.Contains(gb, want) {
			t.Errorf("list lacks %q", want)
		}
	}
	if strings.Contains(e.logs.String(), pw[7:]) {
		t.Error("secret in logs")
	}
	recs, _ := e.recs.List(secUserA)
	if len(recs) != 1 || bytes.Contains(recs[0].PasswordHash, []byte(pw)) {
		t.Errorf("records = %v", recs)
	}
}

func TestCreateAppPasswordRejectsBadLabel(t *testing.T) {
	e := newSecEnv(t)
	// Control: a good label is accepted, so the refusals below are the
	// label's doing.
	if w := e.create(t, secUserA, "ok"); w.Code != http.StatusOK {
		t.Fatalf("control status = %d", w.Code)
	}
	for name, label := range map[string]string{
		"empty": "", "spaces": "   ", "control": "a\x00b", "newline": "a\nb", "long": strings.Repeat("x", 65),
	} {
		t.Run(name, func(t *testing.T) {
			w := e.create(t, secUserA, label)
			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", w.Code)
			}
			if strings.Contains(w.Body.String(), "app-password-new") {
				t.Error("rejected create revealed a password")
			}
			if !strings.Contains(w.Body.String(), "Enter a label of up to 64 characters") {
				t.Error("rejected create shows no error banner")
			}
		})
	}
	if n := e.count(t, secUserA); n != 1 {
		t.Errorf("%d credentials, want only the control's", n)
	}
}

func TestCreateAppPasswordFailures(t *testing.T) {
	e := newSecEnv(t)
	e.flaky.failPut = true
	if w := e.create(t, secUserA, "x"); w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "app-password-new") {
		t.Errorf("put failure: status %d", w.Code)
	}
	e.flaky.failPut = false

	// Create counts the existing credentials first; if it cannot, it refuses
	// rather than ignore the limit.
	e.flaky.failList = true
	if w := e.create(t, secUserA, "x"); w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "app-password-new") {
		t.Errorf("list failure in Create: status %d", w.Code)
	}
	e.flaky.failList = false
	if e.count(t, secUserA) != 0 {
		t.Fatal("refused create stored a credential")
	}

	// The list failing on the page after a good create must not hide the only
	// copy.
	e.flaky.listCalls, e.flaky.failListOn = 0, 2
	w := e.create(t, secUserA, "x")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	plaintextIn(t, w.Body.String())
	e.flaky.failListOn = 0
	e.flaky.failList = true

	// A failing list on GET is an error state, not "no app passwords".
	g := e.get(t, secUserA)
	if !strings.Contains(g.Body.String(), "could not be loaded") || strings.Contains(g.Body.String(), "You have no app passwords") {
		t.Errorf("list failure rendered as empty: %d", g.Code)
	}
}

func TestRevokeAppPasswordNotAnOracle(t *testing.T) {
	e := newSecEnv(t)
	mk := func(label string) string {
		pw := plaintextIn(t, e.create(t, secUserA, label).Body.String())
		return pw[:6]
	}
	realID := mk("one")
	keep := mk("two")

	// Control: the real id removes exactly one credential.
	first := e.revoke(t, secUserA, realID, false)
	if e.count(t, secUserA) != 1 {
		t.Fatalf("control: real revoke left %d credentials", e.count(t, secUserA))
	}
	for _, hx := range []bool{false, true} {
		ref := e.revoke(t, secUserA, mk("tmp"), hx)
		if hx && ref.Code != http.StatusOK || !hx && ref.Code != http.StatusSeeOther {
			t.Fatalf("hx=%v: success status %d", hx, ref.Code)
		}
		for name, id := range map[string]string{
			"repeat": realID, "unknown": "abcdef", "empty": "", "upper": "ABCDEF",
			"traversal": "../x", "long": strings.Repeat("a", 300),
		} {
			w := e.revoke(t, secUserA, id, hx)
			if w.Code != ref.Code || w.Header().Get("Location") != ref.Header().Get("Location") ||
				w.Header().Get("HX-Redirect") != ref.Header().Get("HX-Redirect") || w.Body.String() != ref.Body.String() {
				t.Errorf("hx=%v %s: response differs from a real revoke: %d %q", hx, name, w.Code, w.Header())
			}
		}
	}
	if first.Code != http.StatusSeeOther {
		t.Errorf("plain revoke status = %d", first.Code)
	}
	if e.count(t, secUserA) != 1 {
		t.Errorf("unrelated credential count = %d, want 1 (%s kept)", e.count(t, secUserA), keep)
	}
}

func TestRevokeAppPasswordOnlyOwn(t *testing.T) {
	e := newSecEnv(t)
	bPW := plaintextIn(t, e.create(t, secUserB, "bob phone").Body.String())
	bID := bPW[:6]

	e.revoke(t, secUserA, bID, false)
	rec, ok, err := e.recs.Get(secUserB, bID)
	if err != nil || !ok {
		t.Fatalf("B's credential was revoked by A: %v", err)
	}
	_, secret, _ := auth.ParseAppPassword(bPW)
	if err := bcrypt.CompareHashAndPassword(rec.PasswordHash, []byte(secret)); err != nil {
		t.Fatalf("B's credential no longer verifies: %v", err)
	}
	// Control: B can revoke it.
	e.revoke(t, secUserB, bID, false)
	if e.count(t, secUserB) != 0 {
		t.Error("owner could not revoke")
	}
}

func TestRevokeAppPasswordPersistenceFailure(t *testing.T) {
	e := newSecEnv(t)
	id := plaintextIn(t, e.create(t, secUserA, "x").Body.String())[:6]
	e.flaky.failDelete = true
	for _, hx := range []bool{false, true} {
		w := e.revoke(t, secUserA, id, hx)
		if w.Code != http.StatusInternalServerError || w.Header().Get("HX-Redirect") != "" || w.Header().Get("Location") != "" {
			t.Errorf("hx=%v: status %d, headers %v", hx, w.Code, w.Header())
		}
	}
	if e.count(t, secUserA) != 1 {
		t.Error("credential vanished despite the failure")
	}
}

func TestAppPasswordDatesAreDates(t *testing.T) {
	e := newSecEnv(t)
	pw := plaintextIn(t, e.create(t, secUserA, "x").Body.String())
	// last_used_at is the start of a UTC day.
	if err := e.recs.Touch(secUserA, pw[:6], 1700000000); err != nil {
		t.Fatal(err)
	}
	body := e.get(t, secUserA).Body.String()
	if !strings.Contains(body, "Tue 14 Nov 2023") {
		t.Errorf("last-used date missing or not a UTC date: %s", body)
	}
	if strings.Contains(body, "22:13") || strings.Contains(body, "00:00") {
		t.Error("a time of day is shown")
	}
}

// Require2FA exempts /settings/security by prefix so a user can enrol. A
// session held by the policy must not be able to mint a credential.
func TestCreateAppPasswordRefusedWhileHeldFor2FA(t *testing.T) {
	e := newSecEnv(t)
	run := func(user string, fn http.HandlerFunc, method string, form url.Values) *httptest.ResponseRecorder {
		var body *strings.Reader
		body = strings.NewReader("")
		if form != nil {
			body = strings.NewReader(form.Encode())
		}
		r := httptest.NewRequest(method, "/settings/security/app-passwords", body)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r = withUserContext(r, user, user+"@example.com")
		w := httptest.NewRecorder()
		middleware.Require2FA(psKeys{e.ps})(fn).ServeHTTP(w, r)
		return w
	}
	label := url.Values{"label": {"x"}, "password": {secPassword}}

	// Policy off, no TOTP: allowed.
	if w := run(secUserA, e.h.CreateAppPassword, http.MethodPost, label); w.Code != http.StatusOK {
		t.Fatalf("policy off: status %d", w.Code)
	}
	if e.count(t, secUserA) != 1 {
		t.Fatal("policy off: nothing created")
	}

	if err := e.ps.Set([]byte("/system/settings/require_2fa"), []byte("1")); err != nil {
		t.Fatal(err)
	}
	// Policy on, no TOTP: refused, form hidden, list and revoke still work.
	w := run(secUserB, e.h.CreateAppPassword, http.MethodPost, label)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/settings/security" {
		t.Errorf("held: status %d location %q", w.Code, w.Header().Get("Location"))
	}
	if e.count(t, secUserB) != 0 {
		t.Error("held session created a credential")
	}
	page := run(secUserB, e.h.SecuritySettings, http.MethodGet, nil).Body.String()
	if strings.Contains(page, `action="/settings/security/app-passwords"`) {
		t.Error("create form offered to a held session")
	}
	if e.count(t, secUserA) != 1 {
		t.Fatal("setup")
	}
	if run(secUserB, e.h.RevokeAppPassword, http.MethodPost, url.Values{"id": {"abcdef"}}).Code != http.StatusSeeOther {
		t.Error("revoke unavailable to a held session")
	}

	// Control: the same user, enrolled, is allowed.
	codes := putProfileCodes(t, e.ps, secUserB, true)
	label.Set("totp_code", codes[0])
	if w := run(secUserB, e.h.CreateAppPassword, http.MethodPost, label); w.Code != http.StatusOK || e.count(t, secUserB) != 1 {
		t.Errorf("enrolled: status %d, count %d", w.Code, e.count(t, secUserB))
	}
}

func TestAppPasswordsNilServiceDegrades(t *testing.T) {
	e := newSecEnv(t)
	e.h.SetAppPasswords(nil)
	if w := e.get(t, secUserA); w.Code != http.StatusOK || strings.Contains(w.Body.String(), "app-passwords") {
		t.Errorf("GET: %d, card rendered without a service", w.Code)
	}
	if w := e.create(t, secUserA, "x"); w.Code != http.StatusNotFound {
		t.Errorf("create: %d", w.Code)
	}
	if w := e.revoke(t, secUserA, "abcdef", false); w.Code != http.StatusNotFound {
		t.Errorf("revoke: %d", w.Code)
	}
}
