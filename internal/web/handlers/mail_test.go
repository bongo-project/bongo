package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-go/bongo-go/internal/store"
	"github.com/bongo-go/bongo-go/internal/web/middleware"
	"github.com/bongo-go/bongo-go/internal/web/templates"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

func TestBlockRemoteImages(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantBlocked bool
		wantContain string
		wantAbsent  string
	}{
		{
			name:        "no images",
			input:       `<p>Hello world</p>`,
			wantBlocked: false,
			wantContain: "Hello world",
		},
		{
			name:        "http remote image blocked",
			input:       `<img src="http://example.com/tracker.gif">`,
			wantBlocked: true,
			wantContain: `class="blocked-image"`,
			wantAbsent:  `<img`,
		},
		{
			name:        "https remote image blocked",
			input:       `<img src="https://example.com/image.png" alt="test">`,
			wantBlocked: true,
			wantContain: `data-src="https://example.com/image.png"`,
			wantAbsent:  `<img`,
		},
		{
			name:        "cid inline image preserved",
			input:       `<img src="cid:abc123@example.com">`,
			wantBlocked: false,
			wantContain: `<img`,
		},
		{
			name:        "data uri inline image preserved",
			input:       `<img src="data:image/png;base64,abc123">`,
			wantBlocked: false,
			wantContain: `<img`,
		},
		{
			name:        "mixed: remote blocked cid preserved",
			input:       `<img src="cid:part1"><img src="https://tracker.example.com/px.gif">`,
			wantBlocked: true,
			wantContain: `cid:part1`,
		},
		{
			name:        "single-quoted src",
			input:       `<img src='https://example.com/image.png'>`,
			wantBlocked: true,
			wantContain: `class="blocked-image"`,
		},
		{
			name:        "original url stored in data-src",
			input:       `<img src="https://example.com/track.gif">`,
			wantBlocked: true,
			wantContain: `[Remote image blocked]`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, blocked := blockRemoteImages(tc.input)
			if blocked != tc.wantBlocked {
				t.Errorf("blocked = %v, want %v", blocked, tc.wantBlocked)
			}
			if tc.wantContain != "" && !strings.Contains(got, tc.wantContain) {
				t.Errorf("output does not contain %q\ngot: %s", tc.wantContain, got)
			}
			if tc.wantAbsent != "" && strings.Contains(got, tc.wantAbsent) {
				t.Errorf("output should not contain %q\ngot: %s", tc.wantAbsent, got)
			}
		})
	}
}

func TestHTMLAttachmentsAreNotSafeForInlineHTMXPreview(t *testing.T) {
	if safeInlineTypes["text/html"] {
		t.Fatal("text/html attachments must not be swapped into the authenticated DOM as inline HTMX previews")
	}
}

const serveBlobTestUser = "test-user"

func setupServeBlobRouter(t *testing.T) (*chi.Mux, *store.BlobStore, *store.PebbleStore) {
	t.Helper()
	dir := t.TempDir()
	bs, err := store.NewBlobStore(dir)
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}
	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })

	h := NewMailHandler(ps, bs, nil, discardLogger())
	r := chi.NewRouter()
	// Inject an authenticated user so ServeBlob's ownership check can run.
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := middleware.WithUserContext(req.Context(), serveBlobTestUser, "test@localhost")
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Get("/mail/blob/{hash}", h.ServeBlob)
	return r, bs, ps
}

// ownBlobForTestUser writes a MailMeta owned by serveBlobTestUser that
// references hash as an attachment, so ServeBlob's ownership check passes.
func ownBlobForTestUser(t *testing.T, ps *store.PebbleStore, hash string) {
	t.Helper()
	meta := &bongov1.MailMeta{
		Guid:        "test-guid-" + hash[:8],
		Attachments: []*bongov1.BlobRef{{Hash: hash}},
	}
	data, err := proto.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal meta: %v", err)
	}
	key := fmt.Sprintf("/user/%s/mail/%s", serveBlobTestUser, meta.Guid)
	if err := ps.Set([]byte(key), data); err != nil {
		t.Fatalf("store meta: %v", err)
	}
}

