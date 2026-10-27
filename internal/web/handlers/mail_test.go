package handlers

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jhillyerd/enmime"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/mimesafe"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	"github.com/bongo-project/bongo/internal/web/templates"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// inboundZipRaw returns a raw multipart message carrying a zip attachment
// (base64-encoded) and the decoded attachment bytes. It mimics inbound mail:
// only the raw message is stored as a blob, attachments are not.
func inboundZipRaw() (raw string, zipBytes []byte) {
	zipBytes = []byte("PK\x03\x04 pretend-zip-payload")
	b64 := base64.StdEncoding.EncodeToString(zipBytes)
	raw = "From: sender@ext.test\r\n" +
		"To: admin@example.com\r\n" +
		"Subject: has zip\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=\"B\"\r\n\r\n" +
		"--B\r\nContent-Type: text/plain\r\n\r\nsee attached\r\n" +
		"--B\r\n" +
		"Content-Type: application/zip; name=\"archive.zip\"\r\n" +
		"Content-Disposition: attachment; filename=\"archive.zip\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" +
		b64 + "\r\n" +
		"--B--\r\n"
	return raw, zipBytes
}

// TestInboundAttachmentUsesMessageScopedURL reproduces the reported bug: an
// inbound attachment has no blob hash, so its download link must point at the
// message-scoped route (not the empty /mail/blob/ that failed).
func TestInboundAttachmentUsesMessageScopedURL(t *testing.T) {
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

	raw, _ := inboundZipRaw()
	hash, err := bs.Put([]byte(raw))
	if err != nil {
		t.Fatalf("blob Put: %v", err)
	}
	// Inbound: attachment recorded with filename but NO hash (see deliver.go).
	meta := &bongov1.MailMeta{
		RawMessage:  &bongov1.BlobRef{Hash: hash},
		Attachments: []*bongov1.BlobRef{{Filename: "archive.zip", ContentType: "application/zip"}},
	}

	h := NewMailHandler(ps, bs, nil, discardLogger())
	detail := templates.MessageDetail{Mailbox: "INBOX", UID: 7}
	h.applyEnvelopeBody(&detail, meta)

	if len(detail.Attachments) != 1 {
		t.Fatalf("attachments = %d, want 1", len(detail.Attachments))
	}
	att := detail.Attachments[0]
	if att.Hash != "" {
		t.Fatalf("inbound attachment should have no blob hash, got %q", att.Hash)
	}
	if want := "/mail/INBOX/7/attachment/0"; att.URL != want {
		t.Fatalf("attachment URL = %q, want %q (must not be an empty /mail/blob/ link)", att.URL, want)
	}
}

// inboundAttachmentRaw builds a raw multipart message carrying one attachment
// of the given content type (base64-encoded).
func inboundAttachmentRaw(ct, filename string, body []byte) string {
	b64 := base64.StdEncoding.EncodeToString(body)
	return "From: s@ext.test\r\nTo: a@example.com\r\nSubject: att\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=\"B\"\r\n\r\n" +
		"--B\r\nContent-Type: text/plain\r\n\r\nhi\r\n" +
		"--B\r\nContent-Type: " + ct + "; name=\"" + filename + "\"\r\n" +
		"Content-Disposition: attachment; filename=\"" + filename + "\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" + b64 + "\r\n--B--\r\n"
}

// serveInboundAttachment delivers a one-attachment inbound message and returns a
// router wired to ServeAttachment plus the message UID.
func serveInboundAttachment(t *testing.T, ct, filename string, body []byte) (*chi.Mux, uint32) {
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
	if err := mailboxes.CreateMailbox("user1", "INBOX"); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	hash, err := bs.Put([]byte(inboundAttachmentRaw(ct, filename, body)))
	if err != nil {
		t.Fatalf("blob Put: %v", err)
	}
	meta := &bongov1.MailMeta{
		Guid: "m-att", Subject: "att",
		RawMessage:  &bongov1.BlobRef{Hash: hash},
		Attachments: []*bongov1.BlobRef{{Filename: filename, ContentType: ct}},
	}
	uid, err := mailboxes.DeliverMessage("user1", "INBOX", meta, 0)
	if err != nil {
		t.Fatalf("DeliverMessage: %v", err)
	}
	h := NewMailHandler(ps, bs, mailboxes, discardLogger())
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(middleware.WithUserContext(req.Context(), "user1", "u@example.test")))
		})
	})
	r.Get("/mail/{mailbox}/{uid}/attachment/{idx}", h.ServeAttachment)
	return r, uid
}

