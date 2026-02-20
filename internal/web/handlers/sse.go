package handlers

import (
	"fmt"
	"net/http"
	"time"

	bongoimap "github.com/bongo-go/bongo-go/internal/imap"
	"github.com/bongo-go/bongo-go/internal/web/middleware"
)

// SSEHandler handles Server-Sent Events for real-time updates.
type SSEHandler struct {
	notifier *bongoimap.Notifier
}

// NewSSEHandler creates an SSEHandler.
func NewSSEHandler(notifier *bongoimap.Notifier) *SSEHandler {
	return &SSEHandler{notifier: notifier}
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
				fmt.Fprintf(w, "event: newmail\ndata: {\"mailbox\":%q,\"uid\":%d,\"exists\":%d}\n\n",
					mailbox, notif.UID, notif.ExistsCount)
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
