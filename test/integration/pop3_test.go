//go:build integration

package integration

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	smtplib "net/smtp"
	"strings"
	"testing"
	"time"

	"github.com/bongo-go/bongo-go/internal/agent"
	"github.com/bongo-go/bongo-go/internal/auth"
	"github.com/bongo-go/bongo-go/internal/config"
)

// pop3Client is a simple POP3 client for integration testing.
type pop3Client struct {
	conn   net.Conn
	reader *bufio.Reader
	t      *testing.T
}

func dialPOP3(t *testing.T, addr string) *pop3Client {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial POP3 %s: %v", addr, err)
	}
	c := &pop3Client{
		conn:   conn,
		reader: bufio.NewReader(conn),
		t:      t,
	}
	// Read greeting.
	line := c.readLine()
	if !strings.HasPrefix(line, "+OK") {
		t.Fatalf("expected +OK greeting, got: %s", line)
	}
	return c
}

func (c *pop3Client) send(cmd string) {
	c.t.Helper()
	fmt.Fprintf(c.conn, "%s\r\n", cmd)
}

func (c *pop3Client) readLine() string {
	c.t.Helper()
	c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := c.reader.ReadString('\n')
	if err != nil {
		c.t.Fatalf("read: %v", err)
	}
	return strings.TrimRight(line, "\r\n")
}

func (c *pop3Client) readMulti() []string {
	c.t.Helper()
	var lines []string
	for {
		line := c.readLine()
		if line == "." {
			break
		}
		lines = append(lines, line)
	}
	return lines
}

func (c *pop3Client) expectOK() string {
	c.t.Helper()
	line := c.readLine()
	if !strings.HasPrefix(line, "+OK") {
		c.t.Fatalf("expected +OK, got: %s", line)
	}
	return line
}

func (c *pop3Client) expectERR() string {
	c.t.Helper()
	line := c.readLine()
	if !strings.HasPrefix(line, "-ERR") {
		c.t.Fatalf("expected -ERR, got: %s", line)
	}
	return line
}

func (c *pop3Client) close() {
	c.conn.Close()
}

func (c *pop3Client) login(user, pass string) {
	c.t.Helper()
	c.send("USER " + user)
	c.expectOK()
	c.send("PASS " + pass)
	c.expectOK()
}

// startPOP3Agent starts a dev agent with SMTP, IMAP, and POP3 enabled.
func startPOP3Agent(t *testing.T) (*agent.Agent, context.CancelFunc) {
	t.Helper()

	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	cfg.Server.BindAddr = "127.0.0.1"
	sharedAssignFreePorts(t, cfg)
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

	// Wait for ready.
	deadline := time.Now().Add(15 * time.Second)
	for !a.Ready() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if !a.Ready() {
		cancel()
		t.Fatal("agent not ready in time")
	}

	return a, cancel
}

