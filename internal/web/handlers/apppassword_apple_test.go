package handlers

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
)

const aspenType = "application/x-apple-aspen-config"

func appleForm(label, password, code string) url.Values {
	f := createForm(label, password, code)
	f.Set("kind", "apple")
	return f
}

// appleEnv is a secEnv with client settings wired and a LocalAuth over the
// same records, so a downloaded password can be tried as a protocol login.
func appleEnv(t *testing.T) (*secEnv, *auth.LocalAuth) {
	t.Helper()
	e := newSecEnv(t)
	e.h.SetClientSettings(newTestAutoconfig().ClientSettings)
	mm, err := store.NewMetadataManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mm.Close() })
	for _, u := range []string{secUserA, secUserB} {
		if _, err := mm.System.DB().Exec(
			"INSERT INTO users (id, username, display_name, enabled, created_at) VALUES (?, ?, 'x', 1, 0)", u, u+"@example.com"); err != nil {
			t.Fatal(err)
		}
	}
	la := auth.NewLocalAuth(e.ps, mm)
	la.SetAppPasswords(e.apps)
	return e, la
}

func passwordKeys(t *testing.T, payloads []plistPayload) []string {
	t.Helper()
	var out []string
	for _, p := range payloads {
		for _, k := range []string{"IncomingPassword", "OutgoingPassword", "CalDAVPassword", "CardDAVPassword"} {
			if v, ok := p[k]; ok {
				out = append(out, v)
			}
		}
	}
	return out
}

