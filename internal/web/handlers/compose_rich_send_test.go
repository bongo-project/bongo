package handlers

import (
	"bytes"
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
