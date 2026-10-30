package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/mail"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/emersion/go-ical"
	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/icalevent"
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
	quota     QuotaChecker // nil = unlimited
	shares    shareTargetResolver
	// shareLimiter caps ShareCalendar requests per user. nil = unlimited.
	shareLimiter shareRateLimiter
	// zones supplies the user's preferred time zone to the agenda. nil falls
	// back to the process zone, which the month, week and day views use.
	zones zoneSource
	// now is the clock; nil means time.Now. Tests inject a fixed one.
	now func() time.Time
}

// zoneSource resolves a user's preferred time zone.
type zoneSource interface {
	Location(userID string) *time.Location
}

// SetZoneSource wires the user time-zone preference the agenda reads.
func (h *CalendarHandler) SetZoneSource(z zoneSource) { h.zones = z }

// nowIn returns the current time in the user's zone, the one every calendar
// view groups days, marks today and builds Today links in.
func (h *CalendarHandler) nowIn(userID string) time.Time {
	now := time.Now
	if h.now != nil {
		now = h.now
	}
	return now().In(h.userLocation(userID))
}

// clockRequest returns r carrying the user's current time, so the templates
// mark today and render times in the same zone the handler grouped days in.
func clockRequest(r *http.Request, now time.Time) *http.Request {
	return r.WithContext(templates.WithCalendarNow(r.Context(), now))
}

// userLocation returns the zone every calendar view groups days in.
func (h *CalendarHandler) userLocation(userID string) *time.Location {
	if h.zones != nil {
		return h.zones.Location(userID)
	}
	return time.Local
}

// shareRateLimiter is the per-user limiter ShareCalendar consults.
type shareRateLimiter interface {
	Allow(string) bool
	RetryAfter(string) time.Duration
}

// SetShareLimiter sets the per-user cap on calendar share requests.
func (h *CalendarHandler) SetShareLimiter(l shareRateLimiter) { h.shareLimiter = l }

// ShareLimiter returns the per-user share limiter, or nil.
func (h *CalendarHandler) ShareLimiter() shareRateLimiter { return h.shareLimiter }

// maxCalendarShares caps how many users one calendar can be shared with.
const maxCalendarShares = 50

// shareTargetResolver maps an address to the user ID of an existing, enabled
// local account. It must not resolve through catch-alls.
type shareTargetResolver interface {
	ResolveAccount(email string) (string, error)
}

// SetShareResolver wires the resolver ShareCalendar uses to validate targets.
func (h *CalendarHandler) SetShareResolver(r shareTargetResolver) { h.shares = r }

// NewCalendarHandler creates a CalendarHandler.
func NewCalendarHandler(events *store.EventStore, calendars *store.CalendarStore, search *store.SearchManager) *CalendarHandler {
	return &CalendarHandler{events: events, calendars: calendars, search: search}
}

// SetQuotaChecker enables the domain storage-quota check on event writes.
func (h *CalendarHandler) SetQuotaChecker(qc QuotaChecker) { h.quota = qc }

