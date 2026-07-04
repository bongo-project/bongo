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
	"github.com/bongo-go/bongo-go/internal/antispam"
	"github.com/bongo-go/bongo-go/internal/auth"
	"github.com/bongo-go/bongo-go/internal/config"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

func startAntispamAgent(t *testing.T) (*agent.Agent, context.CancelFunc) {
	t.Helper()

	cfg := config.Defaults()
	cfg.Pipeline.PollInterval = "50ms" // deliver promptly so short test waits suffice
	cfg.Server.Role = "dev"
	cfg.Server.BindAddr = "127.0.0.1"
	cfg.Web.Port = sharedFreePort(t)
	cfg.SMTP.Enabled = true
	cfg.IMAP.Enabled = true
	cfg.POP3.Enabled = true

	logger := sharedTestLogger()

	a, err := agent.NewAgent(cfg, logger)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		if err := a.Run(ctx); err != nil {
			logger.Error("agent.Run error", "error", err)
		}
	}()

	deadline := time.Now().Add(15 * time.Second)
	for !a.Ready() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if !a.Ready() {
		cancel()
		t.Fatal("agent not ready within 15s")
	}

	return a, cancel
}

func createAntispamUser(t *testing.T, a *agent.Agent, email, password string) string {
	t.Helper()
	userID, err := auth.CreateUser(a, a.Metadata, email, "Antispam Test", password, false)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if a.Mailboxes != nil {
		if err := a.Mailboxes.EnsureDefaultMailboxes(userID); err != nil {
			t.Fatalf("EnsureDefaultMailboxes: %v", err)
		}
	}
	time.Sleep(500 * time.Millisecond)
	return userID
}

func TestSpamScoringAutoJunk(t *testing.T) {
	a, cancel := startAntispamAgent(t)
	defer cancel()

	smtpAddr := a.SMTP.MXAddr()
	email := "spamtest@localhost"
	userID := createAntispamUser(t, a, email, "testpass")

	// Send a suspicious message: no Message-ID, no Date, no DKIM, empty subject.
	msg := fmt.Sprintf("From: spammer@evil-domain.com\r\n"+
		"To: %s\r\n"+
		"\r\n"+
		"Buy cheap stuff now!\r\n", email)

	err := smtplib.SendMail(smtpAddr, nil, "spammer@evil-domain.com", []string{email}, []byte(msg))
	if err != nil {
		t.Fatalf("SendMail: %v", err)
	}

	time.Sleep(2 * time.Second)

	// Check that the message ended up in Junk, not INBOX.
	junkMsgs, _, err := a.Mailboxes.ListMessagesPaged(userID, "Junk", 0, 10)
	if err != nil {
		t.Fatalf("ListMessagesPaged Junk: %v", err)
	}

	inboxMsgs, _, err := a.Mailboxes.ListMessagesPaged(userID, "INBOX", 0, 10)
	if err != nil {
		t.Fatalf("ListMessagesPaged INBOX: %v", err)
	}

	if len(junkMsgs) == 0 {
		t.Errorf("expected message in Junk, got 0 (INBOX has %d)", len(inboxMsgs))
	}

	// Verify spam score > 0.5 on the delivered message.
	if len(junkMsgs) > 0 {
		meta := loadMailMeta(t, a, userID, junkMsgs[0].MailGuid)
		if meta.SpamScore < 0.5 {
			t.Errorf("spam score = %.2f, want >= 0.5", meta.SpamScore)
		}
	}
}

func TestCleanMailToInbox(t *testing.T) {
	a, cancel := startAntispamAgent(t)
	defer cancel()

	smtpAddr := a.SMTP.MXAddr()
	email := "cleantest@localhost"
	userID := createAntispamUser(t, a, email, "testpass")

	// Send a clean message with all proper headers.
	msg := fmt.Sprintf("From: sender@localhost\r\n"+
		"To: %s\r\n"+
		"Subject: Hello World\r\n"+
		"Message-Id: <clean-%d@localhost>\r\n"+
		"Date: %s\r\n"+
		"MIME-Version: 1.0\r\n"+
		"Content-Type: text/plain\r\n"+
		"\r\n"+
		"This is a normal email.\r\n",
		email,
		time.Now().UnixNano(),
		time.Now().Format("Mon, 02 Jan 2006 15:04:05 -0700"))

	err := smtplib.SendMail(smtpAddr, nil, "sender@localhost", []string{email}, []byte(msg))
	if err != nil {
		t.Fatalf("SendMail: %v", err)
	}

	time.Sleep(2 * time.Second)

	// Check that the message is in INBOX.
	inboxMsgs, _, err := a.Mailboxes.ListMessagesPaged(userID, "INBOX", 0, 10)
	if err != nil {
		t.Fatalf("ListMessagesPaged INBOX: %v", err)
	}

	if len(inboxMsgs) == 0 {
		t.Error("expected message in INBOX, got 0")
	}

	// Verify spam score < 0.5.
	if len(inboxMsgs) > 0 {
		meta := loadMailMeta(t, a, userID, inboxMsgs[0].MailGuid)
		if meta.SpamScore >= 0.5 {
			t.Errorf("clean message spam score = %.2f, want < 0.5", meta.SpamScore)
		}
	}
}

