package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bongo-go/bongo-go/internal/store"
	"github.com/bongo-go/bongo-go/internal/web/middleware"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
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
