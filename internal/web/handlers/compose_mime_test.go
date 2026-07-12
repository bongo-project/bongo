package handlers

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"net/textproto"
	"strings"
	"testing"
)

// mimepart holds a pre-read MIME part: header plus fully-buffered body bytes.
// This is necessary because multipart.Part is a streaming reader; once the
// outer multipart.Reader advances to the next part it consumes the current
// part's body, making the Part unreadable.
type mimepart struct {
	header textproto.MIMEHeader
	body   []byte
}

// hdr returns the first value of the named header, canonicalising the key.
func (p mimepart) hdr(key string) string { return p.header.Get(key) }

// parseMessage parses raw RFC 5322 bytes into a net/mail.Message.
func parseMessage(t *testing.T, raw []byte) *mail.Message {
	t.Helper()
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("mail.ReadMessage: %v", err)
	}
	return msg
}

// mediaType extracts the media type from a Content-Type header (drops params).
func mediaType(t *testing.T, ct string) string {
	t.Helper()
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		t.Fatalf("mime.ParseMediaType(%q): %v", ct, err)
	}
	return mt
}

// boundaryOf extracts the boundary parameter from a Content-Type header.
func boundaryOf(t *testing.T, ct string) string {
	t.Helper()
	_, params, err := mime.ParseMediaType(ct)
	if err != nil {
		t.Fatalf("mime.ParseMediaType(%q): %v", ct, err)
	}
	b := params["boundary"]
	if b == "" {
		t.Fatalf("no boundary in Content-Type %q", ct)
	}
	return b
}

// readParts eagerly reads all immediate parts from body with the given boundary.
// Each part's body is fully buffered before NextPart() advances the reader so
// the returned mimepart values are safe to use after readParts returns.
func readParts(t *testing.T, body io.Reader, boundary string) []mimepart {
	t.Helper()
	mr := multipart.NewReader(body, boundary)
	var parts []mimepart
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("multipart.NextPart: %v", err)
		}
		data, err := io.ReadAll(p)
		if err != nil {
			t.Fatalf("read part body: %v", err)
		}
		parts = append(parts, mimepart{
			header: p.Header,
			body:   data,
		})
	}
	return parts
}

// stripAngle removes angle brackets from a Content-ID value,
// e.g. "<abc@bongo>" -> "abc@bongo".
func stripAngle(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "<")
	s = strings.TrimSuffix(s, ">")
	return s
}

// samplePNG is a minimal valid 1x1 red PNG, base64-encoded, for test use.
const samplePNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwADhQGAWjR9awAAAABJRU5ErkJggg=="

func TestBuildMIMEPlainOnly(t *testing.T) {
	p := mimeParts{
		from:      "from@example.com",
		to:        "to@example.com",
		subject:   "Hello",
		plain:     "Plain body text",
		messageID: "<test@bongo>",
	}
	raw := buildMIME(p)

	msg := parseMessage(t, raw)
	ct := msg.Header.Get("Content-Type")
	if got := mediaType(t, ct); got != "text/plain" {
		t.Errorf("Content-Type = %q, want text/plain", got)
	}

	body, err := io.ReadAll(msg.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != "Plain body text" {
		t.Errorf("body = %q, want %q", string(body), "Plain body text")
	}

	// Confirm no multipart boundary appears in the output.
	if strings.Contains(string(raw), "--bongo") {
		t.Error("plain-only output must not contain multipart boundary")
	}
}

func TestBuildMIMEPlainAndHTML(t *testing.T) {
	p := mimeParts{
		from:      "from@example.com",
		to:        "to@example.com",
		subject:   "HTML test",
		plain:     "Plain fallback",
		html:      "<p>HTML body</p>",
		messageID: "<test2@bongo>",
	}
	raw := buildMIME(p)

	msg := parseMessage(t, raw)
	ct := msg.Header.Get("Content-Type")
	if got := mediaType(t, ct); got != "multipart/alternative" {
		t.Errorf("top-level Content-Type = %q, want multipart/alternative", got)
	}

	boundary := boundaryOf(t, ct)
	parts := readParts(t, msg.Body, boundary)
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(parts))
	}

	if got := mediaType(t, parts[0].hdr("Content-Type")); got != "text/plain" {
		t.Errorf("part[0] Content-Type = %q, want text/plain", got)
	}
	if got := mediaType(t, parts[1].hdr("Content-Type")); got != "text/html" {
		t.Errorf("part[1] Content-Type = %q, want text/html", got)
	}

	htmlBody := string(parts[1].body)
	if !strings.Contains(htmlBody, "<p>HTML body</p>") {
		t.Errorf("html part body = %q, want to contain <p>HTML body</p>", htmlBody)
	}
}

