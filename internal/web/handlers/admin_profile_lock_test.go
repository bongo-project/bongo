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

// Every admin read-modify-write of a profile must wait for the per-user lock
// that login and logout hold, or it can overwrite TotpLastStep or the session
// generation with an older value.
func TestAdminProfileWritesTakeUserLock(t *testing.T) {
	const userID = "u-locked"
	handlers := map[string]func(*AdminHandler) http.HandlerFunc{
		"UpdateUser":     func(h *AdminHandler) http.HandlerFunc { return h.UpdateUser },
		"ToggleDisabled": func(h *AdminHandler) http.HandlerFunc { return h.ToggleDisabled },
		"ResetPassword":  func(h *AdminHandler) http.HandlerFunc { return h.ResetPassword },
	}
	for name, pick := range handlers {
		t.Run(name, func(t *testing.T) {
			h, _, _, applier := newQuotaTestEnv(t)
			writeProfile(t, applier, userID, "locked@example.com")

			form := url.Values{"display_name": {"x"}}
			r := httptest.NewRequest(http.MethodPost, "/admin/users/"+userID, strings.NewReader(form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			ctx := middleware.WithUserContext(r.Context(), "admin-id", "admin@example.com")
			ctx = middleware.WithAdminScopeContext(ctx, globalAdminScope())
			r = withChiParam(r.WithContext(ctx), "userID", userID)

			unlock := lockUser(userID)
			done := make(chan int, 1)
			go func() {
				w := httptest.NewRecorder()
				pick(h)(w, r)
				done <- w.Code
			}()

			select {
			case code := <-done:
				unlock()
				t.Fatalf("handler ran (status %d) while the user lock was held", code)
			case <-time.After(200 * time.Millisecond):
			}
			unlock()
			select {
			case code := <-done:
				// ResetPassword renders the new password in its response
				// instead of redirecting.
				want := http.StatusSeeOther
				if name == "ResetPassword" {
					want = http.StatusOK
				}
				if code != want {
					t.Fatalf("status %d after unlock, want %d", code, want)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("handler did not finish after unlock")
			}
		})
	}
}
