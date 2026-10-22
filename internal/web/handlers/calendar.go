package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/emersion/go-ical"
	"github.com/go-chi/chi/v5"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	"github.com/bongo-project/bongo/internal/web/templates"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// CalendarHandler handles calendar and event endpoints.
type CalendarHandler struct {
	events    *store.EventStore
	calendars *store.CalendarStore
	search    *store.SearchManager
}

// NewCalendarHandler creates a CalendarHandler.
func NewCalendarHandler(events *store.EventStore, calendars *store.CalendarStore, search *store.SearchManager) *CalendarHandler {
	return &CalendarHandler{events: events, calendars: calendars, search: search}
}

// CalendarIndex redirects to the current month view.
func (h *CalendarHandler) CalendarIndex(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	http.Redirect(w, r, fmt.Sprintf("/calendar/%d/%d", now.Year(), int(now.Month())), http.StatusSeeOther)
}

// MonthView renders the calendar month grid.
func (h *CalendarHandler) MonthView(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())

	year, err := strconv.Atoi(chi.URLParam(r, "year"))
	if err != nil {
		http.Error(w, "invalid year", http.StatusBadRequest)
		return
	}
	month, err := strconv.Atoi(chi.URLParam(r, "month"))
	if err != nil || month < 1 || month > 12 {
		http.Error(w, "invalid month", http.StatusBadRequest)
		return
	}

	// Ensure default calendar.
	if _, err := h.calendars.EnsureDefault(userID); err != nil {
		http.Error(w, "failed to init calendar", http.StatusInternalServerError)
		return
	}

	calendars, _ := h.calendars.List(userID)

	// Calculate month boundaries.
	loc := time.Now().Location()
	monthStart := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, loc)
	monthEnd := monthStart.AddDate(0, 1, 0)

	// Load events for this month.
	events, err := h.events.ListByDateRange(userID, monthStart.Unix(), monthEnd.Unix(), "")
	if err != nil {
		http.Error(w, "failed to load events", http.StatusInternalServerError)
		return
	}

	// Build day cells for the grid.
	days := buildMonthGrid(year, time.Month(month), events, loc)

	flash := r.URL.Query().Get("flash")

	// Check if HTMX partial request.
	if r.Header.Get("HX-Request") == "true" {
		templ.Handler(templates.CalendarMonthGrid(year, month, days, calendars)).ServeHTTP(w, r)
		return
	}

	templ.Handler(templates.CalendarPage(year, month, days, calendars, email, flash)).ServeHTTP(w, r)
}

// WeekView renders the calendar week view.
func (h *CalendarHandler) WeekView(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())

	year, err := strconv.Atoi(chi.URLParam(r, "year"))
	if err != nil {
		http.Error(w, "invalid year", http.StatusBadRequest)
		return
	}
	week, err := strconv.Atoi(chi.URLParam(r, "week"))
	if err != nil || week < 1 || week > 53 {
		http.Error(w, "invalid week", http.StatusBadRequest)
		return
	}

	if _, err := h.calendars.EnsureDefault(userID); err != nil {
		http.Error(w, "failed to init calendar", http.StatusInternalServerError)
		return
	}
	calendars, _ := h.calendars.List(userID)
	calColorMap := buildCalColorMap(calendars)

	// Find Monday of the given ISO week.
	weekStart := isoWeekMonday(year, week)
	weekEnd := weekStart.AddDate(0, 0, 7)

	events, err := h.events.ListByDateRange(userID, weekStart.Unix(), weekEnd.Unix(), "")
	if err != nil {
		http.Error(w, "failed to load events", http.StatusInternalServerError)
		return
	}

	days := buildWeekColumns(weekStart, events, calColorMap)

	flash := r.URL.Query().Get("flash")

	if r.Header.Get("HX-Request") == "true" {
		templ.Handler(templates.WeekViewGrid(year, week, days, calendars, calColorMap)).ServeHTTP(w, r)
		return
	}

	templ.Handler(templates.WeekViewPage(year, week, days, calendars, calColorMap, email, flash)).ServeHTTP(w, r)
}

