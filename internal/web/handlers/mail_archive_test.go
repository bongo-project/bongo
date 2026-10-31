package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// failMoveApplier fails every composite mailbox move and passes the rest on.
type failMoveApplier struct{ *directApplierTags }

func (a failMoveApplier) ApplyCommand(cmd *bongov1.RaftCommand, d time.Duration) error {
	if strings.HasPrefix(string(cmd.Key), "/__mailbox_move__/") {
		return fmt.Errorf("raft unavailable")
	}
	return a.directApplierTags.ApplyCommand(cmd, d)
}

func doArchiveRequest(h *MailHandler, userID, verb, mailbox, uid string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/mail/"+url.PathEscape(mailbox)+"/"+uid+"/"+verb, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("mailbox", mailbox)
	rctx.URLParams.Add("uid", uid)
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	r = r.WithContext(middleware.WithUserContext(r.Context(), userID, userID+"@example.com"))
	w := httptest.NewRecorder()
	if verb == "archive" {
		h.ArchiveMessage(w, r)
	} else {
		h.UnarchiveMessage(w, r)
	}
	return w
}

func doBulkRequest(h *MailHandler, userID, mailbox, action string, uids ...uint32) *httptest.ResponseRecorder {
	form := url.Values{"action": {action}}
	for _, u := range uids {
		form.Add("uids", fmt.Sprint(u))
	}
	r := httptest.NewRequest(http.MethodPost, "/mail/"+mailbox+"/bulk", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("mailbox", mailbox)
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	r = r.WithContext(middleware.WithUserContext(r.Context(), userID, userID+"@example.com"))
	w := httptest.NewRecorder()
	h.BulkAction(w, r)
	return w
}

func countIn(t *testing.T, env *mailTagsTestEnv, userID, mailbox string) int {
	t.Helper()
	msgs, err := env.mailboxes.ListMessages(userID, mailbox)
	if err != nil {
		t.Fatalf("list %s: %v", mailbox, err)
	}
	return len(msgs)
}

func TestArchiveMessage(t *testing.T) {
	tests := []struct {
		name      string
		src       string // real mailbox the message is seeded in
		urlBox    string // mailbox in the URL (may be virtual)
		verb      string
		flagged   bool
		wantCode  int
		wantInbox int
		wantArch  int
	}{
		{"archive creates Archive when absent", "INBOX", "INBOX", "archive", false, 200, 0, 1},
		{"archive from a virtual view uses the real mailbox", "INBOX", "starred", "archive", true, 200, 0, 1},
		{"archive in Archive is rejected", "Archive", "Archive", "archive", false, 400, 0, 1},
		{"unarchive moves to INBOX", "Archive", "Archive", "unarchive", false, 200, 1, 0},
		{"unarchive from a virtual view uses the real mailbox", "Archive", "starred", "unarchive", true, 200, 1, 0},
		{"unarchive outside Archive is rejected", "INBOX", "INBOX", "unarchive", false, 400, 1, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newMailTagsTestEnv(t)
			user := "user-arch"
			uid := env.seedMessage(t, user, tt.src, "guid-a", nil)
			// INBOX always exists in production; the move does not create it.
			_ = env.mailboxes.CreateMailbox(user, "INBOX")
			if tt.flagged {
				if err := env.mailboxes.AddFlags(user, tt.src, uid, store.FlagFlagged); err != nil {
					t.Fatal(err)
				}
			}
			rec := doArchiveRequest(env.handler, user, tt.verb, tt.urlBox, fmt.Sprint(uid))
			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tt.wantCode, rec.Body.String())
			}
			if got := countIn(t, env, user, "INBOX"); got != tt.wantInbox {
				t.Errorf("INBOX = %d, want %d", got, tt.wantInbox)
			}
			if got := countIn(t, env, user, "Archive"); got != tt.wantArch {
				t.Errorf("Archive = %d, want %d", got, tt.wantArch)
			}
			if tt.wantCode == 200 && rec.Header().Get("HX-Redirect") == "" {
				t.Error("no HX-Redirect on success")
			}
		})
	}
}

func TestArchiveMessageInvalidUID(t *testing.T) {
	env := newMailTagsTestEnv(t)
	if rec := doArchiveRequest(env.handler, "u", "archive", "INBOX", "abc"); rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestArchiveMessageFailedMoveFailsRequest(t *testing.T) {
	for _, verb := range []string{"archive", "unarchive"} {
		t.Run(verb, func(t *testing.T) {
			env := newMailTagsTestEnv(t)
			user := "user-fail"
			src := "INBOX"
			if verb == "unarchive" {
				src = "Archive"
			}
			uid := env.seedMessage(t, user, src, "guid-f", nil)
			// Same Pebble, but moves fail.
			env.handler.mailboxes = store.NewMailboxStore(env.ps, failMoveApplier{env.applier})

			rec := doArchiveRequest(env.handler, user, verb, src, fmt.Sprint(uid))
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500", rec.Code)
			}
			if rec.Header().Get("HX-Redirect") != "" {
				t.Error("failed move must not redirect as if it succeeded")
			}
			if got := countIn(t, env, user, src); got != 1 {
				t.Errorf("message left %s despite the failed move", src)
			}
		})
	}
}

func TestBulkArchive(t *testing.T) {
	env := newMailTagsTestEnv(t)
	user := "user-bulk"
	a := env.seedMessage(t, user, "INBOX", "g1", nil)
	b := env.seedMessage(t, user, "INBOX", "g2", nil)
	for _, uid := range []uint32{a, b} {
		if err := env.mailboxes.AddFlags(user, "INBOX", uid, store.FlagFlagged); err != nil {
			t.Fatal(err)
		}
	}

	// Archive from the starred view: the source mailbox is INBOX, not "starred".
	if rec := doBulkRequest(env.handler, user, "starred", "archive", a, b); rec.Code != http.StatusOK {
		t.Fatalf("bulk archive status = %d (%s)", rec.Code, rec.Body.String())
	}
	if got := countIn(t, env, user, "Archive"); got != 2 {
		t.Fatalf("Archive = %d, want 2", got)
	}
	if got := countIn(t, env, user, "INBOX"); got != 0 {
		t.Errorf("INBOX = %d, want 0", got)
	}

	// Unarchive moves back only the selected message.
	arch, _ := env.mailboxes.ListMessages(user, "Archive")
	if rec := doBulkRequest(env.handler, user, "Archive", "unarchive", arch[0].Uid); rec.Code != http.StatusOK {
		t.Fatalf("bulk unarchive status = %d", rec.Code)
	}
	if got := countIn(t, env, user, "INBOX"); got != 1 {
		t.Errorf("INBOX = %d, want 1", got)
	}
	if got := countIn(t, env, user, "Archive"); got != 1 {
		t.Errorf("Archive = %d, want 1", got)
	}
}

func TestBulkArchiveFailedMoveFailsRequest(t *testing.T) {
	env := newMailTagsTestEnv(t)
	user := "user-bulkfail"
	uid := env.seedMessage(t, user, "INBOX", "g1", nil)
	env.handler.mailboxes = store.NewMailboxStore(env.ps, failMoveApplier{env.applier})
	if rec := doBulkRequest(env.handler, user, "INBOX", "archive", uid); rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestBulkArchiveUnknownUIDFails(t *testing.T) {
	env := newMailTagsTestEnv(t)
	if rec := doBulkRequest(env.handler, "u", "starred", "archive", 99); rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
