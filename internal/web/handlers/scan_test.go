package handlers

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- minimal fakes for ScanHandler tests ---

// fakeScanMailboxes records calls and returns configured data.
type fakeScanMailboxes struct {
	mu       sync.Mutex
	mailbox  string // the mailbox that "exists"
	messages []fakeMsg
	moved    []string // UIDs moved
}

type fakeMsg struct {
	uid     uint32
	guid    string
	mailbox string
}

func (f *fakeScanMailboxes) GetMailbox(userID, name string) (interface{}, error) {
	if name == f.mailbox {
		return struct{}{}, nil
	}
	return nil, errNotFound
}

func (f *fakeScanMailboxes) ListMessages(userID, mailbox string) ([]fakeMsg, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.messages, nil
}

func (f *fakeScanMailboxes) MoveMessage(userID, mailbox string, uid uint32, dest string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.moved = append(f.moved, dest)
	return nil
}

// scanTestHandler builds a ScanHandler wired to simple in-memory stubs that
// don't require real Pebble or blob storage. Scan logic is tested via the
// exported HTTP surface (StartScan, CancelScan, ScanProgress).
func scanTestHandler() *ScanHandler {
	return &ScanHandler{
		pebble:      nil,
		blobs:       nil,
		mailboxes:   nil,
		applier:     nil,
		scorer:      nil,
		logger:      discardLogger(),
		activeScans: make(map[string]*scanJob),
	}
}

// TestStartScanReturns202 verifies that StartScan returns 202 when a scan key
// does not already exist. Because the handler requires real stores for the
// mailbox existence check we test the conflict path (existing job) directly.
func TestStartScanAlreadyRunning(t *testing.T) {
	h := scanTestHandler()

	// Pre-insert a fake running job so we can hit the 409 path without needing
	// real storage dependencies.
	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	key := scanKey("user1", "INBOX")
	progressCh := make(chan ScanProgress, 1)
	h.mu.Lock()
	h.activeScans[key] = &scanJob{
		cancel:   cancel,
		progress: progressCh,
	}
	h.mu.Unlock()

	// Build a chi request with the mailbox URL param.
	req := httptest.NewRequest(http.MethodPost, "/api/mailbox/INBOX/scan", nil)
	req = withChiParam(req, "mailbox", "INBOX")
	req = withUserContext(req, "user1", "user1@example.com")

	rec := httptest.NewRecorder()
	h.StartScan(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d (conflict)", rec.Code, http.StatusConflict)
	}
}

// TestCancelScan verifies that CancelScan returns 204 when a scan is running
// and 404 when no scan is running.
func TestCancelScan(t *testing.T) {
	t.Run("cancels running scan", func(t *testing.T) {
		h := scanTestHandler()

		cancelled := make(chan struct{})
		ctx, cancel := context.WithCancel(context.Background())

		wrappedCancel := func() {
			cancel()
			close(cancelled)
		}

		key := scanKey("user1", "INBOX")
		progressCh := make(chan ScanProgress, 1)
		h.mu.Lock()
		h.activeScans[key] = &scanJob{
			cancel:   wrappedCancel,
			progress: progressCh,
		}
		h.mu.Unlock()

		req := httptest.NewRequest(http.MethodDelete, "/api/mailbox/INBOX/scan", nil)
		req = withChiParam(req, "mailbox", "INBOX")
		req = withUserContext(req, "user1", "user1@example.com")

		rec := httptest.NewRecorder()
		h.CancelScan(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusNoContent)
		}

		// Ensure context was actually cancelled.
		select {
		case <-cancelled:
		case <-time.After(time.Second):
			t.Error("cancel was not called within timeout")
		}
		_ = ctx
	})

	t.Run("returns 404 when no scan running", func(t *testing.T) {
		h := scanTestHandler()

		req := httptest.NewRequest(http.MethodDelete, "/api/mailbox/INBOX/scan", nil)
		req = withChiParam(req, "mailbox", "INBOX")
		req = withUserContext(req, "user1", "user1@example.com")

		rec := httptest.NewRecorder()
		h.CancelScan(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
		}
	})
}

// TestScanProgress verifies that SSE events are received from an active scan job.
func TestScanProgress(t *testing.T) {
	h := scanTestHandler()

	key := scanKey("user1", "INBOX")
	progressCh := make(chan ScanProgress, 4)
	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	h.mu.Lock()
	h.activeScans[key] = &scanJob{
		cancel:   cancel,
		progress: progressCh,
	}
	h.mu.Unlock()

	// Send a progress event and a done event.
	progressCh <- ScanProgress{Scanned: 5, Spam: 1, Total: 10, Done: false}
	progressCh <- ScanProgress{Scanned: 10, Spam: 2, Total: 10, Done: true}

	req := httptest.NewRequest(http.MethodGet, "/api/mailbox/INBOX/scan/progress", nil)
	req = withChiParam(req, "mailbox", "INBOX")
	req = withUserContext(req, "user1", "user1@example.com")

	rec := newFlushRecorder()
	h.ScanProgress(rec, req)

	body := rec.Body.String()
	lines := strings.Split(strings.TrimSpace(body), "\n")

	var events []ScanProgress
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		var p ScanProgress
		if err := json.Unmarshal([]byte(payload), &p); err != nil {
			t.Fatalf("unmarshal SSE data %q: %v", payload, err)
		}
		events = append(events, p)
	}

	if len(events) < 2 {
		t.Fatalf("expected at least 2 SSE events, got %d\nbody:\n%s", len(events), body)
	}

	if events[0].Scanned != 5 || events[0].Spam != 1 || events[0].Total != 10 {
		t.Errorf("first event = %+v, want {5 1 10 false}", events[0])
	}
	if !events[len(events)-1].Done {
		t.Errorf("last event should have Done=true")
	}
}

// TestScanProgressNoScan verifies 404 when no scan is running.
func TestScanProgressNoScan(t *testing.T) {
	h := scanTestHandler()

	req := httptest.NewRequest(http.MethodGet, "/api/mailbox/INBOX/scan/progress", nil)
	req = withChiParam(req, "mailbox", "INBOX")
	req = withUserContext(req, "user1", "user1@example.com")

	rec := httptest.NewRecorder()
	h.ScanProgress(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// TestScanKeyUniqueness verifies that different user+mailbox combos get
// separate keys so scans don't collide.
func TestScanKeyUniqueness(t *testing.T) {
	tests := []struct {
		userID  string
		mailbox string
		key     string
	}{
		{"u1", "INBOX", "u1:INBOX"},
		{"u2", "INBOX", "u2:INBOX"},
		{"u1", "Junk", "u1:Junk"},
	}
	for _, tc := range tests {
		got := scanKey(tc.userID, tc.mailbox)
		if got != tc.key {
			t.Errorf("scanKey(%q, %q) = %q, want %q", tc.userID, tc.mailbox, got, tc.key)
		}
	}
}

// --- helpers ---

// flushRecorder is an httptest.ResponseRecorder that also implements http.Flusher.
type flushRecorder struct {
	*httptest.ResponseRecorder
	flushed int
}

func newFlushRecorder() *flushRecorder {
	return &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
}

func (f *flushRecorder) Flush() {
	f.flushed++
	f.ResponseRecorder.Flush()
}

// lineScanner returns a bufio.Scanner over the response body, scanning line by line.
func lineScanner(rec *flushRecorder) *bufio.Scanner {
	return bufio.NewScanner(strings.NewReader(rec.Body.String()))
}

var _ = lineScanner // prevent unused warning
