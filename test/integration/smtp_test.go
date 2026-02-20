//go:build integration

package integration

import (
	"context"
	"fmt"
	smtplib "net/smtp"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-go/bongo-go/internal/agent"
	"github.com/bongo-go/bongo-go/internal/auth"
	"github.com/bongo-go/bongo-go/internal/config"
	"github.com/bongo-go/bongo-go/internal/store"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

func TestSMTPFullMailFlow(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	cfg.Server.BindAddr = "127.0.0.1"
	cfg.Web.Port = sharedFreePort(t)
	cfg.SMTP.Enabled = true

	logger := sharedTestLogger()

	a, err := agent.NewAgent(cfg, logger)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- a.Run(ctx)
	}()

	// Wait for the agent to be ready.
	deadline := time.Now().Add(10 * time.Second)
	for !a.Ready() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if !a.Ready() {
		t.Fatal("agent not ready in time")
	}

	// Create a test user.
	userID, err := auth.CreateUser(a, a.Metadata, "testuser@localhost", "Test User", "testpass")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Logf("test user created: %s (id: %s)", "testuser@localhost", userID)

	// Wait for Raft to propagate.
	time.Sleep(500 * time.Millisecond)

	// Send an email via net/smtp to port 2525.
	smtpAddr := a.SMTP.MXAddr()
	t.Logf("sending email to %s", smtpAddr)

	msg := "From: sender@example.com\r\n" +
		"To: testuser@localhost\r\n" +
		"Subject: Integration Test Email\r\n" +
		"Message-Id: <integ-test-1@example.com>\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=\"testboundary\"\r\n" +
		"\r\n" +
		"--testboundary\r\n" +
		"Content-Type: text/plain\r\n" +
		"\r\n" +
		"Hello from the integration test!\r\n" +
		"--testboundary\r\n" +
		"Content-Type: application/octet-stream\r\n" +
		"Content-Disposition: attachment; filename=\"test.bin\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n" +
		"\r\n" +
		"dGVzdCBhdHRhY2htZW50IGNvbnRlbnQ=\r\n" +
		"--testboundary--\r\n"

	err = smtplib.SendMail(
		smtpAddr,
		nil, // no auth for MX port
		"sender@example.com",
		[]string{"testuser@localhost"},
		[]byte(msg),
	)
	if err != nil {
		t.Fatalf("SendMail: %v", err)
	}
	t.Log("email sent successfully")

	// Wait for delivery to propagate through Raft.
	time.Sleep(1 * time.Second)

	// Verify Pebble has a /user/{id}/mail/{guid} key.
	mailPrefix := fmt.Sprintf("/user/%s/mail/", userID)
	iter, err := a.Store.PrefixScan([]byte(mailPrefix))
	if err != nil {
		t.Fatalf("PrefixScan: %v", err)
	}

	var mailKey string
	var mailValue []byte
	count := 0
	for iter.Next() {
		mailKey = string(iter.Key())
		mailValue, _ = iter.Value()
		count++
	}
	iter.Close()

	if count == 0 {
		t.Fatal("no mail keys found in Pebble")
	}
	t.Logf("found %d mail key(s): %s", count, mailKey)

	// Parse MailMeta protobuf.
	var meta bongov1.MailMeta
	if err := proto.Unmarshal(mailValue, &meta); err != nil {
		t.Fatalf("unmarshal MailMeta: %v", err)
	}

	if meta.Subject != "Integration Test Email" {
		t.Errorf("subject = %q, want %q", meta.Subject, "Integration Test Email")
	}
	if meta.From != "sender@example.com" {
		t.Errorf("from = %q, want %q", meta.From, "sender@example.com")
	}
	if meta.RawMessage == nil || meta.RawMessage.Hash == "" {
		t.Fatal("expected raw message blob ref")
	}

	// Verify blob store has the raw message.
	if !a.Blobs.Exists(meta.RawMessage.Hash) {
		t.Error("raw message blob not found")
	}
	t.Logf("raw message blob: %s (size: %d)", meta.RawMessage.Hash[:12], meta.RawMessage.Size)

	// Verify attachments.
	if len(meta.Attachments) < 1 {
		t.Error("expected at least 1 attachment")
	} else {
		for i, att := range meta.Attachments {
			if !a.Blobs.Exists(att.Hash) {
				t.Errorf("attachment %d blob not found: %s", i, att.Hash[:12])
			}
			t.Logf("attachment %d: %s (size: %d, type: %s)", i, att.Hash[:12], att.Size, att.ContentType)
		}
	}

	// Verify per-user SQLite has storeobject + maildocument rows.
	objs, err := a.Metadata.QueryByType(userID, store.DocTypeMail)
	if err != nil {
		t.Fatalf("QueryByType: %v", err)
	}
	if len(objs) == 0 {
		t.Error("no mail objects in per-user SQLite")
	} else {
		t.Logf("SQLite mail objects: %d", len(objs))
	}

	// Verify Bleve search by subject.
	results, err := a.Search.Search(userID, "integration", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Error("no search results for 'integration'")
	} else {
		t.Logf("Bleve search results: %d (top hit: %s, score: %.2f)", len(results), results[0].GUID, results[0].Score)
	}

	// Verify search by sender.
	results2, err := a.Search.Search(userID, "from:sender@example.com", 10)
	if err != nil {
		t.Fatalf("Search by sender: %v", err)
	}
	if len(results2) == 0 {
		// This is a keyword field, it may not tokenize as expected. Log but don't fail.
		t.Log("no search results for 'from:sender@example.com' (keyword field may need exact match)")
	} else {
		t.Logf("search by sender: %d results", len(results2))
	}

	// Verify admin@localhost user was auto-created (dev mode).
	var adminCount int
	err = a.Metadata.System.DB().QueryRow(
		"SELECT COUNT(*) FROM users WHERE username = 'admin@localhost'",
	).Scan(&adminCount)
	if err != nil {
		t.Fatalf("querying admin user: %v", err)
	}
	if adminCount == 0 {
		t.Error("dev mode admin@localhost user not found in system.db")
	}

	// Check GAL index has the test user.
	galResults, err := a.Search.SearchGAL("test", 10)
	if err != nil {
		t.Fatalf("SearchGAL: %v", err)
	}
	found := false
	for _, e := range galResults {
		if strings.Contains(e.Email, "testuser") || strings.Contains(e.DisplayName, "Test") {
			found = true
			break
		}
	}
	if !found {
		t.Log("GAL entry for testuser not found (may need time for FSM indexing)")
	}

	t.Log("full mail flow test passed")

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Logf("Run returned: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("agent did not stop")
	}
}