func TestAddDeviceApple(t *testing.T) {
	e, la := appleEnv(t)
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })

	page := e.do(t, e.h.AddDevice, secUserA, http.MethodGet, devicesURL, nil, false).Body.String()
	if !strings.Contains(page, `value="apple"`) {
		t.Fatal("the page does not offer Apple")
	}

	w := e.post(t, e.h.CreateDevice, secUserA, devicesURL, appleForm("Lance iPhone", secPassword, ""))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, aspenType) {
		t.Fatalf("Content-Type = %q", ct)
	}
	if !strings.Contains(w.Header().Get("Cache-Control"), "no-store") || w.Header().Get("Pragma") != "no-cache" {
		t.Errorf("download is cacheable: %q", w.Header().Get("Cache-Control"))
	}
	cd := w.Header().Get("Content-Disposition")
	if cd != `attachment; filename="example.com.mobileconfig"` {
		t.Errorf("Content-Disposition = %q (the label must not appear)", cd)
	}

	outer, payloads := parseMobileconfig(t, w.Body.String())
	if len(payloads) != 3 {
		t.Fatalf("%d payloads", len(payloads))
	}
	pws := passwordKeys(t, payloads)
	if len(pws) != 4 {
		t.Fatalf("%d password keys, want 4 (incoming, outgoing, CalDAV, CardDAV)", len(pws))
	}
	for _, p := range pws[1:] {
		if p != pws[0] {
			t.Fatal("the password keys disagree")
		}
	}
	if payloads[0]["OutgoingPasswordSameAsIncoming"] != "true" {
		t.Error("OutgoingPasswordSameAsIncoming dropped")
	}
	if outer["PayloadType"] != "Configuration" {
		t.Errorf("outer = %v", outer)
	}

	// The value works as a protocol login, and stops when revoked.
	if id, _, err := la.AuthenticateProtocol(secUserA+"@example.com", pws[0]); err != nil || id != secUserA {
		t.Fatalf("the profile password does not authenticate: %v", err)
	}
	e.apps.Wait()

	// The row is under its label.
	list := e.get(t, secUserA).Body.String()
	if !strings.Contains(list, "Lance iPhone") {
		t.Error("the new device is not listed")
	}
	infos, err := e.apps.List(secUserA)
	if err != nil || len(infos) != 1 {
		t.Fatalf("list: %v %v", infos, err)
	}

	// Not in any log, and the next page load does not carry it.
	for _, l := range []string{logs.String(), e.logs.String()} {
		if strings.Contains(l, pws[0]) || strings.Contains(l, strings.ReplaceAll(pws[0], "-", "")) {
			t.Error("the password is in a log")
		}
	}
	if strings.Contains(list, pws[0]) {
		t.Error("the password is on the Security page")
	}

	if err := e.apps.Revoke(secUserA, infos[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := la.AuthenticateProtocol(secUserA+"@example.com", pws[0]); err == nil {
		t.Error("a revoked profile password still authenticates")
	}
}

// The personal and the public profile are one document apart from the
// password keys and the top-level UUID.
func TestApplePersonalIdentity(t *testing.T) {
	e, _ := appleEnv(t)
	get := func(user, label string) (plistPayload, []plistPayload) {
		w := e.post(t, e.h.CreateDevice, user, devicesURL, appleForm(label, secPassword, ""))
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d", user, w.Code)
		}
		return parseMobileconfig(t, w.Body.String())
	}
	o1, p1 := get(secUserA, "one")
	o2, p2 := get(secUserA, "two")
	o3, p3 := get(secUserB, "bob")

	if o1["PayloadIdentifier"] != o2["PayloadIdentifier"] {
		t.Error("a second profile for the same user has a different identifier")
	}
	if o1["PayloadUUID"] == o2["PayloadUUID"] {
		t.Error("two credentials share a top-level UUID")
	}
	for i := range p1 {
		if p1[i]["PayloadIdentifier"] != p2[i]["PayloadIdentifier"] || p1[i]["PayloadUUID"] != p2[i]["PayloadUUID"] {
			t.Errorf("payload %d identity changed between installs", i)
		}
		if p1[i]["PayloadIdentifier"] == p3[i]["PayloadIdentifier"] || p1[i]["PayloadUUID"] == p3[i]["PayloadUUID"] {
			t.Errorf("payload %d is shared between users", i)
		}
	}
	if o1["PayloadIdentifier"] == o3["PayloadIdentifier"] || o1["PayloadUUID"] == o3["PayloadUUID"] {
		t.Error("two users share top-level identity")
	}

	// The personal profile replaces the public one of the same user.
	pub := newTestAutoconfig()
	oPub, _ := parseMobileconfig(t, fetchMobileconfig(t, pub, secUserA+"@example.com", ""))
	if oPub["PayloadIdentifier"] != o1["PayloadIdentifier"] {
		t.Error("the personal and public profiles differ in identifier")
	}
	if oPub["PayloadUUID"] == o1["PayloadUUID"] {
		t.Error("the personal profile reuses the public top-level UUID")
	}
}

