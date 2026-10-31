//go:build integration

package integration

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/sendmail"
	"github.com/bongo-project/bongo/internal/store"
)

// TestSendmailAgainstDevServer submits through the real implicit-TLS listener
// of a dev agent with the real client code.
func TestSendmailAgainstDevServer(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	cfg.Server.BindAddr = "127.0.0.1"
	sharedAssignFreePorts(t, cfg)
	cfg.SMTP.Enabled = true

	a, cancel, _ := sharedStartAgent(t, cfg)
	defer cancel()

	ids := map[string]string{}
	for _, addr := range []string{"alice@localhost", "bob@localhost", "carol@localhost"} {
		id, err := auth.CreateUser(a, a.Metadata, addr, addr, "pass-"+addr, false)
		if err != nil {
			t.Fatalf("CreateUser %s: %v", addr, err)
		}
		ids[addr] = id
	}
	resolver := store.NewRecipientResolver(a.AddressStore, a.Store, a.Metadata.System)
	deadline := time.Now().Add(10 * time.Second)
	for addr, id := range ids {
		for {
			if got, err := resolver.ResolveRecipient(addr); err == nil && got == id {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s did not resolve", addr)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	subHost, subPort, err := net.SplitHostPort(a.SMTP.SubmissionsAddr())
	if err != nil {
		t.Fatal(err)
	}
	plainHost, plainPort, err := net.SplitHostPort(a.SMTP.SubmissionAddr())
	if err != nil {
		t.Fatal(err)
	}

	// The dev certificate is self-signed and names the banner host, not
	// 127.0.0.1, so verification is skipped; the client allows that on loopback.
	writeConf := func(t *testing.T, host, port, tlsMode, password string, mode os.FileMode) string {
		t.Helper()
		body := fmt.Sprintf("host = %q\nport = %s\ntls = %q\nusername = \"alice@localhost\"\npassword = %q\ninsecure_skip_verify = true\n",
			host, port, tlsMode, password)
		p := filepath.Join(t.TempDir(), "sendmail.conf")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	good := writeConf(t, subHost, subPort, "implicit", "pass-alice@localhost", 0o600)
	send := func(t *testing.T, stdin string, args ...string) (int, string) {
		t.Helper()
		var stderr bytes.Buffer
		code := sendmail.Run(args, strings.NewReader(stdin), &stderr)
		return code, stderr.String()
	}
	waitInbox := func(t *testing.T, addr string, want int) {
		t.Helper()
		end := time.Now().Add(10 * time.Second)
		for {
			msgs, _, _ := a.Mailboxes.ListMessagesPaged(ids[addr], "INBOX", 0, 10)
			if len(msgs) == want {
				return
			}
			if time.Now().After(end) {
				t.Fatalf("%s INBOX has %d messages, want %d", addr, len(msgs), want)
			}
			time.Sleep(25 * time.Millisecond)
		}
	}

	t.Run("-t delivers to To and Bcc", func(t *testing.T) {
		msg := "To: bob@localhost\nBcc: carol@localhost\nSubject: sendmail test\n\nhello\n.dot line\n"
		if code, stderr := send(t, msg, "--config", good, "-t", "-i"); code != 0 {
			t.Fatalf("exit %d: %s", code, stderr)
		}
		waitInbox(t, "bob@localhost", 1)
		waitInbox(t, "carol@localhost", 1)
	})

	t.Run("cron style unqualified sender and From", func(t *testing.T) {
		msg := "From: root (Cron Daemon)\nTo: bob@localhost\nSubject: Cron\n\noutput\n"
		code, stderr := send(t, msg, "--config", good, "-FCronDaemon", "-f", "root", "-i", "-B8BITMIME", "-oem", "-oi", "bob@localhost")
		if code != 0 {
			t.Fatalf("exit %d: %s", code, stderr)
		}
		waitInbox(t, "bob@localhost", 2)
	})

	t.Run("send-as refusal is 77 with the server text", func(t *testing.T) {
		code, stderr := send(t, "To: bob@localhost\n\nx\n", "--config", good, "-t", "-f", "someone-else@example.net")
		if code != sendmail.ExitNoPerm {
			t.Fatalf("exit %d, want 77: %s", code, stderr)
		}
		if !strings.Contains(stderr, "not owned") {
			t.Errorf("server text missing: %q", stderr)
		}
	})

	t.Run("foreign From header is 77", func(t *testing.T) {
		code, stderr := send(t, "From: someone-else@example.net\nTo: bob@localhost\n\nx\n", "--config", good, "-t")
		if code != sendmail.ExitNoPerm {
			t.Fatalf("exit %d, want 77: %s", code, stderr)
		}
	})

	t.Run("wrong password is 77 and not echoed", func(t *testing.T) {
		conf := writeConf(t, subHost, subPort, "implicit", "totally-wrong-pw", 0o600)
		code, stderr := send(t, "To: bob@localhost\n\nx\n", "--config", conf, "-t")
		if code != sendmail.ExitNoPerm {
			t.Fatalf("exit %d, want 77: %s", code, stderr)
		}
		if strings.Contains(stderr, "totally-wrong-pw") {
			t.Errorf("password echoed: %q", stderr)
		}
	})

	t.Run("group-readable config is 78", func(t *testing.T) {
		conf := writeConf(t, subHost, subPort, "implicit", "pass-alice@localhost", 0o640)
		if code, stderr := send(t, "To: bob@localhost\n\nx\n", "--config", conf, "-t"); code != sendmail.ExitConfig {
			t.Fatalf("exit %d, want 78: %s", code, stderr)
		}
	})

	t.Run("starttls against a listener without it is 69, never plaintext AUTH", func(t *testing.T) {
		conf := writeConf(t, plainHost, plainPort, "starttls", "pass-alice@localhost", 0o600)
		code, stderr := send(t, "To: bob@localhost\n\nx\n", "--config", conf, "-t")
		if code != sendmail.ExitUnavailable {
			t.Fatalf("exit %d, want 69: %s", code, stderr)
		}
	})
}
