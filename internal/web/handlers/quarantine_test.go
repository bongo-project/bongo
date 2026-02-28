package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/bongo-go/bongo-go/internal/pipeline"
	"github.com/bongo-go/bongo-go/internal/store"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// newTestSpool creates a SpoolStore in a temp dir.
func newTestSpool(t *testing.T) *pipeline.SpoolStore {
	t.Helper()
	spool, err := pipeline.NewSpoolStore(t.TempDir(), discardLogger())
	if err != nil {
		t.Fatalf("NewSpoolStore: %v", err)
	}
	return spool
}

// seedQuarantine writes a QueueEnvelope into spool/quarantine/.
func seedQuarantine(t *testing.T, spool *pipeline.SpoolStore, env *bongov1.QueueEnvelope) {
	t.Helper()
	if err := spool.WriteEnvelope("quarantine", env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}
}

// testEnv returns a minimal QueueEnvelope for quarantine testing.
func testEnv(id, sender string, receivedAt time.Time) *bongov1.QueueEnvelope {
	return &bongov1.QueueEnvelope{
		Id:         id,
		Sender:     sender,
		Recipients: []string{"recipient@example.com"},
		ReceivedAt: receivedAt.Unix(),
		Stages: []*bongov1.StageResult{
			{
				Stage:   "antispam",
				Action:  "quarantine",
				Score:   0.92,
				Reasons: []string{"dnsbl match", "high score"},
			},
		},
	}
}

// spoolCount returns the number of envelopes in a spool directory.
func spoolCount(t *testing.T, spool *pipeline.SpoolStore, dir string) int {
	t.Helper()
	envs, err := spool.List(dir)
	if err != nil {
		t.Fatalf("spool.List(%q): %v", dir, err)
	}
	return len(envs)
}

// quarantineRequest builds an http.Request with chi params and user context injected.
func quarantineRequest(t *testing.T, method, path string, body *strings.Reader, chiParams map[string]string) *http.Request {
	t.Helper()
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, path, body)
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req = withUserContext(req, "uid-admin", "admin@example.com")
	for k, v := range chiParams {
		req = withChiParam(req, k, v)
	}
	return req
}

// --- Tests ---

func TestListQuarantine(t *testing.T) {
	spool := newTestSpool(t)
	seedQuarantine(t, spool, testEnv("msg-1", "spam1@example.com", time.Now()))
	seedQuarantine(t, spool, testEnv("msg-2", "spam2@example.com", time.Now()))

	h := NewQuarantineHandler(spool, nil, discardLogger())
	req := quarantineRequest(t, http.MethodGet, "/admin/quarantine", nil, nil)
	rec := httptest.NewRecorder()

	h.ListQuarantine(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "spam1@example.com") {
		t.Errorf("body missing spam1@example.com")
	}
	if !strings.Contains(body, "spam2@example.com") {
		t.Errorf("body missing spam2@example.com")
	}
}

func TestListQuarantineEmpty(t *testing.T) {
	spool := newTestSpool(t)
	h := NewQuarantineHandler(spool, nil, discardLogger())
	req := quarantineRequest(t, http.MethodGet, "/admin/quarantine", nil, nil)
	rec := httptest.NewRecorder()

	h.ListQuarantine(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), "No quarantined messages") {
		t.Errorf("expected empty-state text in response")
	}
}

