package handlers

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"regexp"
	"strings"
	"time"

	"github.com/bongo-project/bongo/internal/mail"
)

// perInlineImageCapBytes is the server-side cap for a single decoded inline image
// in a rich send. Matches richeditor.js IMAGE_CAP_BYTES = 1 * 1024 * 1024.
// The client cap (file size) and server cap (decoded bytes) are equivalent because
// base64 decoding produces approximately the original binary size.
const perInlineImageCapBytes = 1 << 20 // 1 MiB

// totalInlineImageCapBytes is the server-side cap for the sum of all decoded inline
// image bytes in a single rich send.
const totalInlineImageCapBytes = 10 << 20 // 10 MiB

// Sentinel errors returned by checkInlineImageCaps; used by buildRichMessage to
// reject oversized inline images with a clear 400 before any user-visible writes.
var (
	errInlineImageTooLarge = errors.New("inline image too large")
	errTotalInlineTooLarge = errors.New("total inline images too large")
)

// checkInlineImageCaps enforces per-image and total decoded-byte limits over the
// inline images extracted by extractDataURIImages. It returns errInlineImageTooLarge
// if any single image exceeds perInlineImageCapBytes, or errTotalInlineTooLarge if
// the running total exceeds totalInlineImageCapBytes.
//
// Inline images are intentionally NOT blob-stored separately: the whole raw message
// is blob-stored and referenced by MailMeta; standalone inline blobs would be
// unreferenced and eligible for GC within ~1 hour. Do NOT silently drop oversized
// images — reject the send so the user is clearly informed.
func checkInlineImageCaps(imgs []inlineImage) error {
	var total int
	for _, img := range imgs {
		if len(img.Data) > perInlineImageCapBytes {
			return errInlineImageTooLarge
		}
		total += len(img.Data)
		if total > totalInlineImageCapBytes {
			return errTotalInlineTooLarge
		}
	}
	return nil
}

// preCheckInlineImageSizes fast-scans html for data: URI image sizes, computing
// exact decoded byte counts from the base64 string lengths instead of decoding,
// and returns errInlineImageTooLarge or errTotalInlineTooLarge when the raw HTML
// exceeds the inline-image caps. This allows rejection before the expensive HTML
// sanitizer runs.
//
// It is an optimistic fast-path over the raw (unsanitised) user-supplied HTML
// only. The definitive check on extractDataURIImages output still runs afterward
// to cover the appended signature HTML and any edge cases.
func preCheckInlineImageSizes(html string) error {
	var total int
	for _, loc := range dataURIImageRe.FindAllStringSubmatchIndex(html, -1) {
		if len(loc) < 6 {
			continue
		}
		rawType := strings.TrimSpace(html[loc[2]:loc[3]])
		mt, _, err := mime.ParseMediaType(rawType)
		if err != nil || !allowedInlineMediaTypes[mt] {
			continue
		}
		// Compute exact decoded length from the base64 string at loc[4]:loc[5]
		// without materialising it. Standard base64: decodedLen = b64Len*3/4 minus
		// the number of trailing '=' padding characters (0, 1, or 2).
		b64Len := loc[5] - loc[4]
		padding := 0
		if b64Len > 0 && html[loc[5]-1] == '=' {
			padding++
			if b64Len > 1 && html[loc[5]-2] == '=' {
				padding++
			}
		}
		decodedLen := b64Len*3/4 - padding
		if decodedLen > perInlineImageCapBytes {
			return errInlineImageTooLarge
		}
		total += decodedLen
		if total > totalInlineImageCapBytes {
			return errTotalInlineTooLarge
		}
	}
	return nil
}

// inlineImage holds a decoded inline image extracted from a data: URI.
// ContentID does not include angle brackets.
type inlineImage struct {
	ContentID   string // e.g. "abc123@bongo" — no angle brackets
	ContentType string // e.g. "image/png"
	Data        []byte
}

// mimeParts is the canonical input for buildMIME.
type mimeParts struct {
	from        string
	to          string
	cc          string
	subject     string
	plain       string // required plain-text body
	html        string // optional HTML alternative (empty = plain-only)
	messageID   string
	inReplyTo   string
	references  string
	inline      []inlineImage    // inline cid: images (requires html != "")
	attachments []attachmentData // file attachments
}

// allowedInlineMediaTypes is the whitelist of image types we accept from data:
// URIs. This prevents crafted URIs from injecting CRLF or unexpected content
// via the Content-Type header.
var allowedInlineMediaTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
}

// dataURIImageRe matches <img …> tags that carry a data: URI src attribute
// (double-quoted). Group 1 = media type, Group 2 = base64 data.
var dataURIImageRe = regexp.MustCompile(`(?i)<img\b[^>]*\bsrc="data:([^;]+);base64,([^"]+)"[^>]*>`)

