//go:build integration

package integration

import (
	"crypto/tls"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-sasl"
	gosmtp "github.com/emersion/go-smtp"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// vacationSubjects returns the subjects of every message stored for a user.
func vacationSubjects(t *testing.T, st *store.PebbleStore, userID string) []string {
	t.Helper()
	iter, err := st.PrefixScan([]byte("/user/" + userID + "/mail/"))
	if err != nil {
		t.Fatalf("PrefixScan: %v", err)
	}
	defer iter.Close()
	var out []string
	for iter.Next() {
		val, err := iter.Value()
		if err != nil {
			continue
		}
		var meta bongov1.MailMeta
		if proto.Unmarshal(val, &meta) == nil {
			out = append(out, meta.Subject)
		}
	}
	return out
}

// An authenticated local user writes to a user who is away. The reply goes
// through the real submission listener, pipeline, deliver stage, tracking key
// and local re-delivery, once; a second message inside the interval and a
// message to a user whose vacation is off get none.
func TestVacationReplyEndToEnd(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	cfg.Server.BindAddr = "127.0.0.1"
	sharedAssignFreePorts(t, cfg)
	cfg.SMTP.Enabled = true

	a, cancel, _ := sharedStartAgent(t, cfg)
	defer cancel()

	aliceID, err := auth.CreateUser(a, a.Metadata, "alice@localhost", "Alice", "alicepass", false)
	if err != nil {
		t.Fatalf("CreateUser alice: %v", err)
	}
	carolID, err := auth.CreateUser(a, a.Metadata, "carol@localhost", "Carol", "carolpass", false)
	if err != nil {
		t.Fatalf("CreateUser carol: %v", err)
	}
	daveID, err := auth.CreateUser(a, a.Metadata, "dave@localhost", "Dave", "davepass", false)
	if err != nil {
		t.Fatalf("CreateUser dave: %v", err)
	}
	resolver := store.NewRecipientResolver(a.AddressStore, a.Store, a.Metadata.System)
	for _, addr := range []string{"alice@localhost", "carol@localhost", "dave@localhost"} {
		deadline := time.Now().Add(10 * time.Second)
		for {
			if _, rerr := resolver.ResolveRecipient(addr); rerr == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s did not resolve", addr)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	prefs := store.DefaultPreferences()
	prefs.VacationEnabled = true
	prefs.VacationBody = "I am away until Monday."
	if err := a.Preferences.Set(aliceID, prefs); err != nil {
		t.Fatalf("Preferences.Set: %v", err)
	}

	insecure := &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12} // #nosec G402 -- self-signed dev certificate
	submit := func(from, pass, to, subject string) {
		t.Helper()
		c, err := gosmtp.DialTLS(a.SMTP.SubmissionsAddr(), insecure)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer c.Close()
		if err := c.Hello("client.example"); err != nil {
			t.Fatalf("EHLO: %v", err)
		}
		if err := c.Auth(sasl.NewPlainClient("", from, pass)); err != nil {
			t.Fatalf("AUTH: %v", err)
		}
		msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMessage-ID: <%d@client.example>\r\n"+
			"Date: Mon, 01 Jan 2024 12:00:00 +0000\r\n\r\nHello\r\n", from, to, subject, time.Now().UnixNano())
		if err := c.SendMail(from, []string{to}, strings.NewReader(msg)); err != nil {
			t.Fatalf("SendMail: %v", err)
		}
	}
	waitFor := func(what string, ok func() bool) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for !ok() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s", what)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	autoReplies := func(uid string) int {
		n := 0
		for _, s := range vacationSubjects(t, a.Store, uid) {
			if strings.HasPrefix(s, "Auto: ") {
				n++
			}
		}
		return n
	}

	submit("carol@localhost", "carolpass", "alice@localhost", "Lunch")
	waitFor("alice to receive the message", func() bool { return len(vacationSubjects(t, a.Store, aliceID)) >= 1 })
	waitFor("carol's auto-reply", func() bool { return autoReplies(carolID) == 1 })
	if _, err := a.Store.Get(store.VacationSentKey(aliceID, "carol@localhost")); err != nil {
		t.Fatalf("tracking record: %v", err)
	}

	// A second message inside the interval, and then everything else, must not
	// add a reply. The later messages prove the earlier ones were processed.
	submit("carol@localhost", "carolpass", "alice@localhost", "Lunch again")
	submit("carol@localhost", "carolpass", "dave@localhost", "Not away")
	waitFor("dave to receive the message", func() bool { return len(vacationSubjects(t, a.Store, daveID)) >= 1 })
	waitFor("alice to receive the second message", func() bool { return len(vacationSubjects(t, a.Store, aliceID)) >= 2 })
	time.Sleep(2 * time.Second)
	if n := autoReplies(carolID); n != 1 {
		t.Fatalf("carol's auto-replies = %d, want 1", n)
	}
}
