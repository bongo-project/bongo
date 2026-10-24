//go:build integration

package integration

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	smtplib "net/smtp"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/agent"
	"github.com/bongo-project/bongo/internal/config"
)

func setupRenderingAgent(t *testing.T) (*agent.Agent, string, *http.Client, context.CancelFunc) {
	t.Helper()
	cfg := config.Defaults()
	cfg.Pipeline.PollInterval = "50ms" // deliver promptly so short test waits suffice
	cfg.Server.Role = "dev"
	cfg.Server.BindAddr = "127.0.0.1"
	sharedAssignFreePorts(t, cfg)
	cfg.SMTP.Enabled = true
	cfg.IMAP.Enabled = true

	a, cancel, _ := sharedStartAgent(t, cfg)

	// Dev mode serves the web UI over plain HTTP (like every other integration
	// test agent), so use http:// rather than https://.
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", cfg.Web.Port)

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

	// Login.
	resp, err := client.PostForm(baseURL+"/login", url.Values{
		"email":    {"admin@localhost"},
		"password": {"admin"},
	})
	if err != nil {
		cancel()
		t.Fatalf("POST /login: %v", err)
	}
	resp.Body.Close()

	return a, baseURL, client, cancel
}

func TestComposeWithAttachments(t *testing.T) {
	a, baseURL, client, cancel := setupRenderingAgent(t)
	defer cancel()
	_ = a

	// Build multipart form with text body + 2 file attachments.
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	writer.WriteField("send_id", composeSendID())
	writer.WriteField("to", "admin@localhost")
	writer.WriteField("subject", "Attachment Test")
	writer.WriteField("body", "Message with attachments")

	// PNG attachment (1x1 red pixel).
	pngData := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
		0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xde}
	part1, _ := writer.CreateFormFile("attachments", "test.png")
	part1.Write(pngData)

	// Text attachment.
	part2, _ := writer.CreateFormFile("attachments", "readme.txt")
	part2.Write([]byte("Hello from the test file."))

	writer.Close()

	req, _ := http.NewRequest("POST", baseURL+"/compose/send", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /compose/send: %v", err)
	}
	defer resp.Body.Close()

	// Should redirect to Sent folder.
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && !strings.Contains(string(respBody), "Sent") {
		t.Errorf("expected redirect to Sent, got status %d", resp.StatusCode)
	}

	// Wait for local delivery.
	time.Sleep(500 * time.Millisecond)

	// Check inbox for the message.
	resp2, err := client.Get(baseURL + "/mail/INBOX")
	if err != nil {
		t.Fatalf("GET /mail/INBOX: %v", err)
	}
	defer resp2.Body.Close()
	inboxBody, _ := io.ReadAll(resp2.Body)
	if !strings.Contains(string(inboxBody), "Attachment Test") {
		t.Error("inbox does not contain 'Attachment Test'")
	}

	// View the message to check attachments are listed.
	resp3, err := client.Get(baseURL + "/mail/INBOX/1")
	if err != nil {
		t.Fatalf("GET /mail/INBOX/1: %v", err)
	}
	defer resp3.Body.Close()
	msgBody, _ := io.ReadAll(resp3.Body)
	msgStr := string(msgBody)

	if !strings.Contains(msgStr, "test.png") {
		t.Error("message does not show test.png attachment")
	}
	if !strings.Contains(msgStr, "readme.txt") {
		t.Error("message does not show readme.txt attachment")
	}
}

func TestInlineAttachmentPreview(t *testing.T) {
	a, baseURL, client, cancel := setupRenderingAgent(t)
	defer cancel()

	// Send a message with an image attachment via compose.
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	writer.WriteField("send_id", composeSendID())
	writer.WriteField("to", "admin@localhost")
	writer.WriteField("subject", "Inline Preview Test")
	writer.WriteField("body", "Check inline preview")

	pngData := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
		0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xde}
	part1, _ := writer.CreateFormFile("attachments", "photo.png")
	part1.Write(pngData)
	writer.Close()

	req, _ := http.NewRequest("POST", baseURL+"/compose/send", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /compose/send: %v", err)
	}
	resp.Body.Close()

	time.Sleep(500 * time.Millisecond)

	// View the message.
	resp2, err := client.Get(baseURL + "/mail/INBOX/1")
	if err != nil {
		t.Fatalf("GET message: %v", err)
	}
	defer resp2.Body.Close()
	msgBody, _ := io.ReadAll(resp2.Body)
	msgStr := string(msgBody)

	// Check for inline image tag (thumbnail).
	if !strings.Contains(msgStr, "attachment-thumbnail") {
		t.Log("response:", msgStr[:min(len(msgStr), 2000)])
		t.Error("message does not contain inline image thumbnail")
	}

	// Verify inline blob URL.
	if !strings.Contains(msgStr, "inline=1") {
		t.Error("message does not contain inline blob URL")
	}

	_ = a
}