// DayView renders the calendar day view.
func (h *CalendarHandler) DayView(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())

	year, err := strconv.Atoi(chi.URLParam(r, "year"))
	if err != nil {
		http.Error(w, "invalid year", http.StatusBadRequest)
		return
	}
	month, err := strconv.Atoi(chi.URLParam(r, "month"))
	if err != nil || month < 1 || month > 12 {
		http.Error(w, "invalid month", http.StatusBadRequest)
		return
	}
	day, err := strconv.Atoi(chi.URLParam(r, "day"))
	if err != nil || day < 1 || day > 31 {
		http.Error(w, "invalid day", http.StatusBadRequest)
		return
	}

	if _, err := h.calendars.EnsureDefault(userID); err != nil {
		http.Error(w, "failed to init calendar", http.StatusInternalServerError)
		return
	}
	calendars, _ := h.calendars.List(userID)
	calColorMap := buildCalColorMap(calendars)

	loc := time.Now().Location()
	date := time.Date(year, time.Month(month), day, 0, 0, 0, 0, loc)
	dayEnd := date.AddDate(0, 0, 1)

	events, err := h.events.ListByDateRange(userID, date.Unix(), dayEnd.Unix(), "")
	if err != nil {
		http.Error(w, "failed to load events", http.StatusInternalServerError)
		return
	}

	col := buildDayColumn(date, events, calColorMap)

	flash := r.URL.Query().Get("flash")

	if r.Header.Get("HX-Request") == "true" {
		templ.Handler(templates.DayViewGrid(date, col, calendars, calColorMap)).ServeHTTP(w, r)
		return
	}

	templ.Handler(templates.DayViewPage(date, col, calendars, calColorMap, email, flash)).ServeHTTP(w, r)
}

// RescheduleEvent handles POST to update only start/end times (drag-and-drop).
func (h *CalendarHandler) RescheduleEvent(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	guid := chi.URLParam(r, "guid")

	event, err := h.events.Get(userID, guid)
	if err != nil {
		http.Error(w, "event not found", http.StatusNotFound)
		return
	}

	startStr := r.FormValue("start_time")
	endStr := r.FormValue("end_time")

	newStart, err := strconv.ParseInt(startStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid start_time", http.StatusBadRequest)
		return
	}
	newEnd, err := strconv.ParseInt(endStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid end_time", http.StatusBadRequest)
		return
	}

	event.StartTime = newStart
	event.EndTime = newEnd

	if err := h.events.Update(userID, guid, event); err != nil {
		http.Error(w, "failed to reschedule event", http.StatusInternalServerError)
		return
	}

	w.Header().Set("HX-Trigger", "calendarRefresh")
	w.WriteHeader(http.StatusOK)
}

// UpdateCalendar handles PUT to update calendar properties (name, colour).
func (h *CalendarHandler) UpdateCalendar(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	calID := chi.URLParam(r, "calendarID")

	cal, err := h.calendars.Get(userID, calID)
	if err != nil {
		http.Error(w, "calendar not found", http.StatusNotFound)
		return
	}

	if name := r.FormValue("name"); name != "" {
		cal.Name = name
	}
	if color := r.FormValue("color"); color != "" {
		cal.Color = color
	}

	if err := h.calendars.Update(userID, calID, cal); err != nil {
		http.Error(w, "failed to update calendar", http.StatusInternalServerError)
		return
	}

	w.Header().Set("HX-Trigger", "calendarRefresh")
	w.WriteHeader(http.StatusOK)
}

// ShareCalendar handles POST to share a calendar with another user.
func (h *CalendarHandler) ShareCalendar(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	calID := chi.URLParam(r, "calendarID")

	cal, err := h.calendars.Get(userID, calID)
	if err != nil {
		http.Error(w, "calendar not found", http.StatusNotFound)
		return
	}

	shareWith := r.FormValue("share_with")
	if shareWith == "" {
		http.Error(w, "share_with is required", http.StatusBadRequest)
		return
	}

	// Avoid duplicates.
	for _, u := range cal.SharedWith {
		if u == shareWith {
			w.WriteHeader(http.StatusOK)
			return
		}
	}

	cal.SharedWith = append(cal.SharedWith, shareWith)
	if err := h.calendars.Update(userID, calID, cal); err != nil {
		http.Error(w, "failed to share calendar", http.StatusInternalServerError)
		return
	}

	w.Header().Set("HX-Trigger", "calendarRefresh")
	w.WriteHeader(http.StatusOK)
}