// TestServeAttachmentInlineImageOptIn: a PNG served with ?inline=1 renders inline
// with its real content type; without it, it downloads.
func TestServeAttachmentInlineImageOptIn(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n fake png bytes")
	r, uid := serveInboundAttachment(t, "image/png", "pic.png", png)

	inline := httptest.NewRecorder()
	r.ServeHTTP(inline, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/mail/INBOX/%d/attachment/0?inline=1", uid), nil))
	if inline.Code != http.StatusOK {
		t.Fatalf("inline status = %d", inline.Code)
	}
	if ct := inline.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("inline Content-Type = %q, want image/png", ct)
	}
	if cd := inline.Header().Get("Content-Disposition"); cd != "inline" {
		t.Errorf("inline Content-Disposition = %q, want inline", cd)
	}
	if inline.Body.String() != string(png) {
		t.Errorf("inline body mismatch")
	}

	dl := httptest.NewRecorder()
	r.ServeHTTP(dl, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/mail/INBOX/%d/attachment/0", uid), nil))
	if ct := dl.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("download Content-Type = %q, want octet-stream", ct)
	}
}

// TestServeAttachmentSVGNeverInline: an SVG is never served inline even with
// ?inline=1 (script/XSS carrier); it is forced to download.
func TestServeAttachmentSVGNeverInline(t *testing.T) {
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
	r, uid := serveInboundAttachment(t, "image/svg+xml", "evil.svg", svg)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/mail/INBOX/%d/attachment/0?inline=1", uid), nil))
	if ct := rec.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("SVG Content-Type = %q, want octet-stream (never inline)", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); cd != "attachment" {
		t.Errorf("SVG Content-Disposition = %q, want attachment", cd)
	}
}

