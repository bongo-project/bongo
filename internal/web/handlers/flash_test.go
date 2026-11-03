package handlers

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/web/middleware"
)

var flashLocales = []string{"en", "de", "fr", "es"}

func flashReq(target, locale string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, target, nil)
	return r.WithContext(middleware.WithLocale(r.Context(), locale))
}

// handlerSources parses every non-test Go file of the package.
func handlerSources(t *testing.T) map[string]*ast.File {
	t.Helper()
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	files := map[string]*ast.File{}
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		src, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, p, src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", p, err)
		}
		files[p] = f
	}
	return files
}

func isFlashKeyLit(e ast.Expr) bool {
	cl, ok := e.(*ast.CompositeLit)
	if !ok {
		return false
	}
	id, ok := cl.Type.(*ast.Ident)
	return ok && id.Name == "flashKey"
}

// TestFlashWritersUseTheAllowList enumerates every flash written by a handler:
// nothing outside flash.go may build a flash= parameter, name the "flash" query
// key or mint a flashKey, so each key a redirect carries is a flash.go
// variable, and each of those is in the allow-list.
func TestFlashWritersUseTheAllowList(t *testing.T) {
	files := handlerSources(t)
	if _, ok := files["flash.go"]; !ok {
		t.Fatal("flash.go not found")
	}

	declared := map[string]string{} // Go name -> wire key
	used := map[string]bool{}       // Go name -> referenced outside flash.go
	for name, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.BasicLit:
				if x.Kind != token.STRING || name == "flash.go" {
					return true
				}
				s, _ := strconv.Unquote(x.Value)
				if strings.Contains(s, "flash=") {
					t.Errorf("%s: string literal %s builds a flash= parameter outside flash.go", name, x.Value)
				}
				if s == "flash" {
					t.Errorf("%s: %s names the flash query key outside flash.go", name, x.Value)
				}
			case *ast.CompositeLit:
				if name != "flash.go" && isFlashKeyLit(x) {
					t.Errorf("%s: flashKey literal outside flash.go", name)
				}
			case *ast.Ident:
				if name != "flash.go" && strings.HasPrefix(x.Name, "flash") {
					used[x.Name] = true
				}
			}
			return true
		})
	}

	for _, d := range files["flash.go"].Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, v := range vs.Values {
				if !isFlashKeyLit(v) {
					continue
				}
				lit := v.(*ast.CompositeLit).Elts[0].(*ast.BasicLit)
				wire, _ := strconv.Unquote(lit.Value)
				declared[vs.Names[i].Name] = wire
			}
		}
	}

	// A broken scan must not pass vacuously.
	if len(declared) < 60 {
		t.Fatalf("found only %d flash keys in flash.go, the scan is broken", len(declared))
	}
	wires := map[string]bool{}
	for goName, wire := range declared {
		if wires[wire] {
			t.Errorf("wire key %q declared twice", wire)
		}
		wires[wire] = true
		if _, ok := flashMessages[wire]; !ok {
			t.Errorf("%s (%q) is not in flashMessages", goName, wire)
		}
		if wire != strings.ToLower(wire) || strings.ContainsAny(wire, " .&=%+?") {
			t.Errorf("%s: wire key %q is not lowercase-hyphen", goName, wire)
		}
		if !used[goName] {
			t.Errorf("%s is never written by a handler; remove it from the allow-list", goName)
		}
	}
	for wire := range flashMessages {
		if !wires[wire] {
			t.Errorf("flashMessages entry %q has no flashKey variable", wire)
		}
	}
}

// TestFlashMessagesTranslated: every allow-listed key resolves in all four
// locales, new flash.* messages are not English copies, and none carries a
// format verb (nothing is ever interpolated into a flash).
func TestFlashMessagesTranslated(t *testing.T) {
	for wire, id := range flashMessages {
		en := i18n.T("en", id)
		for _, loc := range flashLocales {
			got := i18n.T(loc, id)
			if got == "" || got == id {
				t.Errorf("%s (%s): missing in %s", wire, id, loc)
				continue
			}
			if strings.Contains(got, "%") {
				t.Errorf("%s (%s): %s text %q has a format verb", wire, id, loc, got)
			}
			if loc != "en" && strings.HasPrefix(id, "flash.") && got == en {
				t.Errorf("%s (%s): %s is an English copy", wire, id, loc)
			}
		}
	}
}

