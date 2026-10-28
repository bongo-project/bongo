package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/bongo-project/bongo/internal/web/middleware"
)

func getUnreadCounts(t *testing.T, h *MailHandler, userID string) unreadCountsResponse {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/mail/unread-counts", nil)
	r = r.WithContext(middleware.WithUserContext(r.Context(), userID, userID+"@example.com"))
	w := httptest.NewRecorder()
	h.UnreadCounts(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("unread-counts status = %d", w.Code)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	var got unreadCountsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, w.Body.String())
	}
	return got
}

func doBulk(t *testing.T, h *MailHandler, userID, mailbox, action string, uids ...uint32) {
	t.Helper()
	form := url.Values{"action": {action}}
	for _, uid := range uids {
		form.Add("uids", fmt.Sprint(uid))
	}
	r := httptest.NewRequest(http.MethodPost, "/mail/"+mailbox+"/bulk", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("mailbox", mailbox)
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	r = r.WithContext(middleware.WithUserContext(r.Context(), userID, userID+"@example.com"))
	w := httptest.NewRecorder()
	h.BulkAction(w, r)
	if w.Code >= 300 {
		t.Fatalf("bulk %s: status %d (%s)", action, w.Code, w.Body.String())
	}
}

// TestUnreadCountsFollowMailActions: the sidebar re-reads this endpoint after
// every mail action, so it must reflect each one, with INBOX including its
// subfolders as the sidebar tree does and the badge counting INBOX alone.
func TestUnreadCountsFollowMailActions(t *testing.T) {
	env := newMailTagsTestEnv(t)
	h, user := env.handler, "user-counts"
	a := env.seedMessage(t, user, "INBOX", "g-a", nil)
	b := env.seedMessage(t, user, "INBOX", "g-b", nil)
	env.seedMessage(t, user, "INBOX/Receipts", "g-r", nil)
	env.seedMessage(t, user, "Archive", "g-x", nil)

	check := func(step string, want map[string]int, inbox int) {
		t.Helper()
		got := getUnreadCounts(t, h, user)
		for mb, n := range want {
			if got.Mailboxes[mb] != n {
				t.Errorf("%s: %s = %d, want %d (all: %v)", step, mb, got.Mailboxes[mb], n, got.Mailboxes)
			}
		}
		if got.Inbox != inbox {
			t.Errorf("%s: inbox badge = %d, want %d", step, got.Inbox, inbox)
		}
	}

	check("seeded", map[string]int{"INBOX": 3, "INBOX/Receipts": 1, "Archive": 1}, 2)

	doBulk(t, h, user, "INBOX", "mark-read", a)
	check("mark-read", map[string]int{"INBOX": 2, "INBOX/Receipts": 1}, 1)

	doBulk(t, h, user, "INBOX", "mark-unread", a)
	check("mark-unread", map[string]int{"INBOX": 3}, 2)

	if rec := doDeleteRequest(t, h, user, "INBOX", b); rec.Code != http.StatusOK {
		t.Fatalf("delete: status %d", rec.Code)
	}
	check("delete", map[string]int{"INBOX": 2, trashMailbox: 1}, 1)

	doBulk(t, h, user, "Archive", "mark-read", 1)
	check("archive read", map[string]int{"Archive": 0}, 1)
}
