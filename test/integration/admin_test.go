//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/bongo-go/bongo-go/internal/agent"
	"github.com/bongo-go/bongo-go/internal/config"
)

func startAdminTestAgent(t *testing.T) (baseURL string, cancel context.CancelFunc) {
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

	return fmt.Sprintf("http://127.0.0.1:%d", cfg.Web.Port), cancelFn
}

func loginAs(t *testing.T, baseURL, email, password string) *http.Client {
	t.Helper()
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

	resp, err := client.PostForm(baseURL+"/login", url.Values{
		"email":    {email},
		"password": {password},
	})
	if err != nil {
		t.Fatalf("login as %s: %v", email, err)
	}
	resp.Body.Close()

	return client
}

func TestAdminAuth(t *testing.T) {
	baseURL, cancel := startAdminTestAgent(t)
	defer cancel()

	// Login as admin.
	adminClient := loginAs(t, baseURL, "admin@localhost", "admin")

	t.Run("admin can access dashboard", func(t *testing.T) {
		resp, err := adminClient.Get(baseURL + "/admin")
		if err != nil {
			t.Fatalf("GET /admin: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want 200", resp.StatusCode)
		}

		body, _ := io.ReadAll(resp.Body)
		bodyStr := string(body)
		if !strings.Contains(bodyStr, "System Dashboard") {
			t.Error("dashboard does not contain 'System Dashboard'")
		}
	})

	t.Run("admin sidebar link present", func(t *testing.T) {
		resp, err := adminClient.Get(baseURL + "/mail/INBOX")
		if err != nil {
			t.Fatalf("GET /mail/INBOX: %v", err)
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		bodyStr := string(body)
		if !strings.Contains(bodyStr, "href=\"/admin\"") {
			t.Error("admin link not found in sidebar for admin user")
		}
	})

	t.Run("non-admin gets 403", func(t *testing.T) {
		// Create a regular user first.
		resp, err := adminClient.PostForm(baseURL+"/admin/users", url.Values{
			"email":    {"user@localhost"},
			"password": {"userpass"},
		})
		if err != nil {
			t.Fatalf("create user: %v", err)
		}
		resp.Body.Close()

		// Login as regular user.
		regularClient := loginAs(t, baseURL, "user@localhost", "userpass")

		// Use no-redirect client to check the 403.
		jar, _ := cookiejar.New(nil)
		u, _ := url.Parse(baseURL)
		jar.SetCookies(u, regularClient.Jar.Cookies(u))
		noRedirect := &http.Client{
			Jar: jar,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}

		resp, err = noRedirect.Get(baseURL + "/admin")
		if err != nil {
			t.Fatalf("GET /admin as regular user: %v", err)
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("status = %d, want 403", resp.StatusCode)
		}
	})
}

func TestUserManagement(t *testing.T) {
	baseURL, cancel := startAdminTestAgent(t)
	defer cancel()

	adminClient := loginAs(t, baseURL, "admin@localhost", "admin")

	t.Run("create user", func(t *testing.T) {
		resp, err := adminClient.PostForm(baseURL+"/admin/users", url.Values{
			"email":        {"test@localhost"},
			"display_name": {"Test User"},
			"password":     {"testpass"},
		})
		if err != nil {
			t.Fatalf("POST /admin/users: %v", err)
		}
		resp.Body.Close()
	})

	t.Run("list users shows new user", func(t *testing.T) {
		resp, err := adminClient.Get(baseURL + "/admin/users")
		if err != nil {
			t.Fatalf("GET /admin/users: %v", err)
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		bodyStr := string(body)
		if !strings.Contains(bodyStr, "test@localhost") {
			t.Error("user list does not contain test@localhost")
		}
	})

	t.Run("new user form offers configured domain selector", func(t *testing.T) {
		resp, err := adminClient.PostForm(baseURL+"/admin/domains", url.Values{
			"domain": {"accounts.local"},
		})
		if err != nil {
			t.Fatalf("POST /admin/domains: %v", err)
		}
		resp.Body.Close()

		resp, err = adminClient.Get(baseURL + "/admin/users/new")
		if err != nil {
			t.Fatalf("GET /admin/users/new: %v", err)
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		bodyStr := string(body)
		if !strings.Contains(bodyStr, `name="username"`) {
			t.Fatal("new user form does not contain username field")
		}
		if !strings.Contains(bodyStr, `name="domain"`) {
			t.Fatal("new user form does not contain domain selector")
		}
		if !strings.Contains(bodyStr, "accounts.local") {
			t.Fatal("new user form does not include configured domain")
		}
	})

	t.Run("create user from username and configured domain", func(t *testing.T) {
		resp, err := adminClient.PostForm(baseURL+"/admin/users", url.Values{
			"username":     {"jane"},
			"domain":       {"accounts.local"},
			"display_name": {"Jane Domain"},
			"password":     {"testpass3"},
		})
		if err != nil {
			t.Fatalf("POST /admin/users: %v", err)
		}
		resp.Body.Close()

		resp, err = adminClient.Get(baseURL + "/admin/users?q=" + url.QueryEscape("jane@accounts.local"))
		if err != nil {
			t.Fatalf("GET /admin/users: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if !strings.Contains(string(body), "jane@accounts.local") {
			t.Fatal("user list does not contain composed user address")
		}

		janeClient := loginAs(t, baseURL, "jane@accounts.local", "testpass3")
		resp, err = janeClient.Get(baseURL + "/mail/INBOX")
		if err != nil {
			t.Fatalf("GET /mail/INBOX as composed-domain user: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("composed-domain user mailbox status = %d, want 200", resp.StatusCode)
		}
	})

	t.Run("create user rejects unknown selected domain", func(t *testing.T) {
		resp, err := adminClient.PostForm(baseURL+"/admin/users", url.Values{
			"username": {"mallory"},
			"domain":   {"unknown.local"},
			"password": {"testpass4"},
		})
		if err != nil {
			t.Fatalf("POST /admin/users: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
	})

	t.Run("disable user prevents login", func(t *testing.T) {
		// Ensure user exists.
		adminClient.PostForm(baseURL+"/admin/users", url.Values{
			"email":        {"disable-test@localhost"},
			"display_name": {"Disable Test"},
			"password":     {"testpass2"},
		})

		// Find user ID by listing users page.
		resp, err := adminClient.Get(baseURL + "/admin/users?q=disable-test@localhost")
		if err != nil {
			t.Fatalf("GET /admin/users: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		// Extract user ID from the edit link. Skip /admin/users/new links.
		bodyStr := string(body)
		var userID string
		searchFrom := 0
		for {
			idx := strings.Index(bodyStr[searchFrom:], "/admin/users/")
			if idx == -1 {
				break
			}
			idx += searchFrom
			rest := bodyStr[idx+len("/admin/users/"):]
			endIdx := strings.IndexByte(rest, '"')
			if endIdx == -1 {
				break
			}
			candidate := rest[:endIdx]
			if candidate != "" && candidate != "new" && !strings.Contains(candidate, "/") {
				userID = candidate
				break
			}
			searchFrom = idx + 1
		}
		if userID == "" {
			t.Fatal("could not find user ID in user list")
		}

		// Disable user.
		resp, err = adminClient.PostForm(baseURL+"/admin/users/"+userID+"/toggle-disabled", url.Values{})
		if err != nil {
			t.Fatalf("toggle disable: %v", err)
		}
		resp.Body.Close()

		// Try to login as disabled user.
		jar2, _ := cookiejar.New(nil)
		client2 := &http.Client{
			Jar: jar2,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
		resp, err = client2.PostForm(baseURL+"/login", url.Values{
			"email":    {"disable-test@localhost"},
			"password": {"testpass2"},
		})
		if err != nil {
			t.Fatalf("login disabled user: %v", err)
		}
		loginBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		// Should show error page (200) with login form, NOT redirect to inbox.
		// The user is disabled in system.db, so Authenticate fails with bad credentials,
		// or the profile check catches the disabled flag.
		if resp.StatusCode == http.StatusSeeOther {
			loc := resp.Header.Get("Location")
			if strings.Contains(loc, "/mail/") {
				t.Error("disabled user should not be redirected to mail")
			}
		} else if resp.StatusCode == http.StatusOK {
			// Should show an error message (either "disabled" or "Invalid").
			bodyStr := string(loginBody)
			if !strings.Contains(bodyStr, "disabled") && !strings.Contains(bodyStr, "Invalid") {
				t.Errorf("login page should show error message, got: %s", bodyStr[:min(200, len(bodyStr))])
			}
		}
	})
}

func TestDisabledUserExistingSessionRevoked(t *testing.T) {
	baseURL, cancel := startAdminTestAgent(t)
	defer cancel()

	adminClient := loginAs(t, baseURL, "admin@localhost", "admin")
	resp, err := adminClient.PostForm(baseURL+"/admin/users", url.Values{
		"email":        {"revoke-session@localhost"},
		"display_name": {"Revoke Session"},
		"password":     {"testpass"},
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	resp.Body.Close()

	userClient := loginAs(t, baseURL, "revoke-session@localhost", "testpass")
	resp, err = userClient.Get(baseURL + "/mail/INBOX")
	if err != nil {
		t.Fatalf("GET /mail/INBOX before disable: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pre-disable inbox status = %d, want 200", resp.StatusCode)
	}

	userID := findUserIDByEmail(t, adminClient, baseURL, "revoke-session@localhost")
	resp, err = adminClient.PostForm(baseURL+"/admin/users/"+userID+"/toggle-disabled", url.Values{})
	if err != nil {
		t.Fatalf("toggle disable: %v", err)
	}
	resp.Body.Close()

	noRedirect := &http.Client{
		Jar: userClient.Jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err = noRedirect.Get(baseURL + "/mail/INBOX")
	if err != nil {
		t.Fatalf("GET /mail/INBOX after disable: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatal("disabled user's existing session still has mailbox access")
	}
}

func TestMailBlobRequiresMessageOwnership(t *testing.T) {
	baseURL, cancel := startAdminTestAgent(t)
	defer cancel()

	adminClient := loginAs(t, baseURL, "admin@localhost", "admin")
	for _, user := range []struct {
		email    string
		password string
	}{
		{"blob-owner@localhost", "ownerpass"},
		{"blob-attacker@localhost", "attackerpass"},
	} {
		resp, err := adminClient.PostForm(baseURL+"/admin/users", url.Values{
			"email":        {user.email},
			"display_name": {user.email},
			"password":     {user.password},
		})
		if err != nil {
			t.Fatalf("create %s: %v", user.email, err)
		}
		resp.Body.Close()
	}

	ownerClient := loginAs(t, baseURL, "blob-owner@localhost", "ownerpass")
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("to", "blob-owner@localhost")
	_ = writer.WriteField("subject", "Private attachment")
	_ = writer.WriteField("body", "Attached private data")
	part, err := writer.CreateFormFile("attachments", "private.txt")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	_, _ = part.Write([]byte(privateAttachmentBody))
	_ = writer.Close()

	req, err := http.NewRequest(http.MethodPost, baseURL+"/compose/send", &body)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := ownerClient.Do(req)
	if err != nil {
		t.Fatalf("owner compose: %v", err)
	}
	resp.Body.Close()

	// Delivery runs through the spool pipeline asynchronously, so the message is
	// not visible the instant /compose/send returns. Poll until it lands rather
	// than scanning once and racing the pipeline.
	var attachmentURL string
	var ownerUID int
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline) && attachmentURL == ""; time.Sleep(200 * time.Millisecond) {
		for uid := 1; uid <= 5 && attachmentURL == ""; uid++ {
			resp, err = ownerClient.Get(fmt.Sprintf("%s/mail/INBOX/%d", baseURL, uid))
			if err != nil {
				t.Fatalf("owner message view uid %d: %v", uid, err)
			}
			msgBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if strings.Contains(string(msgBody), "Private attachment") {
				attachmentURL = extractAttachmentURL(string(msgBody))
				ownerUID = uid
			}
		}
	}
	if attachmentURL == "" {
		t.Fatalf("could not find an attachment link in the owner's message view")
	}

	// Positive control: the owner CAN fetch their own attachment. Without this,
	// the attacker's failure below could just mean the URL is broken for
	// everyone, and the test would pass with ownership enforcement removed.
	resp, err = ownerClient.Get(baseURL + attachmentURL)
	if err != nil {
		t.Fatalf("owner attachment fetch: %v", err)
	}
	ownerBody, _ := io.ReadAll(resp.Body)
	ownerStatus := resp.StatusCode
	resp.Body.Close()
	if ownerStatus != http.StatusOK {
		t.Fatalf("owner could not fetch their own attachment: status %d", ownerStatus)
	}
	if !bytes.Contains(ownerBody, []byte(privateAttachmentBody)) {
		t.Fatalf("owner attachment fetch did not return the attachment contents")
	}

	attackerClient := loginAs(t, baseURL, "blob-attacker@localhost", "attackerpass")

	// The live path: attachments are served message-scoped, so ownership is
	// enforced by resolving the message for the authenticated user.
	resp, err = attackerClient.Get(baseURL + attachmentURL)
	if err != nil {
		t.Fatalf("attacker attachment fetch: %v", err)
	}
	attackerBody, _ := io.ReadAll(resp.Body)
	attackerStatus := resp.StatusCode
	resp.Body.Close()
	if attackerStatus == http.StatusOK {
		t.Fatalf("authenticated user without message ownership fetched another user's attachment (uid %d)", ownerUID)
	}
	if bytes.Contains(attackerBody, []byte(privateAttachmentBody)) {
		t.Fatal("attacker response leaked the attachment contents")
	}

	// The legacy content-addressed route. The composer no longer stores decoded
	// attachment blobs, so the message view does not link /mail/blob/{hash} for
	// new mail and a hash cannot be scraped from it. The route still exists and
	// still serves older stored messages, so probe it with the hash the
	// attachment WOULD have: content addressing makes that derivable from the
	// bytes without the UI exposing it.
	blobHash := sha256.Sum256([]byte(privateAttachmentBody))
	resp, err = attackerClient.Get(fmt.Sprintf("%s/mail/blob/%x", baseURL, blobHash))
	if err != nil {
		t.Fatalf("attacker blob fetch: %v", err)
	}
	blobBody, _ := io.ReadAll(resp.Body)
	blobStatus := resp.StatusCode
	resp.Body.Close()
	if blobStatus == http.StatusOK {
		t.Fatal("authenticated user without message ownership could fetch another user's blob")
	}
	if bytes.Contains(blobBody, []byte(privateAttachmentBody)) {
		t.Fatal("blob route leaked the attachment contents to a non-owner")
	}
}

func TestDomainAndAlias(t *testing.T) {
	baseURL, cancel := startAdminTestAgent(t)
	defer cancel()

	adminClient := loginAs(t, baseURL, "admin@localhost", "admin")

	t.Run("add domain", func(t *testing.T) {
		resp, err := adminClient.PostForm(baseURL+"/admin/domains", url.Values{
			"domain": {"test.local"},
		})
		if err != nil {
			t.Fatalf("POST /admin/domains: %v", err)
		}
		resp.Body.Close()
	})

	t.Run("list domains shows new domain", func(t *testing.T) {
		resp, err := adminClient.Get(baseURL + "/admin/domains")
		if err != nil {
			t.Fatalf("GET /admin/domains: %v", err)
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(body), "test.local") {
			t.Error("domain list does not contain test.local")
		}
	})

	t.Run("add alias", func(t *testing.T) {
		resp, err := adminClient.PostForm(baseURL+"/admin/aliases", url.Values{
			"address":     {"info@test.local"},
			"target_user": {"admin@localhost"},
		})
		if err != nil {
			t.Fatalf("POST /admin/aliases: %v", err)
		}
		resp.Body.Close()
	})

	t.Run("list aliases shows new alias", func(t *testing.T) {
		resp, err := adminClient.Get(baseURL + "/admin/aliases")
		if err != nil {
			t.Fatalf("GET /admin/aliases: %v", err)
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(body), "info@test.local") {
			t.Error("alias list does not contain info@test.local")
		}
	})

	t.Run("delete alias", func(t *testing.T) {
		resp, err := adminClient.PostForm(baseURL+"/admin/aliases/info@test.local/delete", url.Values{})
		if err != nil {
			t.Fatalf("delete alias: %v", err)
		}
		resp.Body.Close()

		// Verify removed.
		resp, err = adminClient.Get(baseURL + "/admin/aliases")
		if err != nil {
			t.Fatalf("GET /admin/aliases: %v", err)
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		if strings.Contains(string(body), "info@test.local") {
			t.Error("alias list still contains info@test.local after delete")
		}
	})
}

func findUserIDByEmail(t *testing.T, client *http.Client, baseURL, email string) string {
	t.Helper()
	resp, err := client.Get(baseURL + "/admin/users?q=" + url.QueryEscape(email))
	if err != nil {
		t.Fatalf("GET /admin/users: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	bodyStr := string(body)
	for searchFrom := 0; ; {
		idx := strings.Index(bodyStr[searchFrom:], "/admin/users/")
		if idx == -1 {
			break
		}
		idx += searchFrom
		rest := bodyStr[idx+len("/admin/users/"):]
		endIdx := strings.IndexByte(rest, '"')
		if endIdx == -1 {
			break
		}
		candidate := rest[:endIdx]
		if candidate != "" && candidate != "new" && !strings.Contains(candidate, "/") {
			return candidate
		}
		searchFrom = idx + 1
	}
	t.Fatalf("could not find user ID for %s", email)
	return ""
}

// privateAttachmentBody is the attachment payload. It is deliberately small:
// staying under the attachment-dedup threshold keeps the message off the split
// path, and the exact bytes are needed to derive the content-addressed hash for
// the legacy /mail/blob probe.
const privateAttachmentBody = "private attachment contents"

// extractAttachmentURL pulls the first attachment link out of a rendered
// message view. It accepts BOTH shapes MailHandler.attachmentURL can produce:
// the message-scoped route used for inbound and split messages, and the
// content-addressed /mail/blob/{hash} route used for legacy stored mail.
func extractAttachmentURL(body string) string {
	re := regexp.MustCompile(`/mail/(?:blob/[a-f0-9]{64}|[^"/]+/\d+/attachment/\d+)`)
	return re.FindString(body)
}

func TestQueueInspection(t *testing.T) {
	baseURL, cancel := startAdminTestAgent(t)
	defer cancel()

	adminClient := loginAs(t, baseURL, "admin@localhost", "admin")

	t.Run("queue dashboard renders", func(t *testing.T) {
		resp, err := adminClient.Get(baseURL + "/admin/queue")
		if err != nil {
			t.Fatalf("GET /admin/queue: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want 200", resp.StatusCode)
		}

		body, _ := io.ReadAll(resp.Body)
		bodyStr := string(body)
		if !strings.Contains(bodyStr, "Message Queue") {
			t.Error("queue dashboard does not contain 'Message Queue'")
		}
		if !strings.Contains(bodyStr, "Pending") {
			t.Error("queue dashboard does not contain 'Pending' stats")
		}
	})
}
