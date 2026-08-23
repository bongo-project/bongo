//go:build integration

package integration

// TestRichComposeEndToEnd exercises the rich signature and composer feature end
// to end: it starts a dev node (agent.NewAgent + a.Run, the same pattern as
// webui_test.go), authenticates as admin@localhost via POST /login with a
// cookie-jar client, sets an HTML signature via POST /settings (signature_html)
// from which the server derives the plain-text version with plainTextFromHTML,
// then sends a RICH message (body_mode=rich) carrying a formatting tag, an
// inline data: PNG and a <script>alert(1)</script>. The stored Sent copy is read
// back through the node's own stores (PebbleStore, MailboxStore, BlobStore) and
// parsed with net/mail + mime/multipart, so the assertions cover the whole MIME
// tree as stored: multipart/related at the top (because of the inline image)
// wrapping a multipart/alternative that holds both text/plain and text/html; the
// text/html part carries the body, the HTML signature and a cid: reference with
// no data: URI left; a sibling inline part carries Content-ID and
// Content-Disposition: inline; the text/plain part carries the plain body text
// and the plain-text signature; "<script" and "alert(1)" are absent from
// text/html (sanitized). A second send with body_mode=plain must produce a
// single text/plain part with no multipart anywhere in the raw message.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	netmail "net/mail"
	"net/textproto"
	"net/url"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-go/bongo-go/internal/agent"
	"github.com/bongo-go/bongo-go/internal/config"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// richComposeSamplePNG is a minimal valid 1×1 PNG, base64-encoded.
// Defined locally because the handlers-package constant is not exported.
const richComposeSamplePNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwADhQGAWjR9awAAAABJRU5ErkJggg=="

// richComposeMIMEPart holds a pre-read MIME part (header + buffered body).
type richComposeMIMEPart struct {
	header textproto.MIMEHeader
	body   []byte
}

func (p richComposeMIMEPart) hdr(key string) string { return p.header.Get(key) }

// richComposeMIMEParts reads all immediate parts from body using boundary,
// buffering each part's body before advancing to the next part.
func richComposeMIMEParts(t *testing.T, body io.Reader, boundary string) []richComposeMIMEPart {
	t.Helper()
	mr := multipart.NewReader(body, boundary)
	var parts []richComposeMIMEPart
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
		parts = append(parts, richComposeMIMEPart{header: p.Header, body: data})
	}
	return parts
}

// stripAngleBrackets removes surrounding angle brackets from a Content-ID value.
func stripAngleBrackets(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "<")
	s = strings.TrimSuffix(s, ">")
	return s
}

// resolveAdminUserID scans /user/*/profile keys in the PebbleStore and returns
// the userID whose UserProfile.Email matches admin@localhost.  This mirrors
// ComposeHandler.resolveLocalUser: no /system/alias/admin@localhost key exists
// in dev mode (CreateUser writes only the profile).
func resolveAdminUserID(t *testing.T, a *agent.Agent) string {
	t.Helper()
	iter, err := a.Store.PrefixScan([]byte("/user/"))
	if err != nil {
		t.Fatalf("PrefixScan /user/: %v", err)
	}
	defer iter.Close()

	for iter.Next() {
		keyStr := string(iter.Key())
		if !strings.HasSuffix(keyStr, "/profile") {
			continue
		}
		val, err := iter.Value()
		if err != nil {
			continue
		}
		var profile bongov1.UserProfile
		if err := proto.Unmarshal(val, &profile); err != nil {
			continue
		}
		if strings.EqualFold(profile.Email, "admin@localhost") {
			parts := strings.Split(keyStr, "/")
			if len(parts) >= 3 {
				return parts[2]
			}
		}
	}
	t.Fatal("admin@localhost userID not found in pebble profiles")
	return ""
}

