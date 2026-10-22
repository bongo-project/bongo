//go:build integration

package integration

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2/imapclient"
	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/agent"
)

// setProfileDisabled rewrites the user's profile through Raft, as the admin
// "toggle disabled" action does, then forces the derived SQLite flag back to 1
// so that only the Pebble profile can be what refuses the account.
func setProfileDisabled(t *testing.T, a *agent.Agent, userID string, disabled bool) {
	t.Helper()
	key := []byte("/user/" + userID + "/profile")
	data, err := a.GetKey(key)
	if err != nil {
		t.Fatalf("read profile: %v", err)
	}
	var p bongov1.UserProfile
	if err := proto.Unmarshal(data, &p); err != nil {
		t.Fatal(err)
	}
	p.Disabled = disabled
	data, err = proto.Marshal(&p)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.ApplyCommand(&bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: key, Value: data}, 5*time.Second); err != nil {
		t.Fatalf("apply profile: %v", err)
	}
	if !a.Raft.FSM.WaitIdle(10 * time.Second) {
		t.Fatal("derived indexes did not settle")
	}
	if _, err := a.Metadata.System.DB().Exec("UPDATE users SET enabled = 1 WHERE id = ?", userID); err != nil {
		t.Fatal(err)
	}
}

func TestIMAPSessionEndsWhenAccountDisabled(t *testing.T) {
	a, cancel := startPOP3Agent(t)
	defer cancel()

	email := "imaprevoke@localhost"
	userID := createPOP3User(t, a, email, "testpass")

	client, err := imapclient.DialInsecure(a.IMAP.PlainAddr(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.Login(email, "testpass").Wait(); err != nil {
		t.Fatalf("LOGIN: %v", err)
	}
	if _, err := client.Select("INBOX", nil).Wait(); err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	if err := client.Noop().Wait(); err != nil {
		t.Fatalf("NOOP before disable: %v", err)
	}

	setProfileDisabled(t, a, userID, true)

	if err := client.Noop().Wait(); err == nil {
		t.Fatal("NOOP succeeded after the account was disabled")
	}
	if err := client.Noop().Wait(); err == nil {
		t.Fatal("connection still usable after the account was disabled")
	}

	// A fresh login is refused too, even though SQLite still says enabled=1.
	c2, err := imapclient.DialInsecure(a.IMAP.PlainAddr(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	if err := c2.Login(email, "testpass").Wait(); err == nil {
		t.Fatal("LOGIN succeeded for a disabled account")
	}
}

// A revoked IMAP session must be told so (untagged BYE) and then dropped, not
// merely answered with a tagged NO on every later command. Driven over a raw
// socket because imapclient hides both the BYE and the socket state.
func TestRevokedIMAPSessionSendsByeAndCloses(t *testing.T) {
	a, cancel := startPOP3Agent(t)
	defer cancel()

	email := "imapbye@localhost"
	userID := createPOP3User(t, a, email, "testpass")

	conn, err := net.DialTimeout("tcp", a.IMAP.PlainAddr(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	r := bufio.NewReader(conn)
	conn.SetDeadline(time.Now().Add(10 * time.Second))

	if _, err := r.ReadString('\n'); err != nil {
		t.Fatalf("greeting: %v", err)
	}
	// command sends one tagged command and returns every line up to and
	// including its tagged completion.
	command := func(tag, cmd string) []string {
		t.Helper()
		if _, err := fmt.Fprintf(conn, "%s %s\r\n", tag, cmd); err != nil {
			t.Fatal(err)
		}
		var lines []string
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				t.Fatalf("%s %s: read: %v (lines so far %q)", tag, cmd, err, lines)
			}
			lines = append(lines, line)
			if strings.HasPrefix(line, tag+" ") {
				return lines
			}
		}
	}
	if got := command("a1", `LOGIN "`+email+`" "testpass"`); !strings.HasPrefix(got[len(got)-1], "a1 OK") {
		t.Fatalf("LOGIN = %q", got)
	}
	if got := command("a2", "NOOP"); !strings.HasPrefix(got[len(got)-1], "a2 OK") {
		t.Fatalf("NOOP before disable = %q", got)
	}

	setProfileDisabled(t, a, userID, true)

	if _, err := fmt.Fprintf(conn, "a3 NOOP\r\n"); err != nil {
		t.Fatal(err)
	}
	var sawBye bool
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			break // EOF: the server closed the connection.
		}
		if strings.HasPrefix(line, "* BYE") {
			sawBye = true
		}
		if strings.HasPrefix(line, "a3 OK") {
			t.Fatalf("NOOP succeeded after the account was disabled: %q", line)
		}
	}
	if !sawBye {
		t.Fatal("server closed the revoked session without an untagged BYE")
	}
	// The read loop above ended on an error; it must be EOF, not our deadline.
	if _, err := r.ReadString('\n'); err == nil {
		t.Fatal("connection still readable after BYE")
	} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatal("connection left open after revocation (read timed out)")
	}
}

func TestPOP3SessionEndsWhenAccountDisabled(t *testing.T) {
	a, cancel := startPOP3Agent(t)
	defer cancel()

	email := "pop3revoke@localhost"
	userID := createPOP3User(t, a, email, "testpass")
	deliverTestEmail(t, a.SMTP.MXAddr(), email, "POP3-Revoke-Test")

	c := dialPOP3(t, a.POP3.PlainAddr())
	c.login(email, "testpass")
	c.send("STAT")
	if line := c.expectOK(); !strings.HasPrefix(line, "+OK 1 ") {
		t.Fatalf("STAT = %q, want 1 message", line)
	}
	c.send("DELE 1")
	c.expectOK()

	setProfileDisabled(t, a, userID, true)

	c.send("STAT")
	c.expectERR()
	// The server has closed the connection.
	c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.reader.ReadString('\n'); err == nil {
		t.Fatal("connection still open after revocation")
	}
	c.close()

	// The pending DELE must not have been applied for a revoked account.
	setProfileDisabled(t, a, userID, false)
	time.Sleep(500 * time.Millisecond)
	c = dialPOP3(t, a.POP3.PlainAddr())
	c.login(email, "testpass")
	c.send("STAT")
	if line := c.expectOK(); !strings.HasPrefix(line, "+OK 1 ") {
		t.Fatalf("STAT after re-enable = %q, want the message to survive", line)
	}
	c.send("QUIT")
	c.expectOK()
	c.close()
}