func TestFlashText(t *testing.T) {
	en := func(id string) string { return i18n.T("en", id) }
	tests := []struct {
		name, query, locale, want string
	}{
		{"no flash", "", "en", ""},
		{"known key", "?flash=user-created", "en", en("flash.userCreated")},
		{"known key de", "?flash=user-created", "de", i18n.T("de", "flash.userCreated")},
		{"free text", "?flash=Your+account+is+suspended", "en", ""},
		{"i18n id instead of wire key", "?flash=flash.userCreated", "en", ""},
		{"catalogue id of a reused message", "?flash=compose.flashSent", "en", ""},
		{"arbitrary catalogue id", "?flash=admin.storageLowBanner", "en", ""},
		{"wrong case", "?flash=User-Created", "en", ""},
		{"trailing space", "?flash=user-created+", "en", ""},
		{"empty", "?flash=", "en", ""},
		{"known plus unknown", "?flash=user-created&flash=Pay+now", "en", ""},
		{"unknown plus known", "?flash=Pay+now&flash=user-created", "en", ""},
		{"three keys", "?flash=rule-created&flash=forward-rule-verify&flash=forward-rate-limited", "en",
			en("rules.flashCreated") + ". " + en("settings.fwdRuleVerify") + " " + en("settings.fwdRateLimited")},
		{"over the cap", "?flash=queued&flash=queued&flash=queued&flash=queued", "en", ""},
		{"rule created and verify note", "?flash=rule-created&flash=forward-rule-verify", "en",
			en("rules.flashCreated") + ". " + en("settings.fwdRuleVerify")},
		{"saved with a soft outcome", "?flash=settings-saved-note&flash=forward-rate-limited", "en",
			en("settings.fwdSavedPrefix") + " " + en("settings.fwdRateLimited")},
		{"mark spam with training skipped", "?flash=marked-spam&flash=training-skipped", "en",
			en("flash.markedSpam") + ". " + en("flash.trainingSkipped")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := flashText(flashReq("/x"+tc.query, tc.locale)); got != tc.want {
				t.Errorf("flashText = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestWithFlash(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"plain", withFlash("/admin/users", flashUserCreated), "/admin/users?flash=user-created"},
		{"existing query", withFlash("/calendar?x=1", flashEventCreated), "/calendar?x=1&flash=event-created"},
		{"several", withFlash("/settings/rules", flashRuleCreated, flashFwdRuleVerify), "/settings/rules?flash=rule-created&flash=forward-rule-verify"},
		{"with invite warning", withInviteWarning(withFlash("/calendar", flashEventCreated), []string{"a@example.com"}), "/calendar?flash=event-created&nr=a%40example.com"},
		{"none", withFlash("/x"), "/x"},
	}
	for _, tc := range tests {
		if tc.got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}

const spoof = "Your+account+is+suspended"

// pageBanner renders a page for locale and reports its body.
type pageBanner func(t *testing.T, query, locale string) string

// TestFlashPagesRenderOnlyAllowListed drives the real handlers: spoofed text
// shows no banner on the mail, address and admin pages, and a known key shows
// its translation in the viewer's locale.
func TestFlashPagesRenderOnlyAllowListed(t *testing.T) {
	view := newViewFixture(t)
	verify := newVerifyTestEnvFull(t)
	admin := newAdminScopeTestEnv(t)
	res, _, _ := newResourceAdminEnv(t)

	resW := postToHandler(t, res.h, res.h.CreateUser, "POST", "/admin/users", createResourceForm(testDomain1, "room1"), nil,
		func(r *http.Request) *http.Request { return withGlobalAdmin(r, res.globalAdminID) })
	if resW.Code != http.StatusSeeOther {
		t.Fatalf("create resource: %d", resW.Code)
	}
	room := res.userIDByEmail(t, "room1@"+testDomain1)

	pages := []struct {
		name string
		get  pageBanner
		key  string // wire key that must render
		id   string // its message ID
	}{
		{"mail", func(t *testing.T, q, loc string) string {
			_, body := view.getAs("/mail/INBOX"+q, loc)
			return body
		}, "message-moved", "flash.messageMoved"},
		{"addresses", func(t *testing.T, q, loc string) string {
			r := flashReq("/settings/addresses"+q, loc)
			r = r.WithContext(middleware.WithUserContext(r.Context(), "userA", "a@example.com"))
			w := httptest.NewRecorder()
			verify.handler.ShowAddresses(w, r)
			return w.Body.String()
		}, "address-verified", "flash.addressVerified"},
		{"admin users", func(t *testing.T, q, loc string) string {
			r := withGlobalAdmin(flashReq("/admin/users"+q, loc), admin.globalAdminID)
			r = r.WithContext(middleware.WithLocale(r.Context(), loc))
			w := httptest.NewRecorder()
			admin.h.ListUsers(w, r)
			return w.Body.String()
		}, "user-created", "flash.userCreated"},
		{"resource bookings", func(t *testing.T, q, loc string) string {
			add := withParams(func(r *http.Request) *http.Request {
				return withGlobalAdmin(r, res.globalAdminID)
			}, map[string]string{"userID": room})
			r := add(flashReq("/admin/users/"+room+"/bookings"+q, loc))
			r = r.WithContext(middleware.WithLocale(r.Context(), loc))
			w := httptest.NewRecorder()
			res.h.ResourceBookings(w, r)
			return w.Body.String()
		}, "booking-cancelled", "flash.bookingCancelled"},
	}

	const banner = `class="flash`
	for _, p := range pages {
		t.Run(p.name, func(t *testing.T) {
			if body := p.get(t, "", "en"); strings.Contains(body, banner) {
				t.Fatalf("page renders a banner without a flash")
			}
			for _, q := range []string{
				"?flash=" + spoof,
				"?flash=" + p.key + "&flash=" + spoof,
				"?flash=" + p.key + "&flash=" + p.key + "&flash=" + p.key + "&flash=" + p.key,
				"?flash=" + url.QueryEscape(p.id),
				"?flash=" + url.QueryEscape("<b>x</b>"),
			} {
				body := p.get(t, q, "en")
				if strings.Contains(body, banner) || strings.Contains(body, "suspended") {
					t.Errorf("%s renders a banner", q)
				}
			}
			for _, loc := range []string{"en", "de"} {
				body := p.get(t, "?flash="+p.key, loc)
				want := i18n.T(loc, p.id)
				if !strings.Contains(body, banner) || !strings.Contains(body, want) {
					t.Errorf("%s: banner %q missing", loc, want)
				}
			}
		})
	}
}
