//go:build integration

package integration

import (
	"fmt"
	smtplib "net/smtp"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/auth"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// TestInbound_UnverifiedIdentityDoesNotHijack: user A holds unverified
// identity records for B's address and for an external address. Inbound MX
// mail to B reaches B and not A, and RCPT to the external address is refused.
func TestInbound_UnverifiedIdentityDoesNotHijack(t *testing.T) {
	a, cancel, done := startAgentForAliasTest(t)
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("agent did not stop")
		}
	}()

	idA, err := auth.CreateUser(a, a.Metadata, "a@example.com", "A", "testpass", false)
	if err != nil {
		t.Fatalf("CreateUser A: %v", err)
	}
	idB, err := auth.CreateUser(a, a.Metadata, "b@example.com", "B", "testpass", false)
	if err != nil {
		t.Fatalf("CreateUser B: %v", err)
	}
	time.Sleep(500 * time.Millisecond)

	// Simulate the pre-fix state: B has no address record and A holds
	// unverified identity claims on B's address and an external one.
	if err := a.AddressStore.Delete("b@example.com"); err != nil {
		t.Fatal(err)
	}
	for addr, domain := range map[string]string{"b@example.com": "example.com", "x@external.example": "external.example"} {
		if err := a.AddressStore.Put(addr, &bongov1.AddressRecord{
			UserId: idA, Domain: domain, Kind: "identity",
		}); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(300 * time.Millisecond)

	smtpAddr := a.SMTP.MXAddr()
	msg := buildTestMessage("sender@external.example.com", "b@example.com", "For B", "hello")
	if err := smtplib.SendMail(smtpAddr, nil, "sender@external.example.com", []string{"b@example.com"}, []byte(msg)); err != nil {
		t.Fatalf("SendMail to B: %v", err)
	}

	countKeys := func(userID string) int {
		iter, err := a.Store.PrefixScan([]byte(fmt.Sprintf("/user/%s/mail/", userID)))
		if err != nil {
			t.Fatal(err)
		}
		defer iter.Close()
		n := 0
		for iter.Next() {
			n++
		}
		return n
	}
	deadline := time.Now().Add(10 * time.Second)
	for countKeys(idB) == 0 && time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
	}
	if countKeys(idB) == 0 {
		t.Fatal("mail to B was not delivered to B")
	}
	if n := countKeys(idA); n != 0 {
		t.Errorf("A received %d message(s) addressed to B", n)
	}

	msg = buildTestMessage("sender@external.example.com", "x@external.example", "For X", "hello")
	if err := smtplib.SendMail(smtpAddr, nil, "sender@external.example.com", []string{"x@external.example"}, []byte(msg)); err == nil {
		t.Error("RCPT to an external address with only an unverified identity was accepted")
	}
}
