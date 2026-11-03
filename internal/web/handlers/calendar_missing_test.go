package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func newEventForm(over map[string]string) url.Values {
	f := url.Values{
		"title":            {"Lunch"},
		"start_date":       {"2030-05-06"},
		"start_time":       {"12:00"},
		"end_date":         {"2030-05-06"},
		"end_time":         {"13:00"},
		"reminder_minutes": {"0"},
	}
	for k, v := range over {
		f.Set(k, v)
	}
	return f
}

func TestCreateAndImportRefuseMissingOwnCalendar(t *testing.T) {
	ics := importHead + "BEGIN:VEVENT\r\nUID:m1@x\r\nDTSTAMP:20300101T000000Z\r\nDTSTART:20300506T120000Z\r\nDTEND:20300506T130000Z\r\nSUMMARY:Lunch\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	tests := []struct {
		name     string
		calID    string
		seed     bool
		wantCode int
		wantN    int
	}{
		{"missing calendar", "nope", false, http.StatusNotFound, 0},
		{"missing calendar for a user with calendars", "nope", true, http.StatusNotFound, 0},
		{"empty ref, brand-new user", "", false, 0, 1},
		{"\"default\", brand-new user", "default", false, 0, 1},
		{"existing own calendar", "work", true, 0, 1},
	}
	for _, tc := range tests {
		for _, via := range []string{"create", "import"} {
			t.Run(tc.name+"/"+via, func(t *testing.T) {
				env := newQuotaEnv(t)
				h := NewCalendarHandler(env.events, env.calendars, nil)
				if tc.seed {
					if err := env.calendars.Update(quotaUser, "work", &bongov1.Calendar{Guid: "work", Name: "Work"}); err != nil {
						t.Fatal(err)
					}
				}
				w := httptest.NewRecorder()
				if via == "create" {
					h.CreateEvent(w, formReq(http.MethodPost, "/calendar/event", newEventForm(map[string]string{"calendar_id": tc.calID})))
				} else {
					h.ImportCalendar(w, uploadReq(t, "/calendar/import", "ics", []byte(ics), map[string]string{"calendar_id": tc.calID}))
				}
				if tc.wantCode != 0 && w.Code != tc.wantCode {
					t.Fatalf("status = %d, want %d", w.Code, tc.wantCode)
				}
				if tc.wantCode == 0 && w.Code >= 400 {
					t.Fatalf("status = %d, want success", w.Code)
				}
				if n := env.countEvents(t); n != tc.wantN {
					t.Fatalf("stored events = %d, want %d", n, tc.wantN)
				}
			})
		}
	}
}

func TestUpdateMoveToMissingCalendar(t *testing.T) {
	tests := []struct {
		name     string
		stored   string
		form     string
		wantCode int
		wantCal  string
	}{
		{"move into missing", "default", "nope", http.StatusNotFound, "default"},
		{"stored under missing id, re-saved unchanged", "ghost", "ghost", http.StatusOK, "ghost"},
		{"empty equals default is no move", "", "default", http.StatusOK, "default"},
		{"move to existing", "default", "work", http.StatusOK, "work"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := newQuotaEnv(t)
			h := NewCalendarHandler(env.events, env.calendars, nil)
			// "default" is left out for the empty-equals-default case, so a
			// wrongly applied existence check would answer 404.
			for _, g := range []string{"default", "work"} {
				if tc.stored == "" && g == "default" {
					continue
				}
				if err := env.calendars.Update(quotaUser, g, &bongov1.Calendar{Guid: g, Name: g}); err != nil {
					t.Fatal(err)
				}
			}
			start := time.Date(2030, 5, 6, 12, 0, 0, 0, time.Local)
			guid, err := env.events.Create(quotaUser, &bongov1.Event{
				Title: "Lunch", CalendarId: tc.stored,
				StartTime: start.Unix(), EndTime: start.Add(time.Hour).Unix(),
			})
			if err != nil {
				t.Fatal(err)
			}
			ev, err := env.events.Get(quotaUser, guid)
			if err != nil {
				t.Fatal(err)
			}
			w := putEdit(t, h, guid, browserForm(ev, map[string]string{"calendar_id": tc.form}))
			if tc.wantCode == http.StatusOK {
				if w.Code >= 400 {
					t.Fatalf("status = %d, want success", w.Code)
				}
			} else if w.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d", w.Code, tc.wantCode)
			}
			got, err := env.events.Get(quotaUser, guid)
			if err != nil {
				t.Fatal(err)
			}
			if got.CalendarId != tc.wantCal {
				t.Fatalf("CalendarId = %q, want %q", got.CalendarId, tc.wantCal)
			}
		})
	}
}
