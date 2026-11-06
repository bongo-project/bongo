package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/web/middleware"
)

// Setup mode is the last step of 2FA enrolment. An account without 2FA that
// opens the setup URL must get the ordinary page: no Finish card, no intro
// claiming 2FA is on, no setup field carried into the forms.
func TestSetupModeIgnoredWithout2FA(t *testing.T) {
	e := newSecEnv(t)
	// With no credential Finish is absent anyway, so create one first.
	if _, _, err := e.apps.Create(secUserA, "phone"); err != nil {
		t.Fatal(err)
	}
	assertPlain := func(name, body string) {
		t.Helper()
		for _, bad := range []string{onURL, "Finish", "Two-factor authentication is on", "Skip for now", `name="setup"`, devicesSetupPath} {
			if strings.Contains(body, bad) {
				t.Errorf("%s: non-2FA page contains %q", name, bad)
			}
		}
	}
	assertPlain("get", e.do(t, e.h.AddDevice, secUserA, http.MethodGet, devicesSetupPath, nil, false).Body.String())

	f := createForm("tablet", secPassword, "")
	f.Set("setup", "1")
	w := e.post(t, e.h.CreateDevice, secUserA, devicesURL, f)
	if w.Code != http.StatusOK || e.count(t, secUserA) != 2 {
		t.Fatalf("create: %d, %d credentials", w.Code, e.count(t, secUserA))
	}
	assertPlain("reveal", w.Body.String())

	// Control: an enrolled account keeps setup mode.
	e.enrol(t, secUserB)
	if _, _, err := e.apps.Create(secUserB, "phone"); err != nil {
		t.Fatal(err)
	}
	page := e.do(t, e.h.AddDevice, secUserB, http.MethodGet, devicesSetupPath, nil, false).Body.String()
	for _, want := range []string{onURL, "Finish", "Two-factor authentication is on", `name="setup" value="1"`} {
		if !strings.Contains(page, want) {
			t.Errorf("2FA control lacks %q", want)
		}
	}
}

// A throttled create says so on the page in the session's language, on both
// the plain form and Add a device, and a held session is still sent to the
// Security page.
func TestThrottledCreateIsTranslatedOnThePage(t *testing.T) {
	deRequest := func(e *secEnv, fn http.HandlerFunc, path string, form url.Values, held bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r = withUserContext(r, secUserA, secUserA+"@example.com")
		r = r.WithContext(middleware.WithLocale(r.Context(), "de"))
		w := httptest.NewRecorder()
		if held {
			middleware.Require2FA(psKeys{e.ps})(fn).ServeHTTP(w, r)
		} else {
			fn(w, r)
		}
		return w
	}
	routes := map[string]struct {
		fn   func(e *secEnv) http.HandlerFunc
		path string
		form func(password string) url.Values
		env  func(t *testing.T) (*secEnv, *linkEnv)
	}{
		"plain": {func(e *secEnv) http.HandlerFunc { return e.h.CreateAppPassword }, "/settings/security/app-passwords",
			func(p string) url.Values { return createForm("x", p, "") }, nil},
		"device": {func(e *secEnv) http.HandlerFunc { return e.h.CreateDevice }, devicesURL,
			func(p string) url.Values { return createForm("x", p, "") }, nil},
		// The QR route needs the device-link store, and has no check of its own.
		"qr": {func(e *secEnv) http.HandlerFunc { return e.h.CreateDevice }, devicesURL,
			func(p string) url.Values { return qrForm("x", p, "") },
			func(t *testing.T) (*secEnv, *linkEnv) { le := newLinkEnv(t, "mail.example.com"); return le.secEnv, le }},
	}
	for name, rt := range routes {
		t.Run(name, func(t *testing.T) {
			var e *secEnv
			var le *linkEnv
			if rt.env != nil {
				e, le = rt.env(t)
			} else {
				e = newSecEnv(t)
			}
			e.h.SetRateLimiter(middleware.NewLoginRateLimiter(2, time.Minute))
			for range 2 {
				e.post(t, e.h.CreateAppPassword, secUserA, "/settings/security/app-passwords", createForm("x", "wrong", ""))
			}
			// The right password: a wrong one is refused either way.
			w := deRequest(e, rt.fn(e), rt.path, rt.form(secPassword), false)
			body := w.Body.String()
			if w.Code != http.StatusTooManyRequests {
				t.Fatalf("status %d", w.Code)
			}
			if w.Header().Get("Retry-After") == "" {
				t.Error("no Retry-After")
			}
			if !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
				t.Errorf("content type %q", w.Header().Get("Content-Type"))
			}
			if !strings.Contains(body, "Zu viele Versuche") || strings.Contains(body, "Too many attempts") {
				t.Error("the page does not carry the German message")
			}
			if e.count(t, secUserA) != 0 {
				t.Error("a throttled create stored a credential")
			}
			if le != nil {
				le.mu.Lock()
				n := len(le.captured)
				le.mu.Unlock()
				if n != 0 {
					t.Error("a throttled create made a device link")
				}
			}

			if err := e.ps.Set([]byte("/system/settings/require_2fa"), []byte("1")); err != nil {
				t.Fatal(err)
			}
			w = deRequest(e, rt.fn(e), rt.path, rt.form(secPassword), true)
			if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/settings/security" {
				t.Errorf("held and limited: %d %q", w.Code, w.Header().Get("Location"))
			}
		})
	}
}
