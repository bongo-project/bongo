//go:build integration

package integration

import (
	"crypto/tls"
	"errors"
	"fmt"
	smtplib "net/smtp"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-sasl"
	gosmtp "github.com/emersion/go-smtp"

	"github.com/bongo-project/bongo/internal/agent"
	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Mail to Postmaster@ on a hosted domain with no owner reaches the account
// named by smtp.postmaster_account, through the real SMTP listener, the shared
// resolver and the deliver stage. abuse@ takes the same path.
func TestPostmasterDeliveredToConfiguredAccount(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	cfg.Server.BindAddr = "127.0.0.1"
	sharedAssignFreePorts(t, cfg)
	cfg.SMTP.Enabled = true
	cfg.SMTP.DKIM.Domain = "pm.test" // ensureConfiguredDomain hosts it
	cfg.SMTP.PostmasterAccount = "pmbox@pm.test"

	a, cancel, done := sharedStartAgent(t, cfg)
	defer cancel()
	_ = done

	userID, err := auth.CreateUser(a, a.Metadata, "pmbox@pm.test", "Admin", "testpass", false)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	for i, rcpt := range []string{"Postmaster@pm.test", "abuse@pm.test"} {
		msg := fmt.Sprintf("From: sender@example.com\r\nTo: %s\r\nSubject: Role mailbox %d\r\n"+
			"Message-Id: <pm-%d@example.com>\r\n\r\nHello\r\n", rcpt, i, i)
		deadline := time.Now().Add(10 * time.Second)
		for {
			err = smtplib.SendMail(a.SMTP.MXAddr(), nil, "sender@example.com", []string{rcpt}, []byte(msg))
			if err == nil || time.Now().After(deadline) {
				break
			}
			time.Sleep(50 * time.Millisecond) // the account may not resolve yet
		}
		if err != nil {
			t.Fatalf("SendMail to %s: %v", rcpt, err)
		}
	}

	prefix := fmt.Sprintf("/user/%s/mail/", userID)
	deadline := time.Now().Add(10 * time.Second)
	count := 0
	for {
		iter, err := a.Store.PrefixScan([]byte(prefix))
		if err != nil {
			t.Fatalf("PrefixScan: %v", err)
		}
		count = 0
		for iter.Next() {
			count++
		}
		iter.Close()
		if count >= 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if count != 2 {
		t.Fatalf("admin received %d messages, want 2 (postmaster@ and abuse@)", count)
	}
}

// putDomain hosts a domain through Raft, as the admin handler does. When
// aliasOf is set the alias_of key is written first, matching the handler's
// documented partial-failure order.
func putDomain(t *testing.T, a *agent.Agent, name, aliasOf string) {
	t.Helper()
	if aliasOf != "" {
		cmd := &bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: store.DomainAliasOfKey(name), Value: []byte(aliasOf)}
		if err := a.ApplyCommand(cmd, 5*time.Second); err != nil {
			t.Fatalf("put alias_of %s: %v", name, err)
		}
	}
	cmd := &bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: []byte("/system/domain/" + name), Value: []byte("enabled")}
	if err := a.ApplyCommand(cmd, 5*time.Second); err != nil {
		t.Fatalf("put domain %s: %v", name, err)
	}
}

// sendMXWithRetry sends one message to the MX listener, retrying until the
// recipient resolves or the deadline passes.
func sendMXWithRetry(t *testing.T, a *agent.Agent, rcpt, subject string) {
	t.Helper()
	msg := fmt.Sprintf("From: sender@example.com\r\nTo: %s\r\nSubject: %s\r\n"+
		"Message-Id: <%d@example.com>\r\n\r\nHello\r\n", rcpt, subject, time.Now().UnixNano())
	var err error
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		err = smtplib.SendMail(a.SMTP.MXAddr(), nil, "sender@example.com", []string{rcpt}, []byte(msg))
		if err == nil || time.Now().After(deadline) {
			break
		}
	}
	if err != nil {
		t.Fatalf("SendMail to %s: %v", rcpt, err)
	}
}

func stopAgent(t *testing.T, cancel func(), done <-chan error) {
	t.Helper()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Logf("agent Run returned: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("agent did not stop")
	}
}