// TestSplitAttachmentDecodedOnDownload guards the split-attachment display path:
// a de-MIME'd record's attachment blob holds the ENCODED span, so the display
// must route the attachment through the decoding message-scoped route (not the
// blob hash), and downloading it must yield the DECODED file, never base64.
func TestSplitAttachmentDecodedOnDownload(t *testing.T) {
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
	if err := mailboxes.CreateMailbox("user1", "INBOX"); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}

	decoded := bytes.Repeat([]byte("PDF-BYTES-"), 500) // ~5 KB "pdf" payload
	b64 := strings.Join(chunk76(base64.StdEncoding.EncodeToString(decoded)), "\r\n")
	raw := "From: s@ext.test\r\nTo: u@example.com\r\nSubject: doc\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=\"B\"\r\n\r\n" +
		"--B\r\nContent-Type: text/plain\r\n\r\nhi\r\n" +
		"--B\r\nContent-Type: application/pdf; name=\"doc.pdf\"\r\n" +
		"Content-Disposition: attachment; filename=\"doc.pdf\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" + b64 + "\r\n--B--\r\n"

	sm := store.PrepareSplit([]byte(raw), 512)
	if sm == nil {
		t.Fatal("expected the message to split")
	}
	skHash, err := bs.Put(sm.Skeleton)
	if err != nil {
		t.Fatalf("put skeleton: %v", err)
	}
	for _, enc := range sm.Encoded {
		if _, err := bs.Put(enc); err != nil {
			t.Fatalf("put attachment: %v", err)
		}
	}
	meta := &bongov1.MailMeta{
		Guid: "g-split", Subject: "doc",
		RawMessage:  &bongov1.BlobRef{Hash: skHash, Size: int64(len(raw))},
		Attachments: sm.Attachments,
		Layout:      sm.Layout,
	}
	uid, err := mailboxes.DeliverMessage("user1", "INBOX", meta, 0)
	if err != nil {
		t.Fatalf("DeliverMessage: %v", err)
	}

	h := NewMailHandler(ps, bs, mailboxes, discardLogger())

	// The display must not expose the encoded-span blob hash; it must use the
	// message-scoped (decoding) route.
	detail := templates.MessageDetail{Mailbox: "INBOX", UID: uid}
	h.applyEnvelopeBody(&detail, meta)
	if len(detail.Attachments) != 1 {
		t.Fatalf("attachments = %d, want 1", len(detail.Attachments))
	}
	if detail.Attachments[0].Hash != "" {
		t.Errorf("split attachment must not carry the encoded blob hash, got %q", detail.Attachments[0].Hash)
	}
	wantURL := fmt.Sprintf("/mail/INBOX/%d/attachment/0", uid)
	if detail.Attachments[0].URL != wantURL {
		t.Errorf("split attachment URL = %q, want %q", detail.Attachments[0].URL, wantURL)
	}

	// Downloading via that route must return the DECODED file, not base64.
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(middleware.WithUserContext(req.Context(), "user1", "u@example.test")))
		})
	})
	r.Get("/mail/{mailbox}/{uid}/attachment/{idx}", h.ServeAttachment)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, wantURL, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("download status = %d", rec.Code)
	}
	if !bytes.Equal(rec.Body.Bytes(), decoded) {
		t.Fatalf("split attachment download did not decode: got %d bytes, want %d", rec.Body.Len(), len(decoded))
	}
}

func chunk76(s string) []string {
	var out []string
	for len(s) > 76 {
		out = append(out, s[:76])
		s = s[76:]
	}
	return append(out, s)
}