// rawSentMessageBySubject retrieves the raw RFC 5322 bytes of the Sent message
// matching the given subject.  It lists all Sent messages, loads each MailMeta
// from the PebbleStore to compare the subject, and fetches the raw bytes from
// the BlobStore.
func rawSentMessageBySubject(t *testing.T, a *agent.Agent, userID, subject string) []byte {
	t.Helper()

	msgs, err := a.Mailboxes.ListMessages(userID, "Sent")
	if err != nil {
		t.Fatalf("ListMessages(Sent): %v", err)
	}

	for _, m := range msgs {
		key := fmt.Sprintf("/user/%s/mail/%s", userID, m.MailGuid)
		data, err := a.Store.Get([]byte(key))
		if err != nil {
			continue
		}
		var meta bongov1.MailMeta
		if err := proto.Unmarshal(data, &meta); err != nil {
			continue
		}
		if meta.Subject != subject {
			continue
		}
		if meta.RawMessage == nil {
			t.Fatalf("MailMeta.RawMessage is nil for %s", m.MailGuid)
		}
		rc, err := a.Blobs.Get(meta.RawMessage.Hash)
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

	t.Fatalf("no Sent message with subject %q found", subject)
	return nil
}

// Production code is NOT modified. If a real bug is found the test fails and
// the failure is reported as-is.
func TestRichComposeEndToEnd(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	cfg.Server.BindAddr = "127.0.0.1"
	sharedAssignFreePorts(t, cfg)
	cfg.SMTP.Enabled = true
	cfg.IMAP.Enabled = true

	logger := sharedTestLogger()

	a, err := agent.NewAgent(cfg, logger)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = a.Run(ctx)
	}()

	deadline := time.Now().Add(15 * time.Second)
	for !a.Ready() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if !a.Ready() {
		t.Fatal("agent not ready in time")
	}

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", cfg.Web.Port)

	// Create HTTP client with cookie jar.
	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}

	// --- Step 1: Login ---
	resp, err := client.PostForm(baseURL+"/login", url.Values{
		"email":    {"admin@localhost"},
		"password": {"admin"},
	})
	if err != nil {
		t.Fatalf("POST /login: %v", err)
	}
	resp.Body.Close()

	u, _ := url.Parse(baseURL)
	cookies := jar.Cookies(u)
	hasSession := false
	for _, c := range cookies {
		if c.Name == "bongo_session" {
			hasSession = true
			break
		}
	}
	if !hasSession {
		t.Fatal("no bongo_session cookie after login")
	}

	// --- Step 2: Set HTML signature ---
	// The signature contains readable text (<b>Jane Doe</b>) so the server-derived
	// plain-text signature is "Jane Doe".  This lets us assert both the HTML and
	// plain-text signature in the composed message.
	sigHTML := `<b>Jane Doe</b>`
	resp, err = client.PostForm(baseURL+"/settings", url.Values{
		"signature_html": {sigHTML},
	})
	if err != nil {
		t.Fatalf("POST /settings (signature): %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /settings status = %d, want 200", resp.StatusCode)
	}

	// Resolve the admin userID from the pebble store (needed to read Sent messages).
	adminUserID := resolveAdminUserID(t, a)

	// --- Sub-test A: Rich message with inline image + sanitization ---
	t.Run("rich_with_inline_image_and_signature", func(t *testing.T) {
		richSubject := "RichTest-" + t.Name()
		richBody := `<b>hi</b>` +
			`<img src="data:image/png;base64,` + richComposeSamplePNG + `">` +
			`<script>alert(1)</script>`

		resp, err := client.PostForm(baseURL+"/compose/send", url.Values{
			"to":        {"admin@localhost"},
			"subject":   {richSubject},
			"body":      {richBody},
			"body_mode": {"rich"},
		})
		if err != nil {
			t.Fatalf("POST /compose/send (rich): %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("POST /compose/send (rich) status = %d, want 200 (after redirect)", resp.StatusCode)
		}

		raw := rawSentMessageBySubject(t, a, adminUserID, richSubject)

		msg, err := netmail.ReadMessage(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("ReadMessage: %v", err)
		}

		topCT := msg.Header.Get("Content-Type")
		topMediaType, topParams, err := mime.ParseMediaType(topCT)
		if err != nil {
			t.Fatalf("ParseMediaType top-level %q: %v", topCT, err)
		}

		// Top-level must be multipart/related because of the inline image.
		if topMediaType != "multipart/related" {
			t.Fatalf("top-level Content-Type = %q, want multipart/related", topMediaType)
		}

		relatedParts := richComposeMIMEParts(t, msg.Body, topParams["boundary"])
		if len(relatedParts) < 2 {
			t.Fatalf("expected >= 2 related parts (alternative + inline), got %d", len(relatedParts))
		}

		// --- Assert multipart/alternative (first related part) ---
		altCT := relatedParts[0].hdr("Content-Type")
		altMediaType, altParams, err := mime.ParseMediaType(altCT)
		if err != nil {
			t.Fatalf("ParseMediaType alternative %q: %v", altCT, err)
		}
		if altMediaType != "multipart/alternative" {
			t.Fatalf("first related part Content-Type = %q, want multipart/alternative", altMediaType)
		}

		altParts := richComposeMIMEParts(t, bytes.NewReader(relatedParts[0].body), altParams["boundary"])
		if len(altParts) < 2 {
			t.Fatalf("expected >= 2 alternative parts (plain+html), got %d", len(altParts))
		}

		var plainContent, htmlContent string
		for _, ap := range altParts {
			apCT := ap.hdr("Content-Type")
			switch {
			case strings.HasPrefix(apCT, "text/plain"):
				plainContent = string(ap.body)
			case strings.HasPrefix(apCT, "text/html"):
				htmlContent = string(ap.body)
			}
		}

		if plainContent == "" {
			t.Error("text/plain part is missing or empty")
		}
		if htmlContent == "" {
			t.Error("text/html part is missing or empty")
		}

		// text/plain must contain the derived body text and the plain signature.
		if !strings.Contains(plainContent, "hi") {
			t.Errorf("text/plain does not contain 'hi': %q", plainContent)
		}
		if !strings.Contains(plainContent, "Jane Doe") {
			t.Errorf("text/plain does not contain plain signature 'Jane Doe': %q", plainContent)
		}

		// text/html must contain the HTML body, a cid: image reference (no data:
		// URI remaining), and the HTML signature.
		if !strings.Contains(htmlContent, "<b>hi</b>") {
			t.Errorf("text/html does not contain '<b>hi</b>': %q", htmlContent)
		}
		if !strings.Contains(htmlContent, "cid:") {
			t.Errorf("text/html does not contain a cid: reference — inline image not converted: %q", htmlContent)
		}
		if strings.Contains(htmlContent, "data:") {
			t.Errorf("text/html still contains a data: URI — inline image was not extracted: %q", htmlContent)
		}
		if !strings.Contains(htmlContent, "<b>Jane Doe</b>") {
			t.Errorf("text/html does not contain HTML signature '<b>Jane Doe</b>': %q", htmlContent)
		}

		// Sanitization: <script>alert(1)</script> must not appear in text/html.
		if strings.Contains(htmlContent, "<script") {
			t.Errorf("text/html contains '<script' — sanitization did not strip the script tag: %q", htmlContent)
		}
		if strings.Contains(htmlContent, "alert(1)") {
			t.Errorf("text/html contains 'alert(1)' — sanitization did not strip script content: %q", htmlContent)
		}

		// --- Assert inline image part (sibling of multipart/alternative) ---
		foundInline := false
		for _, rp := range relatedParts[1:] {
			disp := strings.ToLower(strings.TrimSpace(rp.hdr("Content-Disposition")))
			if disp == "inline" {
				cid := rp.hdr("Content-ID")
				if cid == "" {
					t.Error("inline part missing Content-ID header")
				}
				// CID must match what is referenced in the HTML part.
				cidStripped := stripAngleBrackets(cid)
				if !strings.Contains(htmlContent, "cid:"+cidStripped) {
					t.Errorf("html part references cid but inline part CID %q not found in html: %q",
						cidStripped, htmlContent)
				}
				foundInline = true
				break
			}
		}
		if !foundInline {
			t.Error("no sibling inline part with Content-Disposition: inline found in multipart/related")
		}
	})

	// --- Sub-test B: Plain message stays text/plain ---
	t.Run("plain_send_single_part", func(t *testing.T) {
		// Use a subject that does NOT contain the word "multipart" so that the
		// raw-bytes check below does not match the Subject header line.
		plainSubject := "PlainCompose-Integration-Test"
		resp, err := client.PostForm(baseURL+"/compose/send", url.Values{
			"to":        {"admin@localhost"},
			"subject":   {plainSubject},
			"body":      {"hello plain"},
			"body_mode": {"plain"},
		})
		if err != nil {
			t.Fatalf("POST /compose/send (plain): %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("POST /compose/send (plain) status = %d, want 200 (after redirect)", resp.StatusCode)
		}

		raw := rawSentMessageBySubject(t, a, adminUserID, plainSubject)

		msg, err := netmail.ReadMessage(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("ReadMessage: %v", err)
		}

		// Top-level Content-Type must be text/plain (single part).
		ct := msg.Header.Get("Content-Type")
		if !strings.HasPrefix(ct, "text/plain") {
			t.Errorf("Content-Type = %q, want text/plain", ct)
		}

		bodyData, _ := io.ReadAll(msg.Body)
		if !strings.Contains(string(bodyData), "hello plain") {
			t.Errorf("body = %q, want to contain 'hello plain'", string(bodyData))
		}

		// The Content-Type header (already checked above) is the authoritative
		// test for single-part.  As an extra guard, confirm the raw bytes contain
		// no MIME boundary marker (which would indicate multipart encoding).
		// We search for "Content-Type: multipart/" rather than the bare word
		// "multipart" to avoid false-positives from the Subject header value.
		if strings.Contains(strings.ToLower(string(raw)), "content-type: multipart/") {
			t.Error("raw message contains 'Content-Type: multipart/' — expected single text/plain for plain mode")
		}
	})
}