func createPOP3User(t *testing.T, a *agent.Agent, email, password string) string {
	t.Helper()
	userID, err := auth.CreateUser(a, a.Metadata, email, "POP3 Test", password, false)
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

func deliverTestEmail(t *testing.T, smtpAddr, to, subject string) {
	t.Helper()
	msg := fmt.Sprintf("From: sender@example.com\r\n"+
		"To: %s\r\n"+
		"Subject: %s\r\n"+
		"Message-Id: <%s@example.com>\r\n"+
		"MIME-Version: 1.0\r\n"+
		"Content-Type: text/plain\r\n"+
		"\r\n"+
		"Hello from POP3 integration test!\r\n", to, subject, subject)

	err := smtplib.SendMail(smtpAddr, nil, "sender@example.com", []string{to}, []byte(msg))
	if err != nil {
		t.Fatalf("SendMail: %v", err)
	}
	// Wait for delivery/raft propagation.
	time.Sleep(1 * time.Second)
}

func TestPOP3Authentication(t *testing.T) {
	a, cancel := startPOP3Agent(t)
	defer cancel()

	pop3Addr := a.POP3.PlainAddr()
	email := "pop3auth@localhost"
	createPOP3User(t, a, email, "testpass")

	// Successful auth.
	c := dialPOP3(t, pop3Addr)
	c.login(email, "testpass")
	c.send("QUIT")
	c.expectOK()
	c.close()

	// Failed auth.
	c = dialPOP3(t, pop3Addr)
	c.send("USER " + email)
	c.expectOK()
	c.send("PASS wrongpassword")
	c.expectERR()
	c.close()
}

func TestPOP3MailRetrieval(t *testing.T) {
	a, cancel := startPOP3Agent(t)
	defer cancel()

	pop3Addr := a.POP3.PlainAddr()
	email := "pop3retr@localhost"
	createPOP3User(t, a, email, "testpass")

	deliverTestEmail(t, a.SMTP.MXAddr(), email, "POP3-Retrieval-Test")

	c := dialPOP3(t, pop3Addr)
	c.login(email, "testpass")

	// STAT.
	c.send("STAT")
	stat := c.expectOK()
	if !strings.Contains(stat, "1 ") {
		t.Errorf("STAT expected 1 message, got: %s", stat)
	}

	// LIST.
	c.send("LIST")
	listFirst := c.readLine()
	if !strings.HasPrefix(listFirst, "+OK") {
		t.Fatalf("LIST: expected +OK, got: %s", listFirst)
	}
	listLines := c.readMulti()
	if len(listLines) != 1 {
		t.Errorf("LIST: expected 1 message, got %d", len(listLines))
	}

	// UIDL.
	c.send("UIDL")
	uidlFirst := c.readLine()
	if !strings.HasPrefix(uidlFirst, "+OK") {
		t.Fatalf("UIDL: expected +OK, got: %s", uidlFirst)
	}
	uidlLines := c.readMulti()
	if len(uidlLines) != 1 {
		t.Errorf("UIDL: expected 1 entry, got %d", len(uidlLines))
	}

	// RETR.
	c.send("RETR 1")
	retrFirst := c.readLine()
	if !strings.HasPrefix(retrFirst, "+OK") {
		t.Fatalf("RETR 1: expected +OK, got: %s", retrFirst)
	}
	retrLines := c.readMulti()
	retrBody := strings.Join(retrLines, "\n")
	if !strings.Contains(retrBody, "POP3-Retrieval-Test") {
		t.Errorf("RETR: expected subject in body, got: %s", retrBody[:min(200, len(retrBody))])
	}

	// TOP 1 5.
	c.send("TOP 1 5")
	topFirst := c.readLine()
	if !strings.HasPrefix(topFirst, "+OK") {
		t.Fatalf("TOP 1 5: expected +OK, got: %s", topFirst)
	}
	topLines := c.readMulti()
	topBody := strings.Join(topLines, "\n")
	if !strings.Contains(topBody, "Subject:") {
		t.Errorf("TOP: expected headers, got: %s", topBody[:min(200, len(topBody))])
	}

	c.send("QUIT")
	c.expectOK()
	c.close()
}

func TestPOP3DeleteAndQuit(t *testing.T) {
	a, cancel := startPOP3Agent(t)
	defer cancel()

	pop3Addr := a.POP3.PlainAddr()
	email := "pop3dele@localhost"
	createPOP3User(t, a, email, "testpass")

	deliverTestEmail(t, a.SMTP.MXAddr(), email, "POP3-Delete-Test")

	// Session 1: Delete and quit.
	c := dialPOP3(t, pop3Addr)
	c.login(email, "testpass")

	c.send("STAT")
	stat := c.expectOK()
	if !strings.Contains(stat, "1 ") {
		t.Fatalf("STAT expected 1 message, got: %s", stat)
	}

	c.send("DELE 1")
	c.expectOK()

	// RFC 1939 section 5: a message marked deleted is not counted by STAT, so
	// this must report 0 even though nothing has been committed yet. The
	// deletion is only made permanent when the session enters UPDATE at QUIT,
	// which the RSET check below relies on.
	//
	// This assertion used to be a t.Logf, so the behaviour was never checked --
	// and the comment above it claimed the opposite, that STAT "still shows 1".
	// The implementation was right (activeStats skips deleted messages) and the
	// comment was wrong, which is exactly the sort of thing an unasserted log
	// line hides.
	c.send("STAT")
	stat = c.expectOK()
	if !strings.HasPrefix(stat, "+OK 0 ") {
		t.Errorf("STAT after DELE = %q, want 0 messages: RFC 1939 excludes messages "+
			"marked deleted from STAT", stat)
	}

	// RSET clears the deletion marks, so the message is countable again. This
	// is what proves DELE had not actually removed anything yet.
	c.send("RSET")
	c.expectOK()
	c.send("STAT")
	stat = c.expectOK()
	if !strings.HasPrefix(stat, "+OK 1 ") {
		t.Errorf("STAT after RSET = %q, want 1 message: RSET must unmark deletions, "+
			"and DELE must not have removed the message before QUIT", stat)
	}

	// Re-mark it so the rest of the test exercises the commit path.
	c.send("DELE 1")
	c.expectOK()

	c.send("QUIT")
	c.expectOK()
	c.close()

	// Wait for move to propagate.
	time.Sleep(1 * time.Second)

	// Session 2: Verify message is gone.
	c = dialPOP3(t, pop3Addr)
	c.login(email, "testpass")

	c.send("STAT")
	stat = c.expectOK()
	if !strings.HasPrefix(stat, "+OK 0 ") {
		t.Errorf("STAT expected 0 messages after delete, got: %s", stat)
	}

	c.send("QUIT")
	c.expectOK()
	c.close()
}

func TestPOP3STLS(t *testing.T) {
	a, cancel := startPOP3Agent(t)
	defer cancel()

	pop3Addr := a.POP3.PlainAddr()
	email := "pop3stls@localhost"
	createPOP3User(t, a, email, "testpass")

	c := dialPOP3(t, pop3Addr)

	// CAPA should list STLS.
	c.send("CAPA")
	capaFirst := c.readLine()
	if !strings.HasPrefix(capaFirst, "+OK") {
		t.Fatalf("CAPA: expected +OK, got: %s", capaFirst)
	}
	capaLines := c.readMulti()
	capaBody := strings.Join(capaLines, "\n")
	if !strings.Contains(capaBody, "STLS") {
		t.Fatalf("CAPA: expected STLS, got: %s", capaBody)
	}

	// STLS upgrade.
	c.send("STLS")
	c.expectOK()

	// Upgrade the connection to TLS.
	tlsConn := tls.Client(c.conn, &tls.Config{InsecureSkipVerify: true})
	if err := tlsConn.Handshake(); err != nil {
		t.Fatalf("TLS handshake: %v", err)
	}
	c.conn = tlsConn
	c.reader = bufio.NewReader(tlsConn)

	// CAPA over TLS should NOT list STLS.
	c.send("CAPA")
	capaFirst = c.readLine()
	if !strings.HasPrefix(capaFirst, "+OK") {
		t.Fatalf("CAPA over TLS: expected +OK, got: %s", capaFirst)
	}
	capaLines = c.readMulti()
	for _, line := range capaLines {
		if line == "STLS" {
			t.Error("CAPA over TLS should not list STLS")
		}
	}

	// Auth should work over TLS.
	c.login(email, "testpass")

	c.send("STAT")
	c.expectOK()

	c.send("QUIT")
	c.expectOK()
	c.close()
}