// srcDataURIRe matches only the src="data:…" attribute within a matched tag.
var srcDataURIRe = regexp.MustCompile(`(?i)\bsrc="data:[^"]*"`)

// extractDataURIImages scans html for <img src="data:<type>;base64,…"> tags,
// decodes each accepted image, generates a stable content-ID (SHA-256 of the
// decoded bytes formatted as "<hex16>@bongo"), rewrites the src attribute to
// cid:<id> (bare, no angle brackets), and returns the rewritten HTML and the
// list of extracted inline images.
//
// Only image/png, image/jpeg, image/gif, and image/webp are accepted.
// Unsupported types are left unchanged (not extracted, not rewritten).
// Identical images (same SHA-256) share the same content-ID and appear only
// once in the returned slice.
func extractDataURIImages(html string) (string, []inlineImage) {
	var images []inlineImage
	cidSeen := map[string]bool{}

	rewritten := dataURIImageRe.ReplaceAllStringFunc(html, func(match string) string {
		sub := dataURIImageRe.FindStringSubmatch(match)
		if len(sub) < 3 {
			return match
		}
		rawType := strings.TrimSpace(sub[1])
		rawData := strings.TrimSpace(sub[2])

		// Validate and normalise the media type (also guards against CRLF injection).
		mt, _, err := mime.ParseMediaType(rawType)
		if err != nil || !allowedInlineMediaTypes[mt] {
			return match // leave tag unchanged
		}
		safeType := mail.SanitizeHeaderValue(mt)

		decoded, err := base64.StdEncoding.DecodeString(rawData)
		if err != nil {
			return match
		}

		// Deterministic content-ID from the first 16 bytes of the SHA-256 hash.
		sum := sha256.Sum256(decoded)
		cid := hex.EncodeToString(sum[:16]) + "@bongo"

		if !cidSeen[cid] {
			cidSeen[cid] = true
			images = append(images, inlineImage{
				ContentID:   cid,
				ContentType: safeType,
				Data:        decoded,
			})
		}

		// Rewrite only the src attribute within the matched tag.
		return srcDataURIRe.ReplaceAllString(match, fmt.Sprintf(`src="cid:%s"`, cid))
	})

	return rewritten, images
}

// randBoundarySegment returns a short random hex string used as a MIME boundary
// suffix to keep multipart boundaries unique per level.
func randBoundarySegment() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "00000000"
	}
	return hex.EncodeToString(b[:])
}

// writeBase64Wrapped encodes data as standard base64 and writes it to buf,
// wrapping at 76 characters per RFC 2045.
func writeBase64Wrapped(buf *bytes.Buffer, data []byte) {
	encoded := base64.StdEncoding.EncodeToString(data)
	for i := 0; i < len(encoded); i += 76 {
		end := i + 76
		if end > len(encoded) {
			end = len(encoded)
		}
		buf.WriteString(encoded[i:end])
		buf.WriteString("\r\n")
	}
}

// buildAlternativeBody returns the Content-Type string (with boundary) and body
// bytes for a multipart/alternative containing a text/plain and text/html part.
func buildAlternativeBody(plain, html string) (contentType string, body []byte) {
	boundary := "bongo-alt-" + randBoundarySegment()
	contentType = fmt.Sprintf(`multipart/alternative; boundary="%s"`, boundary)

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "--%s\r\n", boundary)
	buf.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	buf.WriteString("\r\n")
	buf.WriteString(plain)
	buf.WriteString("\r\n")

	fmt.Fprintf(&buf, "--%s\r\n", boundary)
	buf.WriteString("Content-Type: text/html; charset=utf-8\r\n")
	buf.WriteString("\r\n")
	buf.WriteString(html)
	buf.WriteString("\r\n")

	fmt.Fprintf(&buf, "--%s--\r\n", boundary)
	return contentType, buf.Bytes()
}

// buildRelatedBody returns the Content-Type string (with boundary) and body
// bytes for a multipart/related that contains a multipart/alternative sub-tree
// plus one inline image part per inlineImage in imgs.
func buildRelatedBody(plain, html string, imgs []inlineImage) (contentType string, body []byte) {
	altCT, altBody := buildAlternativeBody(plain, html)

	boundary := "bongo-rel-" + randBoundarySegment()
	contentType = fmt.Sprintf(`multipart/related; type="multipart/alternative"; boundary="%s"`, boundary)

	var buf bytes.Buffer

	// Nested multipart/alternative as first part.
	fmt.Fprintf(&buf, "--%s\r\n", boundary)
	fmt.Fprintf(&buf, "Content-Type: %s\r\n", altCT)
	buf.WriteString("\r\n")
	buf.Write(altBody)
	buf.WriteString("\r\n")

	// One part per inline image.
	for _, img := range imgs {
		fmt.Fprintf(&buf, "--%s\r\n", boundary)
		fmt.Fprintf(&buf, "Content-Type: %s\r\n", img.ContentType)
		fmt.Fprintf(&buf, "Content-ID: <%s>\r\n", img.ContentID) // RFC 2392: angle brackets
		buf.WriteString("Content-Disposition: inline\r\n")
		buf.WriteString("Content-Transfer-Encoding: base64\r\n")
		buf.WriteString("\r\n")
		writeBase64Wrapped(&buf, img.Data)
	}

	fmt.Fprintf(&buf, "--%s--\r\n", boundary)
	return contentType, buf.Bytes()
}