func TestEMLExport(t *testing.T) {
	a, baseURL, client, cancel := setupRenderingAgent(t)
	defer cancel()

	// Deliver a test email via SMTP.
	smtpAddr := a.SMTP.MXAddr()
	msg := "From: sender@localhost\r\n" +
		"To: admin@localhost\r\n" +
		"Subject: EML Export Test\r\n" +
		"Date: Sun, 22 Feb 2026 12:00:00 +0000\r\n" +
		"\r\n" +
		"Test body for EML export.\r\n"

	err := smtplib.SendMail(smtpAddr, nil, "sender@localhost", []string{"admin@localhost"}, []byte(msg))
	if err != nil {
		t.Fatalf("SendMail: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	// Export the message.
	noRedirectClient := &http.Client{
		Jar:       client.Jar,
		Transport: client.Transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := noRedirectClient.Get(baseURL + "/mail/INBOX/1/export")
	if err != nil {
		t.Fatalf("GET export: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if ct != "message/rfc822" {
		t.Errorf("Content-Type = %q, want message/rfc822", ct)
	}

	cd := resp.Header.Get("Content-Disposition")
	if !strings.HasPrefix(cd, "attachment") {
		t.Errorf("Content-Disposition = %q, want attachment", cd)
	}
	if !strings.Contains(cd, ".eml") {
		t.Errorf("Content-Disposition missing .eml: %q", cd)
	}

	// Verify body is valid RFC 5322.
	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)
	for _, header := range []string{"From:", "To:", "Subject:", "Date:"} {
		if !strings.Contains(bodyStr, header) {
			t.Errorf("EML body missing %s header", header)
		}
	}
}

// TestViewSourceRoute exercises the real middleware stack (RequireAuth +
// Require2FA) for GET /mail/{mailbox}/{uid}/source: an authenticated owner
// gets the raw RFC 5322 source in a <pre>, while a request with no session
// cookie is redirected to login and receives none of the message content.
func TestViewSourceRoute(t *testing.T) {
	a, baseURL, client, cancel := setupRenderingAgent(t)
	defer cancel()

	smtpAddr := a.SMTP.MXAddr()
	msg := "From: sender@localhost\r\n" +
		"To: admin@localhost\r\n" +
		"Subject: View Source Test\r\n" +
		"Date: Sun, 22 Feb 2026 12:00:00 +0000\r\n" +
		"\r\n" +
		"Test body for view source.\r\n"

	if err := smtplib.SendMail(smtpAddr, nil, "sender@localhost", []string{"admin@localhost"}, []byte(msg)); err != nil {
		t.Fatalf("SendMail: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	noRedirectClient := &http.Client{
		Jar:       client.Jar,
		Transport: client.Transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := noRedirectClient.Get(baseURL + "/mail/INBOX/1/source")
	if err != nil {
		t.Fatalf("GET source (authenticated): %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("authenticated status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)
	if !strings.Contains(bodyStr, "<pre") {
		t.Error("authenticated response missing <pre> element")
	}
	for _, header := range []string{"From:", "To:", "Subject: View Source Test"} {
		if !strings.Contains(bodyStr, header) {
			t.Errorf("authenticated response missing %q", header)
		}
	}

	// A request with no session cookie must redirect to login and leak
	// nothing of the message content.
	anonJar, _ := cookiejar.New(nil)
	anonClient := &http.Client{
		Jar: anonJar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	anonResp, err := anonClient.Get(baseURL + "/mail/INBOX/1/source")
	if err != nil {
		t.Fatalf("GET source (anonymous): %v", err)
	}
	defer anonResp.Body.Close()

	if anonResp.StatusCode != http.StatusSeeOther && anonResp.StatusCode != http.StatusFound {
		t.Errorf("anonymous status = %d, want a redirect (303/302)", anonResp.StatusCode)
	}
	if loc := anonResp.Header.Get("Location"); loc != "/login" {
		t.Errorf("anonymous Location = %q, want /login", loc)
	}
	anonBody, _ := io.ReadAll(anonResp.Body)
	if strings.Contains(string(anonBody), "View Source Test") || strings.Contains(string(anonBody), "Test body for view source") {
		t.Errorf("anonymous response leaked message content: %q", anonBody)
	}
}

func TestMboxImport(t *testing.T) {
	_, baseURL, client, cancel := setupRenderingAgent(t)
	defer cancel()

	// Create a valid mbox file with 3 messages.
	var mbox bytes.Buffer
	for i := 1; i <= 3; i++ {
		fmt.Fprintf(&mbox, "From sender@localhost %s\r\n", time.Now().Format(time.ANSIC))
		fmt.Fprintf(&mbox, "From: sender%d@localhost\r\n", i)
		fmt.Fprintf(&mbox, "To: admin@localhost\r\n")
		fmt.Fprintf(&mbox, "Subject: Imported Message %d\r\n", i)
		fmt.Fprintf(&mbox, "Date: Sun, 22 Feb 2026 %02d:00:00 +0000\r\n", i+10)
		fmt.Fprintf(&mbox, "Message-ID: <import-%d@test>\r\n", i)
		fmt.Fprintf(&mbox, "\r\n")
		fmt.Fprintf(&mbox, "Body of message %d.\r\n", i)
		mbox.WriteString("\r\n")
	}

	// POST /import with mbox file.
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	writer.WriteField("mailbox", "Imported")
	part, _ := writer.CreateFormFile("mbox", "test.mbox")
	part.Write(mbox.Bytes())
	writer.Close()

	req, _ := http.NewRequest("POST", baseURL+"/import", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /import: %v", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	respStr := string(respBody)

	if !strings.Contains(respStr, "3") || !strings.Contains(respStr, "imported") {
		t.Errorf("expected '3 messages imported', got: %s", respStr[:min(len(respStr), 500)])
	}

	// Verify messages appear in "Imported" mailbox.
	resp2, err := client.Get(baseURL + "/mail/Imported")
	if err != nil {
		t.Fatalf("GET /mail/Imported: %v", err)
	}
	defer resp2.Body.Close()
	mbBody, _ := io.ReadAll(resp2.Body)
	mbStr := string(mbBody)

	for i := 1; i <= 3; i++ {
		expected := fmt.Sprintf("Imported Message %d", i)
		if !strings.Contains(mbStr, expected) {
			t.Errorf("mailbox does not contain %q", expected)
		}
	}
}

func TestHTMLRendering(t *testing.T) {
	a, baseURL, client, cancel := setupRenderingAgent(t)
	defer cancel()

	// Deliver email with <style> block and styled <div>.
	smtpAddr := a.SMTP.MXAddr()
	msg := "From: sender@localhost\r\n" +
		"To: admin@localhost\r\n" +
		"Subject: HTML Rendering Test\r\n" +
		"Date: Sun, 22 Feb 2026 12:00:00 +0000\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n" +
		"\r\n" +
		"<html><head><style>.highlight { color: red; font-weight: bold; }</style></head>" +
		"<body><div class=\"highlight\">Important text</div></body></html>\r\n"

	err := smtplib.SendMail(smtpAddr, nil, "sender@localhost", []string{"admin@localhost"}, []byte(msg))
	if err != nil {
		t.Fatalf("SendMail: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	// View the message.
	resp, err := client.Get(baseURL + "/mail/INBOX/1")
	if err != nil {
		t.Fatalf("GET message: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)

	// Verify <style> block is removed.
	if strings.Contains(bodyStr, "<style>") {
		t.Error("response still contains <style> block")
	}

	// Verify the div has inline style applied.
	if !strings.Contains(bodyStr, "color: red") && !strings.Contains(bodyStr, "color:red") {
		t.Log("body excerpt:", bodyStr[:min(len(bodyStr), 2000)])
		t.Error("styled div does not have inlined CSS")
	}

	// Verify email-body wrapper.
	if !strings.Contains(bodyStr, "email-body") {
		t.Error("response does not contain email-body wrapper")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
