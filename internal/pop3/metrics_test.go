package pop3

import (
	"io"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/observability/obstest"
)

var pop3Labels = map[string]string{"protocol": "pop3"}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestPOP3Metrics runs a session against the real server and checks the
// connection gauge and counter, that a connection refused by the per-IP cap is
// not counted, and that known and unknown commands land under their labels.
func TestPOP3Metrics(t *testing.T) {
	plain, _ := startLimitedServer(t, 100, 1)

	open0 := obstest.Gauge(t, "bongo_protocol_connections", pop3Labels)
	acc0 := obstest.Counter(t, "bongo_protocol_connections_accepted_total", pop3Labels)
	stat0 := obstest.Command(t, "pop3", "STAT")
	other0 := obstest.Command(t, "pop3", "other")
	quit0 := obstest.Command(t, "pop3", "QUIT")

	c1 := dialAs(t, plain, clientA, false)
	c1.wantGreeting(t, "+OK")
	if got := obstest.Gauge(t, "bongo_protocol_connections", pop3Labels) - open0; got != 1 {
		t.Errorf("open delta = %v while connected, want 1", got)
	}
	if got := obstest.Counter(t, "bongo_protocol_connections_accepted_total", pop3Labels) - acc0; got != 1 {
		t.Errorf("accepted delta = %v, want 1", got)
	}

	// Same source IP over the cap of one: refused, so it must not count.
	c2 := dialAs(t, plain, clientA, false)
	c2.wantRefused(t)
	if got := obstest.Gauge(t, "bongo_protocol_connections", pop3Labels) - open0; got != 1 {
		t.Errorf("open delta = %v after a refused connection, want 1", got)
	}
	if got := obstest.Counter(t, "bongo_protocol_connections_accepted_total", pop3Labels) - acc0; got != 1 {
		t.Errorf("accepted delta = %v after a refused connection, want 1", got)
	}

	for _, cmd := range []string{"STAT", "XYZZY secret-looking-argument"} {
		if _, err := io.WriteString(c1, cmd+"\r\n"); err != nil {
			t.Fatal(err)
		}
		if _, ok := c1.line(5 * time.Second); !ok {
			t.Fatalf("no reply to %q", cmd)
		}
	}
	if got := obstest.Command(t, "pop3", "STAT") - stat0; got != 1 {
		t.Errorf("STAT samples +%d, want +1", got)
	}
	if got := obstest.Command(t, "pop3", "other") - other0; got != 1 {
		t.Errorf("other samples +%d, want +1 for an unknown command", got)
	}

	if _, err := io.WriteString(c1, "QUIT\r\n"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "connection gauge to fall", func() bool {
		return obstest.Gauge(t, "bongo_protocol_connections", pop3Labels) == open0
	})
	if got := obstest.Command(t, "pop3", "QUIT") - quit0; got != 1 {
		t.Errorf("QUIT samples +%d, want +1", got)
	}
}
