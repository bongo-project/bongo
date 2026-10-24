//go:build integration

package integration

import (
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	smtplib "net/smtp"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/config"
)

func TestWebUIFullRoundTrip(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	cfg.Server.BindAddr = "127.0.0.1"
	sharedAssignFreePorts(t, cfg)
	cfg.SMTP.Enabled = true
	cfg.IMAP.Enabled = true

	a, cancel, _ := sharedStartAgent(t, cfg)
	defer cancel()

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

	t.Run("unauthenticated redirects to login", func(t *testing.T) {
		// Use a client that doesn't follow redirects to verify the 303.
		noRedirectClient := &http.Client{
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
		resp, err := noRedirectClient.Get(baseURL + "/mail/INBOX")
		if err != nil {
			t.Fatalf("GET /mail/INBOX: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusSeeOther {
			t.Errorf("status = %d, want 303", resp.StatusCode)
		}
		loc := resp.Header.Get("Location")
		if loc != "/login" {
			t.Errorf("Location = %q, want /login", loc)
		}
	})

	t.Run("login", func(t *testing.T) {
		resp, err := client.PostForm(baseURL+"/login", url.Values{
			"email":    {"admin@localhost"},
			"password": {"admin"},
		})
		if err != nil {
			t.Fatalf("POST /login: %v", err)
		}
		defer resp.Body.Close()

		// Should redirect to /mail/INBOX after login.
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Logf("response body: %s", string(body))
		}

		// Verify we have a session cookie.
		u, _ := url.Parse(baseURL)
		cookies := jar.Cookies(u)
		found := false
		for _, c := range cookies {
			if c.Name == "bongo_session" {
				found = true
				break
			}
		}
		if !found {
			t.Error("no bongo_session cookie set after login")
		}
	})

	t.Run("view inbox", func(t *testing.T) {
		resp, err := client.Get(baseURL + "/mail/INBOX")
		if err != nil {
			t.Fatalf("GET /mail/INBOX: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want 200", resp.StatusCode)
		}

		body, _ := io.ReadAll(resp.Body)
		bodyStr := string(body)
		if !strings.Contains(bodyStr, "INBOX") {
			t.Error("response does not contain 'INBOX'")
		}
		if !strings.Contains(bodyStr, "Compose") {
			t.Error("response does not contain 'Compose' button")
		}
	})

	t.Run("send via SMTP and view", func(t *testing.T) {
		smtpAddr := a.SMTP.MXAddr()

		msg := "From: sender@localhost\r\n" +
			"To: admin@localhost\r\n" +
			"Subject: Web UI Test Message\r\n" +
			"Date: Thu, 20 Feb 2026 12:00:00 +0000\r\n" +
			"\r\n" +
			"Hello from the integration test.\r\n"

		err := smtplib.SendMail(smtpAddr, nil, "sender@localhost", []string{"admin@localhost"}, []byte(msg))
		if err != nil {
			t.Fatalf("SendMail: %v", err)
		}

		// Delivery is asynchronous (the pipeline poll interval defaults to 1s),
		// so poll the inbox until the message appears rather than assuming a
		// fixed delay shorter than the poll interval.
		var bodyStr string
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			resp, err := client.Get(baseURL + "/mail/INBOX")
			if err != nil {
				t.Fatalf("GET /mail/INBOX: %v", err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			bodyStr = string(body)
			if strings.Contains(bodyStr, "Web UI Test Message") {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if !strings.Contains(bodyStr, "Web UI Test Message") {
			t.Error("inbox does not contain 'Web UI Test Message'")
			t.Logf("body length: %d", len(bodyStr))
		}
	})

	t.Run("view message detail", func(t *testing.T) {
		// Get the message UID from the inbox.
		resp, err := client.Get(baseURL + "/mail/INBOX/1")
		if err != nil {
			t.Fatalf("GET /mail/INBOX/1: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want 200", resp.StatusCode)
			return
		}

		body, _ := io.ReadAll(resp.Body)
		bodyStr := string(body)
		if !strings.Contains(bodyStr, "Web UI Test Message") {
			t.Error("message detail does not contain subject")
		}
		if !strings.Contains(bodyStr, "sender@localhost") {
			t.Error("message detail does not contain sender")
		}
	})

	t.Run("compose and send", func(t *testing.T) {
		resp, err := client.PostForm(baseURL+"/compose/send", url.Values{
			"send_id": {composeSendID()},
			"to":      {"admin@localhost"},
			"subject": {"Composed via Web UI"},
			"body":    {"This message was composed in the web UI."},
		})
		if err != nil {
			t.Fatalf("POST /compose/send: %v", err)
		}
		defer resp.Body.Close()

		// Should redirect to Sent.
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Logf("compose response: %d body: %s", resp.StatusCode, body)
		}
	})

	t.Run("search", func(t *testing.T) {
		resp, err := client.Get(baseURL + "/search?q=test")
		if err != nil {
			t.Fatalf("GET /search: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want 200", resp.StatusCode)
		}
	})

	t.Run("logout", func(t *testing.T) {
		// Use a no-redirect client to verify the redirect.
		noRedirectClient := &http.Client{
			Jar: jar,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
		resp, err := noRedirectClient.Post(baseURL+"/logout", "application/x-www-form-urlencoded", nil)
		if err != nil {
			t.Fatalf("POST /logout: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusSeeOther {
			t.Errorf("logout status = %d, want 303", resp.StatusCode)
		}
		loc := resp.Header.Get("Location")
		if loc != "/login" {
			t.Errorf("Location = %q, want /login", loc)
		}

		// Verify the session cookie is cleared.
		for _, c := range resp.Cookies() {
			if c.Name == "bongo_session" && c.MaxAge < 0 {
				return // Cookie properly cleared.
			}
		}
	})
}