// CalendarIndex redirects to the current month view.
func (h *CalendarHandler) CalendarIndex(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	now := h.nowIn(userID)
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
	calColorMap := buildCalColorMap(calendars)

	// Load events for every visible cell, including the neighbouring months'
	// days in the first and last weeks. The one-day widening each side is
	// defensive: it only matters for a zero-length all-day event (DTEND ==
	// DTSTART), whose end sits at its UTC midnight, before the local midnight
	// of a cell in a zone west of UTC.
	now := h.nowIn(userID)
	loc := now.Location()
	r = clockRequest(r, now)
	gridStart, gridEnd := monthGridRange(year, time.Month(month), loc)
	events, err := h.events.ListByDateRange(userID, gridStart.AddDate(0, 0, -1).Unix(), gridEnd.AddDate(0, 0, 1).Unix(), "")
	if err != nil {
		http.Error(w, "failed to load events", http.StatusInternalServerError)
		return
	}

	// Build day cells for the grid.
	days := buildMonthGrid(year, time.Month(month), events, now)

	flash := r.URL.Query().Get("flash")

	// Check if HTMX partial request.
	if r.Header.Get("HX-Request") == "true" {
		templ.Handler(templates.CalendarMonthGrid(year, month, days, calendars, calColorMap)).ServeHTTP(w, r)
		return
	}

	templ.Handler(templates.CalendarPage(year, month, days, calendars, calColorMap, email, flash)).ServeHTTP(w, r)
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
	now := h.nowIn(userID)
	r = clockRequest(r, now)
	weekStart := isoWeekMonday(year, week, now.Location())
	weekEnd := weekStart.AddDate(0, 0, 7)

	events, err := h.events.ListByDateRange(userID, weekStart.Unix(), weekEnd.Unix(), "")
	if err != nil {
		http.Error(w, "failed to load events", http.StatusInternalServerError)
		return
	}

	days := buildWeekColumns(weekStart, events, calColorMap, now)

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

	now := h.nowIn(userID)
	r = clockRequest(r, now)
	date := time.Date(year, time.Month(month), day, 0, 0, 0, 0, now.Location())
	dayEnd := date.AddDate(0, 0, 1)

	events, err := h.events.ListByDateRange(userID, date.Unix(), dayEnd.Unix(), "")
	if err != nil {
		http.Error(w, "failed to load events", http.StatusInternalServerError)
		return
	}

	col := buildDayColumn(date, events, calColorMap, now)

	flash := r.URL.Query().Get("flash")

	if r.Header.Get("HX-Request") == "true" {
		templ.Handler(templates.DayViewGrid(date, col, calendars, calColorMap)).ServeHTTP(w, r)
		return
	}

	templ.Handler(templates.DayViewPage(date, col, calendars, calColorMap, email, flash)).ServeHTTP(w, r)
}

// agendaRangeDays is the fixed agenda length. It is not a request parameter:
// recurrence expansion has no occurrence cap, so a caller-chosen range would
// let one request expand a daily rule across years.
const agendaRangeDays = 30

// AgendaView renders the agenda: the next agendaRangeDays days from ?from
// (default today), grouped by day in the user's time zone.
func (h *CalendarHandler) AgendaView(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())
	now := h.nowIn(userID)
	loc := now.Location()
	r = clockRequest(r, now)

	var from time.Time
	if raw := r.URL.Query().Get("from"); raw != "" {
		d, err := time.ParseInLocation("2006-01-02", raw, loc)
		if err != nil {
			http.Error(w, "invalid from date", http.StatusBadRequest)
			return
		}
		from = d
	} else {
		from = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	}

	if _, err := h.calendars.EnsureDefault(userID); err != nil {
		http.Error(w, "failed to init calendar", http.StatusInternalServerError)
		return
	}
	calendars, _ := h.calendars.List(userID)
	calColorMap := buildCalColorMap(calendars)

	to := from.AddDate(0, 0, agendaRangeDays)
	events, err := h.events.ListByDateRange(userID, from.Unix(), to.Unix(), "")
	if err != nil {
		http.Error(w, "failed to load events", http.StatusInternalServerError)
		return
	}

	data := templates.AgendaData{
		From:      from,
		To:        to.AddDate(0, 0, -1),
		Days:      buildAgenda(from, agendaRangeDays, events, calColorMap, loc),
		PrevPath:  agendaFromPath(from.AddDate(0, 0, -agendaRangeDays)),
		NextPath:  agendaFromPath(to),
		TodayPath: "/calendar/agenda",
	}

	if r.Header.Get("HX-Request") == "true" {
		templ.Handler(templates.AgendaContent(data)).ServeHTTP(w, r)
		return
	}
	templ.Handler(templates.AgendaPage(data, calendars, email, r.URL.Query().Get("flash"))).ServeHTTP(w, r)
}

func agendaFromPath(d time.Time) string {
	return "/calendar/agenda?from=" + d.Format("2006-01-02")
}

