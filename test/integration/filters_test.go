//go:build integration

package integration

import (
	"context"
	"fmt"
	"io"
	smtplib "net/smtp"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/agent"
	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/rules"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func startFilterAgent(t *testing.T) (*agent.Agent, context.CancelFunc) {
	t.Helper()
	return startFilterAgentHost(t, "")
}

// startFilterAgentHost starts a dev agent whose SMTP hostname is host. A
// non-empty host enables Authentication-Results stamping.
func startFilterAgentHost(t *testing.T, host string) (*agent.Agent, context.CancelFunc) {
	t.Helper()

	cfg := config.Defaults()
	if host != "" {
		cfg.SMTP.Hostname = host
	}
	cfg.Pipeline.PollInterval = "50ms" // deliver promptly so short test waits suffice
	cfg.Server.Role = "dev"
	cfg.Server.BindAddr = "127.0.0.1"
	sharedAssignFreePorts(t, cfg)
	cfg.SMTP.Enabled = true
	cfg.IMAP.Enabled = true
	cfg.POP3.Enabled = true

	a, cancel, _ := sharedStartAgent(t, cfg)

	return a, cancel
}

func createFilterUser(t *testing.T, a *agent.Agent, email, password string) string {
	t.Helper()
	userID, err := auth.CreateUser(a, a.Metadata, email, "Filter Test", password, false)
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

func createMailbox(t *testing.T, a *agent.Agent, userID, name string) {
	t.Helper()
	if err := a.Mailboxes.CreateMailbox(userID, name); err != nil {
		t.Fatalf("CreateMailbox %s: %v", name, err)
	}
}

func sendMessage(t *testing.T, smtpAddr, from, to, subject, body string) {
	t.Helper()
	msg := fmt.Sprintf("From: %s\r\n"+
		"To: %s\r\n"+
		"Subject: %s\r\n"+
		"Message-Id: <%d@localhost>\r\n"+
		"Date: %s\r\n"+
		"MIME-Version: 1.0\r\n"+
		"Content-Type: text/plain\r\n"+
		"\r\n"+
		"%s\r\n",
		from, to, subject,
		time.Now().UnixNano(),
		time.Now().Format("Mon, 02 Jan 2006 15:04:05 -0700"),
		body)

	err := smtplib.SendMail(smtpAddr, nil, from, []string{to}, []byte(msg))
	if err != nil {
		t.Fatalf("SendMail: %v", err)
	}
}

func createRule(t *testing.T, rs *rules.RuleStore, userID string, rule *bongov1.FilterRule) {
	t.Helper()
	if err := rs.Save(userID, rule); err != nil {
		t.Fatalf("Save rule: %v", err)
	}
}

func TestFilterRuleRouting(t *testing.T) {
	a, cancel := startFilterAgent(t)
	defer cancel()

	smtpAddr := a.SMTP.MXAddr()
	email := "filter1@localhost"
	userID := createFilterUser(t, a, email, "testpass")

	// Create "Updates" mailbox.
	createMailbox(t, a, userID, "Updates")

	// Create filter rule: from contains "newsletter" -> move to "Updates".
	createRule(t, a.Rules, userID, &bongov1.FilterRule{
		Id:       "rule-1",
		Name:     "Newsletters to Updates",
		Priority: 1,
		Enabled:  true,
		Conditions: []*bongov1.Condition{
			{Field: "from", Operator: "contains", Value: "newsletter"},
		},
		Action:    &bongov1.FilterAction{Type: "move", Target: "Updates"},
		CreatedAt: time.Now().Unix(),
	})

	// Deliver email from newsletter@example.com.
	sendMessage(t, smtpAddr, "newsletter@example.com", email, "Weekly Digest", "This is a newsletter.")

	time.Sleep(2 * time.Second)

	// Verify message landed in "Updates" (not INBOX).
	updatesMsgs, _, _ := a.Mailboxes.ListMessagesPaged(userID, "Updates", 0, 10)
	inboxMsgs, _, _ := a.Mailboxes.ListMessagesPaged(userID, "INBOX", 0, 10)

	if len(updatesMsgs) == 0 {
		t.Errorf("expected message in Updates, got 0 (INBOX has %d)", len(inboxMsgs))
	}
	if len(inboxMsgs) > 0 {
		t.Errorf("expected no messages in INBOX, got %d", len(inboxMsgs))
	}
}

func TestFilterRulePriority(t *testing.T) {
	a, cancel := startFilterAgent(t)
	defer cancel()

	smtpAddr := a.SMTP.MXAddr()
	email := "filter2@localhost"
	userID := createFilterUser(t, a, email, "testpass")

	createMailbox(t, a, userID, "Important")

	// Rule 1: subject contains "urgent" -> move to "Important" (stop processing).
	createRule(t, a.Rules, userID, &bongov1.FilterRule{
		Id:       "rule-1",
		Name:     "Urgent to Important",
		Priority: 1,
		Enabled:  true,
		Conditions: []*bongov1.Condition{
			{Field: "subject", Operator: "contains", Value: "urgent"},
		},
		Action:         &bongov1.FilterAction{Type: "move", Target: "Important"},
		StopProcessing: true,
		CreatedAt:      time.Now().Unix(),
	})

	// Rule 2: from contains "boss@" -> flag as starred.
	createRule(t, a.Rules, userID, &bongov1.FilterRule{
		Id:       "rule-2",
		Name:     "Boss flagged",
		Priority: 2,
		Enabled:  true,
		Conditions: []*bongov1.Condition{
			{Field: "from", Operator: "contains", Value: "boss@"},
		},
		Action:    &bongov1.FilterAction{Type: "flag", Target: "starred"},
		CreatedAt: time.Now().Unix(),
	})

	// Deliver email from boss@ with subject "urgent report".
	sendMessage(t, smtpAddr, "boss@company.com", email, "Urgent report needed", "Please review ASAP.")

	time.Sleep(2 * time.Second)

	// Verify: moved to "Important" (rule 1 matched first).
	importantMsgs, _, _ := a.Mailboxes.ListMessagesPaged(userID, "Important", 0, 10)
	if len(importantMsgs) == 0 {
		t.Error("expected message in Important")
	}
}

func TestFilterRuleDisabled(t *testing.T) {
	a, cancel := startFilterAgent(t)
	defer cancel()

	smtpAddr := a.SMTP.MXAddr()
	email := "filter3@localhost"
	userID := createFilterUser(t, a, email, "testpass")

	// Create disabled rule.
	createRule(t, a.Rules, userID, &bongov1.FilterRule{
		Id:       "rule-1",
		Name:     "Disabled rule",
		Priority: 1,
		Enabled:  false,
		Conditions: []*bongov1.Condition{
			{Field: "from", Operator: "contains", Value: "test"},
		},
		Action:    &bongov1.FilterAction{Type: "move", Target: "Trash"},
		CreatedAt: time.Now().Unix(),
	})

	// Deliver email from test@example.com.
	sendMessage(t, smtpAddr, "test@example.com", email, "Hello", "Normal message.")

	time.Sleep(2 * time.Second)

	// Verify message in INBOX (rule was disabled).
	inboxMsgs, _, _ := a.Mailboxes.ListMessagesPaged(userID, "INBOX", 0, 10)
	trashMsgs, _, _ := a.Mailboxes.ListMessagesPaged(userID, "Trash", 0, 10)

	if len(inboxMsgs) == 0 {
		t.Errorf("expected message in INBOX, got 0 (Trash has %d)", len(trashMsgs))
	}
}

func TestFilterRuleCRUD(t *testing.T) {
	a, cancel := startFilterAgent(t)
	defer cancel()

	email := "filter4@localhost"
	userID := createFilterUser(t, a, email, "testpass")

	// Create.
	rule := &bongov1.FilterRule{
		Id:       "crud-rule",
		Name:     "CRUD Test Rule",
		Priority: 1,
		Enabled:  true,
		Conditions: []*bongov1.Condition{
			{Field: "from", Operator: "contains", Value: "test"},
		},
		Action:    &bongov1.FilterAction{Type: "move", Target: "INBOX"},
		CreatedAt: time.Now().Unix(),
	}
	if err := a.Rules.Save(userID, rule); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// List.
	rules, err := a.Rules.List(userID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if rules[0].Name != "CRUD Test Rule" {
		t.Errorf("name = %q, want %q", rules[0].Name, "CRUD Test Rule")
	}

	// Get.
	got, err := a.Rules.Get(userID, "crud-rule")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "CRUD Test Rule" {
		t.Errorf("name = %q, want %q", got.Name, "CRUD Test Rule")
	}

	// Update.
	got.Name = "Updated Rule"
	got.UpdatedAt = time.Now().Unix()
	if err := a.Rules.Save(userID, got); err != nil {
		t.Fatalf("Save update: %v", err)
	}

	got2, _ := a.Rules.Get(userID, "crud-rule")
	if got2.Name != "Updated Rule" {
		t.Errorf("name after update = %q, want %q", got2.Name, "Updated Rule")
	}

	// Delete.
	if err := a.Rules.Delete(userID, "crud-rule"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	rules, _ = a.Rules.List(userID)
	if len(rules) != 0 {
		t.Errorf("expected 0 rules after delete, got %d", len(rules))
	}
}

func TestFilterRuleWithSpam(t *testing.T) {
	a, cancel := startFilterAgent(t)
	defer cancel()

	smtpAddr := a.SMTP.MXAddr()
	email := "filter5@localhost"
	userID := createFilterUser(t, a, email, "testpass")

	createMailbox(t, a, userID, "News")

	// Create rule: from contains "news" -> move to "News".
	createRule(t, a.Rules, userID, &bongov1.FilterRule{
		Id:       "rule-1",
		Name:     "News to News folder",
		Priority: 1,
		Enabled:  true,
		Conditions: []*bongov1.Condition{
			{Field: "from", Operator: "contains", Value: "news"},
		},
		Action:    &bongov1.FilterAction{Type: "move", Target: "News"},
		CreatedAt: time.Now().Unix(),
	})

	// Send a spammy message from news@example.com (no Message-ID, no Date to trigger spam scoring).
	msg := fmt.Sprintf("From: news@example.com\r\n"+
		"To: %s\r\n"+
		"\r\n"+
		"Breaking news content.\r\n", email)

	err := smtplib.SendMail(smtpAddr, nil, "news@example.com", []string{email}, []byte(msg))
	if err != nil {
		t.Fatalf("SendMail: %v", err)
	}

	time.Sleep(2 * time.Second)

	// Verify: rule takes precedence over spam routing (goes to "News" not "Junk").
	newsMsgs, _, _ := a.Mailboxes.ListMessagesPaged(userID, "News", 0, 10)
	junkMsgs, _, _ := a.Mailboxes.ListMessagesPaged(userID, "Junk", 0, 10)

	if len(newsMsgs) == 0 {
		t.Errorf("expected message in News, got 0 (Junk has %d)", len(junkMsgs))
	}
}

// sendMessageMulti sends one message over the MX port to several recipients.
func sendMessageMulti(t *testing.T, smtpAddr, from string, to []string, subject, body string) {
	t.Helper()
	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMessage-Id: <%d@localhost>\r\n"+
		"Date: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain\r\n\r\n%s\r\n",
		from, strings.Join(to, ", "), subject, time.Now().UnixNano(),
		time.Now().Format("Mon, 02 Jan 2006 15:04:05 -0700"), body)
	if err := smtplib.SendMail(smtpAddr, nil, from, to, []byte(msg)); err != nil {
		t.Fatalf("SendMail: %v", err)
	}
}

// waitMailboxCount polls until the mailbox holds want messages or times out,
// returning the final count.
func waitMailboxCount(a *agent.Agent, userID, mailbox string, want int) int {
	n := 0
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		msgs, _, _ := a.Mailboxes.ListMessagesPaged(userID, mailbox, 0, 10)
		if n = len(msgs); n >= want {
			break
		}
	}
	return n
}

// storedRawMessage returns the stored raw bytes of the first message in a mailbox.
func storedRawMessage(t *testing.T, a *agent.Agent, userID, mailbox string) string {
	t.Helper()
	msgs, _, err := a.Mailboxes.ListMessagesPaged(userID, mailbox, 0, 10)
	if err != nil || len(msgs) == 0 {
		t.Fatalf("no message in %s: %v", mailbox, err)
	}
	val, err := a.Store.Get([]byte(fmt.Sprintf("/user/%s/mail/%s", userID, msgs[0].MailGuid)))
	if err != nil {
		t.Fatalf("get MailMeta: %v", err)
	}
	var meta bongov1.MailMeta
	if err := proto.Unmarshal(val, &meta); err != nil {
		t.Fatalf("unmarshal MailMeta: %v", err)
	}
	rc, err := a.Blobs.Get(meta.RawMessage.Hash)
	if err != nil {
		t.Fatalf("blob get: %v", err)
	}
	defer rc.Close()
	raw, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func authResultsHeader(raw string) string {
	for _, l := range strings.Split(raw, "\r\n") {
		if l == "" {
			break
		}
		if strings.HasPrefix(l, "Authentication-Results:") {
			return l
		}
	}
	return ""
}

// TestSMTPMultiRecipientRulesArePerRecipient: one SMTP transaction with two
// local recipients must apply each recipient's rules to that recipient's copy
// only.
func TestSMTPMultiRecipientRulesArePerRecipient(t *testing.T) {
	t.Run("delete rule drops only that recipient's copy", func(t *testing.T) {
		a, cancel := startFilterAgent(t)
		defer cancel()
		ua := createFilterUser(t, a, "fan-a@localhost", "testpass")
		ub := createFilterUser(t, a, "fan-b@localhost", "testpass")
		createRule(t, a.Rules, ua, &bongov1.FilterRule{
			Id: "del", Name: "drop", Priority: 1, Enabled: true,
			Conditions: []*bongov1.Condition{{Field: "subject", Operator: "contains", Value: "Fanout"}},
			Action:     &bongov1.FilterAction{Type: "delete"},
			CreatedAt:  time.Now().Unix(),
		})

		sendMessageMulti(t, a.SMTP.MXAddr(), "sender@example.com", []string{"fan-a@localhost", "fan-b@localhost"}, "Fanout one", "hello")

		if n := waitMailboxCount(a, ub, "INBOX", 1); n != 1 {
			t.Fatalf("b INBOX = %d, want 1", n)
		}
		time.Sleep(500 * time.Millisecond)
		if msgs, _, _ := a.Mailboxes.ListMessagesPaged(ua, "INBOX", 0, 10); len(msgs) != 0 {
			t.Errorf("a INBOX = %d, want 0", len(msgs))
		}
		q, err := a.Spool.List("quarantine")
		if err != nil {
			t.Fatal(err)
		}
		if len(q) != 1 || len(q[0].Recipients) != 1 || q[0].Recipients[0] != "fan-a@localhost" {
			t.Errorf("quarantine = %v, want one envelope for fan-a only", q)
		}
	})

	t.Run("move rule files only that recipient's copy", func(t *testing.T) {
		a, cancel := startFilterAgentHost(t, "mx.fanout.test")
		defer cancel()
		ua := createFilterUser(t, a, "fan-a@localhost", "testpass")
		ub := createFilterUser(t, a, "fan-b@localhost", "testpass")
		createMailbox(t, a, ua, "Folder")
		createRule(t, a.Rules, ua, &bongov1.FilterRule{
			Id: "mv", Name: "file", Priority: 1, Enabled: true,
			Conditions: []*bongov1.Condition{{Field: "subject", Operator: "contains", Value: "Fanout"}},
			Action:     &bongov1.FilterAction{Type: "move", Target: "Folder"},
			CreatedAt:  time.Now().Unix(),
		})

		sendMessageMulti(t, a.SMTP.MXAddr(), "sender@example.com", []string{"fan-a@localhost", "fan-b@localhost"}, "Fanout two", "hello")

		if n := waitMailboxCount(a, ua, "Folder", 1); n != 1 {
			t.Fatalf("a Folder = %d, want 1", n)
		}
		if n := waitMailboxCount(a, ub, "INBOX", 1); n != 1 {
			t.Fatalf("b INBOX = %d, want 1", n)
		}
		if msgs, _, _ := a.Mailboxes.ListMessagesPaged(ua, "INBOX", 0, 10); len(msgs) != 0 {
			t.Errorf("a INBOX = %d, want 0", len(msgs))
		}

		// Both copies were authenticated from identical input, so their
		// Authentication-Results must match byte for byte. X-Spam-* is not
		// compared: Bayes is per recipient and may differ.
		ra := authResultsHeader(storedRawMessage(t, a, ua, "Folder"))
		rb := authResultsHeader(storedRawMessage(t, a, ub, "INBOX"))
		if ra == "" {
			t.Fatal("copy for a has no Authentication-Results header")
		}
		if ra != rb {
			t.Errorf("Authentication-Results differ:\n a: %s\n b: %s", ra, rb)
		}
	})
}
