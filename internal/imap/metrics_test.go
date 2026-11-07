package imap

import (
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"

	"github.com/bongo-project/bongo/internal/observability"
	"github.com/bongo-project/bongo/internal/observability/obstest"
)

var imapLabels = map[string]string{"protocol": "imap"}

// TestIMAPMetrics drives the real server: the connection gauge and counter
// move with a session, a FETCH is observed under "fetch", and an IDLE is not
// observed at all (it blocks until DONE and would swamp the histogram).
func TestIMAPMetrics(t *testing.T) {
	open0 := obstest.Gauge(t, "bongo_protocol_connections", imapLabels)
	acc0 := obstest.Counter(t, "bongo_protocol_connections_accepted_total", imapLabels)
	login0 := obstest.Command(t, "imap", "login")
	sel0 := obstest.Command(t, "imap", "select")
	fetch0 := obstest.Command(t, "imap", "fetch")

	ms, c, _ := selectWithOneMessage(t, "INBOX")

	if got := obstest.Gauge(t, "bongo_protocol_connections", imapLabels) - open0; got != 1 {
		t.Errorf("open delta = %v while connected, want 1", got)
	}
	if got := obstest.Counter(t, "bongo_protocol_connections_accepted_total", imapLabels) - acc0; got != 1 {
		t.Errorf("accepted delta = %v, want 1", got)
	}
	if got := obstest.Command(t, "imap", "login") - login0; got != 1 {
		t.Errorf("login samples +%d, want +1", got)
	}
	if got := obstest.Command(t, "imap", "select") - sel0; got != 1 {
		t.Errorf("select samples +%d, want +1", got)
	}

	seq := imap.SeqSet{}
	seq.AddNum(1)
	if _, err := c.Fetch(seq, &imap.FetchOptions{Flags: true}).Collect(); err != nil {
		t.Fatalf("FETCH: %v", err)
	}
	if got := obstest.Command(t, "imap", "fetch") - fetch0; got != 1 {
		t.Errorf("fetch samples +%d, want +1", got)
	}

	// IDLE: start, let the server enter it, then end it with DONE. Nothing may
	// be recorded for it, under any label.
	idle, err := c.Idle()
	if err != nil {
		t.Fatalf("IDLE: %v", err)
	}
	deliverPollTestMessage(t, ms, "INBOX", "guid-idle")
	time.Sleep(100 * time.Millisecond)
	if err := idle.Close(); err != nil {
		t.Fatalf("ending IDLE: %v", err)
	}
	if err := idle.Wait(); err != nil {
		t.Fatalf("waiting for IDLE: %v", err)
	}
	families, err := observability.Registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range families {
		if mf.GetName() != "bongo_protocol_command_seconds" {
			continue
		}
		for _, m := range mf.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "command" && l.GetValue() == "idle" {
					t.Errorf("an idle series exists: IDLE must not be timed")
				}
			}
		}
	}

	if err := c.Logout().Wait(); err != nil {
		t.Fatalf("LOGOUT: %v", err)
	}
	_ = c.Close()
	deadline := time.Now().Add(5 * time.Second)
	for obstest.Gauge(t, "bongo_protocol_connections", imapLabels) != open0 {
		if time.Now().After(deadline) {
			t.Fatalf("open gauge delta = %v after logout, want 0",
				obstest.Gauge(t, "bongo_protocol_connections", imapLabels)-open0)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