func TestMarkAsSpamAndNotSpam(t *testing.T) {
	a, cancel := startAntispamAgent(t)
	defer cancel()

	smtpAddr := a.SMTP.MXAddr()
	email := "marktest@localhost"
	userID := createAntispamUser(t, a, email, "testpass")

	// Deliver a clean message to INBOX.
	msg := fmt.Sprintf("From: sender@localhost\r\n"+
		"To: %s\r\n"+
		"Subject: Mark Test\r\n"+
		"Message-Id: <mark-%d@localhost>\r\n"+
		"Date: %s\r\n"+
		"MIME-Version: 1.0\r\n"+
		"Content-Type: text/plain\r\n"+
		"\r\n"+
		"Test message for mark spam.\r\n",
		email,
		time.Now().UnixNano(),
		time.Now().Format("Mon, 02 Jan 2006 15:04:05 -0700"))

	err := smtplib.SendMail(smtpAddr, nil, "sender@localhost", []string{email}, []byte(msg))
	if err != nil {
		t.Fatalf("SendMail: %v", err)
	}

	time.Sleep(2 * time.Second)

	inboxMsgs, _, _ := a.Mailboxes.ListMessagesPaged(userID, "INBOX", 0, 10)
	if len(inboxMsgs) == 0 {
		t.Fatal("expected message in INBOX")
	}

	// Move to Junk via MoveMessage (simulating MarkSpam).
	uid := inboxMsgs[0].Uid
	guid := inboxMsgs[0].MailGuid
	if err := a.Mailboxes.MoveMessage(userID, "INBOX", uid, "Junk"); err != nil {
		t.Fatalf("MoveMessage to Junk: %v", err)
	}

	// Update spam score to 1.0.
	updateMailMetaSpamScore(t, a, userID, guid, 1.0)

	// Verify in Junk with score 1.0.
	junkMsgs, _, _ := a.Mailboxes.ListMessagesPaged(userID, "Junk", 0, 10)
	if len(junkMsgs) == 0 {
		t.Fatal("expected message in Junk after mark-spam")
	}
	meta := loadMailMeta(t, a, userID, guid)
	if meta.SpamScore != 1.0 {
		t.Errorf("after mark-spam, score = %.2f, want 1.0", meta.SpamScore)
	}

	// Move back to INBOX (simulating MarkNotSpam).
	junkUID := junkMsgs[0].Uid
	if err := a.Mailboxes.MoveMessage(userID, "Junk", junkUID, "INBOX"); err != nil {
		t.Fatalf("MoveMessage to INBOX: %v", err)
	}
	updateMailMetaSpamScore(t, a, userID, guid, 0.0)

	meta = loadMailMeta(t, a, userID, guid)
	if meta.SpamScore != 0.0 {
		t.Errorf("after mark-not-spam, score = %.2f, want 0.0", meta.SpamScore)
	}
}

func TestDKIMVerification(t *testing.T) {
	a, cancel := startAntispamAgent(t)
	defer cancel()

	smtpAddr := a.SMTP.MXAddr()
	email := "dkimtest@localhost"
	userID := createAntispamUser(t, a, email, "testpass")

	// Send a message with a fake DKIM-Signature header. It should fail verification.
	msg := fmt.Sprintf("DKIM-Signature: v=1; a=rsa-sha256; d=evil.com; s=fake;\r\n"+
		" h=from:to:subject; bh=AAAA; b=BBBB\r\n"+
		"From: sender@evil.com\r\n"+
		"To: %s\r\n"+
		"Subject: DKIM Test\r\n"+
		"Message-Id: <dkim-%d@evil.com>\r\n"+
		"Date: %s\r\n"+
		"\r\n"+
		"DKIM test body.\r\n",
		email,
		time.Now().UnixNano(),
		time.Now().Format("Mon, 02 Jan 2006 15:04:05 -0700"))

	err := smtplib.SendMail(smtpAddr, nil, "sender@evil.com", []string{email}, []byte(msg))
	if err != nil {
		t.Fatalf("SendMail: %v", err)
	}

	time.Sleep(2 * time.Second)

	// Find the message (could be in INBOX or Junk depending on total score).
	var foundMeta *bongov1.MailMeta
	for _, mailbox := range []string{"INBOX", "Junk"} {
		msgs, _, _ := a.Mailboxes.ListMessagesPaged(userID, mailbox, 0, 10)
		for _, msg := range msgs {
			meta := loadMailMeta(t, a, userID, msg.MailGuid)
			if meta != nil && strings.Contains(meta.Subject, "DKIM Test") {
				foundMeta = meta
				break
			}
		}
		if foundMeta != nil {
			break
		}
	}

	if foundMeta == nil {
		t.Fatal("could not find DKIM test message")
	}

	// DKIM should have failed.
	if foundMeta.DkimResult != "fail" {
		t.Errorf("dkim_result = %q, want %q", foundMeta.DkimResult, "fail")
	}
	if foundMeta.DkimDomain != "evil.com" {
		t.Errorf("dkim_domain = %q, want %q", foundMeta.DkimDomain, "evil.com")
	}
}

