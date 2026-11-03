package icalevent

import (
	"testing"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func TestInviteRelevantChange(t *testing.T) {
	base := &bongov1.Event{
		Title: "Planning", Description: "line one\nline two", Location: "Room 1", StartTime: 1000, EndTime: 2000,
		Rrule:     "FREQ=WEEKLY",
		Attendees: []*bongov1.Attendee{{Address: "bob@d.test"}, {Address: "carol@d.test"}},
	}
	for _, tc := range []struct {
		name string
		edit func(e *bongov1.Event)
		want bool
	}{
		{"identical", func(*bongov1.Event) {}, false},
		{"start", func(e *bongov1.Event) { e.StartTime++ }, true},
		{"end", func(e *bongov1.Event) { e.EndTime++ }, true},
		{"all day", func(e *bongov1.Event) { e.AllDay = true }, true},
		{"location", func(e *bongov1.Event) { e.Location = "Room 2" }, true},
		{"rrule", func(e *bongov1.Event) { e.Rrule = "FREQ=DAILY" }, true},
		{"title only", func(e *bongov1.Event) { e.Title = "Renamed" }, true},
		{"description only", func(e *bongov1.Event) { e.Description = "other" }, true},
		{"description CRLF against LF", func(e *bongov1.Event) { e.Description = "line one\r\nline two" }, false},
		{"description CR against LF", func(e *bongov1.Event) { e.Description = "line one\rline two" }, false},
		{"attendee case", func(e *bongov1.Event) { e.Attendees[0].Address = "BOB@d.test" }, false},
		{"attendee added", func(e *bongov1.Event) { e.Attendees = append(e.Attendees, &bongov1.Attendee{Address: "dave@d.test"}) }, true},
		{"attendee removed", func(e *bongov1.Event) { e.Attendees = e.Attendees[:1] }, true},
		{"attendee swapped", func(e *bongov1.Event) { e.Attendees[1].Address = "erin@d.test" }, true},
		{"raw only", func(e *bongov1.Event) { e.IcalRaw = []byte("x") }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			after := proto.Clone(base).(*bongov1.Event)
			tc.edit(after)
			if got := InviteRelevantChange(base, after); got != tc.want {
				t.Errorf("InviteRelevantChange = %v, want %v", got, tc.want)
			}
		})
	}
}
