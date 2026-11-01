package handlers

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/emersion/go-ical"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// seedWithAttendees stores a weekly series organised by the session user with
// two attendees, wires a recording invite sender and forgets whatever the
// seeding sent.
func (e *instEnv) seedWithAttendees(t *testing.T, k kind, attendees bool) (string, *fakeInviteSender) {
	t.Helper()
	fs := &fakeInviteSender{}
	e.h.SetInviteSender(fs)
	guid := e.seed(t, k, "FREQ=WEEKLY", nil, nil)
	ev := e.get(t, guid)
	ev.Organizer = quotaEmail
	if attendees {
		ev.Attendees = []*bongov1.Attendee{{Address: "bob@example.org"}, {Address: "carol@example.org"}}
	}
	if err := e.events.Update(quotaUser, guid, ev); err != nil {
		t.Fatal(err)
	}
	e.ap.calls = 0
	fs.reset()
	return guid, fs
}

func ridOf(t *testing.T, m sentInvite) string {
	t.Helper()
	if p := m.vevent(t).Props.Get(ical.PropRecurrenceID); p != nil {
		return p.Value
	}
	return ""
}

func uidOf(t *testing.T, m sentInvite) string {
	t.Helper()
	return m.vevent(t).Props.Get(ical.PropUID).Value
}

