package handlers

import (
	"fmt"
	"net/http"
	"time"

	bongoimap "github.com/bongo-go/bongo-go/internal/imap"
	"github.com/bongo-go/bongo-go/internal/store"
	"github.com/bongo-go/bongo-go/internal/web/middleware"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// SSEHandler handles Server-Sent Events for real-time updates.
type SSEHandler struct {
	notifier  *bongoimap.Notifier
	pebble    *store.PebbleStore
	mailboxes *store.MailboxStore
}

// NewSSEHandler creates an SSEHandler.
func NewSSEHandler(notifier *bongoimap.Notifier, pebble *store.PebbleStore, mailboxes *store.MailboxStore) *SSEHandler {
	return &SSEHandler{notifier: notifier, pebble: pebble, mailboxes: mailboxes}
}

// Events streams SSE events for the authenticated user's INBOX.
func (h *SSEHandler) Events(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	if userID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Subscribe to INBOX by default.
	mailbox := r.URL.Query().Get("mailbox")
	if mailbox == "" {
		mailbox = "INBOX"
	}

	ch := h.notifier.Subscribe(userID, mailbox)
	defer h.notifier.Unsubscribe(ch)

	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	ctx := r.Context()

	for {
		select {
		case <-ctx.Done():
			return

		case notif, ok := <-ch:
			if !ok {
				return
			}

			switch notif.Type {
			case bongoimap.NotifNewMail:
				from, subject := h.loadMessageInfo(userID, mailbox, notif.UID)
				fmt.Fprintf(w, "event: newmail\ndata: {\"mailbox\":%q,\"uid\":%d,\"exists\":%d,\"from\":%q,\"subject\":%q}\n\n",
					mailbox, notif.UID, notif.ExistsCount, from, subject)

				// Send OOB badge update for sidebar unread count.
				if h.mailboxes != nil {
					unread := h.mailboxes.TotalUnreadCount(userID)
					if unread > 0 {
						fmt.Fprintf(w, "event: oob\ndata: <span id=\"unread-badge\" class=\"unread-badge\" hx-swap-oob=\"true\">%d</span>\n\n", unread)
					} else {
						fmt.Fprintf(w, "event: oob\ndata: <span id=\"unread-badge\" class=\"unread-badge\" hx-swap-oob=\"true\"></span>\n\n")
					}
				}
			case bongoimap.NotifExpunge:
				fmt.Fprintf(w, "event: expunge\ndata: {\"mailbox\":%q,\"uid\":%d,\"seq\":%d}\n\n",
					mailbox, notif.UID, notif.SeqNum)
			case bongoimap.NotifFlagChange:
				fmt.Fprintf(w, "event: flagchange\ndata: {\"mailbox\":%q,\"uid\":%d,\"flags\":%d}\n\n",
					mailbox, notif.UID, notif.Flags)
			}
			flusher.Flush()

		case <-heartbeat.C:
			fmt.Fprintf(w, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
}

// loadMessageInfo loads from and subject for a message by UID.
func (h *SSEHandler) loadMessageInfo(userID, mailbox string, uid uint32) (string, string) {
	if h.mailboxes == nil || h.pebble == nil {
		return "", ""
	}

	msg, err := h.mailboxes.GetMessage(userID, mailbox, uid)
	if err != nil || msg == nil {
		return "", ""
	}

	key := fmt.Sprintf("/user/%s/mail/%s", userID, msg.MailGuid)
	data, err := h.pebble.Get([]byte(key))
	if err != nil {
		return "", ""
	}

	var meta bongov1.MailMeta
	if err := proto.Unmarshal(data, &meta); err != nil {
		return "", ""
	}
	return meta.From, meta.Subject
}