func TestServeBlobInvalidHash(t *testing.T) {
	router, _, _ := setupServeBlobRouter(t)

	// Note: empty hash and path traversal sequences are rejected by chi's router
	// before reaching the handler (chi normalises paths). The store-level
	// tests cover those inputs. Here we test hashes that do route to the
	// handler but fail IsValidHash.
	badHashes := []struct {
		name string
		hash string
	}{
		{"short", "abc"},
		{"63 chars", "deadbeef0000000000000000000000000000000000000000000000000000dea"},
		{"65 chars", "deadbeef0000000000000000000000000000000000000000000000000000deadf"},
		{"uppercase", "DEADBEEF0000000000000000000000000000000000000000000000000000DEAD"},
		{"percent encoded", "%2e%2e%2f%2e%2e%2fetc%2fpasswd000000000000000000000000000000000000"},
		{"non-hex chars", "zzzzzzzz0000000000000000000000000000000000000000000000000000zzzz"},
	}

	for _, tc := range badHashes {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/mail/blob/"+tc.hash, nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Errorf("ServeBlob(%q): status = %d, want %d", tc.hash, w.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestServeBlobValidHashNotFound(t *testing.T) {
	router, _, ps := setupServeBlobRouter(t)

	hash := "0000000000000000000000000000000000000000000000000000000000000000"
	// Own the hash so we exercise the blob-missing path, not the ownership check.
	ownBlobForTestUser(t, ps, hash)
	req := httptest.NewRequest(http.MethodGet, "/mail/blob/"+hash, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Valid, owned hash but blob does not exist — 404, not 400.
	if w.Code != http.StatusNotFound {
		t.Errorf("ServeBlob valid-not-found: status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestServeBlobRequiresOwnership(t *testing.T) {
	router, bs, _ := setupServeBlobRouter(t)

	// A blob exists in the shared store but the requesting user owns no message
	// referencing it — it must not be served.
	hash, err := bs.Put([]byte("someone else's private attachment"))
	if err != nil {
		t.Fatalf("blob put: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/mail/blob/"+hash, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		t.Fatal("blob served to a user who owns no message referencing it")
	}
	if w.Code != http.StatusNotFound {
		t.Errorf("unowned blob: status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestServeBlobHTMLAttachmentIsNotRenderedInline(t *testing.T) {
	router, bs, ps := setupServeBlobRouter(t)

	payload := []byte("<script>alert(document.cookie)</script>")
	hash, err := bs.Put(payload)
	if err != nil {
		t.Fatalf("blob put: %v", err)
	}
	ownBlobForTestUser(t, ps, hash)

	req := httptest.NewRequest(http.MethodGet, "/mail/blob/"+hash+"?inline=1&ct=text/html", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if got := w.Header().Get("Content-Disposition"); got != "attachment" {
		t.Errorf("HTML attachment Content-Disposition = %q, want %q (must not render inline)", got, "attachment")
	}
	if ct := w.Header().Get("Content-Type"); strings.Contains(ct, "text/html") {
		t.Errorf("HTML attachment Content-Type = %q, must not be served as text/html inline", ct)
	}
	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
}

func TestListMessagesPaginatesAfterThreadGrouping(t *testing.T) {
	dir := t.TempDir()
	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })

	applier := &directApplierTags{ps: ps}
	mailboxes := store.NewMailboxStore(ps, applier)
	if err := mailboxes.CreateMailbox("user1", "Sent"); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}

	rootMessageID := "<root@example.test>"
	var newestUID uint32
	for i := 0; i < 62; i++ {
		guid := fmt.Sprintf("thread-message-%02d", i)
		meta := &bongov1.MailMeta{
			Guid:       guid,
			Subject:    "Re: test Lance",
			From:       "admin@example.com",
			MessageId:  fmt.Sprintf("<message-%02d@example.test>", i),
			ReceivedAt: time.Unix(1_700_000_000+int64(i), 0).Unix(),
		}
		if i == 0 {
			meta.MessageId = rootMessageID
		} else {
			meta.InReplyTo = rootMessageID
			meta.References = rootMessageID
		}
		uid, err := mailboxes.DeliverMessage("user1", "Sent", meta, 0)
		if err != nil {
			t.Fatalf("DeliverMessage %d: %v", i, err)
		}
		newestUID = uid
	}

	h := NewMailHandler(ps, nil, mailboxes, discardLogger())
	h.SetThreadStore(store.NewThreadStore(ps, mailboxes))

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := middleware.WithUserContext(req.Context(), "user1", "admin@example.com")
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Get("/mail/{mailbox}", h.ListMessages)
	r.Get("/mail/{mailbox}/{uid}", h.ShowMessage)

	req := httptest.NewRequest(http.MethodGet, "/mail/Sent", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", w.Code, http.StatusOK, w.Body.String())
	}

	body := w.Body.String()
	if strings.Contains(body, "1 of 3") {
		t.Fatalf("thread-grouped mailbox reported raw-message pages; body contains 1 of 3")
	}
	if !strings.Contains(body, "1 of 1") {
		t.Fatalf("thread-grouped mailbox should report one display page; body missing 1 of 1")
	}
	if got := strings.Count(body, `class="msg-row`); got != 1 {
		t.Fatalf("rendered message rows = %d, want 1", got)
	}

	req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/mail/Sent/%d", newestUID), nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("thread detail status = %d, want %d: %s", w.Code, http.StatusOK, w.Body.String())
	}
	if got := strings.Count(w.Body.String(), `<article class="message">`); got != 62 {
		t.Fatalf("thread detail message cards = %d, want 62", got)
	}
}

func TestServeBlobInlineTextPreviewIsEscaped(t *testing.T) {
	router, bs, ps := setupServeBlobRouter(t)

	// A text/plain attachment whose bytes contain HTML must be neutralised
	// before it is swapped into the page via hx-swap="innerHTML".
	payload := []byte(`hello <img src=x onerror=alert(1)> world`)
	hash, err := bs.Put(payload)
	if err != nil {
		t.Fatalf("blob put: %v", err)
	}
	ownBlobForTestUser(t, ps, hash)

	req := httptest.NewRequest(http.MethodGet, "/mail/blob/"+hash+"?inline=1&ct=text/plain", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	body := w.Body.String()
	if strings.Contains(body, "<img") {
		t.Errorf("inline text preview leaked raw markup (XSS): %q", body)
	}
	if !strings.Contains(body, "&lt;img") {
		t.Errorf("inline text preview should HTML-escape markup, got: %q", body)
	}
	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
}

// TestShowMessageFullPageIncludesLayout verifies that a direct navigation /
// browser refresh of a message URL returns a complete styled document (layout +
// stylesheet), while an HTMX request returns only the content fragment. This
// guards the regression where refreshing a message view lost all styling
// because the handler returned the bare content without the layout wrapper.
// TestApplyEnvelopeBodyParsesOnce verifies the shared MIME-parse path populates
// HTML/text body and attachments, and merges proto attachment hashes by filename
// (the single ReadEnvelope path used by both ShowMessage and thread siblings).
func TestApplyEnvelopeBodyParsesOnce(t *testing.T) {
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

	raw := "From: sender@example.test\r\n" +
		"To: user1@example.test\r\n" +
		"Subject: Body test\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=\"BOUND\"\r\n\r\n" +
		"--BOUND\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n\r\n" +
		"<p>Hello <b>world</b></p>\r\n" +
		"--BOUND\r\n" +
		"Content-Type: text/plain; name=\"notes.txt\"\r\n" +
		"Content-Disposition: attachment; filename=\"notes.txt\"\r\n\r\n" +
		"file contents\r\n" +
		"--BOUND--\r\n"

	hash, err := bs.Put([]byte(raw))
	if err != nil {
		t.Fatalf("blob Put: %v", err)
	}

	meta := &bongov1.MailMeta{
		RawMessage: &bongov1.BlobRef{Hash: hash},
		Attachments: []*bongov1.BlobRef{
			{Filename: "notes.txt", Hash: "att-hash-123", ContentType: "text/plain"},
		},
	}

	h := NewMailHandler(ps, bs, nil, discardLogger())
	var detail templates.MessageDetail
	h.applyEnvelopeBody(&detail, meta)

	if !strings.Contains(detail.HTMLBody, "Hello") || !strings.Contains(detail.HTMLBody, "world") {
		t.Errorf("HTMLBody missing parsed content: %q", detail.HTMLBody)
	}
	if len(detail.Attachments) != 1 {
		t.Fatalf("attachments = %d, want 1 (matched by filename, not duplicated)", len(detail.Attachments))
	}
	att := detail.Attachments[0]
	if att.Filename != "notes.txt" {
		t.Errorf("attachment filename = %q, want notes.txt", att.Filename)
	}
	if att.Hash != "att-hash-123" {
		t.Errorf("attachment hash = %q, want proto hash merged in by filename", att.Hash)
	}
}

func TestShowMessageFullPageIncludesLayout(t *testing.T) {
	dir := t.TempDir()
	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })

	applier := &directApplierTags{ps: ps}
	mailboxes := store.NewMailboxStore(ps, applier)
	if err := mailboxes.CreateMailbox("user1", "INBOX"); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	meta := &bongov1.MailMeta{
		Guid:       "msg-1",
		Subject:    "Styling regression check",
		From:       "sender@example.test",
		MessageId:  "<m1@example.test>",
		ReceivedAt: time.Unix(1_700_000_000, 0).Unix(),
	}
	uid, err := mailboxes.DeliverMessage("user1", "INBOX", meta, 0)
	if err != nil {
		t.Fatalf("DeliverMessage: %v", err)
	}

	h := NewMailHandler(ps, nil, mailboxes, discardLogger())
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := middleware.WithUserContext(req.Context(), "user1", "user1@example.test")
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Get("/mail/{mailbox}/{uid}", h.ShowMessage)

	url := fmt.Sprintf("/mail/INBOX/%d", uid)

	// Full-page load (no HX-Request): must be a complete document with the stylesheet.
	full := httptest.NewRecorder()
	r.ServeHTTP(full, httptest.NewRequest(http.MethodGet, url, nil))
	if full.Code != http.StatusOK {
		t.Fatalf("full-page status = %d: %s", full.Code, full.Body.String())
	}
	fb := full.Body.String()
	if !strings.Contains(fb, "<html") || !strings.Contains(fb, "bongo.css") {
		t.Errorf("full-page message view is missing the layout/stylesheet (styling would be lost on refresh)")
	}
	if !strings.Contains(fb, "Styling regression check") {
		t.Errorf("full-page message view missing the message subject")
	}

	// HTMX request: must be just the content fragment (no full document).
	htmx := httptest.NewRecorder()
	hreq := httptest.NewRequest(http.MethodGet, url, nil)
	hreq.Header.Set("HX-Request", "true")
	r.ServeHTTP(htmx, hreq)
	if htmx.Code != http.StatusOK {
		t.Fatalf("htmx status = %d", htmx.Code)
	}
	if strings.Contains(htmx.Body.String(), "<html") {
		t.Errorf("HTMX message response should be a fragment, not a full <html> document")
	}
}