// buildAgenda groups events by day over [from, from+days) in loc. All-day
// events keep their UTC date (eventDateKey). The store query overlaps the
// neighbouring day, so occurrences whose date falls outside the range are
// dropped here. Within a day, all-day events come first, then start time.
func buildAgenda(from time.Time, days int, events []*bongov1.Event, calColorMap map[string]string, loc *time.Location) []templates.AgendaDay {
	first := from.In(loc)
	firstKey := first.Format("2006-01-02")
	endKey := first.AddDate(0, 0, days).Format("2006-01-02")

	type keyed struct {
		key string
		ev  *bongov1.Event
	}
	var in []keyed
	for _, e := range events {
		k := eventDateKey(e, loc)
		if k < firstKey || k >= endKey {
			continue
		}
		in = append(in, keyed{k, e})
	}
	sort.SliceStable(in, func(a, b int) bool {
		if in[a].key != in[b].key {
			return in[a].key < in[b].key
		}
		if in[a].ev.AllDay != in[b].ev.AllDay {
			return in[a].ev.AllDay
		}
		return in[a].ev.StartTime < in[b].ev.StartTime
	})

	var out []templates.AgendaDay
	for _, k := range in {
		if n := len(out); n == 0 || out[n-1].Date.Format("2006-01-02") != k.key {
			d, _ := time.ParseInLocation("2006-01-02", k.key, loc)
			out = append(out, templates.AgendaDay{Date: d})
		}
		item := templates.AgendaItem{Event: k.ev, ColourClass: templates.EventColourClass(k.ev, calColorMap)}
		if !k.ev.AllDay {
			item.TimeLabel = time.Unix(k.ev.StartTime, 0).In(loc).Format("15:04")
		}
		out[len(out)-1].Items = append(out[len(out)-1].Items, item)
	}
	return out
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

	original := proto.Clone(event).(*bongov1.Event)
	event.StartTime = newStart
	event.EndTime = newEnd

	// A CalDAV-created event is served from ical_raw, so a move that only
	// touched the proto fields would leave clients on the old time under an
	// unchanged ETag.
	if len(event.IcalRaw) > 0 {
		raw, err := applyEventEditToICal(event.IcalRaw, original, event, time.Now())
		switch {
		case errors.Is(err, errICalDecode):
			slog.Warn("calendar: stored ical_raw does not decode, rescheduling structured fields only", "user", userID, "guid", guid, "error", err)
			event.IcalRaw = nil
		case err != nil:
			slog.Error("calendar: rewriting ical_raw failed", "user", userID, "guid", guid, "error", err)
			http.Error(w, "failed to reschedule event", http.StatusInternalServerError)
			return
		default:
			event.IcalRaw = raw
		}
	}

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

	// Rate-limit first, per authenticated user, so every outcome counts and a
	// logged-in user cannot probe which addresses are live accounts. The
	// budget is per node and resets on restart, like the other mail.RateLimiter
	// budgets.
	if h.shareLimiter != nil && !h.shareLimiter.Allow(userID) {
		secs := max(int(math.Ceil(h.shareLimiter.RetryAfter(userID).Seconds())), 1)
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		http.Error(w, "too many share requests", http.StatusTooManyRequests)
		return
	}

	cal, err := h.calendars.Get(userID, calID)
	if err != nil {
		http.Error(w, "calendar not found", http.StatusNotFound)
		return
	}

	shareWith := strings.TrimSpace(r.FormValue("share_with"))
	if shareWith == "" {
		http.Error(w, "share_with is required", http.StatusBadRequest)
		return
	}
	if h.shares == nil {
		http.Error(w, "sharing is unavailable", http.StatusServiceUnavailable)
		return
	}

	// Every negative outcome (malformed, unknown, disabled, catch-all only,
	// self) gets the same reply so the response does not say which it was.
	const refused = "cannot share with that address"
	// The cap is checked before resolving so a full calendar cannot be used
	// to tell a valid address from an invalid one.
	if len(cal.SharedWith) >= maxCalendarShares {
		http.Error(w, refused, http.StatusBadRequest)
		return
	}
	parsed, err := mail.ParseAddress(shareWith)
	if err != nil || parsed.Name != "" {
		http.Error(w, refused, http.StatusBadRequest)
		return
	}
	// The stored value is the target's user ID, which is what
	// CalendarStore.IsSharedWith compares against; an address would never match.
	targetID, err := h.shares.ResolveAccount(parsed.Address)
	if err != nil || targetID == userID {
		http.Error(w, refused, http.StatusBadRequest)
		return
	}

	if slices.Contains(cal.SharedWith, targetID) {
		w.WriteHeader(http.StatusOK)
		return
	}

	cal.SharedWith = append(cal.SharedWith, targetID)
	if err := h.calendars.Update(userID, calID, cal); err != nil {
		http.Error(w, "failed to share calendar", http.StatusInternalServerError)
		return
	}

	w.Header().Set("HX-Trigger", "calendarRefresh")
	w.WriteHeader(http.StatusOK)
}