// TestServeAttachmentStreamsInboundZip verifies the message-scoped route serves
// the attachment bytes from the raw message as a download.
func TestServeAttachmentStreamsInboundZip(t *testing.T) {
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

	applier := &directApplierTags{ps: ps}
	mailboxes := store.NewMailboxStore(ps, applier)
	if err := mailboxes.CreateMailbox("user1", "INBOX"); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}

	raw, zipBytes := inboundZipRaw()
	hash, err := bs.Put([]byte(raw))
	if err != nil {
		t.Fatalf("blob Put: %v", err)
	}
	meta := &bongov1.MailMeta{
		Guid:        "m-zip-1",
		Subject:     "has zip",
		RawMessage:  &bongov1.BlobRef{Hash: hash},
		Attachments: []*bongov1.BlobRef{{Filename: "archive.zip", ContentType: "application/zip"}},
	}
	uid, err := mailboxes.DeliverMessage("user1", "INBOX", meta, 0)
	if err != nil {
		t.Fatalf("DeliverMessage: %v", err)
	}

	h := NewMailHandler(ps, bs, mailboxes, discardLogger())
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := middleware.WithUserContext(req.Context(), "user1", "user1@example.test")
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Get("/mail/{mailbox}/{uid}/attachment/{idx}", h.ServeAttachment)

	rec := httptest.NewRecorder()
	url := fmt.Sprintf("/mail/INBOX/%d/attachment/0", uid)
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if cd := rec.Header().Get("Content-Disposition"); cd != "attachment" {
		t.Errorf("Content-Disposition = %q, want attachment", cd)
	}
	if got := rec.Body.Bytes(); string(got) != string(zipBytes) {
		t.Errorf("body = %q, want the zip bytes %q", got, zipBytes)
	}

	// Out-of-range index is a clean 404, not a panic.
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/mail/INBOX/%d/attachment/9", uid), nil))
	if rec2.Code != http.StatusNotFound {
		t.Errorf("out-of-range index status = %d, want 404", rec2.Code)
	}
}

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
			Subject:    "Re: test message",
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
	// The header counts messages, not the one thread-collapsed row.
	if !strings.Contains(body, "62 total") || !strings.Contains(body, "62 unread") {
		t.Fatalf("header should count 62 raw messages; body: %s", body)
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

// blobSplitFixture is a handler over a store holding one split message that
// carries a base64 inline image with a filename, delivered to "user1".
type blobSplitFixture struct {
	h        *MailHandler
	bs       *store.BlobStore
	ps       *store.PebbleStore
	meta     *bongov1.MailMeta
	uid      uint32
	raw      string
	png      []byte
	skelHash string
	spanHash string
}

func newBlobSplitFixture(t *testing.T) *blobSplitFixture {
	t.Helper()
	return newBlobSplitFixtureWithPart(t, "Content-Type: image/png; name=\"x.png\"\r\n"+
		"Content-Disposition: inline; filename=\"x.png\"\r\n")
}

// newBlobSplitFixtureWithPart is newBlobSplitFixture with caller-chosen
// headers (CRLF-terminated, excluding the transfer encoding) on the base64 part.
func newBlobSplitFixtureWithPart(t *testing.T, partHeaders string) *blobSplitFixture {
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
	if err := mailboxes.CreateMailbox("user1", "INBOX"); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}

	// 1537 bytes: deliberately not a round number or a multiple of three.
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte("IMG-DATA!"), 170)...)
	b64 := strings.Join(chunk76(base64.StdEncoding.EncodeToString(png)), "\r\n")
	raw := "From: s@ext.test\r\nTo: u@example.com\r\nSubject: pic\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=\"B\"\r\n\r\n" +
		"--B\r\nContent-Type: text/plain\r\n\r\nhi\r\n" +
		"--B\r\n" + partHeaders +
		"Content-Transfer-Encoding: base64\r\n\r\n" + b64 + "\r\n--B--\r\n"

	sm := store.PrepareSplit([]byte(raw), 512)
	if sm == nil {
		t.Fatal("expected the message to split")
	}
	skHash, err := bs.Put(sm.Skeleton)
	if err != nil {
		t.Fatalf("put skeleton: %v", err)
	}
	for _, enc := range sm.Encoded {
		if _, err := bs.Put(enc); err != nil {
			t.Fatalf("put span: %v", err)
		}
	}
	if len(sm.Attachments) != 1 {
		t.Fatalf("split attachments = %d, want 1", len(sm.Attachments))
	}
	meta := &bongov1.MailMeta{
		Guid: "g-inline", Subject: "pic",
		RawMessage:  &bongov1.BlobRef{Hash: skHash, Size: int64(len(raw))},
		Attachments: sm.Attachments,
		Layout:      sm.Layout,
	}
	uid, err := mailboxes.DeliverMessage("user1", "INBOX", meta, 0)
	if err != nil {
		t.Fatalf("DeliverMessage: %v", err)
	}
	return &blobSplitFixture{
		h: NewMailHandler(ps, bs, mailboxes, discardLogger()), bs: bs, ps: ps,
		meta: meta, uid: uid, raw: raw, png: png,
		skelHash: skHash, spanHash: sm.Attachments[0].Hash,
	}
}

func (f *blobSplitFixture) router(userID string) *chi.Mux {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(middleware.WithUserContext(req.Context(), userID, userID+"@example.test")))
		})
	})
	r.Get("/mail/blob/{hash}", f.h.ServeBlob)
	r.Get("/mail/{mailbox}/{uid}/attachment/{idx}", f.h.ServeAttachment)
	r.Get("/mail/{mailbox}/{uid}/export", f.h.ExportEML)
	return r
}