func TestInstanceInvites(t *testing.T) {
	both := "bob@example.org,carol@example.org"
	for _, k := range []kind{kinds[0], kinds[1]} {
		t.Run(k.name+" edit this", func(t *testing.T) {
			e := newInstEnv(t)
			guid, fs := e.seedWithAttendees(t, k, true)
			uid := guid
			if !k.web {
				uid = "client-uid-" + k.name
			}
			over := map[string]string{"occurrence": ridNov04Timed, "scope": "this", "start_time": "14:30", "end_time": "15:30"}
			w := e.update(guid, occurrenceForm(k, "FREQ=WEEKLY", "Moved", "2030-11-04", over))
			if w.Code != http.StatusSeeOther {
				t.Fatalf("status = %d: %s", w.Code, w.Body.String())
			}
			msgs := fs.messages()
			if len(msgs) != 1 {
				t.Fatalf("messages = %d, want 1", len(msgs))
			}
			m := msgs[0]
			if m.method != "REQUEST" || addrsOf(m) != both || ridOf(t, m) != ridNov04Timed || uidOf(t, m) != uid {
				t.Errorf("method %s to %s rid %q uid %q", m.method, addrsOf(m), ridOf(t, m), uidOf(t, m))
			}
			// The override's own SEQUENCE, not the series'.
			_, overrides := rawParts(t, e.get(t, guid).IcalRaw)
			if len(overrides) != 1 {
				t.Fatalf("overrides = %d", len(overrides))
			}
			want := sequenceValue(overrides[0])
			if got := sequenceValue(m.vevent(t)); got != want || want == 0 {
				t.Errorf("SEQUENCE = %d, want the override's %d", got, want)
			}
			if m.vevent(t).Props.Get(ical.PropRecurrenceRule) != nil {
				t.Error("an instance message must not carry an RRULE")
			}
			if got := m.vevent(t).Props.Get(ical.PropSummary).Value; got != "Moved" {
				t.Errorf("SUMMARY = %q, want the override's", got)
			}
		})
		t.Run(k.name+" delete this", func(t *testing.T) {
			e := newInstEnv(t)
			guid, fs := e.seedWithAttendees(t, k, true)
			w := e.remove(guid, url.Values{"occurrence": {ridNov04Timed}, "scope": {"this"}}, false)
			if w.Code != http.StatusSeeOther {
				t.Fatalf("status = %d", w.Code)
			}
			msgs := fs.messages()
			if len(msgs) != 1 {
				t.Fatalf("messages = %d, want 1", len(msgs))
			}
			m := msgs[0]
			if m.method != "CANCEL" || addrsOf(m) != both || ridOf(t, m) != ridNov04Timed {
				t.Errorf("method %s to %s rid %q", m.method, addrsOf(m), ridOf(t, m))
			}
		})
		t.Run(k.name+" edit following", func(t *testing.T) {
			e := newInstEnv(t)
			guid, fs := e.seedWithAttendees(t, k, true)
			over := map[string]string{"occurrence": ridNov04Timed, "scope": "following", "start_time": "10:00", "end_time": "11:00",
				"attendees": "bob@example.org, carol@example.org"}
			w := e.update(guid, occurrenceForm(k, "FREQ=WEEKLY", "Following", "2030-11-04", over))
			if w.Code != http.StatusSeeOther {
				t.Fatalf("status = %d: %s", w.Code, w.Body.String())
			}
			msgs := fs.messages()
			if len(msgs) != 2 {
				t.Fatalf("messages = %d, want 2", len(msgs))
			}
			newGuid := e.otherGuid(t, guid)
			orig, fresh := msgs[0], msgs[1]
			if orig.method != "REQUEST" || ridOf(t, orig) != "" || orig.vevent(t).Props.Get(ical.PropRecurrenceRule) == nil ||
				!strings.Contains(orig.vevent(t).Props.Get(ical.PropRecurrenceRule).Value, "UNTIL=") {
				t.Errorf("truncated series message: %s rid %q rrule %v", orig.method, ridOf(t, orig), orig.vevent(t).Props.Get(ical.PropRecurrenceRule))
			}
			if fresh.method != "REQUEST" || ridOf(t, fresh) != "" || uidOf(t, fresh) == uidOf(t, orig) {
				t.Errorf("new series message: %s rid %q uid %q (original %q)", fresh.method, ridOf(t, fresh), uidOf(t, fresh), uidOf(t, orig))
			}
			if k.web && uidOf(t, fresh) != newGuid {
				t.Errorf("new series UID = %q, want %q", uidOf(t, fresh), newGuid)
			}
			if addrsOf(orig) != both || addrsOf(fresh) != both {
				t.Errorf("recipients %s / %s", addrsOf(orig), addrsOf(fresh))
			}
		})
		t.Run(k.name+" delete following", func(t *testing.T) {
			e := newInstEnv(t)
			guid, fs := e.seedWithAttendees(t, k, true)
			w := e.remove(guid, url.Values{"occurrence": {ridNov04Timed}, "scope": {"following"}}, false)
			if w.Code != http.StatusSeeOther {
				t.Fatalf("status = %d", w.Code)
			}
			msgs := fs.messages()
			if len(msgs) != 1 {
				t.Fatalf("messages = %d, want 1", len(msgs))
			}
			m := msgs[0]
			if m.method != "REQUEST" || ridOf(t, m) != "" || addrsOf(m) != both ||
				!strings.Contains(m.vevent(t).Props.Get(ical.PropRecurrenceRule).Value, "UNTIL=") {
				t.Errorf("method %s rid %q to %s", m.method, ridOf(t, m), addrsOf(m))
			}
		})
		t.Run(k.name+" no attendees sends nothing", func(t *testing.T) {
			e := newInstEnv(t)
			guid, fs := e.seedWithAttendees(t, k, false)
			over := map[string]string{"occurrence": ridNov04Timed, "scope": "this", "start_time": "14:30", "end_time": "15:30"}
			if w := e.update(guid, occurrenceForm(k, "FREQ=WEEKLY", "Moved", "2030-11-04", over)); w.Code != http.StatusSeeOther {
				t.Fatalf("edit status = %d", w.Code)
			}
			if w := e.remove(guid, url.Values{"occurrence": {ridOct21Timed}, "scope": {"this"}}, false); w.Code != http.StatusSeeOther {
				t.Fatalf("delete status = %d", w.Code)
			}
			over["scope"] = "following"
			if w := e.update(guid, occurrenceForm(k, "FREQ=WEEKLY", "F", "2030-11-04", over)); w.Code != http.StatusSeeOther {
				t.Fatalf("following status = %d", w.Code)
			}
			if n := len(fs.messages()); n != 0 {
				t.Errorf("messages = %d for events without attendees", n)
			}
		})
	}
}

