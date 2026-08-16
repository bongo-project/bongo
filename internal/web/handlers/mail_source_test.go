package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/bongo-go/bongo-go/internal/store"
	"github.com/bongo-go/bongo-go/internal/web/middleware"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// setupViewSourceRouter wires up a MailHandler behind a router that injects
// userID as the authenticated user, mirroring the RequireAuth context
// available in production. Two independent mailbox stores exist only in the
// sense that DeliverMessage is called per-userID; ownership is enforced by
// the key prefix (mailboxMsgKey includes userID), exactly like ExportEML.
func setupViewSourceRouter(t *testing.T, userID string) (*chi.Mux, *store.PebbleStore, *store.MailboxStore, *store.BlobStore) {
	t.Helper()
	dir := t.TempDir()
	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	bs, err := store.NewBlobStore(dir + "/blobs")
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}
	mailboxes := store.NewMailboxStore(ps, &directApplierTags{ps: ps})
	if err := mailboxes.CreateMailbox(userID, "INBOX"); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}

	return viewSourceRouterFor(t, userID, ps, mailboxes, bs), ps, mailboxes, bs
}

// viewSourceRouterFor builds a router authenticated as userID against EXISTING
// stores. Tests that check cross-user access MUST use this rather than calling
// setupViewSourceRouter twice: separate stores make a 404 prove only that the
// message is absent, not that ownership was enforced.
func viewSourceRouterFor(t *testing.T, userID string, ps *store.PebbleStore, mailboxes *store.MailboxStore, bs *store.BlobStore) *chi.Mux {
	t.Helper()
	h := NewMailHandler(ps, bs, mailboxes, discardLogger())
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(middleware.WithUserContext(req.Context(), userID, userID+"@example.test")))
		})
	})
	r.Get("/mail/{mailbox}/{uid}/source", h.ViewSource)
	return r
}

// deliverRawMessage stores raw as a blob and delivers a MailMeta referencing
// it to userID's INBOX, returning the assigned UID.
func deliverRawMessage(t *testing.T, mailboxes *store.MailboxStore, bs *store.BlobStore, userID, subject, raw string) uint32 {
	t.Helper()
	hash, err := bs.Put([]byte(raw))
	if err != nil {
		t.Fatalf("blob Put: %v", err)
	}
	meta := &bongov1.MailMeta{
		Guid:       fmt.Sprintf("m-%s-%d", userID, len(raw)),
		Subject:    subject,
		RawMessage: &bongov1.BlobRef{Hash: hash},
	}
	uid, err := mailboxes.DeliverMessage(userID, "INBOX", meta, 0)
	if err != nil {
		t.Fatalf("DeliverMessage: %v", err)
	}
	return uid
}

// TestViewSourceRendersRawHeadersAndBody: 200, and the page contains the raw
// From/Subject headers plus the body inside a <pre>.
func TestViewSourceRendersRawHeadersAndBody(t *testing.T) {
	router, _, mailboxes, bs := setupViewSourceRouter(t, "user1")
	raw := "From: sender@example.test\r\n" +
		"To: user1@example.test\r\n" +
		"Subject: Hello there\r\n" +
		"Date: Sun, 22 Feb 2026 12:00:00 +0000\r\n" +
		"\r\n" +
		"This is the body text.\r\n"
	uid := deliverRawMessage(t, mailboxes, bs, "user1", "Hello there", raw)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/mail/INBOX/%d/source", uid), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "<pre") {
		t.Errorf("response missing <pre> element")
	}
	for _, want := range []string{"From: sender@example.test", "Subject: Hello there", "This is the body text."} {
		if !strings.Contains(body, want) {
			t.Errorf("response missing raw content %q", want)
		}
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

// TestViewSourceEscapesHTMLInSubject is the direct analogue of
// TestServeBlobInlineTextPreviewIsEscaped: a subject containing a script tag
// must render as visible escaped text and must never appear as executable
// markup in the response (stored XSS check).
func TestViewSourceEscapesHTMLInSubject(t *testing.T) {
	router, _, mailboxes, bs := setupViewSourceRouter(t, "user1")
	raw := "From: sender@example.test\r\n" +
		"To: user1@example.test\r\n" +
		"Subject: <script>alert(1)</script>\r\n" +
		"\r\n" +
		"body\r\n"
	uid := deliverRawMessage(t, mailboxes, bs, "user1", "<script>alert(1)</script>", raw)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/mail/INBOX/%d/source", uid), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Fatalf("response contains unescaped script tag (stored XSS): %q", body)
	}
	if !strings.Contains(body, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Errorf("response should HTML-escape the subject/source, got: %q", body)
	}
}

