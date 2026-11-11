package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	bongoimap "github.com/bongo-project/bongo/internal/imap"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
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

// streamContext returns the context a long-lived stream should watch: it ends
// when the client goes away or when the server starts shutting down.
// http.Server.Shutdown waits for active requests and does not cancel them, so
// a stream that watched only r.Context() held every shutdown for the full
// drain timeout while a browser tab was open. Container runtimes give 10s
// before SIGKILL, which then landed before Raft and the stores were closed.
func streamContext(r *http.Request) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(r.Context())
	done := middleware.ServerShutdown(r.Context())
	if done == nil {
		return ctx, cancel
	}
	go func() {
		select {
		case <-done:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
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

// Events streams changes for the mailbox currently rendered by the browser and
// notification-worthy events from INBOX. Keeping those subscriptions separate
// prevents browsing Sent (or a nested folder) from disabling desktop alerts.
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

	mailbox := r.URL.Query().Get("mailbox")
	if mailbox == "" {
		mailbox = "INBOX"
	}
	if len(mailbox) > 255 || strings.ContainsAny(mailbox, "\x00\r\n") {
		http.Error(w, "Invalid mailbox", http.StatusBadRequest)
		return
	}

	viewed := h.notifier.Subscribe(userID, mailbox)
	defer h.notifier.Unsubscribe(viewed)
	var inbox chan bongoimap.Notification
	if mailbox != "INBOX" {
		inbox = h.notifier.Subscribe(userID, "INBOX")
		defer h.notifier.Unsubscribe(inbox)
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

	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()
	var (
		pending    []bongoimap.Notification
		burstTimer *time.Timer
		burstC     <-chan time.Time
	)
	defer func() {
		if burstTimer != nil {
			burstTimer.Stop()
		}
	}()

	ctx, cancel := streamContext(r)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return

		case notif, ok := <-viewed:
			if !ok {
				return
			}
			if notif.Type == bongoimap.NotifEventReminder {
				h.writeReminder(w, notif)
				flusher.Flush()
				continue
			}
			if mailbox == "INBOX" && notif.Type == bongoimap.NotifNewMail {
				h.writeNewMail(w, userID, notif)
				flusher.Flush()
			}
			pending = append(pending, notif)
			if burstTimer == nil {
				burstTimer = time.NewTimer(500 * time.Millisecond)
				burstC = burstTimer.C
			}

		case notif, ok := <-inbox:
			if !ok {
				return
			}
			h.writeInboxEvent(w, userID, notif)
			flusher.Flush()

		case <-burstC:
			h.writeMailboxBurst(w, mailbox, pending)
			pending = nil
			burstTimer = nil
			burstC = nil
			flusher.Flush()

		case <-heartbeat.C:
			fmt.Fprintf(w, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
}

func (h *SSEHandler) writeMailboxBurst(w http.ResponseWriter, mailbox string, notifications []bongoimap.Notification) {
	if len(notifications) > 20 {
		writeSSEJSON(w, "resync", map[string]string{"mailbox": mailbox})
		return
	}
	for _, notif := range notifications {
		action := ""
		switch notif.Type {
		case bongoimap.NotifNewMail, bongoimap.NotifFlagChange:
			action = "upsert"
		case bongoimap.NotifExpunge:
			action = "remove"
		}
		if action != "" {
			writeSSEJSON(w, "mailboxchange", map[string]any{"mailbox": mailbox, "uid": notif.UID, "action": action})
		}
	}
}

func (h *SSEHandler) writeInboxEvent(w http.ResponseWriter, userID string, notif bongoimap.Notification) {
	switch notif.Type {
	case bongoimap.NotifNewMail:
		h.writeNewMail(w, userID, notif)
	case bongoimap.NotifEventReminder:
		h.writeReminder(w, notif)
	case bongoimap.NotifFlagChange, bongoimap.NotifExpunge:
		writeSSEJSON(w, "mailboxcounts", map[string]string{"mailbox": "INBOX"})
	}
}

func (h *SSEHandler) writeNewMail(w http.ResponseWriter, userID string, notif bongoimap.Notification) {
	from, subject := h.loadMessageInfo(userID, "INBOX", notif.UID)
	writeSSEJSON(w, "newmail", map[string]any{
		"mailbox": "INBOX", "uid": notif.UID, "exists": notif.ExistsCount,
		"from": from, "subject": subject,
	})
}

func (h *SSEHandler) writeReminder(w http.ResponseWriter, notif bongoimap.Notification) {
	writeSSEJSON(w, "reminder", map[string]any{
		"title": notif.EventTitle, "start_time": notif.EventStart,
		"minutes_before": notif.MinutesBefore,
	})
}

func writeSSEJSON(w http.ResponseWriter, event string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
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