// UnshareCalendar removes one exact entry from a calendar's SharedWith list.
// The entry is a user ID or a legacy raw string that never matched
// IsSharedWith; it is compared as sent, without trimming, so an entry with
// stray whitespace can still be removed. Removing an absent entry is a 200
// with no write. No rate limit: removal reveals nothing about which accounts
// exist.
func (h *CalendarHandler) UnshareCalendar(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	calID := chi.URLParam(r, "calendarID")

	cal, err := h.calendars.Get(userID, calID)
	if err != nil {
		http.Error(w, "calendar not found", http.StatusNotFound)
		return
	}

	entry := r.FormValue("share_with")
	if entry == "" {
		http.Error(w, "share_with is required", http.StatusBadRequest)
		return
	}
	if !slices.Contains(cal.SharedWith, entry) {
		w.WriteHeader(http.StatusOK)
		return
	}

	// DeleteFunc also drops duplicate copies of a legacy entry.
	cal.SharedWith = slices.DeleteFunc(cal.SharedWith, func(s string) bool { return s == entry })
	if err := h.calendars.Update(userID, calID, cal); err != nil {
		http.Error(w, "failed to update calendar", http.StatusInternalServerError)
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
	shared, err := h.calendars.IsSharedWith(targetUser, callerID)
	return err == nil && shared
}

// NewEvent renders the create event form.
func (h *CalendarHandler) NewEvent(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())
	date := r.URL.Query().Get("date")

	calendars, _ := h.calendars.List(userID)
	r = clockRequest(r, h.nowIn(userID))

	templ.Handler(templates.EventFormPage(nil, calendars, true, date, email)).ServeHTTP(w, r)
}