func TestBuildMIMEPlainHTMLWithInlineImages(t *testing.T) {
	pngBytes, err := base64.StdEncoding.DecodeString(samplePNGBase64)
	if err != nil {
		t.Fatalf("decode sample PNG: %v", err)
	}

	inline := []inlineImage{
		{
			ContentID:   "abc123@bongo",
			ContentType: "image/png",
			Data:        pngBytes,
		},
	}
	htmlBody := `<p>Hello</p><img src="cid:abc123@bongo">`

	p := mimeParts{
		from:      "from@example.com",
		to:        "to@example.com",
		subject:   "Inline image test",
		plain:     "Plain fallback",
		html:      htmlBody,
		inline:    inline,
		messageID: "<test3@bongo>",
	}
	raw := buildMIME(p)

	msg := parseMessage(t, raw)
	ct := msg.Header.Get("Content-Type")
	if got := mediaType(t, ct); got != "multipart/related" {
		t.Errorf("top-level Content-Type = %q, want multipart/related", got)
	}

	relBoundary := boundaryOf(t, ct)
	relParts := readParts(t, msg.Body, relBoundary)
	// Expect: multipart/alternative + 1 inline image part.
	if len(relParts) != 2 {
		t.Fatalf("related parts = %d, want 2", len(relParts))
	}

	// First part must be multipart/alternative.
	altCT := relParts[0].hdr("Content-Type")
	if got := mediaType(t, altCT); got != "multipart/alternative" {
		t.Errorf("related[0] Content-Type = %q, want multipart/alternative", got)
	}
	altBoundary := boundaryOf(t, altCT)
	altParts := readParts(t, bytes.NewReader(relParts[0].body), altBoundary)
	if len(altParts) != 2 {
		t.Fatalf("alternative parts = %d, want 2", len(altParts))
	}
	if got := mediaType(t, altParts[0].hdr("Content-Type")); got != "text/plain" {
		t.Errorf("alt[0] Content-Type = %q, want text/plain", got)
	}
	if got := mediaType(t, altParts[1].hdr("Content-Type")); got != "text/html" {
		t.Errorf("alt[1] Content-Type = %q, want text/html", got)
	}

	// Second part must be the inline image.
	imgPart := relParts[1]
	if got := mediaType(t, imgPart.hdr("Content-Type")); got != "image/png" {
		t.Errorf("inline part Content-Type = %q, want image/png", got)
	}
	rawCID := imgPart.hdr("Content-ID")
	if rawCID == "" {
		t.Fatal("inline part has no Content-ID")
	}
	cid := stripAngle(rawCID)
	if cid != "abc123@bongo" {
		t.Errorf("Content-ID = %q, want abc123@bongo", cid)
	}
	disp := imgPart.hdr("Content-Disposition")
	if !strings.EqualFold(strings.TrimSpace(disp), "inline") {
		t.Errorf("Content-Disposition = %q, want inline", disp)
	}

	// Decode base64 bytes and compare to original.
	encodedData := strings.ReplaceAll(string(imgPart.body), "\r\n", "")
	encodedData = strings.ReplaceAll(encodedData, "\n", "")
	decoded, err := base64.StdEncoding.DecodeString(encodedData)
	if err != nil {
		t.Fatalf("decode inline part: %v", err)
	}
	if !bytes.Equal(decoded, pngBytes) {
		t.Error("inline part bytes do not match original image bytes")
	}
}

func TestBuildMIMEWithAttachments(t *testing.T) {
	p := mimeParts{
		from:      "from@example.com",
		to:        "to@example.com",
		subject:   "Attachment test",
		plain:     "See attached",
		html:      "<p>See attached</p>",
		messageID: "<test4@bongo>",
		attachments: []attachmentData{
			{Filename: "hello.txt", ContentType: "text/plain", Data: []byte("hello file")},
		},
	}
	raw := buildMIME(p)

	msg := parseMessage(t, raw)
	ct := msg.Header.Get("Content-Type")
	if got := mediaType(t, ct); got != "multipart/mixed" {
		t.Errorf("top-level Content-Type = %q, want multipart/mixed", got)
	}

	mixedBoundary := boundaryOf(t, ct)
	mixedParts := readParts(t, msg.Body, mixedBoundary)
	if len(mixedParts) != 2 {
		t.Fatalf("mixed parts = %d, want 2", len(mixedParts))
	}

	// First part must be multipart/alternative (because html is set).
	altCT := mixedParts[0].hdr("Content-Type")
	if got := mediaType(t, altCT); got != "multipart/alternative" {
		t.Errorf("mixed[0] Content-Type = %q, want multipart/alternative", got)
	}

	// Second part must be the attachment.
	attPart := mixedParts[1]
	if got := mediaType(t, attPart.hdr("Content-Type")); got != "text/plain" {
		t.Errorf("attachment Content-Type = %q, want text/plain", got)
	}
}

