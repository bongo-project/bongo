//go:build integration

package integration

import (
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	gosmtp "github.com/emersion/go-smtp"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// relayTx is one accepted SMTP transaction at the fake relay.
type relayTx struct {
	from string
	rcpt []string
	data string
}

// srsRelayBackend answers 550 at RCPT for rejectRcpt (a permanent failure the
// queue worker bounces at once) and accepts everything else, recording each
// accepted transaction.
type srsRelayBackend struct {
	rejectRcpt string
	accepted   chan relayTx
}

func (b *srsRelayBackend) NewSession(_ *gosmtp.Conn) (gosmtp.Session, error) {
	return &srsRelaySession{b: b}, nil
}

type srsRelaySession struct {
	b    *srsRelayBackend
	from string
	rcpt []string
}

func (s *srsRelaySession) AuthPlain(_, _ string) error { return nil }
func (s *srsRelaySession) Mail(from string, _ *gosmtp.MailOptions) error {
	s.from = from
	return nil
}
func (s *srsRelaySession) Rcpt(to string, _ *gosmtp.RcptOptions) error {
	if strings.EqualFold(to, s.b.rejectRcpt) {
		return &gosmtp.SMTPError{Code: 550, EnhancedCode: gosmtp.EnhancedCode{5, 1, 1}, Message: "no such user"}
	}
	s.rcpt = append(s.rcpt, to)
	return nil
}
func (s *srsRelaySession) Data(r io.Reader) error {
	data, _ := io.ReadAll(r)
	s.b.accepted <- relayTx{from: s.from, rcpt: append([]string(nil), s.rcpt...), data: string(data)}
	return nil
}
func (s *srsRelaySession) Reset()        {}
func (s *srsRelaySession) Logout() error { return nil }

// TestSRSBounceEndToEnd drives an authenticated forward that the remote side
// refuses: the queue worker's BounceGenerator writes a real DSN to the SRS
// sender, the DSN goes through the inbound spool and deliver stage, and
// exactly one null-sender message reaches the original sender. Nothing lands
// in the forwarding user's mailboxes.
func TestSRSBounceEndToEnd(t *testing.T) {
	const (
		target = "ext@dest.example"
		bob    = "bob@example.org"
		userB  = "b@hosted.test"
	)
	backend := &srsRelayBackend{rejectRcpt: target, accepted: make(chan relayTx, 10)}
	srv := gosmtp.NewServer(backend)
	srv.Domain = "localhost"
	srv.AllowInsecureAuth = true
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("relay listen: %v", err)
	}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })

	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	cfg.Server.BindAddr = "127.0.0.1"
	sharedAssignFreePorts(t, cfg)
	cfg.SMTP.Enabled = true
	cfg.SMTP.Hostname = "mail.hosted.test" // seeds hosted.test as a hosted domain
	cfg.SMTP.Relay.Enabled = true
	cfg.SMTP.Relay.Host = l.Addr().String()
	cfg.Queue.PollInterval = "500ms"

	a := startDevAgent(t, cfg)

	userID, err := auth.CreateUser(a, a.Metadata, userB, "B", "pass", false)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if a.Mailboxes != nil {
		a.Mailboxes.EnsureDefaultMailboxes(userID)
	}
	if err := a.Preferences.Set(userID, &bongov1.UserPreferences{ForwardAddress: target, ForwardKeepCopy: false}); err != nil {
		t.Fatalf("Preferences.Set: %v", err)
	}
	if a.ForwardTargets == nil {
		t.Fatal("ForwardTargets not wired")
	}
	if err := a.ForwardTargets.SetState(userID, target, store.ForwardStateVerified); err != nil {
		t.Fatalf("SetState: %v", err)
	}

	raw := "From: Bob <" + bob + ">\r\nTo: " + userB + "\r\nSubject: srs-e2e-original\r\n" +
		"Message-ID: <srs-e2e@example.org>\r\nDate: Mon, 01 Jan 2024 12:00:00 +0000\r\n\r\nhello\r\n"
	hash, err := a.Blobs.Put([]byte(raw))
	if err != nil {
		t.Fatalf("Blobs.Put: %v", err)
	}
	// Real SMTP from 127.0.0.1 cannot produce spf=pass for example.org, so the
	// message enters the spool directly.
	env := &bongov1.QueueEnvelope{
		Id: "srs-e2e", Sender: bob, Recipients: []string{userB},
		SpfResult: "pass", BlobHash: hash, ReceivedAt: time.Now().Unix(),
	}
	if err := a.Spool.Enqueue(env); err != nil {
		t.Fatalf("Spool.Enqueue: %v", err)
	}

	var tx relayTx
	select {
	case tx = <-backend.accepted:
	case <-time.After(60 * time.Second):
		t.Fatal("no message relayed to " + bob)
	}
	if tx.from != "" {
		t.Errorf("MAIL FROM = %q, want null sender", tx.from)
	}
	if len(tx.rcpt) != 1 || !strings.EqualFold(tx.rcpt[0], bob) {
		t.Errorf("recipients = %v, want [%s]", tx.rcpt, bob)
	}
	for _, want := range []string{"Reporting-MTA", "message/delivery-status", "srs-e2e-original"} {
		if !strings.Contains(tx.data, want) {
			t.Errorf("relayed message lacks %q; it is not the generated DSN:\n%s", want, tx.data)
		}
	}

	select {
	case extra := <-backend.accepted:
		t.Errorf("second message relayed: from=%q rcpt=%v", extra.from, extra.rcpt)
	case <-time.After(5 * time.Second):
	}

	// Every mailbox of B, not only INBOX, must be empty.
	for _, prefix := range []string{
		fmt.Sprintf("/user/%s/mail/", userID),
		fmt.Sprintf("/user/%s/mailbox/", userID),
	} {
		iter, err := a.Store.PrefixScan([]byte(prefix))
		if err != nil {
			t.Fatalf("PrefixScan %s: %v", prefix, err)
		}
		for iter.Next() {
			key := string(iter.Key())
			if strings.HasPrefix(prefix, fmt.Sprintf("/user/%s/mailbox/", userID)) && !strings.Contains(key, "/msg/") {
				continue
			}
			t.Errorf("unexpected message record in B's account: %s", key)
		}
		iter.Close()
	}
}
