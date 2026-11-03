package smtp

import (
	"bufio"
	"context"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/bongo-project/bongo/internal/config"
)

// refusingSTARTTLSServer is a minimal SMTP server that advertises STARTTLS and
// then refuses it with 454, the reply an attacker on the path can inject to
// downgrade a session. It accepts the message in plaintext if the client goes
// on, and records whether a MAIL command arrived.
type refusingSTARTTLSServer struct {
	addr string
	mu   sync.Mutex
	mail bool
}

func (s *refusingSTARTTLSServer) sawMail() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mail
}

func startRefusingSTARTTLS(t *testing.T) *refusingSTARTTLSServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	s := &refusingSTARTTLSServer{addr: ln.Addr().String()}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(conn)
		}
	}()
	return s
}

func (s *refusingSTARTTLSServer) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	r := bufio.NewReader(conn)
	reply := func(line string) { _, _ = conn.Write([]byte(line + "\r\n")) }
	reply("220 mx.test ESMTP")
	inData := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		if inData {
			if cmd == "." {
				inData = false
				reply("250 2.0.0 queued")
			}
			continue
		}
		switch {
		case strings.HasPrefix(cmd, "EHLO"):
			reply("250-mx.test")
			reply("250-STARTTLS")
			reply("250 8BITMIME")
		case cmd == "STARTTLS":
			reply("454 4.7.0 TLS not available")
		case strings.HasPrefix(cmd, "MAIL"):
			s.mu.Lock()
			s.mail = true
			s.mu.Unlock()
			reply("250 2.1.0 ok")
		case strings.HasPrefix(cmd, "RCPT"):
			reply("250 2.1.5 ok")
		case cmd == "DATA":
			inData = true
			reply("354 go ahead")
		case cmd == "QUIT":
			reply("221 bye")
			return
		default:
			reply("250 ok")
		}
	}
}

// A domain whose MTA-STS or DANE policy requires TLS must not be continued in
// plaintext when the remote offers STARTTLS but refuses it. Without this, an
// injected 454 downgrades exactly the mail the policy exists to protect.
func TestRelayRequireTLSRefusedSTARTTLSNeverFallsBackToPlaintext(t *testing.T) {
	raw := []byte("From: alice@example.com\r\nTo: bob@external.com\r\nSubject: t\r\n\r\nhi")

	t.Run("policy requires TLS", func(t *testing.T) {
		srv := startRefusingSTARTTLS(t)
		relay := NewRelay(&config.SMTPRelay{Enabled: true, Host: srv.addr}, "mail.example.test", slog.Default())
		_, err := relay.sendToHost(context.Background(), srv.addr, "alice@example.com",
			[]string{"bob@external.com"}, raw, deliveryTLSPolicy{requireTLS: true})
		if err == nil {
			t.Fatal("relay delivered in plaintext after STARTTLS was refused, to a host whose policy requires TLS")
		}
		if !strings.Contains(err.Error(), "policy requires") {
			t.Errorf("error = %q, want it to name the TLS policy", err)
		}
		if srv.sawMail() {
			t.Error("MAIL was sent in the clear to a host whose policy requires TLS")
		}
	})

	// Control: without a policy the refusal is tolerated and the message goes
	// out in plaintext, which proves the refusal path is reached and that only
	// the policy stops delivery above.
	t.Run("no policy continues without TLS", func(t *testing.T) {
		srv := startRefusingSTARTTLS(t)
		relay := NewRelay(&config.SMTPRelay{Enabled: true, Host: srv.addr}, "mail.example.test", slog.Default())
		if _, err := relay.sendToHost(context.Background(), srv.addr, "alice@example.com",
			[]string{"bob@external.com"}, raw, deliveryTLSPolicy{}); err != nil {
			t.Fatalf("opportunistic delivery failed: %v", err)
		}
		if !srv.sawMail() {
			t.Error("message was not delivered without a policy")
		}
	})
}