func TestServeBlobCacheControlPrivate(t *testing.T) {
	f := newBlobSplitFixture(t)
	r := f.router("user1")

	check := func(t *testing.T, path string) {
		t.Helper()
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: status = %d", path, rec.Code)
		}
		cc := rec.Header().Get("Cache-Control")
		if cc == "" || strings.Contains(cc, "public") || !strings.Contains(cc, "private") {
			t.Errorf("GET %s: Cache-Control = %q, want private and never public", path, cc)
		}
	}

	t.Run("blob", func(t *testing.T) {
		check(t, "/mail/blob/"+f.skelHash)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/mail/blob/"+f.skelHash, nil))
		if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "max-age") {
			t.Errorf("blob Cache-Control = %q, want a max-age", cc)
		}
	})
	t.Run("attachment", func(t *testing.T) {
		check(t, fmt.Sprintf("/mail/INBOX/%d/attachment/0", f.uid))
	})
	t.Run("eml", func(t *testing.T) {
		check(t, fmt.Sprintf("/mail/INBOX/%d/export", f.uid))
	})
}

func TestSplitInlineImageWithFilenameServedDecoded(t *testing.T) {
	f := newBlobSplitFixture(t)

	detail := templates.MessageDetail{Mailbox: "INBOX", UID: f.uid}
	f.h.applyEnvelopeBody(&detail, f.meta)
	if len(detail.Attachments) != 1 {
		t.Fatalf("attachments = %d, want 1", len(detail.Attachments))
	}
	att := detail.Attachments[0]
	if att.Hash != "" {
		t.Errorf("split inline part must not carry the encoded span hash, got %q", att.Hash)
	}
	wantURL := fmt.Sprintf("/mail/INBOX/%d/attachment/0", f.uid)
	if att.URL != wantURL {
		t.Fatalf("inline part URL = %q, want %q", att.URL, wantURL)
	}

	r := f.router("user1")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, att.URL+"?inline=1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !bytes.Equal(rec.Body.Bytes(), f.png) {
		t.Fatalf("inline image not decoded: got %d bytes, want %d", rec.Body.Len(), len(f.png))
	}

	// The encoded span must not be reachable as base64 text.
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/mail/blob/"+f.spanHash, nil))
	if rec.Code == http.StatusOK {
		t.Errorf("encoded span served via /mail/blob (%d bytes)", rec.Body.Len())
	}
}

func TestServeBlobSplitRawHashReturnsReconstructed(t *testing.T) {
	f := newBlobSplitFixture(t)
	rec := httptest.NewRecorder()
	f.router("user1").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/mail/blob/"+f.skelHash, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Body.String() != f.raw {
		t.Fatalf("raw hash served %d bytes, want the full %d byte message", rec.Body.Len(), len(f.raw))
	}
}

func TestServeBlobCrossUser(t *testing.T) {
	t.Run("shared blob", func(t *testing.T) {
		router, bs, ps := setupServeBlobRouter(t)
		hash, err := bs.Put([]byte("shared dataset blob"))
		if err != nil {
			t.Fatalf("blob put: %v", err)
		}
		// Only another user references the blob.
		other := &bongov1.MailMeta{Guid: "other-g", Attachments: []*bongov1.BlobRef{{Hash: hash}}}
		data, err := proto.Marshal(other)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if err := ps.Set([]byte("/user/other-user/mail/other-g"), data); err != nil {
			t.Fatalf("set: %v", err)
		}
		get := func() int {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/mail/blob/"+hash, nil))
			return rec.Code
		}
		if code := get(); code != http.StatusNotFound {
			t.Fatalf("non-owner status = %d, want 404", code)
		}
		// Positive control: same router, same blob, now referenced by the caller.
		ownBlobForTestUser(t, ps, hash)
		if code := get(); code != http.StatusOK {
			t.Fatalf("owner status = %d, want 200", code)
		}
	})

	t.Run("split skeleton", func(t *testing.T) {
		f := newBlobSplitFixture(t)
		for _, hash := range []string{f.skelHash, f.spanHash} {
			rec := httptest.NewRecorder()
			f.router("user2").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/mail/blob/"+hash, nil))
			if rec.Code != http.StatusNotFound {
				t.Errorf("user2 fetching user1's %s: status = %d, want 404", hash[:8], rec.Code)
			}
		}
		rec := httptest.NewRecorder()
		f.router("user1").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/mail/blob/"+f.skelHash, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("owner fetching own raw hash: status = %d, want 200", rec.Code)
		}
	})
}

