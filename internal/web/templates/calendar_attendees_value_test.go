package templates

import (
	"net/mail"
	"strings"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// The attendees field is plain text the user edits, so a non-ASCII name must
// show as typed (no RFC 2047 encoding) and every entry must parse back to the
// same name and address, which is what the form handler does with it.
func TestEventAttendeesValueRoundTrips(t *testing.T) {
	attendees := []*bongov1.Attendee{
		{Address: "jm@example.com", Name: "Jürgen Müller"},
		{Address: "doe@example.com", Name: "Doe, Jane"},
		{Address: "q@example.com", Name: `Say "hi" \ there`},
		{Address: "dot@example.com", Name: "J. Smith"},
		{Address: "plain@example.com", Name: "Plain Name"},
		{Address: "bare@example.com"},
	}
	got := eventAttendeesValue(&bongov1.Event{Attendees: attendees})
	if strings.Contains(got, "=?") {
		t.Errorf("name is RFC 2047-encoded: %s", got)
	}
	if !strings.Contains(got, "Jürgen Müller <jm@example.com>") {
		t.Errorf("non-ASCII name not shown as typed: %s", got)
	}
	list, err := mail.ParseAddressList(got)
	if err != nil {
		t.Fatalf("does not parse back: %v\n%s", err, got)
	}
	if len(list) != len(attendees) {
		t.Fatalf("parsed %d entries, want %d: %s", len(list), len(attendees), got)
	}
	for i, a := range attendees {
		if list[i].Address != a.Address || list[i].Name != a.Name {
			t.Errorf("entry %d: got %q <%s>, want %q <%s>", i, list[i].Name, list[i].Address, a.Name, a.Address)
		}
	}
}
