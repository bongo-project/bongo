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

// doDeleteRequest performs POST /mail/{mailbox}/{uid}/delete and returns the recorder.
func doDeleteRequest(t *testing.T, h *MailHandler, userID, mailbox string, uid uint32) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/mail/%s/%d/delete", mailbox, uid), nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("mailbox", mailbox)
	rctx.URLParams.Add("uid", fmt.Sprintf("%d", uid))
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	r = r.WithContext(middleware.WithUserContext(r.Context(), userID, userID+"@example.com"))

	w := httptest.NewRecorder()
	h.DeleteMessage(w, r)
	return w
}

func TestDeleteMessageMovesToTrash(t *testing.T) {
	env := newMailTagsTestEnv(t)
	userID := "user-del"
	uid := env.seedMessage(t, userID, "INBOX", "guid-move", nil)

	rec := doDeleteRequest(t, env.handler, userID, "INBOX", uid)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	inbox, err := env.mailboxes.ListMessages(userID, "INBOX")
	if err != nil {
		t.Fatalf("list INBOX: %v", err)
	}
	if len(inbox) != 0 {
		t.Errorf("INBOX has %d messages, want 0 after delete", len(inbox))
	}

	trash, err := env.mailboxes.ListMessages(userID, trashMailbox)
	if err != nil {
		t.Fatalf("list Trash: %v", err)
	}
	if len(trash) != 1 {
		t.Fatalf("Trash has %d messages, want 1 (moved from INBOX)", len(trash))
	}
	if trash[0].MailGuid != "guid-move" {
		t.Errorf("Trash message guid = %q, want %q", trash[0].MailGuid, "guid-move")
	}
}

func TestSentCopyIsNotCountedUnread(t *testing.T) {
	env := newMailTagsTestEnv(t)
	userID := "user-sent"

	// Mirror how the compose handler stores the Sent copy: delivered with the
	// Seen flag so it never inflates unread counts.
	meta := &bongov1.MailMeta{Guid: "sent-1", Subject: "Hi", ReceivedAt: time.Now().Unix()}
	if _, err := env.mailboxes.DeliverMessage(userID, "Sent", meta, store.FlagSeen); err != nil {
		t.Fatalf("DeliverMessage to Sent: %v", err)
	}

	_, _, unseen, err := env.mailboxes.CountMessages(userID, "Sent")
	if err != nil {
		t.Fatalf("CountMessages: %v", err)
	}
	if unseen != 0 {
		t.Errorf("Sent unseen = %d, want 0 (sent mail must not count as unread)", unseen)
	}
}

func TestDeleteMessageFromTrashIsPermanent(t *testing.T) {
	env := newMailTagsTestEnv(t)
	userID := "user-del2"
	uid := env.seedMessage(t, userID, trashMailbox, "guid-perm", nil)

	rec := doDeleteRequest(t, env.handler, userID, trashMailbox, uid)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	trash, err := env.mailboxes.ListMessages(userID, trashMailbox)
	if err != nil {
		t.Fatalf("list Trash: %v", err)
	}
	if len(trash) != 0 {
		t.Errorf("Trash has %d messages, want 0 after permanent delete", len(trash))
	}
}

func doBulkDeleteRequest(t *testing.T, h *MailHandler, userID, mailbox string, uids ...uint32) *httptest.ResponseRecorder {
	t.Helper()

	form := url.Values{}
	form.Set("action", "delete")
	for _, uid := range uids {
		form.Add("uids", fmt.Sprint(uid))
	}
	r := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/mail/%s/bulk", mailbox), strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("mailbox", mailbox)
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	r = r.WithContext(middleware.WithUserContext(r.Context(), userID, userID+"@example.com"))

	w := httptest.NewRecorder()
	h.BulkAction(w, r)
	return w
}

func TestBulkDeleteThreadMovesAllThreadMessagesToTrash(t *testing.T) {
	env := newMailTagsTestEnv(t)
	userID := "user-thread-delete"
	env.handler.SetThreadStore(store.NewThreadStore(env.ps, env.mailboxes))

	root := &bongov1.MailMeta{
		Guid:       "thread-root",
		MessageId:  "<thread-root@example.test>",
		Subject:    "Thread root",
		From:       "sender@example.test",
		ReceivedAt: time.Now().Add(-3 * time.Minute).Unix(),
	}
	reply1 := &bongov1.MailMeta{
		Guid:       "thread-reply-1",
		MessageId:  "<thread-reply-1@example.test>",
		InReplyTo:  root.MessageId,
		References: root.MessageId,
		Subject:    "Re: Thread root",
		From:       "reply1@example.test",
		ReceivedAt: time.Now().Add(-2 * time.Minute).Unix(),
	}
	reply2 := &bongov1.MailMeta{
		Guid:       "thread-reply-2",
		MessageId:  "<thread-reply-2@example.test>",
		InReplyTo:  root.MessageId,
		References: root.MessageId,
		Subject:    "Re: Thread root",
		From:       "reply2@example.test",
		ReceivedAt: time.Now().Add(-1 * time.Minute).Unix(),
	}

	rootUID, err := env.mailboxes.DeliverMessage(userID, "INBOX", root, 0)
	if err != nil {
		t.Fatalf("deliver root: %v", err)
	}
	if _, err := env.mailboxes.DeliverMessage(userID, "INBOX", reply1, 0); err != nil {
		t.Fatalf("deliver reply1: %v", err)
	}
	if _, err := env.mailboxes.DeliverMessage(userID, "INBOX", reply2, 0); err != nil {
		t.Fatalf("deliver reply2: %v", err)
	}

	rec := doBulkDeleteRequest(t, env.handler, userID, "INBOX", rootUID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	inbox, err := env.mailboxes.ListMessages(userID, "INBOX")
	if err != nil {
		t.Fatalf("list INBOX: %v", err)
	}
	if len(inbox) != 0 {
		t.Fatalf("INBOX has %d messages, want 0 after thread delete", len(inbox))
	}

	trash, err := env.mailboxes.ListMessages(userID, trashMailbox)
	if err != nil {
		t.Fatalf("list Trash: %v", err)
	}
	if len(trash) != 3 {
		t.Fatalf("Trash has %d messages, want 3", len(trash))
	}
}

func TestEmptyTrashPermanentlyDeletesAllTrashMessages(t *testing.T) {
	env := newMailTagsTestEnv(t)
	userID := "user-empty-trash"
	for i := 0; i < 3; i++ {
		env.seedMessage(t, userID, trashMailbox, fmt.Sprintf("trash-%d", i), nil)
	}

	r := httptest.NewRequest(http.MethodPost, "/mail/Trash/empty-trash", nil)
	r = r.WithContext(middleware.WithUserContext(r.Context(), userID, userID+"@example.com"))
	w := httptest.NewRecorder()
	env.handler.EmptyTrash(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("HX-Redirect"); loc != "/mail/Trash?flash=Trash+emptied" {
		t.Fatalf("HX-Redirect = %q, want Trash emptied redirect", loc)
	}
	trash, err := env.mailboxes.ListMessages(userID, trashMailbox)
	if err != nil {
		t.Fatalf("list Trash: %v", err)
	}
	if len(trash) != 0 {
		t.Fatalf("Trash has %d messages, want 0", len(trash))
	}
}
