package handlers

import (
	"fmt"
	"strings"
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// invitations is the HTML of the invitations section.
func (e *summaryEnv) invitations(t *testing.T) string {
	t.Helper()
	return section(t, e.page(t), "invitations")
}

// listed reports whether the invitations section shows title. Every test that
// asserts an invitation is absent first proves, with the same input minus the
// one thing under test, that it would otherwise be listed.
func (e *summaryEnv) listed(t *testing.T, title string) bool {
	t.Helper()
	return strings.Contains(e.invitations(t), title)
}

func TestSummaryInvitationAnsweredIsExcluded(t *testing.T) {
	e := newSummaryEnv(t)
	o := icsOpts{uid: "answered@example.org", summary: "Answered meeting"}
	e.place(t, quotaUser, o.build("REQUEST"))
	// The message the card answers from; the schedule inbox holds the same
	// object.
	uid := e.invite(t, o)
	if !e.listed(t, "Answered meeting") {
		t.Fatal("control: an unanswered REQUEST is not listed")
	}
	if w := e.answer(t, uid, "ACCEPTED"); w.Code >= 400 {
		t.Fatalf("answer = %d: %s", w.Code, w.Body)
	}
	if e.listed(t, "Answered meeting") {
		t.Error("an accepted invitation is still listed")
	}
}

func TestSummaryInvitationTentativeAndDeclinedAreExcluded(t *testing.T) {
	for _, partstat := range []string{"TENTATIVE", "DECLINED"} {
		t.Run(partstat, func(t *testing.T) {
			e := newSummaryEnv(t)
			o := icsOpts{uid: "ans-" + strings.ToLower(partstat) + "@example.org", summary: "Meeting " + partstat}
			e.place(t, quotaUser, o.build("REQUEST"))
			uid := e.invite(t, o)
			if !e.listed(t, "Meeting "+partstat) {
				t.Fatal("control: not listed before the answer")
			}
			e.answer(t, uid, partstat)
			if e.listed(t, "Meeting "+partstat) {
				t.Errorf("a %s invitation is still listed", partstat)
			}
		})
	}
}

func TestSummaryInvitationSelfOrganisedIsExcluded(t *testing.T) {
	e := newSummaryEnv(t)
	e.place(t, quotaUser, icsOpts{uid: "ctl@example.org", summary: "Control meeting"}.build("REQUEST"))
	if !e.listed(t, "Control meeting") {
		t.Fatal("control: not listed")
	}
	e.place(t, quotaUser, icsOpts{uid: "self@example.org", summary: "My own meeting", organiser: quotaEmail}.build("REQUEST"))
	if e.listed(t, "My own meeting") {
		t.Error("an invitation the user organised themselves is listed")
	}
}

func TestSummaryInvitationNotAddressedToUserIsExcluded(t *testing.T) {
	e := newSummaryEnv(t)
	e.place(t, quotaUser, icsOpts{uid: "ctl@example.org", summary: "Control meeting"}.build("REQUEST"))
	e.place(t, quotaUser, icsOpts{uid: "other@example.org", summary: "Meeting for Dave", attendee: "dave@example.org"}.build("REQUEST"))
	if !e.listed(t, "Control meeting") || e.listed(t, "Meeting for Dave") {
		t.Errorf("section: %s", e.invitations(t))
	}
}

func TestSummaryInvitationCancelledAndEndedAreExcluded(t *testing.T) {
	e := newSummaryEnv(t)
	e.place(t, quotaUser, icsOpts{uid: "ctl@example.org", summary: "Control meeting"}.build("REQUEST"))
	e.place(t, quotaUser, icsOpts{uid: "gone@example.org", summary: "Cancelled meeting"}.build("REQUEST"))
	e.place(t, quotaUser, icsOpts{uid: "gone@example.org", summary: "Cancelled meeting", seq: 1}.build("CANCEL"))
	e.place(t, quotaUser, icsOpts{uid: "past@example.org", summary: "Past meeting", start: "20200101T090000Z", end: "20200101T100000Z"}.build("REQUEST"))
	got := e.invitations(t)
	if !strings.Contains(got, "Control meeting") {
		t.Fatalf("control missing: %s", got)
	}
	for _, not := range []string{"Cancelled meeting", "Past meeting"} {
		if strings.Contains(got, not) {
			t.Errorf("%q is listed", not)
		}
	}
}

func TestSummaryInvitationKeepsTheHighestSequence(t *testing.T) {
	// The SEQUENCE decides, whichever item arrived last: a delayed older
	// message must not hide the update, and a first-seen rule must not either.
	for _, order := range []string{"update first", "update last"} {
		t.Run(order, func(t *testing.T) {
			e := newSummaryEnv(t)
			update := icsOpts{uid: "upd@example.org", summary: "Updated title", seq: 2}.build("REQUEST")
			original := icsOpts{uid: "upd@example.org", summary: "Original title", seq: 0}.build("REQUEST")
			if order == "update first" {
				e.place(t, quotaUser, update)
				e.place(t, quotaUser, original)
			} else {
				e.place(t, quotaUser, original)
				e.place(t, quotaUser, update)
			}
			got := e.invitations(t)
			if rowCount(got) != 1 || !strings.Contains(got, "Updated title") || strings.Contains(got, "Original title") {
				t.Errorf("section: %s", got)
			}
		})
	}
}

func TestSummaryInvitationFromOwnCalendar(t *testing.T) {
	e := newSummaryEnv(t)
	soon := summaryNow.Add(72 * time.Hour).Unix()
	me := func(partstat string) []*bongov1.Attendee {
		return []*bongov1.Attendee{{Address: quotaEmail, Partstat: partstat}}
	}
	e.event(t, quotaUser, &bongov1.Event{Guid: "cal-needs", Title: "Needs an answer", StartTime: soon, Organizer: "olive@example.org", Attendees: me("NEEDS-ACTION")})
	e.event(t, quotaUser, &bongov1.Event{Guid: "cal-accepted", Title: "Accepted already", StartTime: soon, Organizer: "olive@example.org", Attendees: me("ACCEPTED")})
	e.event(t, quotaUser, &bongov1.Event{Guid: "cal-mine", Title: "Organised by me", StartTime: soon, Organizer: quotaEmail, Attendees: me("NEEDS-ACTION")})
	e.event(t, quotaUser, &bongov1.Event{Guid: "cal-cancelled", Title: "Cancelled event", StartTime: soon, Organizer: "olive@example.org", Status: "CANCELLED", Attendees: me("NEEDS-ACTION")})
	e.event(t, quotaUser, &bongov1.Event{Guid: "cal-far", Title: "Far away", StartTime: summaryNow.Add(90 * 24 * time.Hour).Unix(), Organizer: "olive@example.org", Attendees: me("NEEDS-ACTION")})
	got := e.invitations(t)
	if !strings.Contains(got, "Needs an answer") {
		t.Fatalf("an unanswered invitation in the calendar is not listed: %s", got)
	}
	for _, not := range []string{"Accepted already", "Organised by me", "Cancelled event", "Far away"} {
		if strings.Contains(got, not) {
			t.Errorf("%q is listed", not)
		}
	}
}

func TestSummaryInvitationInInboxAndCalendarIsListedOnce(t *testing.T) {
	e := newSummaryEnv(t)
	e.place(t, quotaUser, icsOpts{uid: "both", summary: "Both places"}.build("REQUEST"))
	e.event(t, quotaUser, &bongov1.Event{
		Guid: "both", Title: "Both places", StartTime: summaryNow.Add(72 * time.Hour).Unix(), Organizer: imipOrganiser,
		Attendees: []*bongov1.Attendee{{Address: quotaEmail, Partstat: "NEEDS-ACTION"}},
	})
	if n := rowCount(e.invitations(t)); n != 1 {
		t.Errorf("%d rows, want 1", n)
	}
}

func TestSummaryInvitationLinksToTheMessage(t *testing.T) {
	now := time.Now().Unix()
	day := "/calendar/day/2030/5/6"
	cases := []struct {
		name     string
		messages []mailSeed
		wantUID  bool // the single message's link
		wantDay  bool
	}{
		{"one message from the organiser", []mailSeed{{from: "Olive <" + imipOrganiser + ">", at: now}}, true, false},
		{"two messages are ambiguous", []mailSeed{{from: imipOrganiser, at: now}, {from: imipOrganiser, at: now + 5}}, false, true},
		{"another sender", []mailSeed{{from: "mallory@example.net", at: now}}, false, true},
		{"delivered an hour earlier", []mailSeed{{from: imipOrganiser, at: now - 3600}}, false, true},
		{"no message", nil, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newSummaryEnv(t)
			var uids []uint32
			for _, m := range tc.messages {
				m.subject = "Invitation"
				uids = append(uids, e.mail(t, m))
			}
			e.place(t, quotaUser, icsOpts{uid: "link@example.org", summary: "Linked meeting"}.build("REQUEST"))
			got := e.invitations(t)
			if !strings.Contains(got, "Linked meeting") {
				t.Fatalf("not listed: %s", got)
			}
			msgLink := ""
			if len(uids) == 1 {
				msgLink = fmt.Sprintf(`href="/mail/INBOX/%d"`, uids[0])
			}
			if tc.wantUID != (msgLink != "" && strings.Contains(got, msgLink)) {
				t.Errorf("message link present = %v, want %v: %s", !tc.wantUID, tc.wantUID, got)
			}
			if tc.wantDay != strings.Contains(got, `href="`+day+`"`) {
				t.Errorf("calendar day link present = %v, want %v: %s", !tc.wantDay, tc.wantDay, got)
			}
		})
	}
}

func TestSummaryUpcomingKeepsTheWeekAndOpenTasksOnly(t *testing.T) {
	e := newSummaryEnv(t)
	e.event(t, quotaUser, &bongov1.Event{Guid: "in", Title: "Inside the week", StartTime: summaryNow.Add(6 * 24 * time.Hour).Unix()})
	e.event(t, quotaUser, &bongov1.Event{Guid: "out", Title: "Eight days away", StartTime: summaryNow.Add(8 * 24 * time.Hour).Unix()})
	e.task(t, "Due this week", summaryNow.Add(2*24*time.Hour))
	e.task(t, "Due next month", summaryNow.Add(25*24*time.Hour))
	got := section(t, e.page(t), "upcoming")
	for _, want := range []string{"Inside the week", "Due this week"} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q: %s", want, got)
		}
	}
	for _, not := range []string{"Eight days away", "Due next month"} {
		if strings.Contains(got, not) {
			t.Errorf("lists %q", not)
		}
	}
}
