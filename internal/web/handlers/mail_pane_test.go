package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/bongo-project/bongo/internal/web/middleware"
)

// TestShowMessagePaneReceivesMailboxes: the reading pane (?pane=1) must be
// given the mailbox list, otherwise the Move menu is empty and Archive is
// never offered.
func TestShowMessagePaneReceivesMailboxes(t *testing.T) {
	_, ps, mailboxes, bs := setupViewSourceRouter(t, "user1")
	for _, name := range []string{"Archive", "Projects"} {
		if err := mailboxes.CreateMailbox("user1", name); err != nil {
			t.Fatal(err)
		}
	}
	raw := "From: Sender <sender@example.test>\r\nTo: user1@example.test\r\nSubject: Pane\r\n" +
		"Date: Sun, 22 Feb 2026 12:00:00 +0000\r\n\r\nBody.\r\n"
	uid := deliverRawMessage(t, mailboxes, bs, "user1", "Pane", raw)

	h := NewMailHandler(ps, bs, mailboxes, discardLogger())
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(middleware.WithUserContext(req.Context(), "user1", "user1@example.test")))
		})
	})
	r.Get("/mail/{mailbox}/{uid}", h.ShowMessage)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/mail/INBOX/%d?pane=1", uid), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{`<option value="Projects"`, `hx-vals="{&#34;destination&#34;:&#34;Archive&#34;}"`} {
		if !strings.Contains(body, want) {
			t.Errorf("pane response missing %q", want)
		}
	}
}