// FreeBusy returns busy time blocks for a user in a date range.
func (h *CalendarHandler) FreeBusy(w http.ResponseWriter, r *http.Request) {
	callerID, _ := middleware.UserFromContext(r.Context())
	targetUser := r.URL.Query().Get("user")
	startStr := r.URL.Query().Get("start")
	endStr := r.URL.Query().Get("end")

	if targetUser == "" || startStr == "" || endStr == "" {
		http.Error(w, "user, start, and end are required", http.StatusBadRequest)
		return
	}

	// Authorization: a caller may read their own free/busy, or that of a user
	// who has shared a calendar with them. Without this check any authenticated
	// user could read every user's schedule by guessing the ?user= id (IDOR).
	if targetUser != callerID && !h.targetSharesWith(targetUser, callerID) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	start, err := strconv.ParseInt(startStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid start", http.StatusBadRequest)
		return
	}
	end, err := strconv.ParseInt(endStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid end", http.StatusBadRequest)
		return
	}

	events, err := h.events.ListByDateRange(targetUser, start, end, "")
	if err != nil {
		http.Error(w, "failed to load events", http.StatusInternalServerError)
		return
	}

	type busyBlock struct {
		Start int64 `json:"start"`
		End   int64 `json:"end"`
	}
	var blocks []busyBlock
	for _, e := range events {
		if !e.AllDay {
			blocks = append(blocks, busyBlock{Start: e.StartTime, End: e.EndTime})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(blocks); err != nil {
		return
	}
}

// targetSharesWith reports whether targetUser has shared any of their calendars
// with callerID, which authorizes callerID to view targetUser's free/busy.
func (h *CalendarHandler) targetSharesWith(targetUser, callerID string) bool {
	if callerID == "" || h.calendars == nil {
		return false
	}
	shared, err := h.calendars.ListSharedCalendars(callerID)
	if err != nil {
		return false
	}
	for _, sc := range shared {
		if sc.OwnerID == targetUser {
			return true
		}
	}
	return false
}

// NewEvent renders the create event form.
func (h *CalendarHandler) NewEvent(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())
	date := r.URL.Query().Get("date")

	calendars, _ := h.calendars.List(userID)

	templ.Handler(templates.EventFormPage(nil, calendars, true, date, email)).ServeHTTP(w, r)
}

// CreateEvent handles POST to create a new event.
func (h *CalendarHandler) CreateEvent(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())

	event, err := parseEventForm(r)
	if err != nil {
		http.Error(w, "invalid form data: "+err.Error(), http.StatusBadRequest)
		return
	}

	if _, err := h.events.Create(userID, event); err != nil {
		http.Error(w, "failed to create event", http.StatusInternalServerError)
		return
	}

	t := time.Unix(event.StartTime, 0)
	http.Redirect(w, r, fmt.Sprintf("/calendar/%d/%d?flash=Event+created", t.Year(), int(t.Month())), http.StatusSeeOther) // #nosec G710 -- fixed same-origin calendar path
}

// ShowEvent renders an event detail/edit view.
func (h *CalendarHandler) ShowEvent(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())
	guid := chi.URLParam(r, "guid")

	event, err := h.events.Get(userID, guid)
	if err != nil {
		http.Error(w, "event not found", http.StatusNotFound)
		return
	}

	calendars, _ := h.calendars.List(userID)

	templ.Handler(templates.EventFormPage(event, calendars, false, "", email)).ServeHTTP(w, r)
}

