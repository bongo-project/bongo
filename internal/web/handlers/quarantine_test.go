package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

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
	env := testEnv("msg-abc", "spammer@example.com", time.Now())
	env.Flags = pipeline.MsgFlagSpamChecked | pipeline.MsgFlagQuarantined
	seedQuarantine(t, spool, env)

	h := NewQuarantineHandler(spool, nil, discardLogger())
	req := quarantineRequest(t, http.MethodPost, "/admin/quarantine/msg-abc/release", nil,
		map[string]string{"id": "msg-abc"})
	rec := httptest.NewRecorder()

	h.ReleaseMessage(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}

	// Released messages move to incoming/ for delivery, not deliver/ (which is a terminal archive).
	if got := spoolCount(t, spool, "incoming"); got != 1 {
		t.Errorf("incoming/ count = %d, want 1", got)
	}
	if got := spoolCount(t, spool, "deliver"); got != 0 {
		t.Errorf("deliver/ count = %d, want 0 (released messages must go to incoming/, not deliver/)", got)
	}
	if got := spoolCount(t, spool, "quarantine"); got != 0 {
		t.Errorf("quarantine/ count = %d, want 0 after release", got)
	}

	// Verify the released message retains its checked flags (unlike rescan which clears them).
	released, err := spool.ReadEnvelope("incoming", "msg-abc")
	if err != nil {
		t.Fatalf("ReadEnvelope incoming: %v", err)
	}
	if !pipeline.HasFlag(released.Flags, pipeline.MsgFlagSpamChecked) {
		t.Error("expected MsgFlagSpamChecked to be retained after release (unlike rescan)")
	}
	if pipeline.HasFlag(released.Flags, pipeline.MsgFlagQuarantined) {
		t.Error("expected MsgFlagQuarantined to be cleared after release")
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
		env := testEnv(id, "spam@example.com", time.Now())
		env.Flags = pipeline.MsgFlagSpamChecked | pipeline.MsgFlagQuarantined
		seedQuarantine(t, spool, env)
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
	// Released messages move to incoming/ for delivery, not deliver/ (which is a terminal archive).
	if got := spoolCount(t, spool, "incoming"); got != 3 {
		t.Errorf("incoming/ count = %d, want 3", got)
	}
	if got := spoolCount(t, spool, "deliver"); got != 0 {
		t.Errorf("deliver/ count = %d, want 0 (released messages must go to incoming/, not deliver/)", got)
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

func TestBulkRescan(t *testing.T) {
	spool := newTestSpool(t)
	env := testEnv("rescan-1", "spam@example.com", time.Now())
	env.Flags = pipeline.MsgFlagVirusChecked | pipeline.MsgFlagVirus | pipeline.MsgFlagQuarantined
	env.Stages = []*bongov1.StageResult{
		{Stage: "antivirus", Action: "quarantine", Reasons: []string{"scanner unreachable"}},
	}
	seedQuarantine(t, spool, env)

	form := url.Values{}
	form.Set("action", "rescan")
	form.Add("ids", "rescan-1")

	h := NewQuarantineHandler(spool, nil, discardLogger())
	req := quarantineRequest(t, http.MethodPost, "/admin/quarantine/bulk",
		strings.NewReader(form.Encode()), nil)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	h.BulkAction(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if got := spoolCount(t, spool, "incoming"); got != 1 {
		t.Errorf("incoming/ count = %d, want 1", got)
	}
	if got := spoolCount(t, spool, "deliver"); got != 0 {
		t.Errorf("deliver/ count = %d, want 0 (rescan must not skip the pipeline)", got)
	}
	if got := spoolCount(t, spool, "quarantine"); got != 0 {
		t.Errorf("quarantine/ count = %d, want 0 after bulk rescan", got)
	}

	requeued, err := spool.ReadEnvelope("incoming", "rescan-1")
	if err != nil {
		t.Fatalf("ReadEnvelope incoming: %v", err)
	}
	if pipeline.HasFlag(requeued.Flags, pipeline.MsgFlagVirusChecked) {
		t.Error("expected MsgFlagVirusChecked to be cleared")
	}
	if pipeline.HasFlag(requeued.Flags, pipeline.MsgFlagVirus) {
		t.Error("expected MsgFlagVirus to be cleared")
	}
	if pipeline.HasFlag(requeued.Flags, pipeline.MsgFlagQuarantined) {
		t.Error("expected MsgFlagQuarantined to be cleared")
	}
	for _, sr := range requeued.Stages {
		if sr.Stage == "antivirus" || sr.Stage == "antispam" {
			t.Errorf("expected stale %s StageResult to be dropped, found %+v", sr.Stage, sr)
		}
	}
}

func TestBulkRescanNotFound(t *testing.T) {
	spool := newTestSpool(t)

	form := url.Values{}
	form.Set("action", "rescan")
	form.Add("ids", "ghost-id")

	h := NewQuarantineHandler(spool, nil, discardLogger())
	req := quarantineRequest(t, http.MethodPost, "/admin/quarantine/bulk",
		strings.NewReader(form.Encode()), nil)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	h.BulkAction(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want %d (redirect even on per-id failure)", rec.Code, http.StatusSeeOther)
	}
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "1+failed") {
		t.Errorf("Location = %q, want flash reporting the failure, not silent success", loc)
	}
	if got := spoolCount(t, spool, "incoming"); got != 0 {
		t.Errorf("incoming/ count = %d, want 0 for a nonexistent id", got)
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

// TestDeleteMessageWithBlob covers deleting a quarantined message that has a
// blob.
//
// The expectation here was INVERTED on 2026-08-23. It previously asserted the
// blob was deleted, which is what the handler did and what turned out to be a
// cross-user data-loss bug: the blob store is content-addressed and shared, so
// the hash of quarantined spam is the same hash as any byte-identical copy
// delivered to another user, and deleting it destroyed their mail body. The
// envelope is removed; reclaiming the blob belongs to the GC, which is the
// only component that sees the whole reference set. See
// TestQuarantineDeleteDoesNotDestroySharedBlob.
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
	// The envelope is gone from quarantine...
	if got := spoolCount(t, spool, quarantineDir); got != 0 {
		t.Errorf("quarantine still holds %d envelope(s) after delete, want 0", got)
	}
	// ...but the blob survives for the GC to reclaim once nothing references
	// it. Deleting it here would destroy any other user's byte-identical copy.
	if !blobs.Exists(hash) {
		t.Errorf("blob %s was deleted by the handler; a byte-identical message held "+
			"by another user would now be unreadable", hash)
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

// --- Release Behavior Tests ---

// TestReleaseMessagePreservesCheckedFlags verifies that releasing a message
// (unlike rescanning) preserves the checked flags. This is the critical
// difference: release means "I approve this", so we skip re-checking.
// Rescan means "check again", so we clear the checked flags.
func TestReleaseMessagePreservesCheckedFlags(t *testing.T) {
	spool := newTestSpool(t)

	// Create a quarantined message with both antispam and antivirus checked.
	env := testEnv("flags-test", "spam@example.com", time.Now())
	env.Flags = pipeline.MsgFlagSpamChecked | pipeline.MsgFlagSpam |
		pipeline.MsgFlagVirusChecked | pipeline.MsgFlagVirus |
		pipeline.MsgFlagQuarantined
	env.Stages = []*bongov1.StageResult{
		{Stage: "antispam", Action: "quarantine", Score: 0.95, Reasons: []string{"spam"}},
		{Stage: "antivirus", Action: "tag", Reasons: []string{"no virus"}},
	}
	seedQuarantine(t, spool, env)

	// Release the message.
	h := NewQuarantineHandler(spool, nil, discardLogger())
	req := quarantineRequest(t, http.MethodPost, "/admin/quarantine/flags-test/release", nil,
		map[string]string{"id": "flags-test"})
	rec := httptest.NewRecorder()
	h.ReleaseMessage(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("ReleaseMessage returned %d, expected redirect", rec.Code)
	}

	// Read the released message from incoming/.
	released, err := spool.ReadEnvelope("incoming", "flags-test")
	if err != nil {
		t.Fatalf("ReadEnvelope incoming: %v", err)
	}

	// CRITICAL ASSERTIONS for release behavior:
	// 1. Quarantine flag MUST be cleared.
	if pipeline.HasFlag(released.Flags, pipeline.MsgFlagQuarantined) {
		t.Error("MsgFlagQuarantined was not cleared — message is still marked quarantined")
	}

	// 2. Checked flags MUST be retained (unlike rescan).
	if !pipeline.HasFlag(released.Flags, pipeline.MsgFlagSpamChecked) {
		t.Error("MsgFlagSpamChecked was cleared — " +
			"this means release will re-run antispam and possibly re-quarantine the message")
	}
	if !pipeline.HasFlag(released.Flags, pipeline.MsgFlagVirusChecked) {
		t.Error("MsgFlagVirusChecked was cleared — " +
			"this means release will re-run antivirus")
	}

	// 3. Outcome flags MUST be retained (to reflect what was originally found).
	if !pipeline.HasFlag(released.Flags, pipeline.MsgFlagSpam) {
		t.Error("MsgFlagSpam was cleared")
	}
	if !pipeline.HasFlag(released.Flags, pipeline.MsgFlagVirus) {
		t.Error("MsgFlagVirus was cleared")
	}

	// 4. StageResults MUST be retained (as an audit record).
	if len(released.Stages) != 2 {
		t.Errorf("StageResults length = %d, want 2 (audit record of quarantine)", len(released.Stages))
	}
}

// TestReleaseVsRescanFlagClearing ensures that release and rescan behave
// differently in terms of flag clearing. This is the core difference between
// the two operations.
func TestReleaseVsRescanFlagClearing(t *testing.T) {
	spool := newTestSpool(t)

	// Create two identical quarantined messages.
	createMsg := func(id string) *bongov1.QueueEnvelope {
		env := testEnv(id, "spam@example.com", time.Now())
		env.Flags = pipeline.MsgFlagSpamChecked | pipeline.MsgFlagQuarantined
		env.Stages = []*bongov1.StageResult{
			{Stage: "antispam", Action: "quarantine", Score: 0.95, Reasons: []string{"spam"}},
		}
		return env
	}

	seedQuarantine(t, spool, createMsg("release-msg"))
	seedQuarantine(t, spool, createMsg("rescan-msg"))

	// Release one message.
	h := NewQuarantineHandler(spool, nil, discardLogger())
	req := quarantineRequest(t, http.MethodPost, "/admin/quarantine/release-msg/release", nil,
		map[string]string{"id": "release-msg"})
	rec := httptest.NewRecorder()
	h.ReleaseMessage(rec, req)

	// Rescan the other message.
	form := url.Values{}
	form.Set("action", "rescan")
	form.Add("ids", "rescan-msg")

	req = quarantineRequest(t, http.MethodPost, "/admin/quarantine/bulk",
		strings.NewReader(form.Encode()), nil)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	h.BulkAction(rec, req)

	// Check released message: should retain checked flag.
	released, _ := spool.ReadEnvelope("incoming", "release-msg")
	if !pipeline.HasFlag(released.Flags, pipeline.MsgFlagSpamChecked) {
		t.Error("Released message: MsgFlagSpamChecked was cleared (should be retained)")
	}

	// Check rescanned message: should NOT have checked flag.
	rescanned, _ := spool.ReadEnvelope("incoming", "rescan-msg")
	if pipeline.HasFlag(rescanned.Flags, pipeline.MsgFlagSpamChecked) {
		t.Error("Rescanned message: MsgFlagSpamChecked was retained (should be cleared)")
	}
}

// TestReleaseMessageReadFailure verifies that errors reading the envelope
// are properly propagated and reported in the flash message for bulk operations.
func TestReleaseMessageReadFailure(t *testing.T) {
	// Use a spool with no actual files to trigger a read failure.
	spool := newTestSpool(t)

	h := NewQuarantineHandler(spool, nil, discardLogger())
	req := quarantineRequest(t, http.MethodPost, "/admin/quarantine/nonexistent/release", nil,
		map[string]string{"id": "nonexistent"})
	rec := httptest.NewRecorder()

	h.ReleaseMessage(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("ReleaseMessage with missing envelope returned %d, want 500", rec.Code)
	}
}

// TestBulkReleasePartialFailure verifies that bulk release operations
// properly count successes and failures, and report them in the redirect.
func TestBulkReleasePartialFailure(t *testing.T) {
	spool := newTestSpool(t)
	// Create only 2 of the 3 messages to release.
	seedQuarantine(t, spool, testEnv("good-1", "spam@example.com", time.Now()))
	seedQuarantine(t, spool, testEnv("good-2", "spam@example.com", time.Now()))
	// "missing-1" is not created, so its release will fail.

	form := url.Values{}
	form.Set("action", "release")
	form.Add("ids", "good-1")
	form.Add("ids", "missing-1")
	form.Add("ids", "good-2")

	h := NewQuarantineHandler(spool, nil, discardLogger())
	req := quarantineRequest(t, http.MethodPost, "/admin/quarantine/bulk",
		strings.NewReader(form.Encode()), nil)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	h.BulkAction(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("BulkAction returned %d, want 303 redirect", rec.Code)
	}

	loc := rec.Header().Get("Location")
	// Should report 2 successes and 1 failure.
	if !strings.Contains(loc, "2+message(s)+released") {
		t.Errorf("Location = %q, want '2+message(s)+released'", loc)
	}
	if !strings.Contains(loc, "1+failed") {
		t.Errorf("Location = %q, want '1+failed'", loc)
	}
}

// --- Load-bearing delivery test ---
//
// Everything above this point proves the envelope moved to the right spool
// directory with the right flags. None of it proves the message is ever
// actually delivered — a flags-only or directory-only assertion would still
// pass against a runner that never picks the file up. The tests below drive
// a real pipeline.Runner against the spool the handler wrote to, so they
// fail if release (or rescan) ever regresses back to parking mail somewhere
// nothing reads.

// recordingProcessor is a Processor stub whose call counter is safe under
// -race. Unlike pipeline package's own mockProcessor (runner_test.go),
// which is only race-safe because those tests drive the unexported
// processMessage synchronously, this stub is driven through Runner.Run's
// worker goroutines, so calls must be counted atomically.
type recordingProcessor struct {
	name    string
	action  string
	setFlag uint32 // OR'd into env.Flags when this stage runs, e.g. MsgFlagDelivered.
	calls   atomic.Int64
}

func (p *recordingProcessor) Name() string { return p.name }

func (p *recordingProcessor) Process(_ context.Context, env *bongov1.QueueEnvelope, _ io.Reader) (*bongov1.StageResult, error) {
	p.calls.Add(1)
	if p.setFlag != 0 {
		env.Flags = pipeline.SetFlag(env.Flags, p.setFlag)
	}
	return &bongov1.StageResult{Stage: p.name, Action: p.action}, nil
}

// fixedBlobReader returns a fixed RFC 5322 message for any hash, mirroring
// pipeline package's mockBlobReader.
type fixedBlobReader struct{}

func (fixedBlobReader) ReadBlob(_ string) (io.ReadCloser, error) {
	const msg = "From: sender@example.com\r\nTo: rcpt@example.com\r\nSubject: test\r\n\r\nHello, world.\r\n"
	return io.NopCloser(strings.NewReader(msg)), nil
}

// waitForSpoolEntry polls dir for id up to timeout. It never fails the
// test itself — callers must cancel and join the runner before calling
// t.Fatal, or a timeout leaves the runner's poll goroutine running past
// the failing assertion.
func waitForSpoolEntry(spool *pipeline.SpoolStore, dir, id string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		envs, err := spool.List(dir)
		if err == nil {
			for _, e := range envs {
				if e.Id == id {
					return true
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// TestReleaseAndRescanThroughRunner is the load-bearing test for task 12.8:
// it proves a released message is actually picked up and delivered by the
// pipeline runner, not merely moved to a directory nobody reads. It also
// proves the critical release/rescan distinction: release must skip the
// antispam stage that already quarantined the message, while rescan must
// re-run it. Both the single-message route and the bulk action are
// exercised as subtests so neither path can silently diverge from the
// other.
func TestReleaseAndRescanThroughRunner(t *testing.T) {
	tests := []struct {
		name         string
		id           string
		trigger      func(t *testing.T, h *QuarantineHandler, id string)
		wantAntispam int64
		wantDeliver  int64
		checkMailbox bool // only release forces TargetMailbox to INBOX
	}{
		{
			name: "single-message release skips antispam",
			id:   "rel-single",
			trigger: func(t *testing.T, h *QuarantineHandler, id string) {
				t.Helper()
				req := quarantineRequest(t, http.MethodPost, "/admin/quarantine/"+id+"/release", nil,
					map[string]string{"id": id})
				rec := httptest.NewRecorder()
				h.ReleaseMessage(rec, req)
				if rec.Code != http.StatusSeeOther {
					t.Fatalf("ReleaseMessage status = %d, want %d", rec.Code, http.StatusSeeOther)
				}
			},
			wantAntispam: 0,
			wantDeliver:  1,
			checkMailbox: true,
		},
		{
			name: "bulk release skips antispam",
			id:   "rel-bulk",
			trigger: func(t *testing.T, h *QuarantineHandler, id string) {
				t.Helper()
				form := url.Values{}
				form.Set("action", "release")
				form.Add("ids", id)
				req := quarantineRequest(t, http.MethodPost, "/admin/quarantine/bulk",
					strings.NewReader(form.Encode()), nil)
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				rec := httptest.NewRecorder()
				h.BulkAction(rec, req)
				if rec.Code != http.StatusSeeOther {
					t.Fatalf("BulkAction status = %d, want %d", rec.Code, http.StatusSeeOther)
				}
			},
			wantAntispam: 0,
			wantDeliver:  1,
			checkMailbox: true,
		},
		{
			name: "bulk rescan re-runs antispam",
			id:   "rescan-runner",
			trigger: func(t *testing.T, h *QuarantineHandler, id string) {
				t.Helper()
				form := url.Values{}
				form.Set("action", "rescan")
				form.Add("ids", id)
				req := quarantineRequest(t, http.MethodPost, "/admin/quarantine/bulk",
					strings.NewReader(form.Encode()), nil)
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				rec := httptest.NewRecorder()
				h.BulkAction(rec, req)
				if rec.Code != http.StatusSeeOther {
					t.Fatalf("BulkAction status = %d, want %d", rec.Code, http.StatusSeeOther)
				}
			},
			wantAntispam: 1,
			wantDeliver:  1,
			checkMailbox: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spool := newTestSpool(t)

			// Seed a message quarantined by antispam: SpamChecked|Spam|Quarantined
			// flags set, plus the antispam StageResult that caused the quarantine.
			env := testEnv(tc.id, "spammer@example.com", time.Now())
			env.Flags = pipeline.MsgFlagSpamChecked | pipeline.MsgFlagSpam | pipeline.MsgFlagQuarantined
			env.BlobHash = "deadbeef"
			seedQuarantine(t, spool, env)

			h := NewQuarantineHandler(spool, nil, discardLogger())

			// Drive the real HTTP handler path (single-route or bulk).
			tc.trigger(t, h, tc.id)

			// Now drive the envelope through a real pipeline.Runner with
			// recording stub stages, exactly as production would.
			antispamStub := &recordingProcessor{name: "antispam", action: "allow"}
			deliverStub := &recordingProcessor{name: "deliver", action: "delivered", setFlag: pipeline.MsgFlagDelivered}

			runner := pipeline.NewRunner(
				spool,
				fixedBlobReader{},
				[]pipeline.Processor{antispamStub, deliverStub},
				discardLogger(),
				pipeline.WithWorkers(1),
				pipeline.WithPollInterval(5*time.Millisecond),
			)

			ctx, cancel := context.WithCancel(context.Background())
			runDone := make(chan struct{})
			go func() {
				defer close(runDone)
				_ = runner.Run(ctx)
			}()

			reached := waitForSpoolEntry(spool, "deliver", tc.id, 2*time.Second)

			// Cancel and join BEFORE reading counters or failing: Run's
			// worker goroutines write p.calls concurrently with the poll
			// loop above, and Run does wg.Wait() internally, so joining on
			// runDone is the race-free synchronization point.
			cancel()
			<-runDone

			if !reached {
				t.Fatalf("envelope %s did not reach deliver/ within deadline", tc.id)
			}

			if got := antispamStub.calls.Load(); got != tc.wantAntispam {
				t.Errorf("antispam stage calls = %d, want %d", got, tc.wantAntispam)
			}
			if got := deliverStub.calls.Load(); got != tc.wantDeliver {
				t.Errorf("deliver stage calls = %d, want %d", got, tc.wantDeliver)
			}

			delivered, err := spool.ReadEnvelope("deliver", tc.id)
			if err != nil {
				t.Fatalf("ReadEnvelope deliver: %v", err)
			}
			if !pipeline.HasFlag(delivered.Flags, pipeline.MsgFlagDelivered) {
				t.Error("delivered envelope missing MsgFlagDelivered")
			}
			if tc.checkMailbox && delivered.TargetMailbox != "INBOX" {
				t.Errorf("TargetMailbox = %q, want INBOX (released mail must not be silently filed to Junk)", delivered.TargetMailbox)
			}
		})
	}
}

// TestRequeueFromQuarantineRejectsIDMismatch guards against a legacy or
// corrupt envelope whose Id field does not match the requested spool
// filename: WriteEnvelope derives its filename from env.Id while Move
// operates on the URL/form id, so a mismatch would otherwise write the
// prepared envelope to a different file than the one that gets moved.
func TestRequeueFromQuarantineRejectsIDMismatch(t *testing.T) {
	dir := t.TempDir()
	spool, err := pipeline.NewSpoolStore(dir, discardLogger())
	if err != nil {
		t.Fatalf("NewSpoolStore: %v", err)
	}

	// Write a quarantine envelope file named "outer.env" whose Id field says
	// "inner" — simulates the mismatch. WriteEnvelope always derives the
	// filename from env.Id, so construct this directly on disk.
	env := testEnv("inner", "spammer@example.com", time.Now())
	env.Flags = pipeline.MsgFlagSpamChecked | pipeline.MsgFlagQuarantined
	data, err := proto.Marshal(env)
	if err != nil {
		t.Fatalf("proto.Marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "quarantine", "outer.env"), data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	h := NewQuarantineHandler(spool, nil, discardLogger())
	req := quarantineRequest(t, http.MethodPost, "/admin/quarantine/outer/release", nil,
		map[string]string{"id": "outer"})
	rec := httptest.NewRecorder()
	h.ReleaseMessage(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d for id/envelope mismatch", rec.Code, http.StatusInternalServerError)
	}
	// Neither file should have moved to incoming/.
	if got := spoolCount(t, spool, "incoming"); got != 0 {
		t.Errorf("incoming/ count = %d, want 0 (mismatch must not requeue anything)", got)
	}
}

// TestReleaseMessageRejectsPathTraversal ensures a crafted id containing
// path-traversal characters is rejected before it ever reaches the
// filesystem. id is user-supplied (URL param); ReadEnvelope's #nosec G304
// annotation only holds for genuinely internal spool names.
func TestReleaseMessageRejectsPathTraversal(t *testing.T) {
	tests := []string{"../../etc/passwd", "a/b", `a\b`, "..", ""}
	for _, id := range tests {
		t.Run(fmt.Sprintf("id=%q", id), func(t *testing.T) {
			spool := newTestSpool(t)
			h := NewQuarantineHandler(spool, nil, discardLogger())
			req := quarantineRequest(t, http.MethodPost, "/admin/quarantine/x/release", nil,
				map[string]string{"id": id})
			rec := httptest.NewRecorder()
			h.ReleaseMessage(rec, req)

			// A malformed id is a client error, not a server failure. The
			// important assertion is that it is rejected before reaching the
			// filesystem at all; the status code just reports that honestly.
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d for id %q", rec.Code, http.StatusBadRequest, id)
			}
		})
	}
}

// TestQuarantineDeleteDoesNotDestroySharedBlob is the cross-user data-loss
// guard.
//
// The blob store is content-addressed and shared across every user, so a
// quarantined message's hash is the SAME hash as any byte-identical copy
// delivered to somebody else. Spam is sent to many recipients at once, which
// makes that the common case, not a corner case. Deleting the blob when an
// admin clears quarantine therefore destroys other users' mail bodies while
// their metadata survives, leaving them an unreadable message and no
// indication of why.
//
// Reclamation belongs to the blob GC, which is the only component that sees
// the whole reference set. These tests assert the handler leaves the blob
// alone, for every path that removes a quarantined envelope.
func TestQuarantineDeleteDoesNotDestroySharedBlob(t *testing.T) {
	// The shared body: quarantined for one recipient, delivered to another.
	sharedBody := []byte("From: spammer@example.com\r\nSubject: buy pills\r\n\r\nbuy pills\r\n")

	newFixture := func(t *testing.T) (*QuarantineHandler, *store.BlobStore, string) {
		t.Helper()
		blobs, err := store.NewBlobStore(t.TempDir())
		if err != nil {
			t.Fatalf("NewBlobStore: %v", err)
		}
		hash, err := blobs.Put(sharedBody)
		if err != nil {
			t.Fatalf("Put shared blob: %v", err)
		}
		spool := newTestSpool(t)
		env := testEnv("msg-shared", "spammer@example.com", time.Now())
		env.BlobHash = hash
		seedQuarantine(t, spool, env)
		return NewQuarantineHandler(spool, blobs, discardLogger()), blobs, hash
	}

	assertSurvives := func(t *testing.T, blobs *store.BlobStore, hash, path string) {
		t.Helper()
		if !blobs.Exists(hash) {
			t.Fatalf("%s deleted a SHARED blob: another user holding a byte-identical "+
				"message now has metadata pointing at a blob that no longer exists", path)
		}
	}

	t.Run("single delete", func(t *testing.T) {
		h, blobs, hash := newFixture(t)
		req := quarantineRequest(t, http.MethodPost, "/admin/quarantine/msg-shared/delete",
			nil, map[string]string{"id": "msg-shared"})
		rec := httptest.NewRecorder()
		h.DeleteMessage(rec, req)

		if rec.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want 303", rec.Code)
		}
		assertSurvives(t, blobs, hash, "DeleteMessage")
	})

	t.Run("bulk delete", func(t *testing.T) {
		h, blobs, hash := newFixture(t)
		form := url.Values{"action": {"delete"}, "ids": {"msg-shared"}}
		req := quarantineRequest(t, http.MethodPost, "/admin/quarantine/bulk",
			strings.NewReader(form.Encode()), nil)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		h.BulkAction(rec, req)

		assertSurvives(t, blobs, hash, "BulkAction delete")
	})

	t.Run("auto-purge", func(t *testing.T) {
		blobs, err := store.NewBlobStore(t.TempDir())
		if err != nil {
			t.Fatalf("NewBlobStore: %v", err)
		}
		hash, err := blobs.Put(sharedBody)
		if err != nil {
			t.Fatalf("Put shared blob: %v", err)
		}
		spool := newTestSpool(t)
		// Old enough that the retention sweep takes it.
		env := testEnv("msg-old", "spammer@example.com", time.Now().Add(-48*time.Hour))
		env.BlobHash = hash
		seedQuarantine(t, spool, env)

		purgeQuarantine(spool, blobs, 24*time.Hour, discardLogger())

		// Auto-purge is the most dangerous of the three: it runs unattended on
		// a timer, so a destroyed blob has nobody watching.
		assertSurvives(t, blobs, hash, "auto-purge")
	})
}
