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

// disableStreamTimeouts clears the server's read/write deadlines for a
// long-lived streaming response (SSE). Without this, http.Server.WriteTimeout
// forcibly resets the connection mid-stream after 60s, which browsers surface
// as ERR_HTTP2_PROTOCOL_ERROR before the EventSource reconnects in a tight loop.
// It is a no-op on writers that do not support deadlines (e.g. test recorders).
func disableStreamTimeouts(w http.ResponseWriter) {
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})
	_ = rc.SetReadDeadline(time.Time{})
}

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

	// Long-lived stream: opt out of the server's WriteTimeout so the connection
	// is not reset mid-stream.
	disableStreamTimeouts(w)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no") // proxy no-buffer; Connection header is illegal over HTTP/2

	// Flush headers immediately so the HTTP/2 stream is established before blocking.
	// Without this, reverse proxies (e.g. Traefik) may buffer the response and the
	// browser receives ERR_HTTP2_PROTOCOL_ERROR when the connection eventually closes.
	fmt.Fprintf(w, ": ping\n\n")
	flusher.Flush()

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
			case bongoimap.NotifEventReminder:
				fmt.Fprintf(w, "event: reminder\ndata: {\"title\":%q,\"start_time\":%d,\"minutes_before\":%d}\n\n",
					notif.EventTitle, notif.EventStart, notif.MinutesBefore)
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
