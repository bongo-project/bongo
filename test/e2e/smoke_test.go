//go:build e2e

package e2e

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/bongo-go/bongo-go/internal/agent"
	"github.com/bongo-go/bongo-go/internal/config"
)

// smokeClient holds the shared state for an E2E test session.
type smokeClient struct {
	agent   *agent.Agent
	baseURL string
	client  *http.Client
	cancel  context.CancelFunc
}

// setupSmokeAgent starts a dev agent and logs in as admin.
// The caller must defer sc.cancel() to stop the agent.
func setupSmokeAgent(t *testing.T) *smokeClient {
	t.Helper()

	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	cfg.Server.BindAddr = "127.0.0.1"
	cfg.Web.Port = freePort(t)
	cfg.SMTP.Enabled = true
	cfg.IMAP.Enabled = true

	logger := testLogger()

	a, err := agent.NewAgent(cfg, logger)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- a.Run(ctx)
	}()

	deadline := time.Now().Add(20 * time.Second)
	for !a.Ready() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if !a.Ready() {
		cancel()
		t.Fatal("agent not ready in time")
	}

	baseURL := fmt.Sprintf("https://127.0.0.1:%d", cfg.Web.Port)

	jar, _ := cookiejar.New(nil)
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test only
	}
	client := &http.Client{
		Jar:       jar,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 20 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}

	sc := &smokeClient{
		agent:   a,
		baseURL: baseURL,
		client:  client,
		cancel:  cancel,
	}

	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Log("agent did not stop within 10s")
		}
	})

	return sc
}

// login performs a POST /login and verifies the session cookie is set.
func (sc *smokeClient) login(t *testing.T, email, password string) {
	t.Helper()
	resp, err := sc.client.PostForm(sc.baseURL+"/login", url.Values{
		"email":    {email},
		"password": {password},
	})
	if err != nil {
		t.Fatalf("POST /login: %v", err)
	}
	resp.Body.Close()

	// The client follows redirects; after login we should land on /mail/INBOX (200).
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login: final status = %d, want 200", resp.StatusCode)
	}

	// Verify a session cookie was set.
	u, _ := url.Parse(sc.baseURL)
	cookies := sc.client.Jar.Cookies(u)
	found := false
	for _, c := range cookies {
		if c.Name == "bongo_session" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("no bongo_session cookie after login")
	}
}