func TestInstanceInviteFailureWarns(t *testing.T) {
	e := newInstEnv(t)
	guid, fs := e.seedWithAttendees(t, kinds[1], true)
	fs.refuse = map[string]bool{"bob@example.org": true}
	over := map[string]string{"occurrence": ridNov04Timed, "scope": "this", "start_time": "14:30", "end_time": "15:30"}
	w := e.update(guid, occurrenceForm(kinds[1], "FREQ=WEEKLY", "Moved", "2030-11-04", over))
	if loc := w.Header().Get("Location"); !strings.Contains(loc, "nr=bob%40example.org") {
		t.Errorf("edit redirect %q lacks the not-reached warning", loc)
	}
	w = e.remove(guid, url.Values{"occurrence": {ridOct21Timed}, "scope": {"this"}}, true)
	if loc := w.Header().Get("HX-Redirect"); !strings.Contains(loc, "nr=bob%40example.org") {
		t.Errorf("delete HX-Redirect %q lacks the not-reached warning", loc)
	}
}

func TestInstanceInviteNotSentForAnotherOrganiser(t *testing.T) {
	e := newInstEnv(t)
	guid, fs := e.seedWithAttendees(t, kinds[1], true)
	ev := e.get(t, guid)
	ev.Organizer = "someone.else@example.net"
	if err := e.events.Update(quotaUser, guid, ev); err != nil {
		t.Fatal(err)
	}
	over := map[string]string{"occurrence": ridNov04Timed, "scope": "this", "start_time": "14:30", "end_time": "15:30"}
	e.update(guid, occurrenceForm(kinds[1], "FREQ=WEEKLY", "Moved", "2030-11-04", over))
	if n := len(fs.messages()); n != 0 {
		t.Errorf("messages = %d, must not answer in another organiser's name", n)
	}
}

// A failed instance write is a 500 and is logged with the user, the event and
// the error, so an operator can find it.
func TestInstanceWriteFailuresAreLogged(t *testing.T) {
	cases := []struct {
		name string
		do   func(e *instEnv, guid string) int
	}{
		{"edit this", func(e *instEnv, guid string) int {
			over := map[string]string{"occurrence": ridNov04Timed, "scope": "this", "start_time": "14:30", "end_time": "15:30"}
			return e.update(guid, occurrenceForm(kinds[1], "FREQ=WEEKLY", "Moved", "2030-11-04", over)).Code
		}},
		{"delete this", func(e *instEnv, guid string) int {
			return e.remove(guid, url.Values{"occurrence": {ridNov04Timed}, "scope": {"this"}}, false).Code
		}},
		{"delete following", func(e *instEnv, guid string) int {
			return e.remove(guid, url.Values{"occurrence": {ridNov04Timed}, "scope": {"following"}}, false).Code
		}},
		{"delete following from the first", func(e *instEnv, guid string) int {
			return e.remove(guid, url.Values{"occurrence": {ridOct14Timed}, "scope": {"following"}}, false).Code
		}},
		{"edit following", func(e *instEnv, guid string) int {
			over := map[string]string{"occurrence": ridNov04Timed, "scope": "following", "start_time": "10:00", "end_time": "11:00"}
			return e.update(guid, occurrenceForm(kinds[1], "FREQ=WEEKLY", "F", "2030-11-04", over)).Code
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newInstEnv(t)
			guid, _ := e.seedWithAttendees(t, kinds[1], false)
			var buf bytes.Buffer
			prev := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
			defer slog.SetDefault(prev)
			e.ap.failAt = 1
			if code := tc.do(e, guid); code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500", code)
			}
			out := buf.String()
			for _, want := range []string{"level=ERROR", "user=" + quotaUser, "guid=" + guid, "injected apply failure"} {
				if !strings.Contains(out, want) {
					t.Errorf("log lacks %q:\n%s", want, out)
				}
			}
		})
	}
}
