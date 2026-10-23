package agent

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// composePipelineAgent starts a dev-mode Agent and returns it with a client
// logged in as admin@localhost and the web base URL.
func composePipelineAgent(t *testing.T) (*Agent, *http.Client, string) {
	t.Helper()
	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	cfg.Web.Port = freePort(t)
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
	return a, client, base
}

func mailCount(a *Agent, uid, mailbox string) int {
	msgs, _ := a.Mailboxes.ListMessages(uid, mailbox)
	return len(msgs)
}

// waitFor polls cond until it holds or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestComposeLocalMailRunsRecipientPipeline drives POST /compose/send on a real
// dev Agent. Unit tests use a fake spool and cannot notice a missing SetSpool
// call in Run, or that a recipient's rules and forwarding really run.
func TestComposeLocalMailRunsRecipientPipeline(t *testing.T) {
	a, client, base := composePipelineAgent(t)

	bobID, err := auth.CreateUser(a, a.Metadata, "bob@localhost", "Bob", "pass1234", false)
	if err != nil {
		t.Fatalf("CreateUser bob: %v", err)
	}
	carolID, err := auth.CreateUser(a, a.Metadata, "carol@localhost", "Carol", "pass1234", false)
	if err != nil {
		t.Fatalf("CreateUser carol: %v", err)
	}

	// Bob files invoices; his preferences forward without keeping a copy to a
	// verified external address. Carol has no rules.
	if err := a.Rules.Save(bobID, &bongov1.FilterRule{
		Id: "r1", Name: "invoices", Enabled: true, Priority: 1,
		Conditions: []*bongov1.Condition{{Field: "subject", Operator: "contains", Value: "invoice"}},
		Action:     &bongov1.FilterAction{Type: "move", Target: "Invoices"},
	}); err != nil {
		t.Fatalf("Save rule: %v", err)
	}

	send := func(subject string, to ...string) {
		t.Helper()
		resp, err := client.PostForm(base+"/compose/send", url.Values{
			"to": {to[0]}, "cc": {strings.Join(to[1:], ", ")}, "subject": {subject}, "body": {"please pay"},
		})
		if err != nil {
			t.Fatalf("POST /compose/send: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("compose send status = %d, want 303", resp.StatusCode)
		}
	}

	t.Run("rule files the message and others are unaffected", func(t *testing.T) {
		send("invoice 42", "bob@localhost", "carol@localhost")
		waitFor(t, "bob's Invoices copy", func() bool { return mailCount(a, bobID, "Invoices") == 1 })
		waitFor(t, "carol's INBOX copy", func() bool { return mailCount(a, carolID, "INBOX") == 1 })
		if n := mailCount(a, bobID, "INBOX"); n != 0 {
			t.Errorf("bob INBOX = %d, the rule moved it", n)
		}
		if n := mailCount(a, carolID, "Invoices"); n != 0 {
			t.Errorf("carol Invoices = %d, bob's rule leaked to her copy", n)
		}
	})

	t.Run("forward without keeping a copy", func(t *testing.T) {
		const target = "ext@remote.example"
		if err := a.forwardTargetStore().SetState(bobID, target, store.ForwardStateVerified); err != nil {
			t.Fatalf("SetState: %v", err)
		}
		prefs := store.DefaultPreferences()
		prefs.ForwardAddress = target
		prefs.ForwardKeepCopy = false
		if err := a.Preferences.Set(bobID, prefs); err != nil {
			t.Fatalf("Set prefs: %v", err)
		}
		// Bob's rule would otherwise file the message; a plain subject keeps
		// the forward the only thing that can happen to it.
		beforeInbox := mailCount(a, bobID, "INBOX")
		seen := map[string]bool{}
		send("hello bob", "bob@localhost", "carol@localhost")
		waitFor(t, "forward to the external address", func() bool {
			pending, _ := a.QueueStore.ListPending(100)
			for _, m := range pending {
				for _, r := range m.Recipients {
					if r == target {
						seen[m.Id] = true
					}
				}
			}
			return len(seen) > 0
		})
		waitFor(t, "carol's second INBOX copy", func() bool { return mailCount(a, carolID, "INBOX") == 2 })
		// Settle: a duplicate forward would be enqueued shortly after the
		// first, so keep sampling the queue for a short window.
		for deadline := time.Now().Add(1 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
			pending, _ := a.QueueStore.ListPending(100)
			for _, m := range pending {
				for _, r := range m.Recipients {
					if r == target {
						seen[m.Id] = true
					}
				}
			}
		}
		if len(seen) != 1 {
			t.Errorf("outbound messages to %s = %d, want 1", target, len(seen))
		}
		if n := mailCount(a, bobID, "INBOX"); n != beforeInbox {
			t.Errorf("bob INBOX = %d, want %d (ForwardKeepCopy=false)", n, beforeInbox)
		}
	})
}
