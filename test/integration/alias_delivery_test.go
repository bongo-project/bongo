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

	"github.com/bongo-project/bongo/internal/agent"
	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// startAgentForAliasTest starts a dev-mode agent with SMTP enabled and
// returns the agent plus a cancel function. Waits for the agent to be ready.
func startAgentForAliasTest(t *testing.T) (*agent.Agent, context.CancelFunc, <-chan error) {
	t.Helper()
	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	cfg.Server.BindAddr = "127.0.0.1"
	sharedAssignFreePorts(t, cfg)
	cfg.SMTP.Enabled = true

	logger := sharedTestLogger()

	a, err := agent.NewAgent(cfg, logger)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- a.Run(ctx)
	}()

	deadline := time.Now().Add(10 * time.Second)
	for !a.Ready() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if !a.Ready() {
		cancel()
		t.Fatal("agent not ready in time")
	}

	return a, cancel, done
}

// TestAliasDelivery_InboundSMTP drives the real inbound SMTP path end-to-end:
//   - External sender → RCPT to alias address → message lands in account's INBOX.
//
// US-RECV-1 acceptance criterion.
func TestAliasDelivery_InboundSMTP(t *testing.T) {
	a, cancel, done := startAgentForAliasTest(t)
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Logf("agent Run returned: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("agent did not stop")
		}
	}()

	// Create the account that the alias will point to.
	ownerEmail := "owner@example.com"
	ownerID, err := auth.CreateUser(a, a.Metadata, ownerEmail, "Test User", "testpass", false)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Logf("created owner account: %s (id: %s)", ownerEmail, ownerID)

	// Wait for Raft to propagate the user profile.
	time.Sleep(500 * time.Millisecond)

	// Also create a mailbox for the owner (INBOX should exist by default in dev mode,
	// but ensure it's there).
	if a.Mailboxes != nil {
		_ = a.Mailboxes.CreateMailbox(ownerID, "INBOX")
	}

	// Populate the AddressStore with:
	// 1. The owner's primary address (so tier 1 resolves it directly).
	// 2. An alias address that points to the owner.
	aliasEmail := "user@example.net"
	if a.AddressStore == nil {
		t.Fatal("AddressStore not available")
	}

	// Primary address record.
	if err := a.AddressStore.Put(ownerEmail, &bongov1.AddressRecord{
		UserId:   ownerID,
		Domain:   "example.com",
		Kind:     "primary",
		CanSend:  true,
		Verified: true,
	}); err != nil {
		t.Fatalf("AddressStore.Put primary: %v", err)
	}

	// Alias address record pointing to the same user.
	if err := a.AddressStore.Put(aliasEmail, &bongov1.AddressRecord{
		UserId:   ownerID,
		Domain:   "example.net",
		Kind:     "alias",
		CanSend:  false,
		Verified: true,
	}); err != nil {
		t.Fatalf("AddressStore.Put alias: %v", err)
	}
	t.Logf("alias %s → %s (%s) written to address index", aliasEmail, ownerEmail, ownerID)

	// Allow Raft to propagate the address index writes.
	time.Sleep(300 * time.Millisecond)

	smtpAddr := a.SMTP.MXAddr()
	t.Logf("SMTP MX address: %s", smtpAddr)

	// --- Positive test: inbound mail to alias is accepted and delivered ---

	msg := buildTestMessage("sender@external.example.com", aliasEmail, "Alias Delivery Test", "Hello via alias!")

	err = smtplib.SendMail(
		smtpAddr,
		nil, // no auth — simulates an external MX connection
		"sender@external.example.com",
		[]string{aliasEmail},
		[]byte(msg),
	)
	if err != nil {
		t.Fatalf("SendMail to alias %s: %v (expected RCPT ACCEPT)", aliasEmail, err)
	}
	t.Logf("message to alias %s accepted at RCPT", aliasEmail)

	// Wait for the pipeline to deliver to the owner's INBOX.
	var (
		mailKey   string
		mailValue []byte
		mailCount int
	)
	mailPrefix := fmt.Sprintf("/user/%s/mail/", ownerID)
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		iter, err := a.Store.PrefixScan([]byte(mailPrefix))
		if err != nil {
			t.Fatalf("PrefixScan: %v", err)
		}
		mailCount = 0
		for iter.Next() {
			mailKey = string(iter.Key())
			mailValue, _ = iter.Value()
			mailCount++
		}
		iter.Close()
		if mailCount > 0 {
			break
		}
	}

	if mailCount == 0 {
		t.Fatalf("mail sent to alias %s was NOT delivered to owner's INBOX (%s) — US-RECV-1 FAIL", aliasEmail, ownerEmail)
	}
	t.Logf("found %d mail key(s) in owner's Pebble namespace: %s", mailCount, mailKey)

	// Parse MailMeta to confirm the message identity.
	var meta bongov1.MailMeta
	if err := proto.Unmarshal(mailValue, &meta); err != nil {
		t.Fatalf("unmarshal MailMeta: %v", err)
	}
	if meta.Subject != "Alias Delivery Test" {
		t.Errorf("subject = %q, want %q", meta.Subject, "Alias Delivery Test")
	}
	if meta.From != "sender@external.example.com" {
		t.Errorf("from = %q, want %q", meta.From, "sender@external.example.com")
	}
	t.Logf("delivery confirmed: subject=%q from=%q", meta.Subject, meta.From)

	// Verify the message also reached the per-user SQLite storeobject table.
	// The derived SQLite index is populated asynchronously by the FSM after the
	// Pebble write, so poll with a short timeout rather than querying immediately.
	var sqliteObjs []store.StoreObject
	for sqDeadline := time.Now().Add(5 * time.Second); time.Now().Before(sqDeadline); time.Sleep(200 * time.Millisecond) {
		objs, qErr := a.Metadata.QueryByType(ownerID, store.DocTypeMail)
		if qErr != nil {
			t.Logf("QueryByType transient error (will retry): %v", qErr)
			continue
		}
		if len(objs) > 0 {
			sqliteObjs = objs
			break
		}
	}
	if len(sqliteObjs) == 0 {
		t.Error("message arrived in Pebble but NOT in per-user SQLite after 5s (pipeline partial failure)")
	} else {
		t.Logf("per-user SQLite: %d mail object(s)", len(sqliteObjs))
	}
}