// TestViewSourceCrossUserReturns404: a UID that belongs to a different user
// returns 404 and leaks none of that message's content.
func TestViewSourceCrossUserReturns404(t *testing.T) {
	// Both users must share ONE store. Giving user2 its own store would make
	// this pass trivially — there would be no message at that UID for anyone,
	// so a 404 would prove nothing about ownership and the test would still
	// pass with the ownership check deleted entirely.
	router1, ps1, mailboxes1, bs1 := setupViewSourceRouter(t, "user1")
	raw := "From: sender@example.test\r\n" +
		"To: user1@example.test\r\n" +
		"Subject: Very Secret Subject\r\n" +
		"\r\n" +
		"Very secret body content.\r\n"
	uid := deliverRawMessage(t, mailboxes1, bs1, "user1", "Very Secret Subject", raw)

	// Positive control: the owner CAN read it at this UID, so a 404 for user2
	// below is attributable to ownership rather than to a missing message.
	reqOwner := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/mail/INBOX/%d/source", uid), nil)
	wOwner := httptest.NewRecorder()
	router1.ServeHTTP(wOwner, reqOwner)
	if wOwner.Code != http.StatusOK {
		t.Fatalf("owner status = %d, want 200 (setup is wrong; the cross-user assertion below would be vacuous)", wOwner.Code)
	}
	if !strings.Contains(wOwner.Body.String(), "Very Secret Subject") {
		t.Fatal("owner response did not contain the message; cross-user assertion would be vacuous")
	}

	// user2 authenticated against the SAME stores, asking for user1's UID.
	router2 := viewSourceRouterFor(t, "user2", ps1, mailboxes1, bs1)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/mail/INBOX/%d/source", uid), nil)
	w := httptest.NewRecorder()
	router2.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, "Very Secret Subject") || strings.Contains(body, "Very secret body content") {
		t.Fatalf("404 response leaked message content: %q", body)
	}
}

// TestViewSourceTruncatesLargeMessage: a message larger than the size cap
// renders the truncation notice, the export link, and a bounded body.
func TestViewSourceTruncatesLargeMessage(t *testing.T) {
	router, _, mailboxes, bs := setupViewSourceRouter(t, "user1")

	// Build a message well over messageSourceMaxBytes, made of repeated
	// newline-terminated lines so the cut point lands on a line boundary.
	var sb strings.Builder
	sb.WriteString("From: sender@example.test\r\nTo: user1@example.test\r\nSubject: Big\r\n\r\n")
	line := strings.Repeat("A", 200) + "\r\n"
	for sb.Len() < messageSourceMaxBytes*2 {
		sb.WriteString(line)
	}
	raw := sb.String()
	uid := deliverRawMessage(t, mailboxes, bs, "user1", "Big", raw)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/mail/INBOX/%d/source", uid), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "too large") && !strings.Contains(body, "Showing the beginning only") {
		t.Errorf("response missing truncation notice: %q", firstN(body, 500))
	}
	if !strings.Contains(body, fmt.Sprintf("/mail/INBOX/%d/export", uid)) {
		t.Errorf("response missing link to full export")
	}
	if len(body) > len(raw) {
		t.Errorf("truncated response (%d bytes) should be smaller than the full raw message (%d bytes)", len(body), len(raw))
	}
}

func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// TestTruncateMessageSource covers the truncation helper directly: line
// boundary cut, no truncation under the cap, and invalid UTF-8 scrubbing.
func TestTruncateMessageSource(t *testing.T) {
	tests := []struct {
		name          string
		raw           []byte
		maxBytes      int
		wantTruncated bool
	}{
		{
			name:          "under cap",
			raw:           []byte("short message"),
			maxBytes:      1000,
			wantTruncated: false,
		},
		{
			name:          "exact cap",
			raw:           []byte("12345"),
			maxBytes:      5,
			wantTruncated: false,
		},
		{
			name:          "over cap cuts at newline",
			raw:           []byte("line one\nline two\nline three\n"),
			maxBytes:      15,
			wantTruncated: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, truncated := truncateMessageSource(tt.raw, tt.maxBytes)
			if truncated != tt.wantTruncated {
				t.Errorf("truncated = %v, want %v", truncated, tt.wantTruncated)
			}
			if !truncated && got != string(tt.raw) {
				t.Errorf("untruncated result = %q, want %q", got, string(tt.raw))
			}
			// truncateMessageSource cuts to maxBytes and THEN runs
			// strings.ToValidUTF8, which replaces each invalid byte with a
			// 3-byte U+FFFD. So the result can legitimately exceed maxBytes
			// for non-ASCII input, and a strict `len(got) > maxBytes` check
			// would fail on a correct implementation. Bound it by the worst
			// case instead: every byte replaced by a 3-byte rune.
			if truncated && len(got) > tt.maxBytes*utf8.UTFMax {
				t.Errorf("truncated result length %d exceeds the worst-case bound %d (maxBytes %d)",
					len(got), tt.maxBytes*utf8.UTFMax, tt.maxBytes)
			}
			// The real invariant regardless of encoding: the output must be
			// shorter than the input, and valid UTF-8 fit for an HTML page.
			if truncated && len(got) >= len(tt.raw) {
				t.Errorf("truncated result length %d is not shorter than input %d", len(got), len(tt.raw))
			}
			if !utf8.ValidString(got) {
				t.Error("result is not valid UTF-8")
			}
		})
	}

	// A cut that lands mid-rune must not produce invalid UTF-8.
	raw := []byte("hello wörld this has a multibyte rune near the cut")
	// Cut right after the 2-byte 'ö' encoding, forcing a split if naive.
	cutAt := strings.Index(string(raw), "wörld") + len("w") + 1 // lands inside 'ö'
	got, _ := truncateMessageSource(raw, cutAt)
	if !utf8.ValidString(got) {
		t.Errorf("truncated result is not valid UTF-8: %q", got)
	}
}
