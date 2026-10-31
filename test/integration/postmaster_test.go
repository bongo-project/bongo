//go:build integration

package integration

import (
	"fmt"
	smtplib "net/smtp"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/config"
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
