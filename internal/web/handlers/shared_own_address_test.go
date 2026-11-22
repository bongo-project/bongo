package handlers

import (
	"errors"
	"fmt"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/icalevent"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Membership in a shared inbox widens sending only. The shared address is not
// one of the member's own addresses, so reply-all keeps it as a recipient and a
// message from it is not "the user's own message".
func TestReplyAllKeepsSharedAddress(t *testing.T) {
	env, _ := newSharedComposeEnv(t)
	req := httptest.NewRequest("GET", "/compose/reply/INBOX/1?all=1", nil)

	meta := &bongov1.MailMeta{From: "customer@external.example", To: []string{sharedAddr, "member@example.com"}, Cc: []string{"colleague@external.example"}}
	to, cc := env.handler.replyRecipients(req, sharedMemberID, "member@example.com", meta, nil)
	if !strings.Contains(to, "customer@external.example") {
		t.Errorf("To = %q", to)
	}
	if !slices.Contains(strings.Split(cc, ", "), sharedAddr) {
		t.Errorf("reply-all Cc = %q, want it to keep %s", cc, sharedAddr)
	}
	if strings.Contains(cc, "member@example.com") {
		t.Errorf("reply-all Cc = %q lists the member's own address", cc)
	}

	// A message sent from the shared address is answered to the shared address.
	meta = &bongov1.MailMeta{From: sharedAddr, To: []string{"customer@external.example"}}
	to, _ = env.handler.replyRecipients(httptest.NewRequest("GET", "/compose/reply/Sent/1", nil), sharedMemberID, "member@example.com", meta, nil)
	if !strings.Contains(to, sharedAddr) {
		t.Errorf("reply to a message from the shared address: To = %q, want %s", to, sharedAddr)
	}
}

// Inviting the shared address from a member's event queues an invitation for
// it: it is not the organiser's own address.
func TestInviteToSharedAddressFromMember(t *testing.T) {
	env, _ := newSharedComposeEnv(t)
	h := &CalendarHandler{addresses: env.addresses}
	atts := []*bongov1.Attendee{{Address: sharedAddr}, {Address: "member@example.com"}, {Address: "x@external.example"}}
	got := h.inviteRecipients(sharedMemberID, "member@example.com", "member@example.com", atts, nil)
	if !slices.Contains(got, sharedAddr) || !slices.Contains(got, "x@external.example") || len(got) != 2 {
		t.Fatalf("invitation recipients = %v, want the shared address and the external one", got)
	}
	// The attendee cap counts the shared address as a recipient too.
	var many []*bongov1.Attendee
	for i := range icalevent.MaxAttendees {
		many = append(many, &bongov1.Attendee{Address: fmt.Sprintf("a%d@external.example", i)})
	}
	many = append(many, &bongov1.Attendee{Address: sharedAddr})
	if err := h.checkAttendeeCap(sharedMemberID, "member@example.com", many, nil); !errors.Is(err, errAttendeesTooMany) {
		t.Fatalf("cap error = %v, want errAttendeesTooMany", err)
	}
}