// UpdateEvent handles PUT to update an event.
func (h *CalendarHandler) UpdateEvent(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	guid := chi.URLParam(r, "guid")

	existing, err := h.events.Get(userID, guid)
	if err != nil {
		http.Error(w, "event not found", http.StatusNotFound)
		return
	}

	updated, err := parseEventForm(r)
	if err != nil {
		http.Error(w, "invalid form data: "+err.Error(), http.StatusBadRequest)
		return
	}

	updated.CreatedAt = existing.CreatedAt
	if err := h.events.Update(userID, guid, updated); err != nil {
		http.Error(w, "failed to update event", http.StatusInternalServerError)
		return
	}

	t := time.Unix(updated.StartTime, 0)
	http.Redirect(w, r, fmt.Sprintf("/calendar/%d/%d?flash=Event+updated", t.Year(), int(t.Month())), http.StatusSeeOther) // #nosec G710 -- fixed same-origin calendar path
}

// DeleteEvent handles DELETE to remove an event.
func (h *CalendarHandler) DeleteEvent(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	guid := chi.URLParam(r, "guid")

	event, _ := h.events.Get(userID, guid)

	if err := h.events.Delete(userID, guid); err != nil {
		http.Error(w, "failed to delete event", http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/calendar")
		w.WriteHeader(http.StatusOK)
		return
	}

	if event != nil {
		t := time.Unix(event.StartTime, 0)
		http.Redirect(w, r, fmt.Sprintf("/calendar/%d/%d?flash=Event+deleted", t.Year(), int(t.Month())), http.StatusSeeOther) // #nosec G710 -- fixed same-origin calendar path
		return
	}
	http.Redirect(w, r, "/calendar?flash=Event+deleted", http.StatusSeeOther)
}

// ExportCalendar exports all events as an .ics file.
func (h *CalendarHandler) ExportCalendar(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())

	events, _, err := h.events.ListAll(userID, 0, 0)
	if err != nil {
		http.Error(w, "failed to list events", http.StatusInternalServerError)
		return
	}

	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropProductID, "-//Bongo-Go//Calendar//EN")

	for _, e := range events {
		if len(e.IcalRaw) > 0 {
			dec := ical.NewDecoder(bytes.NewReader(e.IcalRaw))
			if rawCal, err := dec.Decode(); err == nil {
				for _, child := range rawCal.Children {
					if child.Name == "VEVENT" {
						cal.Children = append(cal.Children, child)
					}
				}
				continue
			}
		}

		vevent := ical.NewEvent()
		vevent.Props.SetText(ical.PropUID, e.Guid)
		vevent.Props.SetText(ical.PropSummary, e.Title)
		if e.Description != "" {
			vevent.Props.SetText(ical.PropDescription, e.Description)
		}
		if e.Location != "" {
			vevent.Props.SetText(ical.PropLocation, e.Location)
		}

		loc := time.UTC
		if e.Timezone != "" {
			if l, err := time.LoadLocation(e.Timezone); err == nil {
				loc = l
			}
		}

		startTime := time.Unix(e.StartTime, 0).In(loc)
		endTime := time.Unix(e.EndTime, 0).In(loc)

		if e.AllDay {
			vevent.Props.SetDate(ical.PropDateTimeStart, startTime)
			vevent.Props.SetDate(ical.PropDateTimeEnd, endTime)
		} else {
			vevent.Props.SetDateTime(ical.PropDateTimeStart, startTime)
			vevent.Props.SetDateTime(ical.PropDateTimeEnd, endTime)
		}
		vevent.Props.SetDateTime(ical.PropDateTimeStamp, time.Now().UTC())

		cal.Children = append(cal.Children, vevent.Component)
	}

	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
		http.Error(w, "failed to encode calendar", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/calendar")
	w.Header().Set("Content-Disposition", `attachment; filename="calendar.ics"`)
	if _, err := w.Write(buf.Bytes()); err != nil {
		return
	}
}

