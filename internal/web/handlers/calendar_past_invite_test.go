package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// pastNow is after every fixture below that is meant to be past and before
// the ones meant to be future.
var pastNow = time.Date(2030, 5, 10, 12, 0, 0, 0, time.UTC)

func TestInviteNothingForPastEvents(t *testing.T) {
	for _, allDay := range []bool{false, true} {
		name := "timed"
		if allDay {
			name = "all-day"
		}
		t.Run(name, func(t *testing.T) {
			env, h, fs := newInviteHandler(t)
			h.now = func() time.Time { return pastNow }
			form := url.Values{
				"title": {"Old"}, "start_date": {"2030-05-06"}, "start_time": {"09:00"},
				"end_date": {"2030-05-06"}, "end_time": {"10:00"}, "attendees": {"bob@example.org"},
			}
			if allDay {
				form.Set("all_day", "on")
			}
			w := httptest.NewRecorder()
			h.CreateEvent(w, formReq(http.MethodPost, "/calendar/event", form))
			if w.Code != http.StatusSeeOther {
				t.Fatalf("create status = %d: %s", w.Code, w.Body)
			}
			if n := len(fs.messages()); n != 0 {
				t.Fatalf("create of a past event sent %d messages", n)
			}
			events, _, err := env.events.ListAll(quotaUser, 0, 0)
			if err != nil || len(events) != 1 {
				t.Fatalf("events = %v, err %v", events, err)
			}
			guid, ev := events[0].Guid, events[0]

			// Past to past: still nothing.
			same := map[string]string{"location": "Elsewhere"}
			if allDay {
				same["all_day"] = "on"
			}
			if w := putEdit(t, h, guid, browserForm(ev, same)); w.Code != http.StatusSeeOther {
				t.Fatalf("past edit status = %d: %s", w.Code, w.Body)
			}
			if n := len(fs.messages()); n != 0 {
				t.Fatalf("edit of a past event sent %d messages", n)
			}

			// Past to future: one REQUEST.
			over := map[string]string{"start_date": "2030-05-11", "end_date": "2030-05-11"}
			if allDay {
				over["all_day"] = "on"
			}
			if w := putEdit(t, h, guid, browserForm(ev, over)); w.Code != http.StatusSeeOther {
				t.Fatalf("move status = %d: %s", w.Code, w.Body)
			}
			msgs := fs.messages()
			if len(msgs) != 1 || msgs[0].method != "REQUEST" || addrsOf(msgs[0]) != "bob@example.org" {
				t.Fatalf("move to tomorrow sent %+v, want one REQUEST to bob", msgs)
			}
		})
	}
}

func TestInviteNothingOnDeleteOfPastEvent(t *testing.T) {
	env, h, fs := newInviteHandler(t)
	h.now = func() time.Time { return pastNow }
	guid, _ := createMeeting(t, h, env, "bob@example.org")
	if n := len(fs.messages()); n != 0 {
		t.Fatalf("create sent %d messages", n)
	}
	req := withGUID(withUserContext(httptest.NewRequest(http.MethodDelete, "/calendar/event/"+guid, nil), quotaUser, quotaEmail), guid)
	w := httptest.NewRecorder()
	h.DeleteEvent(w, req)
	if w.Code != http.StatusSeeOther && w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if n := len(fs.messages()); n != 0 {
		t.Errorf("delete of a past event sent %d messages", n)
	}
}

// A future event moved into the past still tells its attendees.
func TestInviteFutureToPastStillSends(t *testing.T) {
	env, h, fs := newInviteHandler(t)
	h.now = func() time.Time { return time.Date(2030, 5, 1, 0, 0, 0, 0, time.UTC) }
	guid, ev := createMeeting(t, h, env, "bob@example.org")
	fs.reset()
	h.now = func() time.Time { return time.Date(2030, 5, 5, 12, 0, 0, 0, time.UTC) }
	if w := putEdit(t, h, guid, browserForm(ev, map[string]string{"start_date": "2030-05-04", "end_date": "2030-05-04"})); w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", w.Code)
	}
	if n := len(fs.messages()); n != 1 {
		t.Errorf("messages = %d, want 1", n)
	}
}

