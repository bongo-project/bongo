//go:build integration

package integration

import (
	"crypto/tls"
	"fmt"
	"io"
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

// rawBySubject returns the raw bytes of every message of a user whose subject
// starts with prefix.
func rawBySubject(t *testing.T, st *store.PebbleStore, blobs *store.BlobStore, userID, prefix string) []string {
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
		if proto.Unmarshal(val, &meta) != nil || !strings.HasPrefix(meta.Subject, prefix) {
			continue
		}
		raw, err := store.RawMessageBytes(blobs, &meta)
		if err != nil {
			t.Fatalf("RawMessageBytes: %v", err)
		}
		out = append(out, string(raw))
	}
	return out
}

// A local author asks for NOTIFY=SUCCESS through the real submission listener.
// The delivery report reaches the author's INBOX through the pipeline and
// deliver stage, echoes ENVID, ORCPT and (RET=FULL) the original; a message
// sent without NOTIFY produces no report.
func TestDSNSuccessReportEndToEnd(t *testing.T) {
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
	resolver := store.NewRecipientResolver(a.AddressStore, a.Store, a.Metadata.System)
	for _, addr := range []string{"alice@localhost", "carol@localhost"} {
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

	insecure := &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12} // #nosec G402 -- self-signed dev certificate
	submit := func(subject string, mailOpts *gosmtp.MailOptions, rcptOpts *gosmtp.RcptOptions) {
		t.Helper()
		c, err := gosmtp.DialTLS(a.SMTP.SubmissionsAddr(), insecure)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer c.Close()
		if err := c.Hello("client.example"); err != nil {
			t.Fatalf("EHLO: %v", err)
		}
		if err := c.Auth(sasl.NewPlainClient("", "carol@localhost", "carolpass")); err != nil {
			t.Fatalf("AUTH: %v", err)
		}
		if err := c.Mail("carol@localhost", mailOpts); err != nil {
			t.Fatalf("MAIL: %v", err)
		}
		if err := c.Rcpt("alice@localhost", rcptOpts); err != nil {
			t.Fatalf("RCPT: %v", err)
		}
		w, err := c.Data()
		if err != nil {
			t.Fatalf("DATA: %v", err)
		}
		msg := fmt.Sprintf("From: carol@localhost\r\nTo: alice@localhost\r\nSubject: %s\r\nMessage-ID: <%d@client.example>\r\n"+
			"Date: Mon, 01 Jan 2024 12:00:00 +0000\r\n\r\nBODY-MARKER-4471\r\n", subject, time.Now().UnixNano())
		if _, err := io.WriteString(w, msg); err != nil {
			t.Fatalf("write: %v", err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("end of DATA: %v", err)
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
	reports := func() []string {
		return rawBySubject(t, a.Store, a.Blobs, carolID, "Successful Mail Delivery Report")
	}
	received := func(subject string) int {
		return len(rawBySubject(t, a.Store, a.Blobs, aliceID, subject))
	}

	submit("Wanted", &gosmtp.MailOptions{Return: gosmtp.DSNReturnFull, EnvelopeID: "env-int-1"},
		&gosmtp.RcptOptions{
			Notify:                []gosmtp.DSNNotify{gosmtp.DSNNotifySuccess},
			OriginalRecipientType: gosmtp.DSNAddressTypeRFC822,
			OriginalRecipient:     "alice.original@elsewhere.example",
		})
	waitFor("alice to receive the message", func() bool { return received("Wanted") == 1 })
	waitFor("carol's delivery report", func() bool { return len(reports()) == 1 })
	rep := reports()[0]
	for _, want := range []string{
		"Action: delivered\r\n",
		"Status: 2.0.0\r\n",
		"Original-Envelope-Id: env-int-1\r\n",
		"Original-Recipient: rfc822; alice.original@elsewhere.example\r\n",
		"Final-Recipient: rfc822; alice@localhost\r\n",
		"Content-Type: message/rfc822\r\n",
		"BODY-MARKER-4471",
	} {
		if !strings.Contains(rep, want) {
			t.Errorf("report missing %q:\n%s", want, rep)
		}
	}

	// No NOTIFY, then NOTIFY=FAILURE only: neither asks for a success report.
	// Their arrival at alice proves they were processed before the check.
	submit("Plain", nil, nil)
	submit("FailureOnly", nil, &gosmtp.RcptOptions{Notify: []gosmtp.DSNNotify{gosmtp.DSNNotifyFailure}})
	waitFor("alice to receive both", func() bool { return received("Plain") == 1 && received("FailureOnly") == 1 })
	time.Sleep(500 * time.Millisecond)
	if n := len(reports()); n != 1 {
		t.Errorf("delivery reports = %d, want 1", n)
	}
}
