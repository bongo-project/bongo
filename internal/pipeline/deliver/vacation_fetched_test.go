package deliver

import (
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Mail collected from a remote account has the null envelope sender and no
// authenticated user, so a vacation reply is never sent for it, even when the
// message is addressed to the user's own address. A reply would go to whoever
// the From header names, off the back of a mailbox the user merely fetched.
// The guard is the null-sender rule (not "addressed to someone else").
func TestFetchedMailNoVacationReply(t *testing.T) {
	h := newVacHarness(t)
	env := &bongov1.QueueEnvelope{
		Recipients:   []string{"alice@example.com"},
		FetchAccount: "0123456789abcdef0123456789abcdef",
	}
	raw := "From: bob@example.net\r\nTo: alice@example.com\r\nSubject: Lunch?\r\nMessage-ID: <orig-1@example.net>\r\n" +
		"Date: Mon, 01 Jan 2024 12:00:00 +0000\r\n\r\nHello\r\n"
	if err := h.send(env, raw); err != nil {
		t.Fatal(err)
	}
	if n := len(h.autoReplies()); n != 0 {
		t.Fatalf("auto replies = %d, want 0 for fetched mail", n)
	}
	// The same message from the wire with an authenticated sender does reply:
	// the harness is live, so the zero above is the null sender and not setup.
	wire, wraw := extMsg("bob@example.net")
	if err := h.send(wire, wraw); err != nil {
		t.Fatal(err)
	}
	if n := len(h.autoReplies()); n != 1 {
		t.Fatalf("control: auto replies = %d, want 1", n)
	}
}
