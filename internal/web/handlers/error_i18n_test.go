package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/web/middleware"
)

// TestV016ErrorBodiesTranslated pins that the v0.16 http.Error bodies follow
// the request locale, and that the deliberate constant bodies do not.
func TestV016ErrorBodiesTranslated(t *testing.T) {
	serve := func(h http.Handler, req *http.Request, locale string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req.WithContext(middleware.WithLocale(req.Context(), locale)))
		return rr
	}

	t.Run("shared flag with an unknown action", func(t *testing.T) {
		e := newSharedMailEnv(t)
		e.share(t, "bob", "lrswite")
		for _, tc := range []struct{ locale, want string }{
			{"en", "Unknown flag or action"},
			{"de", i18n.T("de", "mail.errUnknownFlagAction")},
			{"es", i18n.T("es", "mail.errUnknownFlagAction")},
			{"fr", i18n.T("fr", "mail.errUnknownFlagAction")},
		} {
			t.Run(tc.locale, func(t *testing.T) {
				req := httptest.NewRequest(http.MethodPost, e.path("alice", e.folderID, "/1/flags"),
					strings.NewReader(url.Values{"flag": {"nonsense"}, "action": {"add"}}.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				req.Header.Set("X-Test-User", "bob")
				rr := serve(e.router, req, tc.locale)
				if rr.Code != http.StatusBadRequest {
					t.Fatalf("status = %d, want 400", rr.Code)
				}
				if got := strings.TrimSpace(rr.Body.String()); got != tc.want {
					t.Errorf("body = %q, want %q", got, tc.want)
				}
				if tc.locale != "en" && strings.TrimSpace(rr.Body.String()) == "Unknown flag or action" {
					t.Errorf("%s body is still the English text", tc.locale)
				}
			})
		}
	})

	t.Run("participant query refused", func(t *testing.T) {
		f := newViewFixture(t)
		for _, tc := range []struct{ locale, want string }{
			{"en", "participant must be one to 20 email addresses"},
			{"de", i18n.T("de", "search.errParticipant")},
		} {
			t.Run(tc.locale, func(t *testing.T) {
				req := httptest.NewRequest(http.MethodGet, "/search?participant=", nil)
				rr := serve(f.router, req, tc.locale)
				if rr.Code != http.StatusBadRequest {
					t.Fatalf("status = %d, want 400", rr.Code)
				}
				if got := strings.TrimSpace(rr.Body.String()); got != tc.want {
					t.Errorf("body = %q, want %q", got, tc.want)
				}
			})
		}
	})

	t.Run("constant not-found body ignores the locale", func(t *testing.T) {
		e := newSharedMailEnv(t) // bob holds no grant
		req := httptest.NewRequest(http.MethodGet, e.path("alice", e.folderID, ""), nil)
		req.Header.Set("X-Test-User", "bob")
		rr := serve(e.router, req, "de")
		if rr.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rr.Code)
		}
		if got := strings.TrimSpace(rr.Body.String()); got != "folder not found" {
			t.Errorf("body = %q, want the fixed %q", got, "folder not found")
		}
	})
}