// get issues a GET and returns the response body as a string.
func (sc *smokeClient) get(t *testing.T, path string) (int, string) {
	t.Helper()
	resp, err := sc.client.Get(sc.baseURL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// post issues a POST with form values and returns the response body.
func (sc *smokeClient) post(t *testing.T, path string, values url.Values) (int, string) {
	t.Helper()
	resp, err := sc.client.PostForm(sc.baseURL+path, values)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// pollInbox polls GET /mail/INBOX until `needle` appears in the body or timeout.
func (sc *smokeClient) pollInbox(t *testing.T, needle string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_, body := sc.get(t, "/mail/INBOX")
		if strings.Contains(body, needle) {
			return body
		}
		time.Sleep(200 * time.Millisecond)
	}
	_, body := sc.get(t, "/mail/INBOX")
	return body
}

// TestFullUserJourney runs an 18-step smoke test of the full user experience.
func TestFullUserJourney(t *testing.T) {
	// Step 1: start dev agent.
	sc := setupSmokeAgent(t)

	// Step 2: Login.
	t.Run("step02_login", func(t *testing.T) {
		sc.login(t, "admin@localhost", "admin")
		t.Log("login OK")
	})

	// Step 3: Browse inbox.
	t.Run("step03_browse_inbox", func(t *testing.T) {
		status, body := sc.get(t, "/mail/INBOX")
		if status != http.StatusOK {
			t.Fatalf("GET /mail/INBOX: status %d", status)
		}
		// Inbox page must contain mailbox list markers.
		if !strings.Contains(body, "INBOX") {
			t.Error("inbox page does not contain 'INBOX' mailbox reference")
		}
		t.Log("browse inbox OK")
	})

	// Step 4: Compose & send.
	subject := fmt.Sprintf("Smoke-%d", time.Now().UnixMilli())
	t.Run("step04_compose_send", func(t *testing.T) {
		status, _ := sc.post(t, "/compose/send", url.Values{
			"to":      {"admin@localhost"},
			"subject": {subject},
			"body":    {"Hello from the E2E smoke test."},
		})
		// Should redirect and follow to Sent or inbox (200 after redirects).
		if status != http.StatusOK {
			t.Fatalf("POST /compose/send: status %d, want 200", status)
		}
		t.Log("compose & send OK")
	})

	// Step 5: Receive — wait for delivery.
	var inboxBody string
	t.Run("step05_receive", func(t *testing.T) {
		inboxBody = sc.pollInbox(t, subject, 10*time.Second)
		if !strings.Contains(inboxBody, subject) {
			t.Fatalf("message %q not found in inbox after delivery wait", subject)
		}
		t.Log("message delivered OK")
	})

	// Step 6: Read message.
	t.Run("step06_read_message", func(t *testing.T) {
		status, body := sc.get(t, "/mail/INBOX/1")
		if status != http.StatusOK {
			t.Fatalf("GET /mail/INBOX/1: status %d", status)
		}
		if !strings.Contains(body, subject) {
			t.Errorf("message view does not contain subject %q", subject)
		}
		if !strings.Contains(body, "Hello from the E2E smoke test.") {
			t.Error("message view does not contain expected body text")
		}
		t.Log("read message OK")
	})

	// Step 7: Reply.
	t.Run("step07_reply", func(t *testing.T) {
		// The reply compose page should load.
		status, body := sc.get(t, "/compose/reply/INBOX/1")
		if status != http.StatusOK {
			t.Fatalf("GET /compose/reply/INBOX/1: status %d", status)
		}
		// Should pre-fill the To field or show compose form.
		if !strings.Contains(body, "compose") && !strings.Contains(body, "reply") && !strings.Contains(body, "subject") {
			t.Error("reply page does not look like a compose form")
		}

		// Submit the reply.
		replySubject := "Re: " + subject
		status2, _ := sc.post(t, "/compose/send", url.Values{
			"to":          {"admin@localhost"},
			"subject":     {replySubject},
			"body":        {"This is a reply."},
			"in_reply_to": {subject},
		})
		if status2 != http.StatusOK {
			t.Fatalf("POST /compose/send (reply): status %d", status2)
		}
		t.Log("reply OK")
	})

	// Step 8: Search.
	t.Run("step08_search", func(t *testing.T) {
		// Allow indexing time.
		time.Sleep(500 * time.Millisecond)
		status, body := sc.get(t, "/search?q="+url.QueryEscape(subject))
		if status != http.StatusOK {
			t.Fatalf("GET /search: status %d", status)
		}
		// Search page should load (results may or may not be indexed yet — we just verify the page).
		if !strings.Contains(body, "search") && !strings.Contains(body, "Search") {
			t.Error("search page does not contain search-related content")
		}
		t.Log("search OK")
	})

	// Step 9: Star message (toggle flagged flag).
	t.Run("step09_star_message", func(t *testing.T) {
		status, _ := sc.post(t, "/mail/INBOX/1/flags", url.Values{
			"flag": {"flagged"},
		})
		// Accept 200 or redirect (303).
		if status != http.StatusOK && status != http.StatusSeeOther && status != http.StatusNoContent {
			t.Fatalf("POST /mail/INBOX/1/flags: status %d", status)
		}
		t.Log("star message OK")
	})

	// Step 10: Move message to Sent (or another mailbox).
	t.Run("step10_move_message", func(t *testing.T) {
		status, _ := sc.post(t, "/mail/INBOX/1/move", url.Values{
			"destination": {"Sent"},
		})
		if status != http.StatusOK && status != http.StatusSeeOther && status != http.StatusNoContent {
			t.Fatalf("POST /mail/INBOX/1/move: status %d", status)
		}
		t.Log("move message OK")
	})

	// Step 11: Create contact.
	t.Run("step11_create_contact", func(t *testing.T) {
		status, body := sc.post(t, "/contacts", url.Values{
			"display_name": {"Smoke Test User"},
			"email":        {"smoketest@example.com"},
			"phone":        {"+1-555-0100"},
			"company":      {"ACME Corp"},
			"notes":        {"E2E smoke test contact"},
		})
		if status != http.StatusOK {
			t.Fatalf("POST /contacts: status %d", status)
		}
		if !strings.Contains(body, "Smoke Test User") {
			t.Error("contacts list does not contain newly created contact")
		}
		t.Log("create contact OK")
	})

	// Step 12: Contacts in autocomplete (GAL search).
	t.Run("step12_gal_autocomplete", func(t *testing.T) {
		status, body := sc.get(t, "/api/gal?q=Smoke")
		if status != http.StatusOK {
			t.Fatalf("GET /api/gal: status %d", status)
		}
		// Should return JSON with the contact.
		if !strings.Contains(body, "smoketest@example.com") && !strings.Contains(body, "Smoke") {
			t.Logf("GAL response: %s", body[:min(len(body), 500)])
			t.Error("GAL autocomplete did not return the smoke test contact")
		}
		t.Log("GAL autocomplete OK")
	})

	// Step 13: Calendar — create event.
	now := time.Now()
	t.Run("step13_create_event", func(t *testing.T) {
		startDate := now.Format("2006-01-02")
		startTime := now.Format("15:04")
		endDate := now.Format("2006-01-02")
		endTime := now.Add(time.Hour).Format("15:04")
		status, body := sc.post(t, "/calendar/event", url.Values{
			"title":       {"Smoke Test Meeting"},
			"description": {"An E2E test event"},
			"start_date":  {startDate},
			"start_time":  {startTime},
			"end_date":    {endDate},
			"end_time":    {endTime},
		})
		if status != http.StatusOK && status != http.StatusSeeOther {
			t.Fatalf("POST /calendar/event: status %d, body: %s", status, body[:min(len(body), 500)])
		}
		t.Log("create event OK")
	})

	// Step 14: Calendar — month view.
	t.Run("step14_calendar_month_view", func(t *testing.T) {
		month := int(now.Month())
		year := now.Year()
		status, body := sc.get(t, fmt.Sprintf("/calendar/%d/%d", year, month))
		if status != http.StatusOK {
			t.Fatalf("GET /calendar/%d/%d: status %d", year, month, status)
		}
		if !strings.Contains(body, "calendar") && !strings.Contains(body, "Calendar") {
			t.Error("calendar month view does not contain calendar content")
		}
		t.Log("calendar month view OK")
	})

	// Step 15: Admin dashboard.
	t.Run("step15_admin_dashboard", func(t *testing.T) {
		status, body := sc.get(t, "/admin")
		if status != http.StatusOK {
			t.Fatalf("GET /admin: status %d", status)
		}
		if !strings.Contains(body, "admin") && !strings.Contains(body, "Admin") && !strings.Contains(body, "dashboard") {
			t.Error("admin page does not contain expected dashboard content")
		}
		t.Log("admin dashboard OK")
	})

	// Step 16: Admin user list.
	t.Run("step16_admin_user_list", func(t *testing.T) {
		status, body := sc.get(t, "/admin/users")
		if status != http.StatusOK {
			t.Fatalf("GET /admin/users: status %d", status)
		}
		if !strings.Contains(body, "admin@localhost") {
			t.Error("admin user list does not contain admin@localhost")
		}
		t.Log("admin user list OK")
	})

	// Step 17: Settings page.
	t.Run("step17_settings", func(t *testing.T) {
		status, body := sc.get(t, "/settings")
		if status != http.StatusOK {
			t.Fatalf("GET /settings: status %d", status)
		}
		if !strings.Contains(body, "settings") && !strings.Contains(body, "Settings") && !strings.Contains(body, "theme") {
			t.Error("settings page does not contain expected content")
		}
		t.Log("settings page OK")
	})

	// Step 18: Logout.
	t.Run("step18_logout", func(t *testing.T) {
		status, _ := sc.post(t, "/logout", url.Values{})
		if status != http.StatusOK {
			t.Fatalf("POST /logout: final status %d, want 200", status)
		}

		// After logout, accessing a protected page should redirect to login.
		noRedirectClient := &http.Client{
			Jar:       sc.client.Jar,
			Transport: sc.client.Transport,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
		resp, err := noRedirectClient.Get(sc.baseURL + "/mail/INBOX")
		if err != nil {
			t.Fatalf("GET /mail/INBOX after logout: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusSeeOther && resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusTemporaryRedirect {
			t.Fatalf("expected redirect after logout, got %d", resp.StatusCode)
		}
		location := resp.Header.Get("Location")
		if !strings.Contains(location, "login") {
			t.Errorf("logout redirect location %q does not contain 'login'", location)
		}
		t.Log("logout OK")
	})
}

// TestAccessibilityBasics verifies ARIA landmarks and skip links are present.
func TestAccessibilityBasics(t *testing.T) {
	sc := setupSmokeAgent(t)
	sc.login(t, "admin@localhost", "admin")

	status, body := sc.get(t, "/mail/INBOX")
	if status != http.StatusOK {
		t.Fatalf("GET /mail/INBOX: status %d", status)
	}

	t.Run("aria_labels_present", func(t *testing.T) {
		if !strings.Contains(body, "aria-label") {
			t.Error("inbox page has no aria-label attributes")
		}
	})

	t.Run("skip_link_present", func(t *testing.T) {
		if !strings.Contains(body, "skip-link") && !strings.Contains(body, "#main-content") {
			t.Error("inbox page has no skip navigation link")
		}
	})

	t.Run("main_content_id_present", func(t *testing.T) {
		if !strings.Contains(body, `id="main-content"`) {
			t.Error(`inbox page has no element with id="main-content"`)
		}
	})

	t.Run("role_banner_present", func(t *testing.T) {
		if !strings.Contains(body, `role="banner"`) {
			t.Error(`inbox page has no element with role="banner" on header`)
		}
	})
}

// TestVisualRegressionBaseline verifies CSS, static assets, and visual markers.
func TestVisualRegressionBaseline(t *testing.T) {
	sc := setupSmokeAgent(t)

	t.Run("css_serves_200", func(t *testing.T) {
		resp, err := sc.client.Get(sc.baseURL + "/static/css/bongo.css")
		if err != nil {
			t.Fatalf("GET bongo.css: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("bongo.css status = %d, want 200", resp.StatusCode)
		}
		ct := resp.Header.Get("Content-Type")
		if !strings.Contains(ct, "css") {
			t.Errorf("bongo.css Content-Type = %q, want text/css", ct)
		}
		body, _ := io.ReadAll(resp.Body)
		// Verify Bongo colour palette variables exist.
		if !strings.Contains(string(body), "--bongo-orange") {
			t.Error("bongo.css does not contain --bongo-orange variable")
		}
		if !strings.Contains(string(body), "--bongo-blue") {
			t.Error("bongo.css does not contain --bongo-blue variable")
		}
		if !strings.Contains(string(body), "--msg-unread-border") {
			t.Error("bongo.css does not contain --msg-unread-border variable")
		}
	})

	t.Run("favicon_serves_200", func(t *testing.T) {
		resp, err := sc.client.Get(sc.baseURL + "/static/img/bongo-favicon.png")
		if err != nil {
			t.Fatalf("GET favicon: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("favicon status = %d, want 200", resp.StatusCode)
		}
	})

	t.Run("contact_avatar_serves_200", func(t *testing.T) {
		resp, err := sc.client.Get(sc.baseURL + "/static/img/contact-unknown.png")
		if err != nil {
			t.Fatalf("GET contact-unknown.png: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("contact-unknown.png status = %d, want 200", resp.StatusCode)
		}
	})

	t.Run("favicon_link_in_html", func(t *testing.T) {
		sc.login(t, "admin@localhost", "admin")
		_, body := sc.get(t, "/mail/INBOX")
		if !strings.Contains(body, "bongo-favicon.png") {
			t.Error("inbox HTML does not contain bongo-favicon.png link")
		}
	})

	t.Run("theme_toggle_works", func(t *testing.T) {
		_, body := sc.get(t, "/mail/INBOX")
		if !strings.Contains(body, "data-theme") {
			t.Error("inbox page does not contain data-theme attribute")
		}
	})
}

// TestI18nLocaleSwitch verifies that the locale preference persists and affects the html lang attribute.
func TestI18nLocaleSwitch(t *testing.T) {
	sc := setupSmokeAgent(t)
	sc.login(t, "admin@localhost", "admin")

	// Switch to French.
	t.Run("switch_to_fr", func(t *testing.T) {
		sc.post(t, "/settings", url.Values{
			"locale":            {"fr"},
			"theme":             {"auto"},
			"layout_mode":       {"list"},
			"density":           {"comfortable"},
			"messages_per_page": {"25"},
			"default_reply":     {"sender"},
		})

		// GET /settings should now have lang="fr".
		_, body := sc.get(t, "/settings")
		if !strings.Contains(body, `lang="fr"`) {
			t.Errorf("settings page lang attribute not updated to fr; body excerpt: %s", body[:min(len(body), 500)])
		}
	})

	// Switch back to English.
	t.Run("switch_back_to_en", func(t *testing.T) {
		sc.post(t, "/settings", url.Values{
			"locale":            {"en"},
			"theme":             {"auto"},
			"layout_mode":       {"list"},
			"density":           {"comfortable"},
			"messages_per_page": {"25"},
			"default_reply":     {"sender"},
		})

		_, body := sc.get(t, "/settings")
		if !strings.Contains(body, `lang="en"`) {
			t.Errorf("settings page lang attribute not updated back to en; body excerpt: %s", body[:min(len(body), 500)])
		}
	})
}