func TestBuildMIMEPlainOnlyWithAttachment(t *testing.T) {
	p := mimeParts{
		from:      "from@example.com",
		to:        "to@example.com",
		subject:   "Attachment no html",
		plain:     "Body",
		messageID: "<test5@bongo>",
		attachments: []attachmentData{
			{Filename: "file.txt", ContentType: "text/plain", Data: []byte("content")},
		},
	}
	raw := buildMIME(p)

	msg := parseMessage(t, raw)
	ct := msg.Header.Get("Content-Type")
	if got := mediaType(t, ct); got != "multipart/mixed" {
		t.Errorf("top-level Content-Type = %q, want multipart/mixed", got)
	}

	boundary := boundaryOf(t, ct)
	parts := readParts(t, msg.Body, boundary)
	if len(parts) != 2 {
		t.Fatalf("mixed parts = %d, want 2", len(parts))
	}
	if got := mediaType(t, parts[0].hdr("Content-Type")); got != "text/plain" {
		t.Errorf("body part Content-Type = %q, want text/plain", got)
	}
}

// TestExtractDataURIImages tests the data: URI extraction helper.
func TestExtractDataURIImages(t *testing.T) {
	pngBytes, err := base64.StdEncoding.DecodeString(samplePNGBase64)
	if err != nil {
		t.Fatalf("decode sample PNG: %v", err)
	}

	tests := []struct {
		name          string
		html          string
		wantImages    int
		wantNoDataURI bool
	}{
		{
			name:          "no images",
			html:          "<p>No images</p>",
			wantImages:    0,
			wantNoDataURI: false,
		},
		{
			name:          "single data URI image",
			html:          fmt.Sprintf(`<p>Hi</p><img src="data:image/png;base64,%s">`, samplePNGBase64),
			wantImages:    1,
			wantNoDataURI: true,
		},
		{
			name:          "image with extra attributes before src",
			html:          fmt.Sprintf(`<img alt="photo" src="data:image/png;base64,%s" width="100">`, samplePNGBase64),
			wantImages:    1,
			wantNoDataURI: true,
		},
		{
			// Identical bytes → same SHA-256 → same CID → deduplicated in returned slice.
			name:          "multiple data URI images same bytes",
			html:          fmt.Sprintf(`<img src="data:image/png;base64,%s"><img src="data:image/png;base64,%s">`, samplePNGBase64, samplePNGBase64),
			wantImages:    1,
			wantNoDataURI: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rewritten, images := extractDataURIImages(tt.html)

			if tt.wantNoDataURI && strings.Contains(rewritten, "data:") {
				t.Errorf("rewritten HTML still contains data: URI: %s", rewritten)
			}

			if tt.wantImages > 0 {
				if len(images) == 0 {
					t.Fatalf("expected images to be extracted, got none")
				}
				for _, img := range images {
					// src in HTML should reference cid:<id> (bare, no angle brackets).
					cidRef := "cid:" + img.ContentID
					if !strings.Contains(rewritten, cidRef) {
						t.Errorf("rewritten HTML does not contain cid reference %q; html=%s", cidRef, rewritten)
					}
					if !bytes.Equal(img.Data, pngBytes) {
						t.Error("extracted image bytes do not match original")
					}
					if img.ContentType != "image/png" {
						t.Errorf("ContentType = %q, want image/png", img.ContentType)
					}
					// ContentID must not contain angle brackets.
					if strings.ContainsAny(img.ContentID, "<>") {
						t.Errorf("ContentID %q must not contain angle brackets", img.ContentID)
					}
				}
			}
		})
	}
}

// TestExtractDataURIImagesRejectedTypes checks that non-image types are rejected.
func TestExtractDataURIImagesRejectedTypes(t *testing.T) {
	html := `<img src="data:application/javascript;base64,YWxlcnQoMSk=">`
	_, images := extractDataURIImages(html)
	if len(images) != 0 {
		t.Errorf("expected 0 images for application/javascript, got %d", len(images))
	}
}