// Every refusal leaves the user on a page, not on a download, and creates
// nothing. Each case is fed input that would be accepted but for one fault.
func TestAppleRefusals(t *testing.T) {
	type tc struct {
		name  string
		setup func(e *secEnv) (url.Values, string)
		code  int
	}
	cases := []tc{
		{"wrong password", func(e *secEnv) (url.Values, string) { return appleForm("x", "wrong", ""), "" }, http.StatusSeeOther},
		{"bad label", func(e *secEnv) (url.Values, string) { return appleForm("", secPassword, ""), "" }, http.StatusBadRequest},
		{"cap", func(e *secEnv) (url.Values, string) {
			for range 10 {
				if _, _, err := e.apps.Create(secUserA, "d"); err != nil {
					t.Fatal(err)
				}
			}
			return appleForm("x", secPassword, ""), ""
		}, http.StatusConflict},
		{"throttled", func(e *secEnv) (url.Values, string) {
			rl := middleware.NewLoginRateLimiter(2, time.Minute)
			e.h.SetRateLimiter(rl)
			for range 2 {
				e.post(t, e.h.CreateDevice, secUserA, devicesURL, appleForm("x", "wrong", ""))
			}
			return appleForm("x", secPassword, ""), ""
		}, http.StatusTooManyRequests},
		{"unknown kind", func(e *secEnv) (url.Values, string) {
			f := createForm("x", secPassword, "")
			f.Set("kind", "windows")
			return f, ""
		}, http.StatusBadRequest},
	}
	for _, c := range cases {
		for _, setup := range []bool{false, true} {
			t.Run(c.name, func(t *testing.T) {
				e, _ := appleEnv(t)
				form, _ := c.setup(e)
				before := e.count(t, secUserA)
				if setup {
					form.Set("setup", "1")
				}
				w := e.post(t, e.h.CreateDevice, secUserA, devicesURL, form)
				if w.Code != c.code {
					t.Fatalf("status = %d, want %d", w.Code, c.code)
				}
				if strings.HasPrefix(w.Header().Get("Content-Type"), aspenType) || strings.Contains(w.Body.String(), "PayloadContent") {
					t.Error("a refusal returned a profile")
				}
				if e.count(t, secUserA) != before {
					t.Error("a refusal created a credential")
				}
				if setup && c.code == http.StatusSeeOther && !strings.HasPrefix(w.Header().Get("Location"), devicesSetupPath) {
					t.Errorf("setup lost: %q", w.Header().Get("Location"))
				}
				if setup && c.code != http.StatusSeeOther && c.name != "unknown kind" && !strings.Contains(w.Body.String(), `name="setup" value="1"`) {
					t.Error("setup lost on the refusal page")
				}
				// A refusal that renders is the Add a device page.
				if w.Code == http.StatusBadRequest && c.name == "bad label" || w.Code == http.StatusConflict || w.Code == http.StatusTooManyRequests {
					if !strings.Contains(w.Body.String(), `action="/settings/security/devices"`) {
						t.Error("the refusal is not the Add a device page")
					}
				}
				if c.code == http.StatusTooManyRequests {
					if w.Header().Get("Retry-After") == "" {
						t.Error("no Retry-After on the throttle page")
					}
					if !strings.Contains(w.Body.String(), "Too many attempts") {
						t.Error("the throttle page does not say why")
					}
				}
			})
		}
	}
}

func TestAppleRefusedWith2FA(t *testing.T) {
	e, _ := appleEnv(t)
	codes := e.enrol(t, secUserA)
	for name, code := range map[string]string{"missing": "", "wrong": "000000"} {
		w := e.post(t, e.h.CreateDevice, secUserA, devicesURL, appleForm("x", secPassword, code))
		if w.Code != http.StatusSeeOther || strings.HasPrefix(w.Header().Get("Content-Type"), aspenType) {
			t.Errorf("%s code: %d", name, w.Code)
		}
	}
	if e.count(t, secUserA) != 0 {
		t.Fatal("a refused create stored a credential")
	}
	// Control: the right code goes through the same route.
	if w := e.post(t, e.h.CreateDevice, secUserA, devicesURL, appleForm("x", secPassword, codes[0])); !strings.HasPrefix(w.Header().Get("Content-Type"), aspenType) {
		t.Errorf("control: %d", w.Code)
	}
}

// With no settings source Apple is not offered and a forced post is refused
// before a credential exists.
func TestAppleNeedsClientSettings(t *testing.T) {
	e := newSecEnv(t)
	page := e.do(t, e.h.AddDevice, secUserA, http.MethodGet, devicesURL, nil, false).Body.String()
	if strings.Contains(page, `value="apple"`) {
		t.Error("Apple is offered with no settings")
	}
	w := e.post(t, e.h.CreateDevice, secUserA, devicesURL, appleForm("x", secPassword, ""))
	if w.Code != http.StatusBadRequest || e.count(t, secUserA) != 0 {
		t.Errorf("status %d, %d credentials", w.Code, e.count(t, secUserA))
	}

	// A settings source with no outgoing server must not panic or create.
	e.h.SetClientSettings(func(*http.Request) ClientSettings { return ClientSettings{Host: "mail.example.com"} })
	w = e.post(t, e.h.CreateDevice, secUserA, devicesURL, appleForm("x", secPassword, ""))
	if w.Code != http.StatusBadRequest || e.count(t, secUserA) != 0 {
		t.Errorf("empty SMTP: status %d, %d credentials", w.Code, e.count(t, secUserA))
	}
}

