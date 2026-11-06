package agent

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/config"
)

// The agent must hand its app password service to the authenticator, or the
// feature is dead in production while every test that builds its own
// LocalAuth stays green.
func TestAgentWiresAppPasswordsIntoAuth(t *testing.T) {
	a := blobGCLeaderTestAgent(t)

	uid, err := auth.CreateUser(a, a.Metadata, "phone@localhost", "Phone", "pass1234", false)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if a.AppPasswords == nil {
		t.Fatal("AppPasswords not created")
	}
	pw, _, err := a.AppPasswords.Create(uid, "phone")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id, _, _, err := a.Auth.AuthenticateProtocol("phone@localhost", pw); err != nil || id != uid {
		t.Fatalf("protocol login with app password = %q, %v", id, err)
	}
	a.AppPasswords.Wait()
	if _, _, err := a.Auth.AuthenticateProfile("phone@localhost", pw); err == nil {
		t.Fatal("web authentication accepted an app password")
	}
}

// Add a device shows the server settings from the same source as the
// autoconfig documents, and offers the Apple profile only when that source is
// wired. Deleting securityH.SetClientSettings leaves the handler's own tests
// green and this one red.
func TestAddDeviceWiredToClientSettings(t *testing.T) {
	cfg := config.Defaults()
	isolatePorts(t, cfg)
	cfg.Server.Role = "dev"
	cfg.SMTP.Enabled = false
	cfg.POP3.Enabled = false
	cfg.IMAP.Enabled = false
	cfg.SMTP.Hostname = "mx.wiring.test"
	a := runWiringAgent(t, cfg, testLogger(), nil)
	t.Cleanup(func() { a.AppPasswords.Wait() })

	_, imaps, submission, _ := autoconfigPorts(a.Config)
	if imaps == 993 || submission == 587 {
		t.Fatalf("fixture: ports %d/%d are the defaults, so a hard-coded port could pass", imaps, submission)
	}

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	base := "http://127.0.0.1:" + strconv.Itoa(cfg.Web.Port)
	login, err := client.PostForm(base+"/login", url.Values{"email": {"admin@localhost"}, "password": {"admin"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = login.Body.Close()

	read := func(resp *http.Response, err error) string {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status %d: %s", resp.StatusCode, b)
		}
		return string(b)
	}

	page := read(client.Get(base + "/settings/security/devices"))
	if !strings.Contains(page, "Apple (iPhone, iPad or Mac)") {
		t.Error("the Apple choice is not offered, so the client settings are not wired")
	}

	body := read(client.PostForm(base+"/settings/security/devices", url.Values{
		"kind": {"other"}, "label": {"wired"}, "password": {"admin"},
	}))
	for _, want := range []string{"mx.wiring.test", strconv.Itoa(imaps), strconv.Itoa(submission)} {
		if !strings.Contains(body, "<span>"+want+"</span>") {
			t.Errorf("the settings lack %q", want)
		}
	}
}