// A part named only by Content-Type name= (no Content-Disposition) lands in
// enmime's OtherParts; attsplit still stores it as an encoded span.
func TestSplitNamedPartWithoutDispositionServedDecoded(t *testing.T) {
	f := newBlobSplitFixtureWithPart(t, "Content-Type: application/pdf; name=\"inv.pdf\"\r\n")

	detail := templates.MessageDetail{Mailbox: "INBOX", UID: f.uid}
	f.h.applyEnvelopeBody(&detail, f.meta)
	if len(detail.Attachments) != 1 {
		t.Fatalf("attachments = %d, want 1", len(detail.Attachments))
	}
	att := detail.Attachments[0]
	if att.Filename != "inv.pdf" {
		t.Errorf("filename = %q, want inv.pdf", att.Filename)
	}
	if att.Hash != "" || strings.Contains(att.URL, "/mail/blob/") {
		t.Errorf("split part exposes span: hash=%q url=%q", att.Hash, att.URL)
	}
	wantURL := fmt.Sprintf("/mail/INBOX/%d/attachment/0", f.uid)
	if att.URL != wantURL {
		t.Fatalf("URL = %q, want %q", att.URL, wantURL)
	}
	rec := httptest.NewRecorder()
	f.router("user1").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, att.URL, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !bytes.Equal(rec.Body.Bytes(), f.png) {
		t.Fatalf("part not decoded: got %d bytes, want %d", rec.Body.Len(), len(f.png))
	}
}

// A split record's proto attachment that no envelope part matches must never
// be listed with a span hash or a /mail/blob URL.
func TestSplitUnmatchedSpanNeverLinked(t *testing.T) {
	f := newBlobSplitFixture(t)
	meta := proto.Clone(f.meta).(*bongov1.MailMeta)
	meta.Attachments = append(meta.Attachments, &bongov1.BlobRef{Hash: "ghostspanhash", Filename: "ghost.bin", Size: 9})

	detail := templates.MessageDetail{Mailbox: "INBOX", UID: f.uid}
	f.h.applyEnvelopeBody(&detail, meta)
	for _, a := range detail.Attachments {
		if a.Hash != "" || strings.Contains(a.URL, "/mail/blob/") {
			t.Errorf("split record exposes span: %+v", a)
		}
	}
}

// The same hash referenced by a split and a non-split message of one user:
// the non-split reference (stored bytes are the real content) must win.
func TestServeBlobPrefersNonSplitReference(t *testing.T) {
	f := newBlobSplitFixture(t)
	r := f.router("user1")
	get := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/mail/blob/"+f.spanHash, nil))
		return rec
	}
	if rec := get(); rec.Code != http.StatusNotFound {
		t.Fatalf("split-only span status = %d, want 404", rec.Code)
	}
	plain := &bongov1.MailMeta{Guid: "z-plain", Attachments: []*bongov1.BlobRef{{Hash: f.spanHash}}}
	data, err := proto.Marshal(plain)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Sorts after the split record's key so a first-match scan would pick the split one.
	if err := f.ps.Set([]byte("/user/user1/mail/z-plain"), data); err != nil {
		t.Fatalf("set: %v", err)
	}
	if rec := get(); rec.Code != http.StatusOK {
		t.Fatalf("with non-split reference status = %d, want 200", rec.Code)
	}
}

// ServeAttachment must answer 404, not index past the slice, for an index at
// or beyond the number of servable parts.
func TestServeAttachmentIndexBounds(t *testing.T) {
	f := newBlobSplitFixture(t)
	r := f.router("user1")
	n := len(servableParts(mustEnvelope(t, f.raw)))
	if n != 1 {
		t.Fatalf("servable parts = %d, want 1", n)
	}
	for _, idx := range []int{n, n + 1, 99} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/mail/INBOX/%d/attachment/%d", f.uid, idx), nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("attachment/%d status = %d, want 404", idx, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/mail/INBOX/%d/attachment/%d", f.uid, n-1), nil))
	if rec.Code != http.StatusOK {
		t.Errorf("last valid index status = %d, want 200", rec.Code)
	}
}