// TestBuildMIMEDataURIEndToEnd tests the full round-trip:
// data: URI in html → extractDataURIImages → buildMIME → parse → assert.
func TestBuildMIMEDataURIEndToEnd(t *testing.T) {
	pngBytes, err := base64.StdEncoding.DecodeString(samplePNGBase64)
	if err != nil {
		t.Fatalf("decode sample PNG: %v", err)
	}

	rawHTML := fmt.Sprintf(`<p>Hello</p><img src="data:image/png;base64,%s">`, samplePNGBase64)
	rewrittenHTML, images := extractDataURIImages(rawHTML)

	if len(images) != 1 {
		t.Fatalf("expected 1 inline image, got %d", len(images))
	}
	if strings.Contains(rewrittenHTML, "data:") {
		t.Error("rewritten HTML still has data: URI")
	}

	cid := images[0].ContentID
	expectedCIDRef := "cid:" + cid
	if !strings.Contains(rewrittenHTML, expectedCIDRef) {
		t.Errorf("rewritten HTML = %q, want cid reference %q", rewrittenHTML, expectedCIDRef)
	}

	p := mimeParts{
		from:      "from@example.com",
		to:        "to@example.com",
		subject:   "End-to-end test",
		plain:     "See the image",
		html:      rewrittenHTML,
		inline:    images,
		messageID: "<e2e@bongo>",
	}
	raw := buildMIME(p)

	msg := parseMessage(t, raw)
	ct := msg.Header.Get("Content-Type")
	if got := mediaType(t, ct); got != "multipart/related" {
		t.Errorf("top-level Content-Type = %q, want multipart/related", got)
	}

	relBoundary := boundaryOf(t, ct)
	relParts := readParts(t, msg.Body, relBoundary)
	if len(relParts) != 2 {
		t.Fatalf("related parts = %d, want 2", len(relParts))
	}

	// Check the text/html part contains cid: reference and NO data: URI.
	altCT := relParts[0].hdr("Content-Type")
	altBoundary := boundaryOf(t, altCT)
	altParts := readParts(t, bytes.NewReader(relParts[0].body), altBoundary)
	if len(altParts) != 2 {
		t.Fatalf("alt parts = %d, want 2", len(altParts))
	}
	htmlPartBody := string(altParts[1].body)
	if strings.Contains(htmlPartBody, "data:") {
		t.Error("text/html part still contains data: URI")
	}
	if !strings.Contains(htmlPartBody, expectedCIDRef) {
		t.Errorf("text/html part = %q, want cid reference %q", htmlPartBody, expectedCIDRef)
	}

	// Check inline image part has matching Content-ID and correct bytes.
	imgPart := relParts[1]
	rawCID := imgPart.hdr("Content-ID")
	gotCID := stripAngle(rawCID)
	if gotCID != cid {
		t.Errorf("inline part Content-ID = %q, want %q", gotCID, cid)
	}
	disp := imgPart.hdr("Content-Disposition")
	if !strings.EqualFold(strings.TrimSpace(disp), "inline") {
		t.Errorf("Content-Disposition = %q, want inline", disp)
	}

	encodedData := strings.ReplaceAll(string(imgPart.body), "\r\n", "")
	encodedData = strings.ReplaceAll(encodedData, "\n", "")
	decoded, err := base64.StdEncoding.DecodeString(encodedData)
	if err != nil {
		t.Fatalf("decode inline image: %v", err)
	}
	if !bytes.Equal(decoded, pngBytes) {
		t.Error("inline image bytes do not match original")
	}
}

// TestBuildRFC5322WithAttachmentsBackwardCompat verifies the existing function
// signature still works and produces a plain-only message when no attachments.
func TestBuildRFC5322WithAttachmentsBackwardCompat(t *testing.T) {
	raw := buildRFC5322WithAttachments(
		"from@example.com", "to@example.com", "", "Subject", "Body text",
		"<msgid@bongo>", "", "", nil,
	)

	msg := parseMessage(t, raw)
	if got := mediaType(t, msg.Header.Get("Content-Type")); got != "text/plain" {
		t.Errorf("Content-Type = %q, want text/plain", got)
	}
	body, _ := io.ReadAll(msg.Body)
	if string(body) != "Body text" {
		t.Errorf("body = %q, want %q", string(body), "Body text")
	}
}