// With no smtp.postmaster_account, Postmaster@ on a hosted domain goes to the
// domain owner's INBOX. The owner's address is not postmaster@, so only the
// owner tier can route it.
func TestPostmasterDeliveredToDomainOwner(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	cfg.Server.BindAddr = "127.0.0.1"
	sharedAssignFreePorts(t, cfg)
	cfg.SMTP.Enabled = true

	a, cancel, done := sharedStartAgent(t, cfg)
	defer stopAgent(t, cancel, done)

	putDomain(t, a, "pmowner.test", "")
	ownerID, err := auth.CreateUser(a, a.Metadata, "boss@pmowner.test", "Boss", "testpass", false)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := store.NewDomainAdminStore(a.Store, a).SetOwner("pmowner.test", ownerID); err != nil {
		t.Fatalf("SetOwner: %v", err)
	}

	sendMXWithRetry(t, a, "Postmaster@pmowner.test", "Owner postmaster")
	if n := waitMailboxCount(a, ownerID, "INBOX", 1); n != 1 {
		t.Fatalf("owner INBOX has %d messages, want 1", n)
	}
	if raw := storedRawMessage(t, a, ownerID, "INBOX"); !strings.Contains(raw, "Subject: Owner postmaster") {
		t.Errorf("stored message is not the postmaster mail: %q", raw)
	}
}

// Mail to an alias domain lands in the target domain's account, and an unknown
// local part on the alias domain is refused with 550 5.1.1 on both the MX and
// the authenticated submission listener.
func TestAliasDomainDeliveryAndUnknownLocalPart(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	cfg.Server.BindAddr = "127.0.0.1"
	sharedAssignFreePorts(t, cfg)
	cfg.SMTP.Enabled = true

	a, cancel, done := sharedStartAgent(t, cfg)
	defer stopAgent(t, cancel, done)

	putDomain(t, a, "target.test", "")
	putDomain(t, a, "alias.test", "target.test")
	const pass = "testpass"
	userID, err := auth.CreateUser(a, a.Metadata, "user@target.test", "User", pass, false)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	sendMXWithRetry(t, a, "user@alias.test", "Via alias domain")
	if n := waitMailboxCount(a, userID, "INBOX", 1); n != 1 {
		t.Fatalf("target INBOX has %d messages, want 1", n)
	}
	if raw := storedRawMessage(t, a, userID, "INBOX"); !strings.Contains(raw, "Subject: Via alias domain") {
		t.Errorf("stored message is not the alias mail: %q", raw)
	}

	insecure := &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12} // #nosec G402 -- self-signed dev certificate
	tests := []struct {
		name string
		rcpt func(t *testing.T) error
	}{
		{"mx", func(t *testing.T) error {
			c, err := gosmtp.Dial(a.SMTP.MXAddr())
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			defer c.Close()
			if err := c.Hello("client.example"); err != nil {
				t.Fatalf("EHLO: %v", err)
			}
			if err := c.Mail("sender@example.com", nil); err != nil {
				t.Fatalf("MAIL: %v", err)
			}
			return c.Rcpt("nobody@alias.test", nil)
		}},
		{"submission", func(t *testing.T) error {
			c, err := gosmtp.DialTLS(a.SMTP.SubmissionsAddr(), insecure)
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			defer c.Close()
			if err := c.Hello("client.example"); err != nil {
				t.Fatalf("EHLO: %v", err)
			}
			if err := c.Auth(sasl.NewPlainClient("", "user@target.test", pass)); err != nil {
				t.Fatalf("AUTH: %v", err)
			}
			if err := c.Mail("user@target.test", nil); err != nil {
				t.Fatalf("MAIL: %v", err)
			}
			return c.Rcpt("nobody@alias.test", nil)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.rcpt(t)
			var se *gosmtp.SMTPError
			if !errors.As(err, &se) {
				t.Fatalf("RCPT error = %v, want *SMTPError", err)
			}
			if se.Code != 550 || se.EnhancedCode != (gosmtp.EnhancedCode{5, 1, 1}) {
				t.Errorf("RCPT = %d %v (%s), want 550 5.1.1", se.Code, se.EnhancedCode, se.Message)
			}
		})
	}
}