func mustEnvelope(t *testing.T, raw string) *enmime.Envelope {
	t.Helper()
	env, _, err := mimesafe.ReadEnvelope([]byte(raw))
	if err != nil {
		t.Fatalf("ReadEnvelope: %v", err)
	}
	return env
}

// Existing /attachment/{idx} links must keep their meaning: regular
// attachments come first, named inline parts after them, whatever the order
// of the parts in the message.
func TestAttachmentIndexOrderRegularBeforeInline(t *testing.T) {
	f := newBlobSplitFixture(t)
	regular := append([]byte("REGULAR-"), bytes.Repeat([]byte("abc"), 101)...)
	b64 := func(b []byte) string {
		return strings.Join(chunk76(base64.StdEncoding.EncodeToString(b)), "\r\n")
	}
	// The inline image precedes the attachment in the message on purpose.
	raw := "From: s@ext.test\r\nTo: u@example.com\r\nSubject: order\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=\"B\"\r\n\r\n" +
		"--B\r\nContent-Type: text/plain\r\n\r\nhi\r\n" +
		"--B\r\nContent-Type: image/png; name=\"x.png\"\r\nContent-Disposition: inline; filename=\"x.png\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" + b64(f.png) + "\r\n" +
		"--B\r\nContent-Type: application/octet-stream; name=\"a.bin\"\r\nContent-Disposition: attachment; filename=\"a.bin\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" + b64(regular) + "\r\n--B--\r\n"

	hash, err := f.bs.Put([]byte(raw))
	if err != nil {
		t.Fatalf("put raw: %v", err)
	}
	mailboxes := store.NewMailboxStore(f.ps, &directApplierTags{ps: f.ps})
	meta := &bongov1.MailMeta{Guid: "g-order", Subject: "order", RawMessage: &bongov1.BlobRef{Hash: hash, Size: int64(len(raw))}}
	uid, err := mailboxes.DeliverMessage("user1", "INBOX", meta, 0)
	if err != nil {
		t.Fatalf("DeliverMessage: %v", err)
	}

	detail := templates.MessageDetail{Mailbox: "INBOX", UID: uid}
	f.h.applyEnvelopeBody(&detail, meta)
	if len(detail.Attachments) != 2 {
		t.Fatalf("attachments = %d, want 2", len(detail.Attachments))
	}
	for i, want := range []string{"a.bin", "x.png"} {
		if got := detail.Attachments[i].Filename; got != want {
			t.Errorf("chip %d filename = %q, want %q", i, got, want)
		}
		if got, wantURL := detail.Attachments[i].URL, fmt.Sprintf("/mail/INBOX/%d/attachment/%d", uid, i); got != wantURL {
			t.Errorf("chip %d URL = %q, want %q", i, got, wantURL)
		}
	}

	r := f.router("user1")
	for i, want := range [][]byte{regular, f.png} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/mail/INBOX/%d/attachment/%d", uid, i), nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("attachment/%d status = %d", i, rec.Code)
		}
		if !bytes.Equal(rec.Body.Bytes(), want) {
			t.Errorf("attachment/%d served %d bytes, want %d (order changed)", i, rec.Body.Len(), len(want))
		}
	}
}