func TestAppleCardGuidance(t *testing.T) {
	e, _ := appleEnv(t)
	page := e.do(t, e.h.AddDevice, secUserA, http.MethodGet, devicesURL, nil, false).Body.String()
	for _, want := range []string{"Profile Downloaded", "Not Verified", "x-data=\"appleDevice\"", "x-cloak"} {
		if !strings.Contains(page, want) {
			t.Errorf("Apple card lacks %q", want)
		}
	}
}

// The builder needs no request, session or cookie: a link-based download can
// call it.
func TestBuildAppleProfileWithoutRequest(t *testing.T) {
	cs := ClientSettings{
		Host: "mail.example.com", IMAP: MailEndpoint{Port: 993, Security: securitySSL},
		SMTP:   []MailEndpoint{{Port: 465, Security: securitySSL}},
		CalDAV: DAVEndpoint{Port: 443, Path: caldavPath}, CardDAV: DAVEndpoint{Port: 443, Path: carddavPath},
	}
	body := buildAppleProfile(cs, "alice@example.com", &appleCredential{Password: "abcdef-ghijkl-mnopqr-stuvwx", LookupID: "abcdef"})
	outer, payloads := parseMobileconfig(t, string(body))
	if len(payloads) != 3 || len(passwordKeys(t, payloads)) != 4 || outer["PayloadUUID"] == "" {
		t.Errorf("outer %v, %d payloads", outer, len(payloads))
	}
	if bytes.Contains(body, []byte("abcdef-ghijkl")) == false {
		t.Error("password missing")
	}
	// The UUID comes from the lookup ID, not the secret.
	other := buildAppleProfile(cs, "alice@example.com", &appleCredential{Password: "zzzzzz-ghijkl-mnopqr-stuvwx", LookupID: "abcdef"})
	o2, _ := parseMobileconfig(t, string(other))
	if o2["PayloadUUID"] != outer["PayloadUUID"] {
		t.Error("the top-level UUID depends on the secret")
	}
	pub, ppay := parseMobileconfig(t, string(buildAppleProfile(cs, "alice@example.com", nil)))
	assertNoPasswords(t, ppay)
	if pub["PayloadUUID"] == outer["PayloadUUID"] {
		t.Error("public and personal share a UUID")
	}
}

// The public profile is byte-for-byte what it was before the builder was
// shared. The golden files were captured from the code before the refactor.
func TestPublicMobileconfigUnchanged(t *testing.T) {
	for name, port := range map[string]int{"465": 465, "no465": 0} {
		t.Run(name, func(t *testing.T) {
			want, err := os.ReadFile("testdata/public_" + name + ".mobileconfig")
			if err != nil {
				t.Fatal(err)
			}
			h := NewAutoconfigHandler("mail.example.com", 143, 993, 587, port)
			h.SetDomainChecker(func(d string) bool { return d == "example.com" })
			req := httptest.NewRequest(http.MethodGet, "/.well-known/mobileconfig?email=alice@example.com", nil)
			rec := httptest.NewRecorder()
			h.AppleMobileconfig(rec, req)
			if !bytes.Equal(rec.Body.Bytes(), want) {
				t.Errorf("public profile changed:\n%s", rec.Body.String())
			}
			if rec.Header().Get("Content-Type") != "application/x-apple-aspen-config; charset=utf-8" ||
				rec.Header().Get("Content-Disposition") != `attachment; filename="example.com.mobileconfig"` ||
				rec.Header().Get("Cache-Control") != "" || len(rec.Header()) != 2 {
				t.Errorf("headers changed: %v", rec.Header())
			}
			_, payloads := parseMobileconfig(t, rec.Body.String())
			assertNoPasswords(t, payloads)
		})
	}
}