// ImportCalendar imports events from an .ics file.
func (h *CalendarHandler) ImportCalendar(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())

	if !parseUploadForm(w, r, calendarImportMaxBytes, "/calendar") {
		return
	}
	file, _, err := r.FormFile("ics")
	if err != nil {
		http.Redirect(w, r, "/calendar?flash=No+file+uploaded", http.StatusSeeOther)
		return
	}
	defer file.Close()

	// Ensure default calendar.
	calID, _ := h.calendars.EnsureDefault(userID)

	dec := ical.NewDecoder(file)
	imported := 0
	errCount := 0

	for {
		cal, err := dec.Decode()
		if err != nil {
			break
		}

		for _, vevent := range cal.Events() {
			event := &bongov1.Event{
				CalendarId: calID,
			}

			if summary, err := vevent.Props.Text(ical.PropSummary); err == nil {
				event.Title = summary
			}
			if desc, err := vevent.Props.Text(ical.PropDescription); err == nil {
				event.Description = desc
			}
			if loc, err := vevent.Props.Text(ical.PropLocation); err == nil {
				event.Location = loc
			}

			if dtstart, err := vevent.DateTimeStart(time.UTC); err == nil {
				event.StartTime = dtstart.Unix()
			}
			if dtend, err := vevent.DateTimeEnd(time.UTC); err == nil {
				event.EndTime = dtend.Unix()
			}

			// Store raw iCal for round-trip.
			rawCal := ical.NewCalendar()
			rawCal.Props.SetText(ical.PropVersion, "2.0")
			rawCal.Children = append(rawCal.Children, vevent.Component)
			var rawBuf bytes.Buffer
			if err := ical.NewEncoder(&rawBuf).Encode(rawCal); err == nil {
				event.IcalRaw = rawBuf.Bytes()
			}

			if _, err := h.events.Create(userID, event); err != nil {
				errCount++
				continue
			}
			imported++
		}
	}

	flash := fmt.Sprintf("Imported+%d+events", imported)
	if errCount > 0 {
		flash = fmt.Sprintf("Imported+%d+events,+%d+errors", imported, errCount)
	}

	http.Redirect(w, r, "/calendar?flash="+strings.ReplaceAll(flash, " ", "+"), http.StatusSeeOther)
}

// MiniCalendar returns the mini calendar sidebar as an HTMX partial.
func (h *CalendarHandler) MiniCalendar(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	year := now.Year()
	month := int(now.Month())
	day := now.Day()

	if y := r.URL.Query().Get("year"); y != "" {
		if v, err := strconv.Atoi(y); err == nil {
			year = v
		}
	}
	if m := r.URL.Query().Get("month"); m != "" {
		if v, err := strconv.Atoi(m); err == nil && v >= 1 && v <= 12 {
			month = v
		}
	}
	if d := r.URL.Query().Get("day"); d != "" {
		if v, err := strconv.Atoi(d); err == nil && v >= 1 && v <= 31 {
			day = v
		}
	}

	templ.Handler(templates.MiniCalendar(year, month, day)).ServeHTTP(w, r)
}

// SearchEvents searches events by query string.
func (h *CalendarHandler) SearchEvents(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())
	query := r.URL.Query().Get("q")

	if query == "" || h.search == nil {
		http.Redirect(w, r, "/calendar", http.StatusSeeOther)
		return
	}

	results, err := h.search.SearchEvents(userID, query, 50)
	if err != nil {
		http.Error(w, "search failed", http.StatusInternalServerError)
		return
	}

	// Load full events for results.
	var events []*bongov1.Event
	for _, sr := range results {
		if e, err := h.events.Get(userID, sr.GUID); err == nil {
			events = append(events, e)
		}
	}

	templ.Handler(templates.CalendarSearchResults(events, query, email)).ServeHTTP(w, r)
}