// buildMIME constructs the raw RFC 5322/2045 bytes for an email message.
//
// Structure selection (in order of precedence):
//
//	no html, no inline, no attachments → single text/plain
//	no html, no inline, + attachments  → multipart/mixed { text/plain, atts… }
//	html, no inline, no attachments    → multipart/alternative { plain, html }
//	html + inline, no attachments      → multipart/related { multipart/alternative
//	                                       { plain, html }, inline… }
//	any of the above + attachments     → multipart/mixed { <above>, atts… }
func buildMIME(p mimeParts) []byte {
	// Sanitize every user-controlled header value to prevent header injection.
	from := mail.SanitizeHeaderValue(p.from)
	to := mail.SanitizeHeaderValue(p.to)
	cc := mail.SanitizeHeaderValue(p.cc)
	subject := mail.SanitizeHeaderValue(p.subject)

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "From: %s\r\n", from)
	fmt.Fprintf(&buf, "To: %s\r\n", to)
	if cc != "" {
		fmt.Fprintf(&buf, "Cc: %s\r\n", cc)
	}
	fmt.Fprintf(&buf, "Subject: %s\r\n", subject)
	fmt.Fprintf(&buf, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	fmt.Fprintf(&buf, "Message-ID: %s\r\n", p.messageID)
	if p.inReplyTo != "" {
		fmt.Fprintf(&buf, "In-Reply-To: %s\r\n", p.inReplyTo)
	}
	if p.references != "" {
		fmt.Fprintf(&buf, "References: %s\r\n", p.references)
	}
	buf.WriteString("MIME-Version: 1.0\r\n")

	hasHTML := p.html != ""
	hasInline := len(p.inline) > 0
	hasAttachments := len(p.attachments) > 0

	// Plain-only with no attachments: produce a single text/plain part that is
	// byte-identical to the original buildRFC5322WithAttachments plain path.
	if !hasHTML && !hasInline && !hasAttachments {
		buf.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
		buf.WriteString("\r\n")
		buf.WriteString(p.plain)
		return buf.Bytes()
	}

	// Build the body sub-tree. bodyCT is a complete Content-Type value (including
	// any boundary parameter); bodyBytes is the sub-tree body (not the header).
	var bodyCT string
	var bodyBytes []byte

	switch {
	case !hasHTML && !hasInline:
		// Plain text body; will be wrapped in multipart/mixed below.
		bodyCT = "text/plain; charset=utf-8"
		bodyBytes = []byte(p.plain)
	case hasHTML && !hasInline:
		bodyCT, bodyBytes = buildAlternativeBody(p.plain, p.html)
	default:
		// HTML with inline images.
		bodyCT, bodyBytes = buildRelatedBody(p.plain, p.html, p.inline)
	}

	if !hasAttachments {
		fmt.Fprintf(&buf, "Content-Type: %s\r\n", bodyCT)
		buf.WriteString("\r\n")
		buf.Write(bodyBytes)
		return buf.Bytes()
	}

	// Wrap the body sub-tree in multipart/mixed with file attachments.
	mixedBoundary := "bongo-mixed-" + randBoundarySegment()
	fmt.Fprintf(&buf, "Content-Type: multipart/mixed; boundary=\"%s\"\r\n", mixedBoundary)
	buf.WriteString("\r\n")

	// Body sub-tree as the first part.
	fmt.Fprintf(&buf, "--%s\r\n", mixedBoundary)
	fmt.Fprintf(&buf, "Content-Type: %s\r\n", bodyCT)
	buf.WriteString("\r\n")
	buf.Write(bodyBytes)
	buf.WriteString("\r\n")

	// Attachment parts.
	for _, att := range p.attachments {
		safeName := mail.SanitizeHeaderFilename(att.Filename)
		fmt.Fprintf(&buf, "--%s\r\n", mixedBoundary)
		fmt.Fprintf(&buf, "Content-Type: %s; name=\"%s\"\r\n", att.ContentType, safeName)
		fmt.Fprintf(&buf, "Content-Disposition: attachment; filename=\"%s\"\r\n", safeName)
		buf.WriteString("Content-Transfer-Encoding: base64\r\n")
		buf.WriteString("\r\n")
		writeBase64Wrapped(&buf, att.Data)
	}

	fmt.Fprintf(&buf, "--%s--\r\n", mixedBoundary)
	return buf.Bytes()
}
