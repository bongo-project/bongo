package handlers

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	netmail "net/mail"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-go/bongo-go/internal/store"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// composeRichTestEnv holds wired-up stores for compose-send integration tests.
type composeRichTestEnv struct {
	ps        *store.PebbleStore
	blobs     *store.BlobStore
	mailboxes *store.MailboxStore
	prefs     *store.PreferenceStore
	handler   *ComposeHandler
}

func newComposeRichTestEnv(t *testing.T) *composeRichTestEnv {
	t.Helper()
	dir := t.TempDir()

	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })

	blobs, err := store.NewBlobStore(dir)
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}

	applier := &directApplierTags{ps: ps}
	mailboxes := store.NewMailboxStore(ps, applier)
	prefStore := store.NewPreferenceStore(ps, applier)

	h := NewComposeHandler(ps, blobs, mailboxes, nil, applier, discardLogger())
	h.SetPreferences(prefStore)

	return &composeRichTestEnv{
		ps:        ps,
		blobs:     blobs,
		mailboxes: mailboxes,
		prefs:     prefStore,
		handler:   h,
	}
}

// registerLocalUser stores a /system/alias entry so resolveLocalUser recognises
// email as belonging to userID. This prevents Send() from requiring a QueueStore
// for external delivery.
func (e *composeRichTestEnv) registerLocalUser(t *testing.T, userID, email string) {
	t.Helper()
	key := []byte("/system/alias/" + strings.ToLower(email))
	if err := e.ps.Set(key, []byte(userID)); err != nil {
		t.Fatalf("registerLocalUser(%s): %v", email, err)
	}
}

// rawSentMessage retrieves the raw RFC 5322 bytes of the first message in the
// user's Sent mailbox by walking MailboxMessage → MailMeta → BlobStore.
func (e *composeRichTestEnv) rawSentMessage(t *testing.T, userID string) []byte {
	t.Helper()

	msgs, err := e.mailboxes.ListMessages(userID, "Sent")
	if err != nil {
		t.Fatalf("ListMessages(Sent): %v", err)
	}
	if len(msgs) == 0 {
		t.Fatal("no messages in Sent mailbox")
	}

	key := fmt.Sprintf("/user/%s/mail/%s", userID, msgs[0].MailGuid)
	data, err := e.ps.Get([]byte(key))
	if err != nil {
		t.Fatalf("get MailMeta key %s: %v", key, err)
	}
	var meta bongov1.MailMeta
	if err := proto.Unmarshal(data, &meta); err != nil {
		t.Fatalf("unmarshal MailMeta: %v", err)
	}
	if meta.RawMessage == nil {
		t.Fatal("MailMeta.RawMessage is nil")
	}

	rc, err := e.blobs.Get(meta.RawMessage.Hash)
	if err != nil {
		t.Fatalf("BlobStore.Get(%s): %v", meta.RawMessage.Hash, err)
	}
	defer rc.Close()
	raw, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll blob: %v", err)
	}
	return raw
}

