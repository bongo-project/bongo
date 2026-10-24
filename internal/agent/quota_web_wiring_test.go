package agent

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/config"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// TestWebWritePathsQuotaWired runs a real dev-mode Agent, logs in over HTTP and
// drives the web write paths. Deleting any of the four SetQuotaChecker calls on
// the mail, compose, contacts and calendar handlers in agent.go leaves every
// handler unit test green; only this test notices.
func TestWebWritePathsQuotaWired(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	cfg.Web.Port = freePort(t)
	cfg.Server.ControlAddr = freeControlAddr(t)
	cfg.Server.BindAddr = "127.0.0.1"
	cfg.SMTP.Enabled = false
	cfg.Cluster.Gossip.Port = freePort(t)
	cfg.POP3.Enabled = false
	cfg.IMAP.Enabled = false

	a, err := NewAgent(cfg, testLogger())
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Run returned error: %v", err)
		}
	})
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) && !a.Ready() {
		time.Sleep(20 * time.Millisecond)
	}
	if !a.Ready() {
		t.Fatal("agent did not become ready")
	}
	if a.DomainQuota == nil {
		t.Fatal("DomainQuota not initialised")
	}

	const limit = 1_048_583
	if err := a.DomainQuota.SetQuota("localhost", &bongov1.DomainQuota{MaxStorageBytes: limit}); err != nil {
		t.Fatalf("SetQuota: %v", err)
	}

	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar:           jar,
		Timeout:       15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	base := "http://127.0.0.1:" + strconv.Itoa(cfg.Web.Port)

	login, err := client.PostForm(base+"/login", url.Values{"email": {"admin@localhost"}, "password": {"admin"}})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	_ = login.Body.Close()
	if login.StatusCode != http.StatusSeeOther && login.StatusCode != http.StatusFound {
		t.Fatalf("login status = %d", login.StatusCode)
	}

	postForm := func(path string, form url.Values) int {
		resp, err := client.PostForm(base+path, form)
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	mbox := "From a@x.example Mon Jan  1 00:00:00 2024\nFrom: a@x.example\nTo: admin@localhost\nSubject: wired\nMessage-ID: <w1@x>\n\nbody\n"
	postMbox := func() int {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		fw, _ := mw.CreateFormFile("mbox", "m.mbox")
		_, _ = fw.Write([]byte(mbox))
		_ = mw.WriteField("new_mailbox", "WiredImport")
		_ = mw.Close()
		resp, err := client.Post(base+"/import", mw.FormDataContentType(), &buf)
		if err != nil {
			t.Fatalf("POST /import: %v", err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	paths := []struct {
		name string
		do   func() int
		ok   func(int) bool
	}{
		{"mbox import", postMbox, func(c int) bool { return c == http.StatusOK }},
		{"compose send", func() int {
			return postForm("/compose/send", url.Values{"send_id": {composeSendID()}, "to": {"admin@localhost"}, "subject": {"wired"}, "body": {"hi"}})
		}, func(c int) bool { return c == http.StatusSeeOther }},
		{"compose draft", func() int {
			return postForm("/compose/draft", url.Values{"to": {"admin@localhost"}, "subject": {"wired"}, "body": {"hi"}})
		}, func(c int) bool { return c == http.StatusSeeOther }},
		{"calendar event", func() int {
			return postForm("/calendar/event", url.Values{"title": {"wired"}, "start_date": {"2030-05-06"}, "start_time": {"09:00"}, "end_time": {"10:00"}})
		}, func(c int) bool { return c == http.StatusSeeOther }},
		{"contact", func() int {
			return postForm("/contacts", url.Values{"display_name": {"Wired"}, "email": {"w@x.example"}})
		}, func(c int) bool { return c == http.StatusSeeOther }},
	}

	// Over quota: usage exactly at the limit (an off-round number).
	if err := a.DomainQuota.SetUsage("localhost", limit); err != nil {
		t.Fatalf("SetUsage: %v", err)
	}
	for _, p := range paths {
		if got := p.do(); got != http.StatusInsufficientStorage {
			t.Errorf("%s over quota: status %d, want 507", p.name, got)
		}
	}

	// Positive control on the same session and routes.
	if err := a.DomainQuota.SetUsage("localhost", limit-100_003); err != nil {
		t.Fatalf("SetUsage: %v", err)
	}
	for _, p := range paths {
		if got := p.do(); !p.ok(got) {
			t.Errorf("%s under quota: status %d, want success", p.name, got)
		}
	}
}
