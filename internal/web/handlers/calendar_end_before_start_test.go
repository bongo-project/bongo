package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/web/middleware"
)

// endForm is a create form with the given times on one day.
func endForm(start, end string) url.Values {
	return url.Values{
		"title": {"Meeting"}, "start_date": {"2030-05-06"}, "start_time": {start},
		"end_date": {"2030-05-06"}, "end_time": {end},
	}
}

func TestCreateEventRefusesEndNotAfterStart(t *testing.T) {
	for _, tc := range []struct {
		name string
		form url.Values
		code int
	}{
		{"end before start", endForm("10:00", "09:00"), http.StatusBadRequest},
		{"end equals start", endForm("10:00", "10:00"), http.StatusBadRequest},
		{"one minute after is accepted", endForm("10:00", "10:01"), http.StatusSeeOther},
		{"all-day end date before start", url.Values{
			"title": {"Trip"}, "all_day": {"on"}, "start_date": {"2030-05-06"}, "end_date": {"2030-05-05"},
		}, http.StatusBadRequest},
		{"all-day same date is accepted", url.Values{
			"title": {"Trip"}, "all_day": {"on"}, "start_date": {"2030-05-06"}, "end_date": {"2030-05-06"},
		}, http.StatusSeeOther},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newQuotaEnv(t)
			h := NewCalendarHandler(env.events, env.calendars, nil)
			req := formReq(http.MethodPost, "/calendar/event", tc.form)
			req = req.WithContext(middleware.WithLocale(req.Context(), "de"))
			w := httptest.NewRecorder()
			h.CreateEvent(w, req)
			if w.Code != tc.code {
				t.Fatalf("status = %d, want %d: %s", w.Code, tc.code, w.Body)
			}
			if tc.code == http.StatusBadRequest {
				if want := i18n.T("de", "calendar.endBeforeStart"); want == "calendar.endBeforeStart" || !strings.Contains(w.Body.String(), want) {
					t.Errorf("body %q is not the translated message %q", w.Body, want)
				}
				if n := env.countEvents(t); n != 0 {
					t.Errorf("events stored = %d, want 0", n)
				}
			}
		})
	}
}

func TestUpdateEventRefusesEndNotAfterStart(t *testing.T) {
	env := newQuotaEnv(t)
	h := NewCalendarHandler(env.events, env.calendars, nil)
	raw := clientICS("DTSTART:20300506T090000Z", "DTEND:20300506T100000Z")
	guid, existing := seedClientEvent(t, env, raw, utcUnix(2030, 5, 6, 9, 0), utcUnix(2030, 5, 6, 10, 0), "")
	for _, tc := range []struct {
		name       string
		start, end string
		code       int
	}{
		{"end before start", "10:00", "09:00", http.StatusBadRequest},
		{"end equals start", "10:00", "10:00", http.StatusBadRequest},
		{"one minute after is accepted", "10:00", "10:01", http.StatusSeeOther},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := browserForm(existing, map[string]string{
				"start_date": "2030-05-06", "start_time": tc.start, "end_date": "2030-05-06", "end_time": tc.end,
			})
			if w := putEdit(t, h, guid, f); w.Code != tc.code {
				t.Fatalf("status = %d, want %d: %s", w.Code, tc.code, w.Body)
			}
		})
	}
}

func TestOccurrenceEditRefusesEndNotAfterStart(t *testing.T) {
	for _, scope := range []string{"this", "following", "all"} {
		for _, k := range kinds {
			t.Run(scope+" "+k.name, func(t *testing.T) {
				e := newInstEnv(t)
				guid := e.seed(t, k, "FREQ=WEEKLY", nil, nil)
				rid := ridFor(k, "20301021")
				bad := map[string]string{"occurrence": rid, "scope": scope}
				if k.allDay {
					bad["end_date"] = "2030-10-20" // before the start of 2030-10-21
				} else {
					bad["end_time"] = "09:00" // equal to the 09:00 start
				}
				before := e.ap.calls
				w := e.update(guid, occurrenceForm(k, "FREQ=WEEKLY", "Edited", "2030-10-21", bad))
				if w.Code != http.StatusBadRequest {
					t.Fatalf("status = %d, want 400: %s", w.Code, w.Body)
				}
				if e.ap.calls != before {
					t.Errorf("a refused form wrote %d commands", e.ap.calls-before)
				}
			})
		}
	}
	t.Run("one minute after is accepted", func(t *testing.T) {
		e := newInstEnv(t)
		k := kinds[0]
		guid := e.seed(t, k, "FREQ=WEEKLY", nil, nil)
		over := map[string]string{"occurrence": ridFor(k, "20301021"), "scope": "this", "end_time": "09:01"}
		if w := e.update(guid, occurrenceForm(k, "FREQ=WEEKLY", "Edited", "2030-10-21", over)); w.Code != http.StatusSeeOther {
			t.Fatalf("status = %d: %s", w.Code, w.Body)
		}
	})
}
