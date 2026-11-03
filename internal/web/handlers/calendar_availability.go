package handlers

import (
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	"github.com/bongo-project/bongo/internal/web/templates"
)

// maxAvailabilityAttendees caps the attendees one availability request looks
// up, so a single request is not a bulk address probe.
const maxAvailabilityAttendees = 20

// SetFreeBusyLimiter sets the per-user cap on availability lookups by
// address. nil = unlimited.
func (h *CalendarHandler) SetFreeBusyLimiter(l shareRateLimiter) { h.freeBusyLimiter = l }

// freeBusyDay answers the event form's availability view: for each attendee
// address, the busy blocks of that address's account on one day, as an HTML
// fragment carrying times only. An address that is external, unknown,
// unverified or not shared with the caller renders identically, and the answer
// is 200 for all of them, so the view is no account-existence oracle.
func (h *CalendarHandler) freeBusyDay(w http.ResponseWriter, r *http.Request, date string) {
	callerID, callerEmail := middleware.UserFromContext(r.Context())
	q := r.URL.Query()

	// The budget covers the whole request whatever addresses it names.
	if h.freeBusyLimiter != nil && !h.freeBusyLimiter.Allow(callerID) {
		secs := max(int(math.Ceil(h.freeBusyLimiter.RetryAfter(callerID).Seconds())), 1)
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		http.Error(w, "too many availability requests", http.StatusTooManyRequests)
		return
	}

	// Authorisation is decided per request below; nothing is cached.
	w.Header().Set("Cache-Control", "no-store")
	loc := h.userLocation(callerID)
	day, err := time.ParseInLocation("2006-01-02", date, loc)
	attendees, aerr := parseAttendees(q.Get("attendees"))
	if err != nil || aerr != nil || len(attendees) == 0 {
		templ.Handler(templates.EventAvailability(nil)).ServeHTTP(w, r)
		return
	}
	if len(attendees) > maxAvailabilityAttendees {
		attendees = attendees[:maxAvailabilityAttendees]
	}
	dayStart := day.Unix()
	dayEnd := time.Date(day.Year(), day.Month(), day.Day()+1, 0, 0, 0, 0, loc).Unix()

	// The event being edited is read from the caller's own store and its UID
	// derived here: a UID taken from the request would show which block is
	// which.
	editUID := ""
	if guid := q.Get("event"); guid != "" {
		if ev, err := h.events.Get(callerID, guid); err == nil {
			editUID = seriesUID(ev)
		}
	}

	view := &templates.AvailabilityView{
		DayStart: dayStart,
		DayEnd:   dayEnd,
		Proposed: proposedSlot(q, day, dayStart, dayEnd, loc),
		Zone:     loc,
	}
	for _, a := range attendees {
		row := templates.AvailabilityRow{Label: a.Address}
		if target, ok := h.availabilityTarget(a.Address, callerID); ok {
			targetEmail := ""
			if target == callerID {
				targetEmail = callerEmail
			}
			// Wider than the day: all-day events sit at UTC midnight and only
			// busyIntervals places them in the target's zone.
			events, err := h.events.ListByDateRange(target, dayStart-86400, dayEnd+86400, "")
			if err == nil {
				row.Shared = true
				kept := events[:0:0]
				for _, e := range events {
					if editUID == "" || seriesUID(e) != editUID {
						kept = append(kept, e)
					}
				}
				for _, iv := range h.busyIntervals(target, targetEmail, kept) {
					if s, e := max(iv[0], dayStart), min(iv[1], dayEnd); e > s {
						row.Blocks = append(row.Blocks, [2]int64{s, e})
					}
				}
			}
		}
		view.Rows = append(view.Rows, row)
	}
	templ.Handler(templates.EventAvailability(view)).ServeHTTP(w, r)
}

// availabilityTarget resolves an attendee address to the user whose free/busy
// the caller may read. Every refusal looks the same to the caller. An
// unverified record never resolves, as it may name the wrong user's schedule.
func (h *CalendarHandler) availabilityTarget(addr, callerID string) (string, bool) {
	if h.addresses == nil {
		return "", false
	}
	rec, found, err := h.addresses.Get(store.NormaliseAddress(addr))
	if err != nil || !found || rec.UserId == "" || !rec.Verified {
		return "", false
	}
	if !h.mayReadFreeBusy(rec.UserId, callerID) {
		return "", false
	}
	return rec.UserId, true
}

// proposedSlot reads the form's proposed time on the day as unix seconds
// clipped to the day: the whole day when the event is all-day or the times are
// not usable.
func proposedSlot(q url.Values, day time.Time, dayStart, dayEnd int64, loc *time.Location) [2]int64 {
	whole := [2]int64{dayStart, dayEnd}
	if v := q.Get("all_day"); v != "" && v != "false" && v != "0" {
		return whole
	}
	st, err1 := time.Parse("15:04", strings.TrimSpace(q.Get("start_time")))
	en, err2 := time.Parse("15:04", strings.TrimSpace(q.Get("end_time")))
	if err1 != nil || err2 != nil {
		return whole
	}
	at := func(t time.Time) int64 {
		return time.Date(day.Year(), day.Month(), day.Day(), t.Hour(), t.Minute(), 0, 0, loc).Unix()
	}
	s, e := at(st), at(en)
	if e <= s {
		e = dayEnd
	}
	return [2]int64{max(s, dayStart), min(e, dayEnd)}
}