func TestCountListMessages(t *testing.T) {
	msg := func(flags uint32) *bongov1.MailboxMessage { return &bongov1.MailboxMessage{Flags: flags} }
	tests := []struct {
		name string
		in   []*bongov1.MailboxMessage
		want templates.ListCounts
	}{
		{"empty", nil, templates.ListCounts{}},
		{"all unread", []*bongov1.MailboxMessage{msg(0), msg(store.FlagFlagged)}, templates.ListCounts{Unread: 2, Total: 2}},
		{"mixed", []*bongov1.MailboxMessage{msg(store.FlagSeen), msg(0), msg(store.FlagSeen | store.FlagFlagged), msg(store.FlagFlagged), msg(store.FlagSeen)}, templates.ListCounts{Unread: 2, Total: 5}},
		{"all read", []*bongov1.MailboxMessage{msg(store.FlagSeen)}, templates.ListCounts{Unread: 0, Total: 1}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := countListMessages(tc.in); got != tc.want {
				t.Errorf("countListMessages = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// listCountsFixture delivers five INBOX messages (two seen) and tags four of
// them (one seen) so the plain and tag views expect different, non-round counts.
func listCountsFixture(t *testing.T) *chi.Mux {
	t.Helper()
	dir := t.TempDir()
	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	meta, err := store.NewMetadataManager(dir + "/meta")
	if err != nil {
		t.Fatalf("NewMetadataManager: %v", err)
	}
	t.Cleanup(func() { meta.Close() })

	mailboxes := store.NewMailboxStore(ps, &directApplierTags{ps: ps})
	if err := mailboxes.CreateMailbox("user1", "INBOX"); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	// guid, seen, tagged
	fixture := []struct {
		seen, tagged bool
	}{{true, true}, {false, true}, {false, true}, {true, false}, {false, true}}
	for i, f := range fixture {
		guid := fmt.Sprintf("counts-%d", i)
		var flags uint32
		if f.seen {
			flags = store.FlagSeen
		}
		m := &bongov1.MailMeta{
			Guid:       guid,
			Subject:    fmt.Sprintf("subject %d", i),
			From:       "a@example.com",
			MessageId:  fmt.Sprintf("<counts-%d@example.test>", i),
			ReceivedAt: time.Unix(1_700_000_000+int64(i), 0).Unix(),
		}
		if _, err := mailboxes.DeliverMessage("user1", "INBOX", m, flags); err != nil {
			t.Fatalf("DeliverMessage %d: %v", i, err)
		}
		if f.tagged {
			if err := meta.IndexMailTags("user1", guid, []string{"work"}); err != nil {
				t.Fatalf("IndexMailTags: %v", err)
			}
		}
	}

	h := NewMailHandler(ps, nil, mailboxes, discardLogger())
	h.SetMetadata(meta)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := middleware.WithUserContext(req.Context(), "user1", "admin@example.com")
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Get("/mail/tag/{name}", h.ListTagMessages)
	r.Get("/mail/{mailbox}", h.ListMessages)
	return r
}

// TestListHeaderCountsOnEveryRenderPath covers the full page and the HTMX
// fragment for both the plain mailbox and the tag view, since each has its own
// call site that builds the header counts.
func TestListHeaderCountsOnEveryRenderPath(t *testing.T) {
	r := listCountsFixture(t)
	tests := []struct {
		name, path string
		hx         bool
		want       []string
		notWant    string
	}{
		{"mailbox full page", "/mail/INBOX", false, []string{"3 unread", "5 total"}, ""},
		{"mailbox htmx", "/mail/INBOX", true, []string{"3 unread", "5 total"}, "<html"},
		{"tag full page", "/mail/tag/work", false, []string{"3 unread", "4 total", ">work</h1>"}, "tag:work</h1>"},
		{"tag htmx", "/mail/tag/work", true, []string{"3 unread", "4 total", ">work</h1>"}, "<html"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			if tt.hx {
				req.Header.Set("HX-Request", "true")
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", w.Code, w.Body.String())
			}
			body := w.Body.String()
			for _, s := range tt.want {
				if !strings.Contains(body, s) {
					t.Errorf("body missing %q", s)
				}
			}
			if tt.notWant != "" && strings.Contains(body, tt.notWant) {
				t.Errorf("body contains %q", tt.notWant)
			}
		})
	}
}
