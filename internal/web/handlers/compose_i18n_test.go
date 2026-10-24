package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/web/middleware"
)

// composeLocaleReq builds a form POST whose context carries the given locale.
func composeLocaleReq(path, userID, email, locale string, form url.Values) *http.Request {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUserContext(req, userID, email)
	return req.WithContext(middleware.WithLocale(req.Context(), locale))
}

func flashOf(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status=%d want 303: %s", w.Code, w.Body.String())
	}
	loc, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	return loc.Query().Get("flash")
}

func TestComposeMessagesAreTranslated(t *testing.T) {
	t.Run("error path", func(t *testing.T) {
		cases := []struct {
			locale, want string
		}{
			{"en", "This compose form is out of date. Please reload the compose page and try again."},
			{"de", i18n.T("de", "compose.errFormOutdated")},
			{"fr", i18n.T("fr", "compose.errFormOutdated")},
		}
		for _, tc := range cases {
			t.Run(tc.locale, func(t *testing.T) {
				env, uid, email := newPipelineEnv(t)
				// Valid except for the missing send ID.
				w := httptest.NewRecorder()
				env.handler.Send(w, composeLocaleReq("/compose/send", uid, email, tc.locale, idemFields("hello")))
				if w.Code != http.StatusBadRequest {
					t.Fatalf("status=%d want 400", w.Code)
				}
				if got := strings.TrimSpace(w.Body.String()); got != tc.want {
					t.Errorf("body = %q, want %q", got, tc.want)
				}
				if tc.locale != "en" && strings.Contains(w.Body.String(), "out of date") {
					t.Errorf("body %q is still English", w.Body.String())
				}
			})
		}
	})

	t.Run("sent flash", func(t *testing.T) {
		cases := []struct {
			name, locale, to, id, enWant string
		}{
			{"local de", "de", "bob@example.com", "compose.flashSent", "Message sent"},
			{"local en", "en", "bob@example.com", "compose.flashSent", "Message sent"},
			{"mixed de", "de", "bob@example.com, ext@external.example", "compose.flashSentLocalQueued", "Message sent to local recipients; external delivery queued"},
			{"external fr", "fr", "ext@external.example", "compose.flashQueued", "Message queued for delivery"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				env, uid, email := newPipelineEnv(t)
				form := url.Values{"to": {tc.to}, "subject": {"s"}, "body": {"b"}, "send_id": {sendID()}}
				w := httptest.NewRecorder()
				env.handler.Send(w, composeLocaleReq("/compose/send", uid, email, tc.locale, form))
				got := flashOf(t, w)
				if want := i18n.T(tc.locale, tc.id); got != want {
					t.Errorf("flash = %q, want %q", got, want)
				}
				if tc.locale == "en" {
					if got != tc.enWant {
						t.Errorf("en flash = %q, want %q", got, tc.enWant)
					}
				} else if got == tc.enWant {
					t.Errorf("flash %q is not translated", got)
				}
			})
		}
	})

	t.Run("draft flash", func(t *testing.T) {
		for _, locale := range []string{"en", "de", "es"} {
			env := newComposeRichTestEnv(t)
			form := url.Values{"to": {"x@example.com"}, "subject": {"s"}, "body": {"b"}, "body_mode": {"plain"}}
			w := httptest.NewRecorder()
			env.handler.SaveDraft(w, composeLocaleReq("/compose/draft", "u-"+locale, "d@example.com", locale, form))
			got := flashOf(t, w)
			if want := i18n.T(locale, "compose.flashDraftSaved"); got != want {
				t.Errorf("%s: flash = %q, want %q", locale, got, want)
			}
			if (locale == "en") != (got == "Draft saved") {
				t.Errorf("%s: flash = %q", locale, got)
			}
		}
	})
}

// handlerKeyPattern matches a message ID passed to one of the handler-side
// translation helpers.
var handlerKeyPattern = regexp.MustCompile(`\b(?:composeText|composeError|fwdText|fwdError)\(\s*(?:[wr]\s*,\s*)*"([^"]+)"`)

// assignedKeyPattern matches a compose message ID assigned to the id or
// flashID variables before the helper is called.
var assignedKeyPattern = regexp.MustCompile(`\b(?:id|flashID)\s*:?=\s*"(compose\.[^"]+)"`)

// TestHandlerMessageKeysExist closes the gap TestI18nCoverage leaves: it only
// sees IDs used in templates, and i18n.T falls back to returning the ID, so a
// misspelled or missing key in handler code would otherwise ship silently.
func TestHandlerMessageKeysExist(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range handlerKeyPattern.FindAllStringSubmatch(string(src), -1) {
			seen[m[1]] = true
		}
		// Keys assigned to a variable before the helper call.
		for _, m := range assignedKeyPattern.FindAllStringSubmatch(string(src), -1) {
			seen[m[1]] = true
		}
	}
	if len(seen) < 20 {
		t.Fatalf("found only %d handler keys, the scan pattern is broken", len(seen))
	}
	for key := range seen {
		en := i18n.T("en", key)
		if en == key {
			t.Errorf("%s: missing from en.json", key)
			continue
		}
		if !strings.HasPrefix(key, "compose.") {
			continue
		}
		for _, locale := range []string{"de", "es", "fr"} {
			if got := i18n.T(locale, key); got == en || got == key {
				t.Errorf("%s: %s value %q is missing or an English copy", key, locale, got)
			}
		}
	}
}

// TestComposeHasNoLiteralUserText fails if compose.go answers with a
// hard-coded English error or flash again: every user-visible string must
// come from the catalogue. Like the key scan above it reads the source, so
// it cannot be mutated with -overlay.
func TestComposeHasNoLiteralUserText(t *testing.T) {
	src, err := os.ReadFile("compose.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, re := range []*regexp.Regexp{
		regexp.MustCompile(`http\.Error\(\s*w\s*,\s*"`),
		regexp.MustCompile(`\?flash=[A-Za-z%+]`),
	} {
		if loc := re.FindIndex(src); loc != nil {
			line := 1 + strings.Count(string(src[:loc[0]]), "\n")
			t.Errorf("compose.go:%d: literal user-facing text (%s); use composeError or composeText", line, re)
		}
	}
}