// TestComposeSendRichMode_MultipartAlternative verifies that body_mode='rich'
// produces a multipart/alternative message containing both a text/plain part
// (with the derived plain text and the plain signature) and a text/html part
// (with the sanitized HTML body and the HTML signature).
func TestComposeSendRichMode_MultipartAlternative(t *testing.T) {
	env := newComposeRichTestEnv(t)
	userID := "user-rich-compose-test"
	email := "rich@example.com"

	// Register as local so delivery doesn't require a queue store.
	env.registerLocalUser(t, userID, email)

	// Set preferences with HTML and plain signatures.
	prefs := store.DefaultPreferences()
	prefs.SignatureHtml = "<b>My Sig</b>"
	prefs.Signature = "My Sig"
	if err := env.prefs.Set(userID, prefs); err != nil {
		t.Fatalf("prefs.Set: %v", err)
	}

	form := url.Values{
		"to":        {email},
		"subject":   {"Test Rich"},
		"body":      {"<p>hello</p>"},
		"body_mode": {"rich"},
	}
	req := httptest.NewRequest(http.MethodPost, "/compose/send", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUserContext(req, userID, email)
	w := httptest.NewRecorder()
	env.handler.Send(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d: %s", w.Code, w.Body.String())
	}

	raw := env.rawSentMessage(t, userID)

	// Parse the RFC 5322 message.
	msg, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	ct := msg.Header.Get("Content-Type")
	mediaType, params, err := mime.ParseMediaType(ct)
	if err != nil {
		t.Fatalf("ParseMediaType(%q): %v", ct, err)
	}
	if mediaType != "multipart/alternative" {
		t.Fatalf("Content-Type = %q, want multipart/alternative", mediaType)
	}

	boundary, ok := params["boundary"]
	if !ok || boundary == "" {
		t.Fatal("multipart/alternative missing boundary parameter")
	}

	mr := multipart.NewReader(msg.Body, boundary)
	var plainContent, htmlContent string
	for {
		part, err := mr.NextPart()
		if err != nil {
			break // io.EOF or end of parts
		}
		partData, _ := io.ReadAll(part)
		partCT := part.Header.Get("Content-Type")
		switch {
		case strings.HasPrefix(partCT, "text/plain"):
			plainContent = string(partData)
		case strings.HasPrefix(partCT, "text/html"):
			htmlContent = string(partData)
		}
	}

	if plainContent == "" {
		t.Error("text/plain part is missing or empty")
	}
	if htmlContent == "" {
		t.Error("text/html part is missing or empty")
	}

	// text/plain must contain the derived body text and the plain signature.
	if !strings.Contains(plainContent, "hello") {
		t.Errorf("text/plain does not contain 'hello': %q", plainContent)
	}
	if !strings.Contains(plainContent, "My Sig") {
		t.Errorf("text/plain does not contain 'My Sig': %q", plainContent)
	}

	// text/html must contain the original HTML body and the HTML signature.
	if !strings.Contains(htmlContent, "<p>hello</p>") {
		t.Errorf("text/html does not contain '<p>hello</p>': %q", htmlContent)
	}
	if !strings.Contains(htmlContent, "<b>My Sig</b>") {
		t.Errorf("text/html does not contain '<b>My Sig</b>': %q", htmlContent)
	}
}

// TestComposeSendPlainMode_SingleTextPlain verifies that body_mode='plain'
// produces a single text/plain message (byte-compatible with the legacy plain
// path). The Sent copy must have Content-Type: text/plain and contain the body.
func TestComposeSendPlainMode_SingleTextPlain(t *testing.T) {
	env := newComposeRichTestEnv(t)
	userID := "user-plain-compose-test"
	email := "plain@example.com"

	env.registerLocalUser(t, userID, email)

	form := url.Values{
		"to":        {email},
		"subject":   {"Test Plain"},
		"body":      {"hello"},
		"body_mode": {"plain"},
	}
	req := httptest.NewRequest(http.MethodPost, "/compose/send", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUserContext(req, userID, email)
	w := httptest.NewRecorder()
	env.handler.Send(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d: %s", w.Code, w.Body.String())
	}

	raw := env.rawSentMessage(t, userID)

	msg, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	ct := msg.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain", ct)
	}

	bodyData, _ := io.ReadAll(msg.Body)
	if !strings.Contains(string(bodyData), "hello") {
		t.Errorf("body = %q, want to contain 'hello'", string(bodyData))
	}

	// Must not be a multipart message.
	if strings.Contains(strings.ToLower(string(raw)), "multipart") {
		t.Error("raw message contains 'multipart' — expected single text/plain")
	}
}

// TestComposeSendOmittedBodyMode_DefaultsToPlain verifies that omitting
// body_mode is equivalent to body_mode='plain' (the legacy behaviour).
func TestComposeSendOmittedBodyMode_DefaultsToPlain(t *testing.T) {
	env := newComposeRichTestEnv(t)
	userID := "user-no-mode-test"
	email := "nomode@example.com"

	env.registerLocalUser(t, userID, email)

	form := url.Values{
		"to":      {email},
		"subject": {"Test No Mode"},
		"body":    {"hello"},
		// body_mode intentionally omitted
	}
	req := httptest.NewRequest(http.MethodPost, "/compose/send", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUserContext(req, userID, email)
	w := httptest.NewRecorder()
	env.handler.Send(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d: %s", w.Code, w.Body.String())
	}

	raw := env.rawSentMessage(t, userID)
	msg, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	ct := msg.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain (plain mode default)", ct)
	}
}

// TestComposeSendRichMode_OversizedInlineImage_PerCap verifies that a rich send
// containing a single inline data: image whose decoded size exceeds
// perInlineImageCapBytes is rejected with 400 and stores NO Sent copy.
func TestComposeSendRichMode_OversizedInlineImage_PerCap(t *testing.T) {
	env := newComposeRichTestEnv(t)
	userID := "user-inline-percap-test"
	email := "percap@example.com"
	env.registerLocalUser(t, userID, email)

	// Single image: perInlineImageCapBytes+1 decoded bytes — exceeds per-image cap.
	data := make([]byte, perInlineImageCapBytes+1)
	data[0] = 0xFF // non-zero first byte avoids all-zero content
	body := `<p>Hi</p><img src="data:image/png;base64,` + base64.StdEncoding.EncodeToString(data) + `">`

	form := url.Values{
		"to":        {email},
		"subject":   {"Oversized per-image inline"},
		"body":      {body},
		"body_mode": {"rich"},
	}
	req := httptest.NewRequest(http.MethodPost, "/compose/send", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUserContext(req, userID, email)
	w := httptest.NewRecorder()
	env.handler.Send(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Inline image too large") {
		t.Errorf("response body = %q, want to contain 'Inline image too large'", w.Body.String())
	}

	// Assert no Sent copy was stored — reject BEFORE any user-visible writes.
	msgs, err := env.mailboxes.ListMessages(userID, "Sent")
	if err != nil {
		t.Fatalf("ListMessages(Sent): %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected no messages in Sent after rejection, got %d", len(msgs))
	}
}

// TestComposeSendRichMode_OversizedTotalInlineImages verifies that a rich send
// where the SUM of decoded inline-image bytes exceeds totalInlineImageCapBytes
// (even though each individual image is under the per-image cap) is rejected
// with 400 and stores NO Sent copy.
//
// Uses multipart/form-data to avoid URL-encoding 15 MB of base64 data, keeping
// the test fast.
func TestComposeSendRichMode_OversizedTotalInlineImages(t *testing.T) {
	env := newComposeRichTestEnv(t)
	userID := "user-inline-totalcap-test"
	email := "totalcap@example.com"
	env.registerLocalUser(t, userID, email)

	// 11 unique images each of perInlineImageCapBytes-1 bytes decoded.
	// Total: 11 * (1<<20 - 1) = 11,534,325 > totalInlineImageCapBytes (10<<20 = 10,485,760).
	// Each individual image < perInlineImageCapBytes so the per-image check passes first.
	const numImages = 11
	var sb strings.Builder
	sb.WriteString("<p>Many images</p>")
	for i := range numImages {
		data := make([]byte, perInlineImageCapBytes-1)
		data[0] = byte(i + 1) // unique first byte ensures distinct SHA-256 (no dedup collapse)
		sb.WriteString(`<img src="data:image/png;base64,`)
		sb.WriteString(base64.StdEncoding.EncodeToString(data))
		sb.WriteString(`">`)
	}

	// Use multipart/form-data to transmit the 15 MB HTML body without URL encoding,
	// keeping this test fast (URL-encoding base64 at this scale is very slow).
	var bodyBuf bytes.Buffer
	mw := multipart.NewWriter(&bodyBuf)
	_ = mw.WriteField("to", email)
	_ = mw.WriteField("subject", "Oversized total inline images")
	_ = mw.WriteField("body", sb.String())
	_ = mw.WriteField("body_mode", "rich")
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/compose/send", &bodyBuf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req = withUserContext(req, userID, email)
	w := httptest.NewRecorder()
	env.handler.Send(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Total inline images too large") {
		t.Errorf("response body = %q, want to contain 'Total inline images too large'", w.Body.String())
	}

	// Assert no Sent copy was stored — reject BEFORE any user-visible writes.
	msgs, err := env.mailboxes.ListMessages(userID, "Sent")
	if err != nil {
		t.Fatalf("ListMessages(Sent): %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected no messages in Sent after rejection, got %d", len(msgs))
	}
}

// TestComposeSendRichMode_InlineImageUnderCap_MultipartRelated is a regression
// guard: a rich send with an inline image UNDER both caps must succeed and
// produce a multipart/related message containing the cid: inline part.
func TestComposeSendRichMode_InlineImageUnderCap_MultipartRelated(t *testing.T) {
	env := newComposeRichTestEnv(t)
	userID := "user-inline-undercap-test"
	email := "undercap@example.com"
	env.registerLocalUser(t, userID, email)

	// samplePNGBase64 is a tiny valid 1×1 PNG — well under both caps.
	body := `<p>Hi</p><img src="data:image/png;base64,` + samplePNGBase64 + `">`

	form := url.Values{
		"to":        {email},
		"subject":   {"Inline image under cap"},
		"body":      {body},
		"body_mode": {"rich"},
	}
	req := httptest.NewRequest(http.MethodPost, "/compose/send", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUserContext(req, userID, email)
	w := httptest.NewRecorder()
	env.handler.Send(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d: %s", w.Code, w.Body.String())
	}

	raw := env.rawSentMessage(t, userID)
	msg, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	ct := msg.Header.Get("Content-Type")
	mediaTypeParsed, params, err := mime.ParseMediaType(ct)
	if err != nil {
		t.Fatalf("ParseMediaType(%q): %v", ct, err)
	}
	if mediaTypeParsed != "multipart/related" {
		t.Errorf("Content-Type = %q, want multipart/related", mediaTypeParsed)
	}

	relParts := readParts(t, msg.Body, params["boundary"])
	if len(relParts) < 2 {
		t.Fatalf("expected >= 2 related parts, got %d", len(relParts))
	}

	// Verify the inline image part exists and has the correct headers.
	foundInline := false
	for _, p := range relParts[1:] {
		disp := strings.ToLower(strings.TrimSpace(p.hdr("Content-Disposition")))
		if disp == "inline" {
			if p.hdr("Content-ID") == "" {
				t.Error("inline part missing Content-ID")
			}
			foundInline = true
		}
	}
	if !foundInline {
		t.Error("sent message has no inline part with Content-Disposition: inline")
	}
}

// TestComposeSendPlainMode_EmptyHTMLSignature_PlainFallback verifies that when a
// user has SignatureHtml="" but a non-empty plain Signature, a plain-mode send
// produces a single text/plain message (NOT multipart) that contains the plain
// signature text.
//
// IMPORTANT — plain mode does NOT consult prefs on the server: compose.go emits the
// submitted body verbatim (the POST body IS the output). Setting prefs here establishes
// the documented precondition state (user has plain sig, no HTML sig). The plain
// signature must be included in the POST body to simulate the client-side prefill
// that the compose textarea performs. This test therefore proves that a prefilled
// plain body round-trips as a single text/plain part; it does NOT prove "empty
// SignatureHtml suppresses HTML in plain mode" — nothing is ever appended in plain
// mode regardless of prefs.
func TestComposeSendPlainMode_EmptyHTMLSignature_PlainFallback(t *testing.T) {
	env := newComposeRichTestEnv(t)
	userID := "user-plain-emptysig-test"
	email := "emptysig@example.com"
	env.registerLocalUser(t, userID, email)

	// Precondition: user has a plain signature but NO HTML signature.
	// The settings form derives Signature from signature_html, so we set them
	// directly on the store to establish the documented precondition state.
	prefs := store.DefaultPreferences()
	prefs.Signature = "Regards, Jane"
	prefs.SignatureHtml = "" // explicitly empty
	if err := env.prefs.Set(userID, prefs); err != nil {
		t.Fatalf("prefs.Set: %v", err)
	}

	// POST body already contains the plain signature (simulates client-side prefill).
	// The plain Send handler emits this verbatim — it does not re-append the signature.
	postBody := "hello\n\n-- \nRegards, Jane"
	form := url.Values{
		"to":        {email},
		"subject":   {"Test Plain Empty HTML Sig"},
		"body":      {postBody},
		"body_mode": {"plain"},
	}
	req := httptest.NewRequest(http.MethodPost, "/compose/send", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUserContext(req, userID, email)
	w := httptest.NewRecorder()
	env.handler.Send(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d: %s", w.Code, w.Body.String())
	}

	raw := env.rawSentMessage(t, userID)
	msg, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	ct := msg.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain", ct)
	}

	bodyData, _ := io.ReadAll(msg.Body)
	bodyStr := string(bodyData)

	// The plain body was submitted with the signature already included; it must appear.
	if !strings.Contains(bodyStr, "Regards, Jane") {
		t.Errorf("body = %q, want to contain 'Regards, Jane'", bodyStr)
	}

	// Must NOT be a multipart message — plain mode never adds an HTML part.
	rawLower := strings.ToLower(string(raw))
	if strings.Contains(rawLower, "multipart/alternative") {
		t.Error("raw message contains 'multipart/alternative' — expected single text/plain")
	}
	if strings.Contains(rawLower, "text/html") {
		t.Error("raw message contains 'text/html' — expected single text/plain")
	}
}

// TestComposeSendRichMode_SignatureInlineImage_RoundTrips verifies that an HTML
// signature containing an inline data: image is rewritten server-side to a cid:
// reference (in buildRichMessage), producing a multipart/related message whose
// sibling inline part carries the image. The POST body is text-only so the ONLY
// inline image originates from the HTML signature.
func TestComposeSendRichMode_SignatureInlineImage_RoundTrips(t *testing.T) {
	env := newComposeRichTestEnv(t)
	userID := "user-sig-inline-test"
	email := "siginline@example.com"
	env.registerLocalUser(t, userID, email)

	// Precondition: HTML signature contains readable text AND a small inline data: image.
	// extractDataURIImages requires a double-quoted src="data:<type>;base64,<b64>" attribute.
	sigHTML := `<b>Jane</b><img src="data:image/png;base64,` + samplePNGBase64 + `">`
	prefs := store.DefaultPreferences()
	prefs.SignatureHtml = sigHTML
	prefs.Signature = "Jane"
	if err := env.prefs.Set(userID, prefs); err != nil {
		t.Fatalf("prefs.Set: %v", err)
	}

	// POST body is TEXT-ONLY (no inline image) so the only inline image comes from
	// the HTML signature that buildRichMessage appends before extractDataURIImages.
	form := url.Values{
		"to":        {email},
		"subject":   {"Test Sig Inline Image"},
		"body":      {"<p>hello</p>"},
		"body_mode": {"rich"},
	}
	req := httptest.NewRequest(http.MethodPost, "/compose/send", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUserContext(req, userID, email)
	w := httptest.NewRecorder()
	env.handler.Send(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d: %s", w.Code, w.Body.String())
	}

	raw := env.rawSentMessage(t, userID)
	msg, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	// Top-level must be multipart/related (alternative content + inline image siblings).
	ct := msg.Header.Get("Content-Type")
	topMediaType, topParams, err := mime.ParseMediaType(ct)
	if err != nil {
		t.Fatalf("ParseMediaType(%q): %v", ct, err)
	}
	if topMediaType != "multipart/related" {
		t.Fatalf("top-level Content-Type = %q, want multipart/related", topMediaType)
	}

	relParts := readParts(t, msg.Body, topParams["boundary"])
	if len(relParts) < 2 {
		t.Fatalf("expected >= 2 related parts (alt + inline image), got %d", len(relParts))
	}

	// First related part must be multipart/alternative.
	altCT := relParts[0].hdr("Content-Type")
	altMediaType, altParams, err := mime.ParseMediaType(altCT)
	if err != nil {
		t.Fatalf("ParseMediaType alt part %q: %v", altCT, err)
	}
	if altMediaType != "multipart/alternative" {
		t.Fatalf("related[0] Content-Type = %q, want multipart/alternative", altMediaType)
	}

	altParts := readParts(t, bytes.NewReader(relParts[0].body), altParams["boundary"])
	if len(altParts) < 2 {
		t.Fatalf("expected >= 2 alternative parts (plain + html), got %d", len(altParts))
	}

	// Locate the text/html part within the alternative and assert on its content.
	var htmlPartBody string
	for _, p := range altParts {
		if strings.HasPrefix(p.hdr("Content-Type"), "text/html") {
			htmlPartBody = string(p.body)
			break
		}
	}
	if htmlPartBody == "" {
		t.Fatal("no text/html part found in multipart/alternative")
	}

	// The signature text must be present in the HTML part.
	if !strings.Contains(htmlPartBody, "Jane") {
		t.Errorf("text/html does not contain signature text 'Jane': %q", htmlPartBody)
	}
	// The data: URI from the signature must have been rewritten to cid: — no data: remains.
	if strings.Contains(htmlPartBody, "data:") {
		t.Error("text/html still contains data: URI — signature image was not rewritten to cid:")
	}
	if !strings.Contains(htmlPartBody, "cid:") {
		t.Error("text/html does not contain cid: reference — signature image rewrite did not occur")
	}

	// At least one sibling related part must be an inline image with a Content-ID.
	var foundInline bool
	var inlineCID string
	for _, p := range relParts[1:] {
		disp := strings.ToLower(strings.TrimSpace(p.hdr("Content-Disposition")))
		if disp == "inline" {
			cid := p.hdr("Content-ID")
			if cid == "" {
				t.Error("inline part missing Content-ID")
			}
			inlineCID = stripAngle(cid)
			foundInline = true
		}
	}
	if !foundInline {
		t.Error("no inline part with Content-Disposition: inline found in multipart/related")
	}

	// The cid: reference in the HTML must match the inline part's Content-ID (stripped of angle brackets).
	if inlineCID != "" && !strings.Contains(htmlPartBody, "cid:"+inlineCID) {
		t.Errorf("text/html cid: reference does not match inline part Content-ID %q", inlineCID)
	}
}

// TestComposeSendPlainMode_QuotedBodyPassedThrough verifies that plain mode
// passes the submitted body through to the text/plain MIME part without any
// HTML encoding, decoding, or tag insertion.
//
// A real plain <textarea> (added to compose.templ for body_mode='plain') submits
// literal characters — '>' quote markers appear as '>' not '&gt;', newlines as
// '\n' not '<br>'. The server must preserve these verbatim. This test exercises
// the server-side plain path with content that would have been incorrectly
// HTML-serialised by the old implementation (which used the richEditor's
// innerHTML even in plain mode).
func TestComposeSendPlainMode_QuotedBodyPassedThrough(t *testing.T) {
	env := newComposeRichTestEnv(t)
	userID := "user-plain-passthrough-test"
	email := "passthrough@example.com"

	env.registerLocalUser(t, userID, email)

	// Simulate a plain textarea submitting a reply-style quoted body.
	// '>' quote markers and newlines must appear literally in the text/plain output.
	quotedBody := "> some quoted text\n> second line\nnew reply here"
	form := url.Values{
		"to":        {email},
		"subject":   {"Test Plain Passthrough"},
		"body":      {quotedBody},
		"body_mode": {"plain"},
	}
	req := httptest.NewRequest(http.MethodPost, "/compose/send", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUserContext(req, userID, email)
	w := httptest.NewRecorder()
	env.handler.Send(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d: %s", w.Code, w.Body.String())
	}

	raw := env.rawSentMessage(t, userID)
	msg, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	ct := msg.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain", ct)
	}

	bodyData, _ := io.ReadAll(msg.Body)
	bodyStr := string(bodyData)

	// Literal '>' must appear — not HTML-encoded to '&gt;'.
	if !strings.Contains(bodyStr, "> some quoted text") {
		t.Errorf("text/plain does not contain '> some quoted text' literally: %q", bodyStr)
	}
	if !strings.Contains(bodyStr, "> second line") {
		t.Errorf("text/plain does not contain '> second line' literally: %q", bodyStr)
	}
	if !strings.Contains(bodyStr, "new reply here") {
		t.Errorf("text/plain does not contain 'new reply here': %q", bodyStr)
	}

	// No HTML artefacts must appear in the text/plain part.
	if strings.Contains(bodyStr, "&gt;") {
		t.Errorf("text/plain contains HTML-encoded '&gt;' — plain body was incorrectly HTML-encoded: %q", bodyStr)
	}
	if strings.Contains(bodyStr, "<br") {
		t.Errorf("text/plain contains '<br' HTML tag — newlines were incorrectly converted: %q", bodyStr)
	}
	if strings.Contains(bodyStr, "<p>") || strings.Contains(bodyStr, "<div>") {
		t.Errorf("text/plain contains HTML block tags — body was incorrectly treated as HTML: %q", bodyStr)
	}

	// Must not be a multipart message.
	if strings.Contains(strings.ToLower(string(raw)), "multipart") {
		t.Error("raw message contains 'multipart' — expected single text/plain for plain mode")
	}
}