func TestBayesianTraining(t *testing.T) {
	a, cancel := startAntispamAgent(t)
	defer cancel()

	smtpAddr := a.SMTP.MXAddr()
	email := "bayestest@localhost"
	userID := createAntispamUser(t, a, email, "testpass")

	// Create a BayesFilter.
	bayesFilter := antispam.NewBayesFilter(a.Store, a)

	// Train with 25+ spam messages.
	for i := 0; i < 30; i++ {
		tokens := antispam.Tokenize(
			fmt.Sprintf("free viagra cheap pills offer %d", i),
			"buy now discount limited time offer free money winner prize claim reward bonus cash gift",
		)
		if err := bayesFilter.Train(userID, tokens, true); err != nil {
			t.Fatalf("Train spam %d: %v", i, err)
		}
		if err := bayesFilter.IncrementTrained(userID); err != nil {
			t.Fatalf("IncrementTrained spam %d: %v", i, err)
		}
	}

	// Train with 25+ ham messages.
	for i := 0; i < 30; i++ {
		tokens := antispam.Tokenize(
			fmt.Sprintf("meeting notes project update %d", i),
			"please review the attached document and provide feedback on the quarterly report deadline",
		)
		if err := bayesFilter.Train(userID, tokens, false); err != nil {
			t.Fatalf("Train ham %d: %v", i, err)
		}
		if err := bayesFilter.IncrementTrained(userID); err != nil {
			t.Fatalf("IncrementTrained ham %d: %v", i, err)
		}
	}

	// Verify trained count.
	trainedCount := bayesFilter.TrainedCount(userID)
	if trainedCount < 20 {
		t.Fatalf("trained count = %d, want >= 20", trainedCount)
	}

	// Deliver a spam-like message. With Bayesian blending, the score should be higher.
	msg := fmt.Sprintf("From: spammer@evil.com\r\n"+
		"To: %s\r\n"+
		"Subject: Free Viagra cheap pills\r\n"+
		"Message-Id: <bayes-%d@evil.com>\r\n"+
		"Date: %s\r\n"+
		"\r\n"+
		"Buy now free money winner prize claim reward bonus cash.\r\n",
		email,
		time.Now().UnixNano(),
		time.Now().Format("Mon, 02 Jan 2006 15:04:05 -0700"))

	err := smtplib.SendMail(smtpAddr, nil, "spammer@evil.com", []string{email}, []byte(msg))
	if err != nil {
		t.Fatalf("SendMail: %v", err)
	}

	time.Sleep(2 * time.Second)

	// Find the message in Junk or INBOX.
	var foundMeta *bongov1.MailMeta
	for _, mailbox := range []string{"Junk", "INBOX"} {
		msgs, _, _ := a.Mailboxes.ListMessagesPaged(userID, mailbox, 0, 10)
		for _, msg := range msgs {
			meta := loadMailMeta(t, a, userID, msg.MailGuid)
			if meta != nil && strings.Contains(meta.Subject, "Free Viagra") {
				foundMeta = meta
				break
			}
		}
		if foundMeta != nil {
			break
		}
	}

	if foundMeta == nil {
		t.Fatal("could not find Bayesian test message")
	}

	// The blended score should reflect Bayesian contribution.
	t.Logf("bayesian test: spam_score=%.4f", foundMeta.SpamScore)
	if foundMeta.SpamScore <= 0 {
		t.Errorf("expected positive spam score with Bayesian blending, got %.4f", foundMeta.SpamScore)
	}
}

// loadMailMeta loads a MailMeta from Pebble.
func loadMailMeta(t *testing.T, a *agent.Agent, userID, guid string) *bongov1.MailMeta {
	t.Helper()
	key := []byte(fmt.Sprintf("/user/%s/mail/%s", userID, guid))
	data, err := a.Store.Get(key)
	if err != nil {
		t.Fatalf("Get mail meta: %v", err)
	}
	var meta bongov1.MailMeta
	if err := proto.Unmarshal(data, &meta); err != nil {
		t.Fatalf("Unmarshal mail meta: %v", err)
	}
	return &meta
}

// updateMailMetaSpamScore updates the spam score on a MailMeta.
func updateMailMetaSpamScore(t *testing.T, a *agent.Agent, userID, guid string, score float32) {
	t.Helper()
	key := []byte(fmt.Sprintf("/user/%s/mail/%s", userID, guid))
	data, err := a.Store.Get(key)
	if err != nil {
		t.Fatalf("Get mail meta: %v", err)
	}
	var meta bongov1.MailMeta
	if err := proto.Unmarshal(data, &meta); err != nil {
		t.Fatalf("Unmarshal mail meta: %v", err)
	}
	meta.SpamScore = score
	metaBytes, err := proto.Marshal(&meta)
	if err != nil {
		t.Fatalf("Marshal mail meta: %v", err)
	}
	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   key,
		Value: metaBytes,
	}
	if err := a.ApplyCommand(cmd, 5*time.Second); err != nil {
		t.Fatalf("ApplyCommand: %v", err)
	}
}