// TestAliasDelivery_RejectUnknownAddress verifies the open-relay guard:
// an address that is neither a user, an alias, nor a catch-all is rejected
// with a 550 at RCPT — no accept-then-drop.
func TestAliasDelivery_RejectUnknownAddress(t *testing.T) {
	a, cancel, done := startAgentForAliasTest(t)
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Logf("agent Run returned: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("agent did not stop")
		}
	}()

	// Wait for dev-mode admin user to be created.
	time.Sleep(500 * time.Millisecond)

	smtpAddr := a.SMTP.MXAddr()
	unknownAddr := "nobody@completely-unknown-domain.example"

	msg := buildTestMessage("sender@external.example.com", unknownAddr, "Should Bounce", "Test")

	err := smtplib.SendMail(
		smtpAddr,
		nil, // unauthenticated external sender
		"sender@external.example.com",
		[]string{unknownAddr},
		[]byte(msg),
	)
	if err == nil {
		t.Fatalf("SendMail to unknown address %s succeeded — OPEN RELAY RISK", unknownAddr)
	}
	t.Logf("SendMail to unknown address correctly rejected: %v", err)

	// Confirm the error indicates relay denied / user not found (5xx class).
	errStr := err.Error()
	if !strings.Contains(errStr, "55") && !strings.Contains(errStr, "relay") && !strings.Contains(errStr, "Relay") {
		t.Errorf("rejection error %q does not look like a 55x relay-denied response", errStr)
	}
}

// TestAliasDelivery_NormalUserStillDelivers verifies that regular (non-alias)
// user addresses continue to work after the resolver change (no regression).
func TestAliasDelivery_NormalUserStillDelivers(t *testing.T) {
	a, cancel, done := startAgentForAliasTest(t)
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Logf("agent Run returned: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("agent did not stop")
		}
	}()

	// Create a regular user (not in address index — relies on tier-2 fallback).
	userEmail := "regular@localhost"
	userID, err := auth.CreateUser(a, a.Metadata, userEmail, "Regular User", "testpass", false)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Logf("created regular user: %s (id: %s)", userEmail, userID)

	time.Sleep(500 * time.Millisecond)

	if a.Mailboxes != nil {
		_ = a.Mailboxes.CreateMailbox(userID, "INBOX")
	}

	smtpAddr := a.SMTP.MXAddr()
	msg := buildTestMessage("sender@external.example.com", userEmail, "Normal Delivery Test", "Hello regular user!")

	err = smtplib.SendMail(
		smtpAddr,
		nil,
		"sender@external.example.com",
		[]string{userEmail},
		[]byte(msg),
	)
	if err != nil {
		t.Fatalf("SendMail to regular user %s: %v (regression: normal delivery broken)", userEmail, err)
	}
	t.Logf("message to regular user %s accepted at RCPT", userEmail)

	// Wait for delivery.
	mailPrefix := fmt.Sprintf("/user/%s/mail/", userID)
	var mailCount int
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		iter, err := a.Store.PrefixScan([]byte(mailPrefix))
		if err != nil {
			t.Fatalf("PrefixScan: %v", err)
		}
		mailCount = 0
		for iter.Next() {
			_, _ = iter.Value()
			mailCount++
		}
		iter.Close()
		if mailCount > 0 {
			break
		}
	}
	if mailCount == 0 {
		t.Error("message to regular user not delivered — tier-2 fallback broken")
	} else {
		t.Logf("regular user delivery confirmed: %d message(s)", mailCount)
	}
}

// buildTestMessage constructs a minimal RFC 5322 message for SMTP testing.
func buildTestMessage(from, to, subject, body string) string {
	return fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: %s\r\nContent-Type: text/plain\r\n\r\n%s\r\n",
		from, to, subject, body,
	)
}
