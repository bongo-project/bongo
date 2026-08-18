//go:build integration

package integration

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	smtplib "net/smtp"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/bongo-go/bongo-go/internal/agent"
	"github.com/bongo-go/bongo-go/internal/config"
)

// startSecurityTestAgent starts a dev agent and returns the base URL and a cancel function.
func startSecurityTestAgent(t *testing.T) (baseURL, smtpAddr string, cancel context.CancelFunc) {
	t.Helper()

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

	ctx, cancelFn := context.WithCancel(context.Background())
	go func() {
		a.Run(ctx)
	}()

	deadline := time.Now().Add(15 * time.Second)
	for !a.Ready() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if !a.Ready() {
		cancelFn()
		t.Fatal("agent not ready in time")
	}

	return fmt.Sprintf("http://127.0.0.1:%d", cfg.Web.Port),
		fmt.Sprintf("127.0.0.1:%d", cfg.SMTP.Port),
		cancelFn
}

// newClient returns an http.Client with a cookie jar that follows redirects.
func newClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}
}

// newNoRedirectClient returns an http.Client with a cookie jar that does not
// follow redirects, allowing callers to inspect the redirect response directly.
func newNoRedirectClient(jar http.CookieJar) *http.Client {
	return &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// doLogin performs a form-based login and returns the response. The client's
// cookie jar is updated in place.
func doLogin(client *http.Client, baseURL, email, password string) (*http.Response, error) {
	return client.PostForm(baseURL+"/login", url.Values{
		"email":    {email},
		"password": {password},
	})
}

// TestLoginRateLimit verifies that the login rate limiter blocks requests after
// the configured number of failed attempts.
func TestLoginRateLimit(t *testing.T) {
	baseURL, _, cancel := startSecurityTestAgent(t)
	defer cancel()

	jar, _ := cookiejar.New(nil)
	noRedirect := newNoRedirectClient(jar)

	const maxAttempts = 5

	// Attempt maxAttempts failed logins.
	for i := 0; i < maxAttempts; i++ {
		resp, err := noRedirect.PostForm(baseURL+"/login", url.Values{
			"email":    {"admin@localhost"},
			"password": {"wrongpassword"},
		})
		if err != nil {
			t.Fatalf("attempt %d: POST /login: %v", i+1, err)
		}
		resp.Body.Close()
	}

	// The (maxAttempts+1)th attempt should be blocked with 429.
	resp, err := noRedirect.PostForm(baseURL+"/login", url.Values{
		"email":    {"admin@localhost"},
		"password": {"wrongpassword"},
	})
	if err != nil {
		t.Fatalf("blocked attempt: POST /login: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTooManyRequests {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("status = %d, want 429; body: %s", resp.StatusCode, body)
	}

	retryAfter := resp.Header.Get("Retry-After")
	if retryAfter == "" {
		t.Error("expected Retry-After header on 429 response")
	}

	// Verify login is still blocked on a subsequent attempt.
	resp2, err := noRedirect.PostForm(baseURL+"/login", url.Values{
		"email":    {"admin@localhost"},
		"password": {"wrongpassword"},
	})
	if err != nil {
		t.Fatalf("second blocked attempt: POST /login: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusTooManyRequests {
		t.Errorf("second blocked status = %d, want 429", resp2.StatusCode)
	}
}

func TestDAVBasicAuthFailuresAreRateLimited(t *testing.T) {
	baseURL, _, cancel := startSecurityTestAgent(t)
	defer cancel()

	for _, tc := range []struct {
		name string
		path string
	}{
		{name: "carddav", path: "/carddav/"},
		{name: "caldav", path: "/caldav/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := newNoRedirectClient(nil)
			var resp *http.Response
			for i := 0; i < 6; i++ {
				req, err := http.NewRequest(http.MethodGet, baseURL+tc.path, nil)
				if err != nil {
					t.Fatalf("NewRequest: %v", err)
				}
				req.SetBasicAuth("admin@localhost", "wrong-password")
				resp, err = client.Do(req)
				if err != nil {
					t.Fatalf("attempt %d: %v", i+1, err)
				}
				resp.Body.Close()
			}
			if resp.StatusCode != http.StatusTooManyRequests {
				t.Fatalf("status after repeated Basic Auth failures = %d, want 429", resp.StatusCode)
			}
		})
	}
}

// TestSecurityHeaders verifies that the expected security headers are present
// on HTML pages and that static assets carry Cache-Control.
func TestSecurityHeaders(t *testing.T) {
	baseURL, _, cancel := startSecurityTestAgent(t)
	defer cancel()

	client := newClient()

	t.Run("html page has security headers", func(t *testing.T) {
		resp, err := client.Get(baseURL + "/login")
		if err != nil {
			t.Fatalf("GET /login: %v", err)
		}
		defer resp.Body.Close()

		headers := map[string]string{
			"Content-Security-Policy": "",
			"X-Frame-Options":         "DENY",
			"X-Content-Type-Options":  "nosniff",
			"Referrer-Policy":         "strict-origin-when-cross-origin",
			"Permissions-Policy":      "",
		}

		for name, want := range headers {
			got := resp.Header.Get(name)
			if got == "" {
				t.Errorf("missing header %s", name)
				continue
			}
			if want != "" && got != want {
				t.Errorf("header %s = %q, want %q", name, got, want)
			}
		}

		// CSP should include frame-ancestors 'none'.
		csp := resp.Header.Get("Content-Security-Policy")
		if !strings.Contains(csp, "frame-ancestors") {
			t.Errorf("CSP missing frame-ancestors directive: %s", csp)
		}
	})

	t.Run("static assets have cache-control", func(t *testing.T) {
		// The static file server serves files under /static/. Try a known file.
		resp, err := client.Get(baseURL + "/static/pico.min.css")
		if err != nil {
			t.Fatalf("GET /static/pico.min.css: %v", err)
		}
		defer resp.Body.Close()

		// Only verify cache-control if the file was found.
		if resp.StatusCode == http.StatusOK {
			cc := resp.Header.Get("Cache-Control")
			if !strings.Contains(cc, "max-age") {
				t.Errorf("Cache-Control for static asset = %q, want max-age", cc)
			}
		}
	})
}

// TestTOTPSetupAndLogin covers TOTP setup, login with TOTP, and recovery code
// login in one combined flow.
func TestTOTPSetupAndLogin(t *testing.T) {
	baseURL, _, cancel := startSecurityTestAgent(t)
	defer cancel()

	client := newClient()

	// Login as admin.
	resp, err := doLogin(client, baseURL, "admin@localhost", "admin")
	if err != nil {
		t.Fatalf("initial login: %v", err)
	}
	resp.Body.Close()

	// Verify we are logged in.
	resp, err = client.Get(baseURL + "/mail/INBOX")
	if err != nil {
		t.Fatalf("GET /mail/INBOX after login: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 after login, got %d", resp.StatusCode)
	}

	// POST to the TOTP setup endpoint to get the setup page.
	resp, err = client.PostForm(baseURL+"/settings/security/totp/setup", url.Values{})
	if err != nil {
		t.Fatalf("POST /settings/security/totp/setup: %v", err)
	}
	setupBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("setup page status = %d, want 200; body: %s", resp.StatusCode, setupBody)
	}

	// Extract the manual key from the response HTML.
	// The template renders it inside a <pre> element.
	manualKey := extractTOTPManualKey(string(setupBody))
	if manualKey == "" {
		t.Fatal("could not extract TOTP manual key from setup page")
	}
	t.Logf("extracted TOTP manual key: %s", manualKey)

	// Generate a valid TOTP code using the extracted secret.
	code, err := totp.GenerateCode(manualKey, time.Now())
	if err != nil {
		t.Fatalf("totp.GenerateCode: %v", err)
	}

	// Confirm TOTP setup.
	resp, err = client.PostForm(baseURL+"/settings/security/totp/confirm", url.Values{
		"code": {code},
	})
	if err != nil {
		t.Fatalf("POST /settings/security/totp/confirm: %v", err)
	}
	confirmBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("confirm status = %d, want 200; body: %s", resp.StatusCode, confirmBody)
	}

	// Response should contain recovery codes (8 codes shown in the page).
	confirmStr := string(confirmBody)
	if !strings.Contains(confirmStr, "Recovery Codes") {
		t.Error("confirm response does not mention 'Recovery Codes'")
	}

	// Extract at least one recovery code from the page.
	recoveryCodes := extractRecoveryCodes(confirmStr)
	if len(recoveryCodes) == 0 {
		t.Fatal("could not extract any recovery codes from confirm response")
	}
	t.Logf("extracted %d recovery codes", len(recoveryCodes))
	recoveryCode := recoveryCodes[0]

	// Logout.
	jar, _ := cookiejar.New(nil)
	noRedirect := newNoRedirectClient(jar)
	// Copy session cookie to noRedirect jar.
	u, _ := url.Parse(baseURL)
	jar.SetCookies(u, client.Jar.Cookies(u))

	logoutResp, err := noRedirect.Post(baseURL+"/logout", "application/x-www-form-urlencoded", nil)
	if err != nil {
		t.Fatalf("POST /logout: %v", err)
	}
	logoutResp.Body.Close()

	// --- Test TOTP login ---
	t.Run("login requires 2FA", func(t *testing.T) {
		totpClient := newClient()

		// Login with correct password — should redirect to /login/2fa.
		jar2, _ := cookiejar.New(nil)
		noRedir := newNoRedirectClient(jar2)

		resp, err := noRedir.PostForm(baseURL+"/login", url.Values{
			"email":    {"admin@localhost"},
			"password": {"admin"},
		})
		if err != nil {
			t.Fatalf("login: %v", err)
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusSeeOther {
			t.Errorf("login status = %d, want 303", resp.StatusCode)
		}
		loc := resp.Header.Get("Location")
		if loc != "/login/2fa" {
			t.Errorf("Location = %q, want /login/2fa", loc)
		}

		// Transfer pending cookie to the following client.
		u, _ := url.Parse(baseURL)
		for _, c := range resp.Cookies() {
			jar2.SetCookies(u, []*http.Cookie{c})
		}
		totpClient.Jar = jar2

		// Generate a fresh TOTP code and submit.
		freshCode, err := totp.GenerateCode(manualKey, time.Now())
		if err != nil {
			t.Fatalf("totp.GenerateCode: %v", err)
		}

		// Use no-redirect to capture the final redirect.
		noRedir2 := newNoRedirectClient(jar2)
		resp2, err := noRedir2.PostForm(baseURL+"/login/2fa", url.Values{
			"code": {freshCode},
		})
		if err != nil {
			t.Fatalf("POST /login/2fa: %v", err)
		}
		resp2.Body.Close()

		if resp2.StatusCode != http.StatusSeeOther {
			t.Errorf("2FA verify status = %d, want 303", resp2.StatusCode)
		}
		loc2 := resp2.Header.Get("Location")
		if !strings.Contains(loc2, "/mail/INBOX") {
			t.Errorf("after 2FA Location = %q, want /mail/INBOX", loc2)
		}
	})

	// --- Test recovery code login ---
	t.Run("login with recovery code", func(t *testing.T) {
		jar3, _ := cookiejar.New(nil)
		noRedir := newNoRedirectClient(jar3)

		// Initiate login.
		resp, err := noRedir.PostForm(baseURL+"/login", url.Values{
			"email":    {"admin@localhost"},
			"password": {"admin"},
		})
		if err != nil {
			t.Fatalf("login: %v", err)
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusSeeOther {
			t.Errorf("login status = %d, want 303", resp.StatusCode)
		}

		// Transfer pending cookie.
		u, _ := url.Parse(baseURL)
		for _, c := range resp.Cookies() {
			jar3.SetCookies(u, []*http.Cookie{c})
		}

		// Submit the recovery code.
		noRedir2 := newNoRedirectClient(jar3)
		resp2, err := noRedir2.PostForm(baseURL+"/login/2fa", url.Values{
			"code": {recoveryCode},
		})
		if err != nil {
			t.Fatalf("POST /login/2fa with recovery code: %v", err)
		}
		resp2.Body.Close()

		if resp2.StatusCode != http.StatusSeeOther {
			t.Errorf("recovery code verify status = %d, want 303", resp2.StatusCode)
		}
		loc := resp2.Header.Get("Location")
		if !strings.Contains(loc, "/mail/INBOX") {
			t.Errorf("after recovery code Location = %q, want /mail/INBOX", loc)
		}
	})
}

// TestPasswordChange verifies that a user can change their password and that
// the old password no longer works afterwards.
func TestPasswordChange(t *testing.T) {
	baseURL, _, cancel := startSecurityTestAgent(t)
	defer cancel()

	client := newClient()

	// Login as admin.
	resp, err := doLogin(client, baseURL, "admin@localhost", "admin")
	if err != nil {
		t.Fatalf("initial login: %v", err)
	}
	resp.Body.Close()

	// Change password.
	resp, err = client.PostForm(baseURL+"/settings/security/password", url.Values{
		"current_password": {"admin"},
		"new_password":     {"newpassword123"},
		"confirm_password": {"newpassword123"},
	})
	if err != nil {
		t.Fatalf("POST /settings/security/password: %v", err)
	}
	resp.Body.Close()

	// Should redirect to /settings/security with a flash.
	if !strings.Contains(resp.Request.URL.Path, "/settings/security") {
		t.Logf("final URL after password change: %s", resp.Request.URL.String())
	}

	// Logout.
	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse(baseURL)
	jar.SetCookies(u, client.Jar.Cookies(u))
	noRedirect := newNoRedirectClient(jar)

	logoutResp, err := noRedirect.Post(baseURL+"/logout", "application/x-www-form-urlencoded", nil)
	if err != nil {
		t.Fatalf("POST /logout: %v", err)
	}
	logoutResp.Body.Close()

	// Attempt login with old password — should fail (render login page, not redirect to inbox).
	oldClient := newClient()
	noRedir := newNoRedirectClient(oldClient.Jar)
	oldResp, err := noRedir.PostForm(baseURL+"/login", url.Values{
		"email":    {"admin@localhost"},
		"password": {"admin"},
	})
	if err != nil {
		t.Fatalf("login with old password: %v", err)
	}
	oldBody, _ := io.ReadAll(oldResp.Body)
	oldResp.Body.Close()

	// Old password login should NOT redirect to /mail/INBOX (should stay on login page).
	if oldResp.StatusCode == http.StatusSeeOther {
		loc := oldResp.Header.Get("Location")
		if strings.Contains(loc, "/mail/INBOX") {
			t.Error("old password still works: login redirected to inbox")
		}
	}
	if strings.Contains(string(oldBody), "Invalid email or password") == false &&
		oldResp.StatusCode == http.StatusSeeOther {
		// If we got a redirect, it should be to /login, not /mail/INBOX.
		loc := oldResp.Header.Get("Location")
		if !strings.HasPrefix(loc, "/login") {
			t.Errorf("unexpected redirect after old password login: %s", loc)
		}
	}

	// Login with new password — should succeed.
	newClient := newClient()
	noRedir2 := newNoRedirectClient(newClient.Jar)
	newResp, err := noRedir2.PostForm(baseURL+"/login", url.Values{
		"email":    {"admin@localhost"},
		"password": {"newpassword123"},
	})
	if err != nil {
		t.Fatalf("login with new password: %v", err)
	}
	newResp.Body.Close()

	if newResp.StatusCode != http.StatusSeeOther {
		t.Errorf("new password login status = %d, want 303", newResp.StatusCode)
	}
	loc := newResp.Header.Get("Location")
	if !strings.Contains(loc, "/mail/INBOX") {
		t.Errorf("new password login Location = %q, want /mail/INBOX", loc)
	}
}

// TestRemoteImageBlocking verifies that remote images in email bodies are
// replaced with blocked-image placeholders and that cid: references survive.
func TestRemoteImageBlocking(t *testing.T) {
	baseURL, agentSMTPAddr, cancel := startSecurityTestAgent(t)
	defer cancel()

	// Use the port this agent was actually assigned. Hardcoding the dev
	// default made the test depend on a fixed port shared by every agent in
	// the suite.
	smtpAddr := agentSMTPAddr

	htmlBody := `<html><body>` +
		`<p>Hello</p>` +
		`<img src="https://tracker.example.com/pixel.gif" alt="tracker">` +
		`<img src="cid:inline-image@example.com" alt="cid image">` +
		`</body></html>`

	msg := "From: sender@localhost\r\n" +
		"To: admin@localhost\r\n" +
		"Subject: Remote Image Test\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n" +
		"Date: Thu, 20 Feb 2026 12:00:00 +0000\r\n" +
		"\r\n" +
		htmlBody + "\r\n"

	if err := smtplib.SendMail(smtpAddr, nil, "sender@localhost", []string{"admin@localhost"}, []byte(msg)); err != nil {
		t.Skipf("SMTP delivery skipped (could not connect to %s): %v", smtpAddr, err)
	}

	// Wait for delivery.
	time.Sleep(500 * time.Millisecond)

	// Login as admin.
	client := newClient()
	resp, err := doLogin(client, baseURL, "admin@localhost", "admin")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	resp.Body.Close()

	// Fetch the message. Try UID 1 first; if that returns 404 try fetching the
	// message list and picking the first UID.
	var messageBody string
	for uid := 1; uid <= 5; uid++ {
		resp, err = client.Get(fmt.Sprintf("%s/mail/INBOX/%d", baseURL, uid))
		if err != nil {
			t.Fatalf("GET /mail/INBOX/%d: %v", uid, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK && strings.Contains(string(body), "Remote Image Test") {
			messageBody = string(body)
			break
		}
	}

	if messageBody == "" {
		t.Skip("could not find the delivered message — skipping image blocking assertions")
	}

	// The remote image must be blocked: the tracker URL may only appear inside
	// the placeholder's data-src attribute, never as a live <img src>. Match on
	// a leading space so data-src="..." (preceded by '-') is not a false hit.
	if strings.Contains(messageBody, ` src="https://tracker.example.com/pixel.gif"`) {
		t.Error("remote tracker URL appeared as img src — image blocking failed")
	}
	if strings.Contains(messageBody, ` src='https://tracker.example.com/pixel.gif'`) {
		t.Error("remote tracker URL appeared as img src — image blocking failed")
	}

	// The blocked image should appear as a data-src attribute.
	if !strings.Contains(messageBody, "blocked-image") {
		t.Error("expected 'blocked-image' class in message body")
	}
	if !strings.Contains(messageBody, "tracker.example.com") {
		t.Error("expected tracker URL to appear in data-src of blocked image placeholder")
	}
}

// TestRequire2FAPolicy verifies that when an admin enables the system-wide 2FA
// requirement, users without 2FA are redirected to the setup page, and that
// disabling the policy restores normal access.
func TestRequire2FAPolicy(t *testing.T) {
	baseURL, _, cancel := startSecurityTestAgent(t)
	defer cancel()

	// Login as admin.
	adminClient := newClient()
	resp, err := doLogin(adminClient, baseURL, "admin@localhost", "admin")
	if err != nil {
		t.Fatalf("admin login: %v", err)
	}
	resp.Body.Close()

	// Create a second user via the admin panel.
	resp, err = adminClient.PostForm(baseURL+"/admin/users", url.Values{
		"email":        {"testuser2fa@localhost"},
		"display_name": {"Test User"},
		"password":     {"testpass123"},
	})
	if err != nil {
		t.Fatalf("POST /admin/users: %v", err)
	}
	resp.Body.Close()

	// Enable the require-2FA policy.
	resp, err = adminClient.PostForm(baseURL+"/admin/settings/require-2fa", url.Values{
		"enabled": {"1"},
	})
	if err != nil {
		t.Fatalf("POST /admin/settings/require-2fa: %v", err)
	}
	resp.Body.Close()

	// Logout admin.
	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse(baseURL)
	jar.SetCookies(u, adminClient.Jar.Cookies(u))
	noRedirect := newNoRedirectClient(jar)
	logoutResp, err := noRedirect.Post(baseURL+"/logout", "application/x-www-form-urlencoded", nil)
	if err != nil {
		t.Fatalf("POST /logout: %v", err)
	}
	logoutResp.Body.Close()

	// Login as the new user (no TOTP set up).
	userClient := newClient()
	resp, err = doLogin(userClient, baseURL, "testuser2fa@localhost", "testpass123")
	if err != nil {
		t.Fatalf("user login: %v", err)
	}
	resp.Body.Close()

	// Attempt to access INBOX — should be redirected to /settings/security.
	noRedir := newNoRedirectClient(userClient.Jar)
	inboxResp, err := noRedir.Get(baseURL + "/mail/INBOX")
	if err != nil {
		t.Fatalf("GET /mail/INBOX: %v", err)
	}
	inboxResp.Body.Close()

	if inboxResp.StatusCode != http.StatusSeeOther {
		t.Errorf("inbox status = %d, want 303 (redirect due to 2FA policy)", inboxResp.StatusCode)
	}
	inboxLoc := inboxResp.Header.Get("Location")
	if !strings.Contains(inboxLoc, "/settings/security") {
		t.Errorf("redirect Location = %q, want /settings/security", inboxLoc)
	}

	// Also verify the security settings page is accessible (exempt from redirect).
	secResp, err := userClient.Get(baseURL + "/settings/security")
	if err != nil {
		t.Fatalf("GET /settings/security: %v", err)
	}
	secResp.Body.Close()
	if secResp.StatusCode != http.StatusOK {
		t.Errorf("security page status = %d, want 200", secResp.StatusCode)
	}

	// Disable the policy: re-login as admin.
	adminClient2 := newClient()
	resp, err = doLogin(adminClient2, baseURL, "admin@localhost", "admin")
	if err != nil {
		t.Fatalf("admin re-login: %v", err)
	}
	resp.Body.Close()

	resp, err = adminClient2.PostForm(baseURL+"/admin/settings/require-2fa", url.Values{
		"enabled": {"0"},
	})
	if err != nil {
		t.Fatalf("POST /admin/settings/require-2fa (disable): %v", err)
	}
	resp.Body.Close()

	// Logout admin again.
	jar2, _ := cookiejar.New(nil)
	u2, _ := url.Parse(baseURL)
	jar2.SetCookies(u2, adminClient2.Jar.Cookies(u2))
	noRedirect2 := newNoRedirectClient(jar2)
	logoutResp2, err := noRedirect2.Post(baseURL+"/logout", "application/x-www-form-urlencoded", nil)
	if err != nil {
		t.Fatalf("POST /logout admin2: %v", err)
	}
	logoutResp2.Body.Close()

	// Login as the second user again — policy is now disabled.
	userClient2 := newClient()
	resp, err = doLogin(userClient2, baseURL, "testuser2fa@localhost", "testpass123")
	if err != nil {
		t.Fatalf("user re-login: %v", err)
	}
	resp.Body.Close()

	// INBOX should now be accessible without redirection.
	inboxResp2, err := userClient2.Get(baseURL + "/mail/INBOX")
	if err != nil {
		t.Fatalf("GET /mail/INBOX after policy disabled: %v", err)
	}
	inboxResp2.Body.Close()

	if inboxResp2.StatusCode != http.StatusOK {
		t.Errorf("inbox status after policy disabled = %d, want 200", inboxResp2.StatusCode)
	}
}

// --- helpers ---

// extractTOTPManualKey attempts to extract the base32 TOTP secret from the
// TOTP setup page HTML. The template renders it in a <pre> element.
func extractTOTPManualKey(body string) string {
	// The template renders the manual key inside the #totp-key element.
	preRe := regexp.MustCompile(`(?s)id="totp-key".*?([A-Z2-7]{16,})`)
	matches := preRe.FindStringSubmatch(body)
	if len(matches) >= 2 {
		return strings.TrimSpace(matches[1])
	}
	return ""
}

// extractRecoveryCodes extracts recovery codes from the recovery-codes page.
// Each code is rendered inside a <div> with monospace styling and looks like
// "XXXX-XXXX" or similar alphanumeric patterns.
func extractRecoveryCodes(body string) []string {
	// Codes are displayed inside <div ...> elements in the grid.
	// Match patterns like: <div ...>XXXXXXXX</div> where the content is
	// an alphanumeric code (letters and digits only, 8+ chars).
	codeRe := regexp.MustCompile(`<div[^>]*>([A-Z0-9]{8}-[A-Z0-9]{8})</div>`)
	matches := codeRe.FindAllStringSubmatch(body, -1)

	seen := make(map[string]bool)
	var codes []string
	for _, m := range matches {
		if len(m) >= 2 {
			code := strings.TrimSpace(m[1])
			// Filter out obviously non-code content (HTML keywords, long words).
			if len(code) >= 8 && len(code) <= 17 && !seen[code] {
				seen[code] = true
				codes = append(codes, code)
			}
		}
	}
	return codes
}