// parseEventForm extracts event data from form values.
func parseEventForm(r *http.Request) (*bongov1.Event, error) {
	event := &bongov1.Event{
		Title:       r.FormValue("title"),
		Description: r.FormValue("description"),
		Location:    r.FormValue("location"),
		CalendarId:  r.FormValue("calendar_id"),
		Timezone:    r.FormValue("timezone"),
		AllDay:      r.FormValue("all_day") == "on",
		Rrule:       r.FormValue("rrule"),
	}

	if event.CalendarId == "" {
		event.CalendarId = "default"
	}

	// Parse reminder minutes.
	if rm := r.FormValue("reminder_minutes"); rm != "" {
		if v, err := strconv.ParseInt(rm, 10, 32); err == nil {
			event.ReminderMinutes = int32(v)
		}
	}

	// Parse start date/time.
	startDate := r.FormValue("start_date")
	startTime := r.FormValue("start_time")
	if startDate == "" {
		return nil, fmt.Errorf("start date is required")
	}

	loc := time.Now().Location()
	if event.AllDay {
		t, err := time.ParseInLocation("2006-01-02", startDate, loc)
		if err != nil {
			return nil, fmt.Errorf("invalid start date: %w", err)
		}
		event.StartTime = t.Unix()
	} else {
		if startTime == "" {
			startTime = "00:00"
		}
		t, err := time.ParseInLocation("2006-01-02 15:04", startDate+" "+startTime, loc)
		if err != nil {
			return nil, fmt.Errorf("invalid start date/time: %w", err)
		}
		event.StartTime = t.Unix()
	}

	// Parse end date/time.
	endDate := r.FormValue("end_date")
	endTime := r.FormValue("end_time")
	if endDate == "" {
		endDate = startDate
	}

	if event.AllDay {
		t, err := time.ParseInLocation("2006-01-02", endDate, loc)
		if err != nil {
			return nil, fmt.Errorf("invalid end date: %w", err)
		}
		// All-day events end at start of next day.
		event.EndTime = t.AddDate(0, 0, 1).Unix()
	} else {
		if endTime == "" {
			endTime = "01:00"
		}
		t, err := time.ParseInLocation("2006-01-02 15:04", endDate+" "+endTime, loc)
		if err != nil {
			return nil, fmt.Errorf("invalid end date/time: %w", err)
		}
		event.EndTime = t.Unix()
	}

	return event, nil
}

// buildMonthGrid builds a 6-week grid of day cells for the given month.
func buildMonthGrid(year int, month time.Month, events []*bongov1.Event, loc *time.Location) []templates.DayCell {
	firstDay := time.Date(year, month, 1, 0, 0, 0, 0, loc)
	lastDay := firstDay.AddDate(0, 1, -1)

	// Start from Monday of the week containing the first day.
	weekday := int(firstDay.Weekday())
	if weekday == 0 {
		weekday = 7 // Sunday = 7
	}
	gridStart := firstDay.AddDate(0, 0, -(weekday - 1))

	today := time.Now().In(loc)
	todayDate := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, loc)

	// Build event map by date string.
	eventsByDate := make(map[string][]*bongov1.Event)
	for _, e := range events {
		t := time.Unix(e.StartTime, 0).In(loc)
		key := t.Format("2006-01-02")
		eventsByDate[key] = append(eventsByDate[key], e)
	}

	var days []templates.DayCell
	// Always show 6 weeks (42 cells) for consistent grid height.
	for i := 0; i < 42; i++ {
		d := gridStart.AddDate(0, 0, i)
		key := d.Format("2006-01-02")
		cell := templates.DayCell{
			Date:           d,
			Events:         eventsByDate[key],
			IsCurrentMonth: d.Month() == month && d.Year() == year,
			IsToday:        d.Equal(todayDate),
		}

		// Stop after 5 weeks if we've passed the last day and are at a Monday.
		if i >= 35 && d.After(lastDay) && d.Weekday() == time.Monday {
			break
		}

		days = append(days, cell)
	}

	return days
}

// buildCalColorMap builds a map of calendar GUID to colour hex string.
func buildCalColorMap(calendars []*bongov1.Calendar) map[string]string {
	m := make(map[string]string, len(calendars))
	for _, c := range calendars {
		if c.Color != "" {
			m[c.Guid] = c.Color
		}
	}
	return m
}