func TestInstanceInvitesPast(t *testing.T) {
	now := time.Date(2030, 12, 1, 12, 0, 0, 0, time.UTC)
	for _, k := range []kind{kinds[0], kinds[1]} {
		t.Run(k.name+" edit this past", func(t *testing.T) {
			e := newInstEnv(t)
			e.h.now = func() time.Time { return now }
			guid, fs := e.seedWithAttendees(t, k, true)
			over := map[string]string{"occurrence": ridNov04Timed, "scope": "this", "start_time": "14:30", "end_time": "15:30"}
			if w := e.update(guid, occurrenceForm(k, "FREQ=WEEKLY", "Moved", "2030-11-04", over)); w.Code != http.StatusSeeOther {
				t.Fatalf("status = %d: %s", w.Code, w.Body.String())
			}
			if n := len(fs.messages()); n != 0 {
				t.Errorf("a past occurrence edit sent %d messages", n)
			}
		})
		t.Run(k.name+" edit this to tomorrow", func(t *testing.T) {
			e := newInstEnv(t)
			e.h.now = func() time.Time { return now }
			guid, fs := e.seedWithAttendees(t, k, true)
			over := map[string]string{"occurrence": ridNov04Timed, "scope": "this"}
			if w := e.update(guid, occurrenceForm(k, "FREQ=WEEKLY", "Moved", "2030-12-02", over)); w.Code != http.StatusSeeOther {
				t.Fatalf("status = %d: %s", w.Code, w.Body.String())
			}
			msgs := fs.messages()
			if len(msgs) != 1 || msgs[0].method != "REQUEST" {
				t.Errorf("messages = %+v, want one REQUEST", msgs)
			}
		})
		t.Run(k.name+" delete this past", func(t *testing.T) {
			e := newInstEnv(t)
			e.h.now = func() time.Time { return now }
			guid, fs := e.seedWithAttendees(t, k, true)
			if w := e.remove(guid, url.Values{"occurrence": {ridNov04Timed}, "scope": {"this"}}, false); w.Code != http.StatusSeeOther {
				t.Fatalf("status = %d", w.Code)
			}
			if n := len(fs.messages()); n != 0 {
				t.Errorf("a past occurrence delete sent %d messages", n)
			}
		})
		t.Run(k.name+" following at a past slot still truncates", func(t *testing.T) {
			e := newInstEnv(t)
			e.h.now = func() time.Time { return now }
			guid, fs := e.seedWithAttendees(t, k, true)
			over := map[string]string{"occurrence": ridNov04Timed, "scope": "following", "start_time": "10:00", "end_time": "11:00",
				"attendees": "bob@example.org, carol@example.org"}
			if w := e.update(guid, occurrenceForm(k, "FREQ=WEEKLY", "Following", "2030-11-04", over)); w.Code != http.StatusSeeOther {
				t.Fatalf("status = %d: %s", w.Code, w.Body.String())
			}
			if n := len(fs.messages()); n != 2 {
				t.Errorf("messages = %d, want the UNTIL REQUEST and the new series", n)
			}
		})
	}
}

// The CalDAV scheduler plans with the same gate: a PUT or DELETE of an event
// that ended yesterday sends nothing and reports no SCHEDULE-STATUS, and a
// re-PUT moving it to tomorrow sends one REQUEST.
func TestSchedulerOrganiseSkipsPastEvents(t *testing.T) {
	hn := newSchedHarness(t)
	hn.h.now = func() time.Time { return pastNow }
	bob := &bongov1.Attendee{Address: "bob@example.org"}
	past := schedMeeting(bob)
	past.StartTime, past.EndTime = utcUnix(2030, 5, 9, 9, 0), utcUnix(2030, 5, 9, 10, 0)
	future := schedMeeting(bob)
	future.StartTime, future.EndTime = utcUnix(2030, 5, 11, 9, 0), utcUnix(2030, 5, 11, 10, 0)

	if st := hn.sched.Organise(schedAlice, nil, past, nil); len(st) != 0 {
		t.Errorf("PUT of a past event reported %v", st)
	}
	if st := hn.sched.Organise(schedAlice, past, nil, nil); len(st) != 0 {
		t.Errorf("DELETE of a past event reported %v", st)
	}
	if n := len(hn.sender.messages()); n != 0 {
		t.Fatalf("past event sent %d messages", n)
	}
	if st := hn.sched.Organise(schedAlice, past, future, nil); st["bob@example.org"] != "1.1" {
		t.Errorf("re-PUT to tomorrow statuses = %v", st)
	}
	if m := hn.sender.messages(); len(m) != 1 || m[0].method != "REQUEST" {
		t.Errorf("messages = %+v, want one REQUEST", m)
	}
}

