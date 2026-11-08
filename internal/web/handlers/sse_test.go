package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	bongoimap "github.com/bongo-project/bongo/internal/imap"
	"github.com/bongo-project/bongo/internal/web/middleware"
)

type sseTestWriter struct {
	mu      sync.Mutex
	header  http.Header
	body    strings.Builder
	flushes chan string
}

func newSSETestWriter() *sseTestWriter {
	return &sseTestWriter{header: make(http.Header), flushes: make(chan string, 16)}
}

func (w *sseTestWriter) Header() http.Header { return w.header }
func (w *sseTestWriter) WriteHeader(int)     {}
func (w *sseTestWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.body.Write(p)
}
func (w *sseTestWriter) Flush() {
	w.mu.Lock()
	snapshot := w.body.String()
	w.mu.Unlock()
	select {
	case w.flushes <- snapshot:
	default:
	}
}

func waitSSEContains(t *testing.T, w *sseTestWriter, want string) string {
	t.Helper()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case body := <-w.flushes:
			if strings.Contains(body, want) {
				return body
			}
		case <-timer.C:
			t.Fatalf("SSE stream never contained %q", want)
		}
	}
}

func TestSSEFollowsViewedMailboxAndKeepsInboxNotifications(t *testing.T) {
	notifier := bongoimap.NewNotifier()
	h := NewSSEHandler(notifier, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req := httptest.NewRequest(http.MethodGet, "/events?mailbox="+url.QueryEscape("Projects/Alpha"), nil).WithContext(ctx)
	req = req.WithContext(middleware.WithUserContext(req.Context(), "u1", "u1@example.test"))
	w := newSSETestWriter()
	done := make(chan struct{})
	go func() {
		h.Events(w, req)
		close(done)
	}()
	waitSSEContains(t, w, ": ping")

	notifier.Notify("u1", "Projects/Alpha", bongoimap.Notification{Type: bongoimap.NotifNewMail, UID: 7})
	body := waitSSEContains(t, w, `"mailbox":"Projects/Alpha"`)
	if !strings.Contains(body, `event: mailboxchange`) || !strings.Contains(body, `"action":"upsert"`) {
		t.Fatalf("viewed mailbox did not produce an upsert: %s", body)
	}
	if strings.Contains(body, "event: newmail") {
		t.Fatalf("non-INBOX delivery produced a desktop notification: %s", body)
	}

	notifier.Notify("u1", "INBOX", bongoimap.Notification{Type: bongoimap.NotifNewMail, UID: 9})
	body = waitSSEContains(t, w, "event: newmail")
	if strings.Count(body, "event: mailboxchange") != 1 {
		t.Fatalf("INBOX delivery patched a different viewed mailbox: %s", body)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("SSE handler did not stop after cancellation")
	}
}

func TestSSERejectsControlCharactersInMailbox(t *testing.T) {
	h := NewSSEHandler(bongoimap.NewNotifier(), nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/events?mailbox=bad%0Aname", nil)
	req = req.WithContext(middleware.WithUserContext(req.Context(), "u1", "u1@example.test"))
	rec := httptest.NewRecorder()
	h.Events(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestSSELargeBurstEmitsOneResync(t *testing.T) {
	notifier := bongoimap.NewNotifier()
	h := NewSSEHandler(notifier, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req := httptest.NewRequest(http.MethodGet, "/events?mailbox=Archive", nil).WithContext(ctx)
	req = req.WithContext(middleware.WithUserContext(req.Context(), "u1", "u1@example.test"))
	w := newSSETestWriter()
	done := make(chan struct{})
	go func() {
		h.Events(w, req)
		close(done)
	}()
	waitSSEContains(t, w, ": ping")

	for uid := uint32(1); uid <= 21; uid++ {
		notifier.Notify("u1", "Archive", bongoimap.Notification{Type: bongoimap.NotifFlagChange, UID: uid})
	}
	body := waitSSEContains(t, w, "event: resync")
	if strings.Count(body, "event: resync") != 1 {
		t.Fatalf("resync events = %d, want 1: %s", strings.Count(body, "event: resync"), body)
	}
	if strings.Contains(body, "event: mailboxchange") {
		t.Fatalf("large burst emitted individual row changes: %s", body)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("SSE handler did not stop after cancellation")
	}
}

func TestSSEEmitsFlagUpdatesAndExpunges(t *testing.T) {
	notifier := bongoimap.NewNotifier()
	h := NewSSEHandler(notifier, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req := httptest.NewRequest(http.MethodGet, "/events?mailbox=INBOX", nil).WithContext(ctx)
	req = req.WithContext(middleware.WithUserContext(req.Context(), "u1", "u1@example.test"))
	w := newSSETestWriter()
	done := make(chan struct{})
	go func() {
		h.Events(w, req)
		close(done)
	}()
	waitSSEContains(t, w, ": ping")

	notifier.Notify("u1", "INBOX", bongoimap.Notification{Type: bongoimap.NotifFlagChange, UID: 4})
	notifier.Notify("u1", "INBOX", bongoimap.Notification{Type: bongoimap.NotifExpunge, UID: 8})
	body := waitSSEContains(t, w, `"uid":8`)
	for _, want := range []string{
		"event: mailboxchange\ndata: {\"action\":\"upsert\",\"mailbox\":\"INBOX\",\"uid\":4}",
		"event: mailboxchange\ndata: {\"action\":\"remove\",\"mailbox\":\"INBOX\",\"uid\":8}",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("SSE body missing %q: %s", want, body)
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("SSE handler did not stop after cancellation")
	}
}