func TestListQuarantineNilSpool(t *testing.T) {
	h := NewQuarantineHandler(nil, nil, discardLogger())
	req := quarantineRequest(t, http.MethodGet, "/admin/quarantine", nil, nil)
	rec := httptest.NewRecorder()

	// Should render successfully without panicking.
	h.ListQuarantine(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestReleaseMessage(t *testing.T) {
	spool := newTestSpool(t)
	seedQuarantine(t, spool, testEnv("msg-abc", "spammer@example.com", time.Now()))

	h := NewQuarantineHandler(spool, nil, discardLogger())
	req := quarantineRequest(t, http.MethodPost, "/admin/quarantine/msg-abc/release", nil,
		map[string]string{"id": "msg-abc"})
	rec := httptest.NewRecorder()

	h.ReleaseMessage(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}

	if got := spoolCount(t, spool, "deliver"); got != 1 {
		t.Errorf("deliver/ count = %d, want 1", got)
	}
	if got := spoolCount(t, spool, "quarantine"); got != 0 {
		t.Errorf("quarantine/ count = %d, want 0 after release", got)
	}
}

func TestReleaseMessageNotFound(t *testing.T) {
	spool := newTestSpool(t)
	h := NewQuarantineHandler(spool, nil, discardLogger())
	req := quarantineRequest(t, http.MethodPost, "/admin/quarantine/nonexistent/release", nil,
		map[string]string{"id": "nonexistent"})
	rec := httptest.NewRecorder()

	h.ReleaseMessage(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d for missing envelope", rec.Code, http.StatusInternalServerError)
	}
}

func TestDeleteMessage(t *testing.T) {
	spool := newTestSpool(t)
	seedQuarantine(t, spool, testEnv("msg-del", "bad@example.com", time.Now()))

	h := NewQuarantineHandler(spool, nil, discardLogger())
	req := quarantineRequest(t, http.MethodPost, "/admin/quarantine/msg-del/delete", nil,
		map[string]string{"id": "msg-del"})
	rec := httptest.NewRecorder()

	h.DeleteMessage(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if got := spoolCount(t, spool, "quarantine"); got != 0 {
		t.Errorf("quarantine/ count = %d, want 0 after delete", got)
	}
}

func TestDeleteMessageNotFound(t *testing.T) {
	spool := newTestSpool(t)
	h := NewQuarantineHandler(spool, nil, discardLogger())
	req := quarantineRequest(t, http.MethodPost, "/admin/quarantine/ghost/delete", nil,
		map[string]string{"id": "ghost"})
	rec := httptest.NewRecorder()

	h.DeleteMessage(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d for missing envelope", rec.Code, http.StatusInternalServerError)
	}
}

func TestBulkRelease(t *testing.T) {
	spool := newTestSpool(t)
	ids := []string{"bulk-1", "bulk-2", "bulk-3"}
	for _, id := range ids {
		seedQuarantine(t, spool, testEnv(id, "spam@example.com", time.Now()))
	}

	form := url.Values{}
	form.Set("action", "release")
	for _, id := range ids {
		form.Add("ids", id)
	}

	h := NewQuarantineHandler(spool, nil, discardLogger())
	req := quarantineRequest(t, http.MethodPost, "/admin/quarantine/bulk",
		strings.NewReader(form.Encode()), nil)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	h.BulkAction(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if got := spoolCount(t, spool, "deliver"); got != 3 {
		t.Errorf("deliver/ count = %d, want 3", got)
	}
	if got := spoolCount(t, spool, "quarantine"); got != 0 {
		t.Errorf("quarantine/ count = %d, want 0 after bulk release", got)
	}
}

func TestBulkDelete(t *testing.T) {
	spool := newTestSpool(t)
	ids := []string{"del-1", "del-2"}
	for _, id := range ids {
		seedQuarantine(t, spool, testEnv(id, "junk@example.com", time.Now()))
	}

	form := url.Values{}
	form.Set("action", "delete")
	for _, id := range ids {
		form.Add("ids", id)
	}

	h := NewQuarantineHandler(spool, nil, discardLogger())
	req := quarantineRequest(t, http.MethodPost, "/admin/quarantine/bulk",
		strings.NewReader(form.Encode()), nil)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	h.BulkAction(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if got := spoolCount(t, spool, "quarantine"); got != 0 {
		t.Errorf("quarantine/ count = %d, want 0 after bulk delete", got)
	}
}

func TestBulkActionNoIDs(t *testing.T) {
	spool := newTestSpool(t)
	form := url.Values{}
	form.Set("action", "release")

	h := NewQuarantineHandler(spool, nil, discardLogger())
	req := quarantineRequest(t, http.MethodPost, "/admin/quarantine/bulk",
		strings.NewReader(form.Encode()), nil)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	h.BulkAction(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want %d (no IDs → redirect, not error)", rec.Code, http.StatusSeeOther)
	}
}

func TestBulkActionUnknownAction(t *testing.T) {
	spool := newTestSpool(t)
	seedQuarantine(t, spool, testEnv("msg-x", "x@example.com", time.Now()))

	form := url.Values{}
	form.Set("action", "requeue")
	form.Add("ids", "msg-x")

	h := NewQuarantineHandler(spool, nil, discardLogger())
	req := quarantineRequest(t, http.MethodPost, "/admin/quarantine/bulk",
		strings.NewReader(form.Encode()), nil)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	h.BulkAction(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d for unknown action", rec.Code, http.StatusBadRequest)
	}
}

func TestRunAutoPurge(t *testing.T) {
	spool := newTestSpool(t)

	// Old message: received 40 days ago.
	old := testEnv("old-msg", "old@example.com", time.Now().Add(-40*24*time.Hour))
	// Recent message: received 1 hour ago.
	recent := testEnv("recent-msg", "recent@example.com", time.Now().Add(-time.Hour))

	seedQuarantine(t, spool, old)
	seedQuarantine(t, spool, recent)

	// Call purge directly with 30-day retention.
	purgeQuarantine(spool, nil, 30*24*time.Hour, discardLogger())

	remaining, err := spool.List("quarantine")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(remaining) != 1 {
		t.Fatalf("expected 1 remaining message, got %d", len(remaining))
	}
	if remaining[0].Id != "recent-msg" {
		t.Errorf("remaining = %q, want recent-msg", remaining[0].Id)
	}
}

func TestRunAutoPurgeCancellation(t *testing.T) {
	spool := newTestSpool(t)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		defer close(done)
		RunAutoPurge(ctx, spool, nil, 30*24*time.Hour, 50*time.Millisecond, discardLogger())
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("RunAutoPurge did not exit within 2s after context cancellation")
	}
}

func TestExtractSubject(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "plain subject",
			raw:  "From: a@b.com\r\nSubject: Hello World\r\n\r\nBody text",
			want: "Hello World",
		},
		{
			name: "no subject header",
			raw:  "From: a@b.com\r\n\r\nBody text",
			want: "",
		},
		{
			name: "subject with leading whitespace stripped",
			raw:  "From: a@b.com\r\nSubject:   Trimmed Value\r\n\r\nBody",
			want: "Trimmed Value",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Write the raw message to a temp BlobStore.
			dataDir := t.TempDir()
			blobs, err := store.NewBlobStore(dataDir)
			if err != nil {
				t.Fatalf("NewBlobStore: %v", err)
			}

			hash, err := blobs.Put([]byte(tc.raw))
			if err != nil {
				t.Fatalf("blobs.Put: %v", err)
			}

			h := NewQuarantineHandler(nil, blobs, discardLogger())
			got := h.extractSubject(hash)
			if got != tc.want {
				t.Errorf("extractSubject = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEnvToEntry(t *testing.T) {
	spool := newTestSpool(t)
	h := NewQuarantineHandler(spool, nil, discardLogger())

	now := time.Now()
	env := &bongov1.QueueEnvelope{
		Id:         "e-1",
		Sender:     "s@example.com",
		Recipients: []string{"a@example.com", "b@example.com"},
		ReceivedAt: now.Unix(),
		Stages: []*bongov1.StageResult{
			{Stage: "antispam", Action: "quarantine", Score: 0.95, Reasons: []string{"dnsbl", "high score"}},
		},
	}

	entry := h.envToEntry(env)

	if entry.ID != "e-1" {
		t.Errorf("ID = %q, want e-1", entry.ID)
	}
	if entry.Sender != "s@example.com" {
		t.Errorf("Sender = %q", entry.Sender)
	}
	if !strings.Contains(entry.Recipients, "a@example.com") {
		t.Errorf("Recipients = %q missing a@example.com", entry.Recipients)
	}
	if entry.Stage != "antispam" {
		t.Errorf("Stage = %q, want antispam", entry.Stage)
	}
	if entry.Score != 0.95 {
		t.Errorf("Score = %f, want 0.95", entry.Score)
	}
	if !strings.Contains(entry.Reason, "dnsbl") {
		t.Errorf("Reason = %q, should contain 'dnsbl'", entry.Reason)
	}
}

func TestDeleteMessageWithBlob(t *testing.T) {
	spool := newTestSpool(t)
	dataDir := t.TempDir()
	blobs, err := store.NewBlobStore(dataDir)
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}

	rawMsg := []byte("From: a@b.com\r\nSubject: Test\r\n\r\nBody")
	hash, err := blobs.Put(rawMsg)
	if err != nil {
		t.Fatalf("blobs.Put: %v", err)
	}

	env := testEnv("msg-blob", "spam@example.com", time.Now())
	env.BlobHash = hash
	seedQuarantine(t, spool, env)

	h := NewQuarantineHandler(spool, blobs, discardLogger())
	req := quarantineRequest(t, http.MethodPost, "/admin/quarantine/msg-blob/delete", nil,
		map[string]string{"id": "msg-blob"})
	rec := httptest.NewRecorder()

	h.DeleteMessage(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	// Blob should be deleted.
	if blobs.Exists(hash) {
		t.Errorf("blob %s still exists after delete", hash)
	}
}

// sha256HexTest computes SHA-256 hex — used for verifying blob filenames in tests.
func sha256HexTest(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// TestTruncate verifies the truncate helper.
func TestTruncate(t *testing.T) {
	tests := []struct {
		name  string
		input string
		n     int
		want  string
	}{
		{"short", "hello", 10, "hello"},
		{"exact", "hello", 5, "hello"},
		{"truncated", "hello world", 5, "hello..."},
		{"empty", "", 5, ""},
		{"unicode safe", "日本語テスト", 4, "日本語テ..."},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := truncate(tc.input, tc.n)
			if got != tc.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tc.input, tc.n, got, tc.want)
			}
		})
	}
}

// TestQuarantineScoreClass verifies CSS class selection by score.
func TestQuarantineScoreClass(t *testing.T) {
	tests := []struct {
		score float32
		want  string
	}{
		{0.9, "badge badge-disabled"},
		{0.8, "badge badge-disabled"},
		{0.6, "badge badge-pending"},
		{0.5, "badge badge-pending"},
		{0.3, "badge"},
		{0.0, "badge"},
	}
	for _, tc := range tests {
		t.Run(fmt.Sprintf("score=%.1f", tc.score), func(t *testing.T) {
			got := quarantineScoreClass(tc.score)
			if got != tc.want {
				t.Errorf("quarantineScoreClass(%f) = %q, want %q", tc.score, got, tc.want)
			}
		})
	}
}