// An all-day event is over at local midnight in the user's zone, not 14 hours
// later: in Berlin (UTC+2) one dated 9 May is past from 22:00Z on 9 May. The
// 21:59Z run is the positive control for the same form.
func TestInviteAllDayPastFromLocalMidnight(t *testing.T) {
	tests := []struct {
		name string
		now  time.Time
		want int
	}{
		{"just before local midnight", time.Date(2030, 5, 9, 21, 59, 0, 0, time.UTC), 1},
		{"just after local midnight", time.Date(2030, 5, 9, 22, 0, 1, 0, time.UTC), 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, h, fs := newInviteHandler(t)
			h.SetZoneSource(fixedZone{mustZone(t, "Europe/Berlin")})
			h.now = func() time.Time { return tc.now }
			form := url.Values{
				"title": {"Away"}, "start_date": {"2030-05-09"}, "end_date": {"2030-05-09"},
				"all_day": {"on"}, "attendees": {"bob@example.org"},
			}
			w := httptest.NewRecorder()
			h.CreateEvent(w, formReq(http.MethodPost, "/calendar/event", form))
			if w.Code != http.StatusSeeOther {
				t.Fatalf("create status = %d: %s", w.Code, w.Body)
			}
			if n := len(fs.messages()); n != tc.want {
				t.Fatalf("create sent %d messages, want %d", n, tc.want)
			}
		})
	}
}

// A series that has ended sends nothing on create or delete; an open-ended
// one from the same year does (the control).
func TestInviteEndedSeriesSendsNothing(t *testing.T) {
	tests := []struct {
		name  string
		rrule string
		sends bool
	}{
		{"count ended", "FREQ=WEEKLY;COUNT=3", false},
		{"until ended", "FREQ=WEEKLY;UNTIL=20300601T090000Z", false},
		{"open-ended", "FREQ=WEEKLY", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env, h, fs := newInviteHandler(t)
			h.SetZoneSource(fixedZone{time.UTC})
			h.now = func() time.Time { return pastNow.AddDate(0, 6, 0) }
			form := url.Values{
				"title": {"Weekly"}, "start_date": {"2030-05-06"}, "start_time": {"09:00"},
				"end_date": {"2030-05-06"}, "end_time": {"10:00"}, "attendees": {"bob@example.org"},
				"rrule": {tc.rrule},
			}
			w := httptest.NewRecorder()
			h.CreateEvent(w, formReq(http.MethodPost, "/calendar/event", form))
			if w.Code != http.StatusSeeOther {
				t.Fatalf("create status = %d: %s", w.Code, w.Body)
			}
			want := 0
			if tc.sends {
				want = 1
			}
			if n := len(fs.messages()); n != want {
				t.Fatalf("create sent %d messages, want %d", n, want)
			}
			events, _, err := env.events.ListAll(quotaUser, 0, 0)
			if err != nil || len(events) != 1 {
				t.Fatalf("events = %v, err %v", events, err)
			}
			guid := events[0].Guid
			req := withGUID(withUserContext(httptest.NewRequest(http.MethodDelete, "/calendar/event/"+guid, nil), quotaUser, quotaEmail), guid)
			w = httptest.NewRecorder()
			h.DeleteEvent(w, req)
			if w.Code != http.StatusSeeOther && w.Code != http.StatusOK {
				t.Fatalf("delete status = %d", w.Code)
			}
			if n := len(fs.messages()); n != want*2 {
				t.Errorf("after delete %d messages, want %d", n, want*2)
			}
		})
	}
}