// CreateEvent handles POST to create a new event.
func (h *CalendarHandler) CreateEvent(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())

	event, err := parseEventForm(r, h.userLocation(userID))
	if err != nil {
		http.Error(w, "invalid form data: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Best-effort domain quota (usage is a periodic recompute): refuse a new
	// event that would not fit. The estimate is the stored record size, the
	// same quantity the usage recompute counts.
	if quotaExceeded(h.quota, nil, userID, store.EventRecordSize(event)) {
		refuseQuota(w, r)
		return
	}

	if _, err := h.events.Create(userID, event); err != nil {
		http.Error(w, "failed to create event", http.StatusInternalServerError)
		return
	}

	t := eventMonthTime(event, h.userLocation(userID))
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

	r = clockRequest(r, h.nowIn(userID))
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

	updated, err := parseEventForm(r, h.userLocation(userID))
	if err != nil {
		http.Error(w, "invalid form data: "+err.Error(), http.StatusBadRequest)
		return
	}
	if !allowedRrule(updated.Rrule, existing.Rrule) {
		http.Error(w, "invalid form data: unsupported repeat rule", http.StatusBadRequest)
		return
	}

	// The form models neither of these, so carry them over rather than wipe
	// them. ReminderMinutes stays a structured-only field that drives server
	// reminders: it is never mapped onto VALARMs, because CalDAV events do not
	// populate it and the form would otherwise delete the client's alarms.
	updated.Timezone = existing.Timezone
	updated.Exdates = existing.Exdates
	updated.IcalRaw = nil
	if len(existing.IcalRaw) > 0 {
		raw, err := applyEventEditToICal(existing.IcalRaw, existing, updated, time.Now())
		switch {
		case errors.Is(err, errICalDecode):
			slog.Warn("calendar: stored ical_raw does not decode, saving structured fields only", "user", userID, "guid", guid, "error", err)
		case err != nil:
			slog.Error("calendar: rewriting ical_raw failed", "user", userID, "guid", guid, "error", err)
			http.Error(w, "failed to update event", http.StatusInternalServerError)
			return
		default:
			updated.IcalRaw = raw
		}
	}

	// At or over quota an update is allowed only if the stored record does not
	// grow, the same rule CalDAV PUT applies: letting a full user edit or shrink
	// what they have lets them make room. Growth is measured on the whole
	// record, including the rewritten ical_raw.
	// The store stamps these identity fields on write; copy them first so the
	// comparison is like for like.
	updated.CreatedAt = existing.CreatedAt
	updated.Guid = existing.Guid
	updated.UpdatedAt = existing.UpdatedAt
	if growth := int64(proto.Size(updated) - proto.Size(existing)); growth > 0 && quotaExceeded(h.quota, nil, userID, growth) {
		refuseQuota(w, r)
		return
	}
	if err := h.events.Update(userID, guid, updated); err != nil {
		http.Error(w, "failed to update event", http.StatusInternalServerError)
		return
	}

	t := eventMonthTime(updated, h.userLocation(userID))
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
		t := eventMonthTime(event, h.userLocation(userID))
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
	cal.Props.SetText(ical.PropProductID, "-//Bongo//Calendar//EN")

	// Each VTIMEZONE a kept event references is written once, ahead of the
	// events; the first definition of a TZID wins.
	var zones, vevents []*ical.Component
	seen := map[string]*ical.Component{}

	for _, e := range events {
		if len(e.IcalRaw) > 0 {
			dec := ical.NewDecoder(bytes.NewReader(e.IcalRaw))
			if rawCal, err := dec.Decode(); err == nil {
				defs := map[string]*ical.Component{}
				for _, child := range rawCal.Children {
					if child.Name != ical.CompTimezone {
						continue
					}
					if p := child.Props.Get(ical.PropTimezoneID); p != nil {
						defs[p.Value] = child
					}
				}
				for _, child := range rawCal.Children {
					if child.Name != ical.CompEvent {
						continue
					}
					vevents = append(vevents, child)
					for _, tzid := range referencedTZIDs(child) {
						def, ok := defs[tzid]
						if !ok {
							// Some CalDAV clients omit the VTIMEZONE; there is nothing to copy.
							slog.Default().Debug("calendar export: TZID has no VTIMEZONE", "tzid", tzid)
							continue
						}
						first, dup := seen[tzid]
						switch {
						case !dup:
							seen[tzid] = def
							zones = append(zones, def)
						case !reflect.DeepEqual(first, def):
							slog.Default().Debug("calendar export: conflicting VTIMEZONE, keeping first", "tzid", tzid)
						}
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

		if e.AllDay {
			// A date, not an instant: the zone plays no part.
			vevent.Props.SetDate(ical.PropDateTimeStart, icalevent.AllDayDate(e.StartTime))
			vevent.Props.SetDate(ical.PropDateTimeEnd, icalevent.AllDayDate(e.EndTime))
		} else {
			// UTC, so no TZID is written without a VTIMEZONE to define it.
			vevent.Props.SetDateTime(ical.PropDateTimeStart, time.Unix(e.StartTime, 0).UTC())
			vevent.Props.SetDateTime(ical.PropDateTimeEnd, time.Unix(e.EndTime, 0).UTC())
		}
		vevent.Props.SetDateTime(ical.PropDateTimeStamp, time.Now().UTC())

		vevents = append(vevents, vevent.Component)
	}
	cal.Children = append(zones, vevents...)

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
	defer removeUploadTemp(r)
	file, hdr, err := r.FormFile("ics")
	if err != nil {
		http.Redirect(w, r, "/calendar?flash=No+file+uploaded", http.StatusSeeOther)
		return
	}
	defer file.Close()

	// All or nothing, best effort: refuse the whole upload up front when it
	// would not fit, rather than importing part of it. The upload size is an
	// approximation: the stored size is roughly the raw bytes plus the copied
	// fields.
	if quotaExceeded(h.quota, nil, userID, hdr.Size) {
		refuseQuotaFlash(w, r, "/calendar")
		return
	}

	// Ensure default calendar.
	calID, _ := h.calendars.EnsureDefault(userID)

	dec := ical.NewDecoder(file)
	imported := 0
	failed := 0

	for {
		cal, err := dec.Decode()
		if err != nil {
			break
		}

		for _, group := range groupEventsByUID(cal) {
			raw, err := encodeEventGroup(cal, group)
			if err != nil {
				failed++
				continue
			}
			rawCal, err := ical.NewDecoder(bytes.NewReader(raw)).Decode()
			if err != nil {
				failed++
				continue
			}
			event, fallback := icalevent.ToEvent(rawCal)
			if fallback {
				slog.Default().Debug("calendar import: no master VEVENT, using earliest override", "user", userID)
			}
			event.CalendarId = calID
			event.IcalRaw = raw

			if _, err := h.events.Create(userID, event); err != nil {
				failed++
				continue
			}
			imported++
		}
	}

	flash := fmt.Sprintf("Imported+%d+events", imported)
	if failed > 0 {
		flash = fmt.Sprintf("Imported+%d+events,+%d+failed", imported, failed)
	}

	http.Redirect(w, r, "/calendar?flash="+strings.ReplaceAll(flash, " ", "+"), http.StatusSeeOther)
}

// groupEventsByUID groups the VEVENTs of cal by UID in first-seen order, so a
// master and its overrides become one event. A VEVENT without a UID is a group
// of its own (it fails encoding later and is counted as failed).
func groupEventsByUID(cal *ical.Calendar) [][]*ical.Component {
	var groups [][]*ical.Component
	index := map[string]int{}
	for _, c := range cal.Children {
		if c.Name != ical.CompEvent {
			continue
		}
		uid := ""
		if p := c.Props.Get(ical.PropUID); p != nil {
			uid = p.Value
		}
		if uid == "" {
			groups = append(groups, []*ical.Component{c})
			continue
		}
		i, ok := index[uid]
		if !ok {
			i = len(groups)
			index[uid] = i
			groups = append(groups, nil)
		}
		groups[i] = append(groups[i], c)
	}
	return groups
}

// referencedTZIDs lists the TZID parameter values on c's properties
// (DTSTART, DTEND, EXDATE, RDATE, RECURRENCE-ID and so on), in map order.
func referencedTZIDs(c *ical.Component) []string {
	var out []string
	for _, props := range c.Props {
		for _, p := range props {
			out = append(out, p.Params["TZID"]...)
		}
	}
	return out
}

// encodeEventGroup builds a standalone calendar from one UID group plus the
// VTIMEZONEs its TZID parameters reference, and encodes it. The error from the
// encoder is returned: an event whose data cannot be encoded is not stored.
func encodeEventGroup(src *ical.Calendar, group []*ical.Component) ([]byte, error) {
	zones := map[string]*ical.Component{}
	for _, c := range src.Children {
		if c.Name != ical.CompTimezone {
			continue
		}
		if p := c.Props.Get(ical.PropTimezoneID); p != nil {
			zones[p.Value] = c
		}
	}

	out := ical.NewCalendar()
	out.Props.SetText(ical.PropVersion, "2.0")
	out.Props.SetText(ical.PropProductID, "-//Bongo//Calendar//EN")

	added := map[string]bool{}
	for _, vevent := range group {
		for _, tzid := range referencedTZIDs(vevent) {
			if z, ok := zones[tzid]; ok && !added[tzid] {
				added[tzid] = true
				out.Children = append(out.Children, z)
			}
		}
	}
	out.Children = append(out.Children, group...)

	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(out); err != nil {
		return nil, fmt.Errorf("encoding imported event: %w", err)
	}
	return buf.Bytes(), nil
}

// MiniCalendar returns the mini calendar sidebar as an HTMX partial.
func (h *CalendarHandler) MiniCalendar(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	now := h.nowIn(userID)
	r = clockRequest(r, now)
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

	r = clockRequest(r, h.nowIn(userID))
	templ.Handler(templates.CalendarSearchResults(events, query, email)).ServeHTTP(w, r)
}

// normaliseNewlines turns the CRLF that browsers submit for textarea newlines
// (and any lone CR) into LF. go-ical escapes LF in text values but refuses a
// raw CR, and a stored description holds LF, so without this a multi-line
// description would differ from the stored one on every save and fail to encode.
func normaliseNewlines(s string) string {
	return strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(s)
}

// eventMonthTime is the time whose year and month the redirect after saving
// should show: an all-day event's UTC date, otherwise its start in loc.
func eventMonthTime(e *bongov1.Event, loc *time.Location) time.Time {
	if e.AllDay {
		return icalevent.AllDayDate(e.StartTime)
	}
	return time.Unix(e.StartTime, 0).In(loc)
}

// parseEventForm extracts event data from form values.
//
// Timed events are read in loc, the zone the views show them in. All-day
// events are dates and are read in UTC.
func parseEventForm(r *http.Request, loc *time.Location) (*bongov1.Event, error) {
	event := &bongov1.Event{
		Title:       normaliseNewlines(r.FormValue("title")),
		Description: normaliseNewlines(r.FormValue("description")),
		Location:    normaliseNewlines(r.FormValue("location")),
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

	// All-day events are dates: CalDAV stores them at UTC midnight, so they
	// are read in UTC.
	if event.AllDay {
		t, err := time.ParseInLocation("2006-01-02", startDate, time.UTC)
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
		t, err := time.ParseInLocation("2006-01-02", endDate, time.UTC)
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

// eventRrulePresets are the repeat rules the edit form offers.
var eventRrulePresets = []string{"FREQ=DAILY", "FREQ=WEEKLY", "FREQ=MONTHLY", "FREQ=YEARLY"}

// allowedRrule reports whether a submitted repeat rule may be stored: none, a
// preset, or exactly the rule the event already has (the form round-trips a
// custom rule from a CalDAV client). Anything else is refused, which also keeps
// CR/LF out of the RRULE value that is written unescaped into the raw iCal.
func allowedRrule(submitted, existing string) bool {
	return submitted == "" || submitted == existing || slices.Contains(eventRrulePresets, submitted)
}

// errICalDecode marks a stored ical_raw that cannot be parsed, as opposed to a
// failure to encode the edited calendar.
var errICalDecode = errors.New("decoding stored ical")

// applyEventEditToICal applies the web edit onto the stored iCalendar and
// returns the re-serialised bytes. It follows the buildVCardFromForm approach:
// the form is lossy, so a property is changed only when the submitted value
// differs from the existing structured value, and everything the form does not
// model (VALARM, ATTENDEE, ORGANIZER, X- properties, UID, EXDATE, VTIMEZONE and
// RECURRENCE-ID override components) is left untouched.
//
// Limitation: when the start of a recurring event moves, EXDATE and
// RECURRENCE-ID values are kept verbatim rather than shifted, so they may no
// longer match an occurrence of the moved series.
func applyEventEditToICal(raw []byte, existing, updated *bongov1.Event, now time.Time) ([]byte, error) {
	cal, err := ical.NewDecoder(bytes.NewReader(raw)).Decode()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errICalDecode, err)
	}

	master, _ := icalevent.Master(cal)
	if master == nil {
		return nil, fmt.Errorf("%w: no VEVENT", errICalDecode)
	}
	props := master.Props

	if updated.Title != existing.Title {
		props.SetText(ical.PropSummary, updated.Title)
	}
	for _, f := range []struct{ name, old, new string }{
		{ical.PropDescription, existing.Description, updated.Description},
		{ical.PropLocation, existing.Location, updated.Location},
	} {
		if f.new == f.old {
			continue
		}
		if f.new == "" {
			props.Del(f.name)
		} else {
			props.SetText(f.name, f.new)
		}
	}

	if eventTimesChanged(props, existing, updated) {
		setEventTimes(props, updated)
	}

	if updated.Rrule != existing.Rrule {
		if updated.Rrule == "" {
			props.Del(ical.PropRecurrenceRule)
		} else {
			rp := ical.NewProp(ical.PropRecurrenceRule)
			rp.Value = updated.Rrule
			props.Set(rp)
		}
	}

	seq := 0
	if p := props.Get(ical.PropSequence); p != nil {
		seq, _ = strconv.Atoi(p.Value)
	}
	sp := ical.NewProp(ical.PropSequence)
	sp.Value = strconv.Itoa(max(seq, 0) + 1)
	props.Set(sp)
	props.SetDateTime(ical.PropDateTimeStamp, now.UTC())
	props.SetDateTime(ical.PropLastModified, now.UTC())

	if cal.Props.Get(ical.PropVersion) == nil {
		cal.Props.SetText(ical.PropVersion, "2.0")
	}
	if cal.Props.Get(ical.PropProductID) == nil {
		cal.Props.SetText(ical.PropProductID, "-//Bongo//Calendar//EN")
	}

	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
		return nil, fmt.Errorf("encoding ical: %w", err)
	}
	return buf.Bytes(), nil
}

// eventTimesChanged reports whether the edit moves the event. All-day events
// are compared by calendar date against the stored DTSTART/DTEND: the UTC date
// of the proto time, which also reads older web-created events stored at local
// midnight.
func eventTimesChanged(props ical.Props, existing, updated *bongov1.Event) bool {
	if existing.AllDay != updated.AllDay {
		return true
	}
	if !updated.AllDay {
		return existing.StartTime != updated.StartTime || existing.EndTime != updated.EndTime
	}
	start, err := props.DateTime(ical.PropDateTimeStart, time.UTC)
	if err != nil {
		return true
	}
	end := start.AddDate(0, 0, 1)
	if p := props.Get(ical.PropDateTimeEnd); p != nil {
		if t, err := p.DateTime(time.UTC); err == nil {
			end = t
		}
	}
	const day = "2006-01-02"
	return start.Format(day) != icalevent.AllDayDate(updated.StartTime).Format(day) ||
		end.Format(day) != icalevent.AllDayDate(updated.EndTime).Format(day)
}

// setEventTimes writes DTSTART and DTEND. A timed event keeps the TZID of its
// DTSTART when the zone is known, otherwise it is written in UTC. DURATION is
// removed because it is mutually exclusive with DTEND.
func setEventTimes(props ical.Props, updated *bongov1.Event) {
	loc := time.UTC
	if p := props.Get(ical.PropDateTimeStart); p != nil {
		if tzid := p.Params.Get(ical.PropTimezoneID); tzid != "" {
			if l, err := time.LoadLocation(tzid); err == nil {
				loc = l
			}
		}
	}
	props.Del(ical.PropDuration)
	if updated.AllDay {
		props.SetDate(ical.PropDateTimeStart, icalevent.AllDayDate(updated.StartTime))
		props.SetDate(ical.PropDateTimeEnd, icalevent.AllDayDate(updated.EndTime))
		return
	}
	for name, ts := range map[string]int64{ical.PropDateTimeStart: updated.StartTime, ical.PropDateTimeEnd: updated.EndTime} {
		p := ical.NewProp(name)
		p.SetDateTime(time.Unix(ts, 0).In(loc))
		p.Params.Del(ical.ParamValue)
		props.Set(p)
	}
}

// eventDateKey is the YYYY-MM-DD day an event belongs to: the UTC date for an
// all-day event, otherwise the start in loc.
func eventDateKey(e *bongov1.Event, loc *time.Location) string {
	if e.AllDay {
		return icalevent.AllDayDate(e.StartTime).Format("2006-01-02")
	}
	return time.Unix(e.StartTime, 0).In(loc).Format("2006-01-02")
}

// monthGridRange returns the first instant of the first cell and the end of
// the last possible cell (42 cells) of a month grid, which starts on the
// Monday of the week containing the 1st. The query and the cells both use it
// so they cannot drift apart.
func monthGridRange(year int, month time.Month, loc *time.Location) (start, end time.Time) {
	firstDay := time.Date(year, month, 1, 0, 0, 0, 0, loc)
	weekday := int(firstDay.Weekday())
	if weekday == 0 {
		weekday = 7 // Sunday = 7
	}
	start = firstDay.AddDate(0, 0, -(weekday - 1))
	return start, start.AddDate(0, 0, 42)
}

// buildMonthGrid builds a 6-week grid of day cells for the given month.
// now carries both the zone the grid is laid out in and the instant that
// marks today.
func buildMonthGrid(year int, month time.Month, events []*bongov1.Event, now time.Time) []templates.DayCell {
	loc := now.Location()
	firstDay := time.Date(year, month, 1, 0, 0, 0, 0, loc)
	lastDay := firstDay.AddDate(0, 1, -1)
	gridStart, _ := monthGridRange(year, month, loc)

	todayDate := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)

	// Build event map by date string.
	eventsByDate := make(map[string][]*bongov1.Event)
	for _, e := range events {
		key := eventDateKey(e, loc)
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
func isoWeekMonday(year, week int, loc *time.Location) time.Time {
	// Jan 4 is always in week 1 of the year.
	jan4 := time.Date(year, 1, 4, 0, 0, 0, 0, loc)
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
func buildWeekColumns(weekStart time.Time, events []*bongov1.Event, calColorMap map[string]string, now time.Time) []templates.DayColumn {
	loc := weekStart.Location()
	todayDate := now.In(loc).Format("2006-01-02")

	// Group events by date.
	eventsByDate := make(map[string][]*bongov1.Event)
	for _, e := range events {
		key := eventDateKey(e, loc)
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
func buildDayColumn(date time.Time, events []*bongov1.Event, calColorMap map[string]string, now time.Time) templates.DayColumn {
	loc := date.Location()
	today := now.In(loc)

	// The range query overlaps neighbouring days, so an all-day event stored
	// at UTC midnight can arrive for the day before or after its date.
	dateKey := date.Format("2006-01-02")
	var allDay, timed []*bongov1.Event
	for _, e := range events {
		if e.AllDay {
			if eventDateKey(e, loc) == dateKey {
				allDay = append(allDay, e)
			}
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