// isoWeekMonday returns the Monday of the given ISO week.
func isoWeekMonday(year, week int) time.Time {
	// Jan 4 is always in week 1 of the year.
	jan4 := time.Date(year, 1, 4, 0, 0, 0, 0, time.Now().Location())
	// Find Monday of week 1.
	weekday := int(jan4.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	week1Monday := jan4.AddDate(0, 0, -(weekday - 1))
	// Add weeks.
	return week1Monday.AddDate(0, 0, (week-1)*7)
}

// buildWeekColumns builds 7 DayColumn structs for week view.
func buildWeekColumns(weekStart time.Time, events []*bongov1.Event, calColorMap map[string]string) []templates.DayColumn {
	loc := weekStart.Location()
	today := time.Now().In(loc)
	todayDate := today.Format("2006-01-02")

	// Group events by date.
	eventsByDate := make(map[string][]*bongov1.Event)
	for _, e := range events {
		t := time.Unix(e.StartTime, 0).In(loc)
		key := t.Format("2006-01-02")
		eventsByDate[key] = append(eventsByDate[key], e)
	}

	days := make([]templates.DayColumn, 7)
	for i := 0; i < 7; i++ {
		d := weekStart.AddDate(0, 0, i)
		key := d.Format("2006-01-02")
		dayEvents := eventsByDate[key]

		// Separate all-day vs timed events.
		var allDay, timed []*bongov1.Event
		for _, e := range dayEvents {
			if e.AllDay {
				allDay = append(allDay, e)
			} else {
				timed = append(timed, e)
			}
		}

		// Sort timed events by start time.
		sort.Slice(timed, func(a, b int) bool {
			return timed[a].StartTime < timed[b].StartTime
		})

		// Build event blocks with positioning.
		var blocks []templates.EventBlock
		for _, e := range timed {
			st := time.Unix(e.StartTime, 0).In(loc)
			et := time.Unix(e.EndTime, 0).In(loc)
			topMinutes := st.Hour()*60 + st.Minute()
			durMin := et.Sub(st).Minutes()
			if durMin < 30 {
				durMin = 30
			}

			color := calColorMap[e.CalendarId]
			if color == "" {
				color = "#4A90D9"
			}

			blocks = append(blocks, templates.EventBlock{
				Event:         e,
				TopMinutes:    topMinutes,
				HeightMinutes: int(durMin),
				ColourClass:   templates.CalendarColourClass(color),
			})
		}

		days[i] = templates.DayColumn{
			Date:    d,
			IsToday: d.Format("2006-01-02") == todayDate,
			AllDay:  allDay,
			Blocks:  blocks,
			DateStr: d.Format("2006-01-02"),
		}
	}

	return days
}

// buildDayColumn builds a DayColumn for day view.
func buildDayColumn(date time.Time, events []*bongov1.Event, calColorMap map[string]string) templates.DayColumn {
	loc := date.Location()
	today := time.Now().In(loc)

	var allDay, timed []*bongov1.Event
	for _, e := range events {
		if e.AllDay {
			allDay = append(allDay, e)
		} else {
			timed = append(timed, e)
		}
	}

	sort.Slice(timed, func(a, b int) bool {
		return timed[a].StartTime < timed[b].StartTime
	})

	var blocks []templates.EventBlock
	for _, e := range timed {
		st := time.Unix(e.StartTime, 0).In(loc)
		et := time.Unix(e.EndTime, 0).In(loc)
		topMinutes := st.Hour()*60 + st.Minute()
		durMin := et.Sub(st).Minutes()
		if durMin < 30 {
			durMin = 30
		}

		color := calColorMap[e.CalendarId]
		if color == "" {
			color = "#4A90D9"
		}

		blocks = append(blocks, templates.EventBlock{
			Event:         e,
			TopMinutes:    topMinutes,
			HeightMinutes: int(durMin),
			ColourClass:   templates.CalendarColourClass(color),
		})
	}

	return templates.DayColumn{
		Date:    date,
		IsToday: date.Format("2006-01-02") == today.Format("2006-01-02"),
		AllDay:  allDay,
		Blocks:  blocks,
		DateStr: date.Format("2006-01-02"),
	}
}
