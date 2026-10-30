package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/bongo-project/bongo/internal/web/middleware"
)

func TestMailboxRedirect(t *testing.T) {
	for _, tc := range []struct {
		mailbox, flash, want string
	}{
		{"INBOX", "Done", "/mail/INBOX?flash=Done"},
		{"INBOX/Important", "Message moved to Trash", "/mail/INBOX%2FImportant?flash=Message+moved+to+Trash"},
		{"Work Stuff", "Moved to A&B", "/mail/Work%20Stuff?flash=Moved+to+A%26B"},
	} {
		if got := mailboxRedirect(tc.mailbox, tc.flash); got != tc.want {
			t.Errorf("mailboxRedirect(%q, %q) = %q, want %q", tc.mailbox, tc.flash, got, tc.want)
		}
	}
}

// After a delete or move from a subfolder the browser was sent to
// /mail/INBOX/Important, which the router reads as message "Important" in
// INBOX and answers with "Invalid UID".
func TestDeleteAndMoveFromSubfolderRedirectToSubfolder(t *testing.T) {
	const userID = "user-sub"
	const mailbox = "INBOX/Important"
	request := func(path string, form url.Values, uid uint32) *http.Request {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("mailbox", url.PathEscape(mailbox))
		rctx.URLParams.Add("uid", fmt.Sprint(uid))
		r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
		return r.WithContext(middleware.WithUserContext(r.Context(), userID, userID+"@example.com"))
	}

	env := newMailTagsTestEnv(t)
	uid := env.seedMessage(t, userID, mailbox, "guid-sub-del", nil)
	w := httptest.NewRecorder()
	env.handler.DeleteMessage(w, request("/mail/x/1/delete", nil, uid))
	if loc := w.Header().Get("HX-Redirect"); !strings.HasPrefix(loc, "/mail/INBOX%2FImportant?") {
		t.Errorf("delete redirect = %q, want the encoded subfolder", loc)
	}

	if err := env.mailboxes.CreateMailbox(userID, "Archive"); err != nil {
		t.Fatalf("create Archive: %v", err)
	}
	uid = env.seedMessage(t, userID, mailbox, "guid-sub-move", nil)
	w = httptest.NewRecorder()
	env.handler.MoveMessage(w, request("/mail/x/1/move", url.Values{"destination": {"Archive"}}, uid))
	if loc := w.Header().Get("HX-Redirect"); loc != "/mail/INBOX%2FImportant?flash=Moved+to+Archive" {
		t.Errorf("move redirect = %q, want the encoded subfolder", loc)
	}
}
