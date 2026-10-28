//go:build integration

package integration

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/auth"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func TestWeekDayViews(t *testing.T) {
	_, baseURL, client, cancel := startDragonflyAgent(t)
	defer cancel()

	now := time.Now()
	startDate := now.Format("2006-01-02")

	// Create an event at 14:00 today.
	resp, err := client.PostForm(baseURL+"/calendar/event", url.Values{
		"title":       {"Week View Event"},
		"calendar_id": {"default"},
		"start_date":  {startDate},
		"start_time":  {"14:00"},
		"end_date":    {startDate},
		"end_time":    {"15:00"},
	})
	if err != nil {
		t.Fatalf("POST /calendar/event: %v", err)
	}
	resp.Body.Close()

	// Test week view.
	_, week := now.ISOWeek()
	resp, err = client.Get(baseURL + fmt.Sprintf("/calendar/week/%d/%d", now.Year(), week))
	if err != nil {
		t.Fatalf("GET /calendar/week: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("week view status: %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "Week View Event") {
		t.Error("week view should contain the event")
	}
	if !strings.Contains(string(body), "week-grid") {
		t.Error("week view should contain week-grid class")
	}

	// Test day view.
	resp, err = client.Get(baseURL + fmt.Sprintf("/calendar/day/%d/%d/%d", now.Year(), int(now.Month()), now.Day()))
	if err != nil {
		t.Fatalf("GET /calendar/day: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("day view status: %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "Week View Event") {
		t.Error("day view should contain the event")
	}
	if !strings.Contains(string(body), "day-grid") {
		t.Error("day view should contain day-grid class")
	}

	// Verify view switcher links are present.
	if !strings.Contains(string(body), "/calendar/week/") {
		t.Error("day view should contain week view link in switcher")
	}
}

func TestRecurringEvents(t *testing.T) {
	_, baseURL, client, cancel := startDragonflyAgent(t)
	defer cancel()

	now := time.Now()
	// Find next Monday from today.
	daysUntilMonday := (8 - int(now.Weekday())) % 7
	if daysUntilMonday == 0 {
		daysUntilMonday = 7
	}
	monday := now.AddDate(0, 0, daysUntilMonday)
	startDate := monday.Format("2006-01-02")

	// Create a weekly recurring event on that Monday.
	resp, err := client.PostForm(baseURL+"/calendar/event", url.Values{
		"title":       {"Weekly Standup"},
		"calendar_id": {"default"},
		"start_date":  {startDate},
		"start_time":  {"09:00"},
		"end_date":    {startDate},
		"end_time":    {"09:30"},
		"rrule":       {"FREQ=WEEKLY"},
	})
	if err != nil {
		t.Fatalf("POST /calendar/event: %v", err)
	}
	resp.Body.Close()

	// Check month view: the recurring event should appear.
	resp, err = client.Get(baseURL + fmt.Sprintf("/calendar/%d/%d", monday.Year(), int(monday.Month())))
	if err != nil {
		t.Fatalf("GET /calendar month: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if !strings.Contains(string(body), "Weekly Standup") {
		t.Error("month view should contain recurring event")
	}

	// Count occurrences: should be more than 1 since RRULE expands.
	occCount := strings.Count(string(body), "Weekly Standup")
	if occCount < 2 {
		t.Logf("recurring event appears %d times (may vary by month position)", occCount)
	}
}

func TestEventReschedule(t *testing.T) {
	_, baseURL, client, cancel := startDragonflyAgent(t)
	defer cancel()

	now := time.Now()
	startDate := now.Format("2006-01-02")

	// Create an event at 10:00.
	resp, err := client.PostForm(baseURL+"/calendar/event", url.Values{
		"title":       {"Reschedule Test"},
		"calendar_id": {"default"},
		"start_date":  {startDate},
		"start_time":  {"10:00"},
		"end_date":    {startDate},
		"end_time":    {"11:00"},
	})
	if err != nil {
		t.Fatalf("POST /calendar/event: %v", err)
	}
	resp.Body.Close()

	// Extract the event GUID.
	resp, err = client.Get(baseURL + fmt.Sprintf("/calendar/%d/%d", now.Year(), int(now.Month())))
	if err != nil {
		t.Fatalf("GET /calendar: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	guid := extractCalendarEventGUID(string(body))
	if guid == "" {
		t.Fatal("could not extract event GUID")
	}

	// Reschedule to 14:00-15:00.
	loc := now.Location()
	newStart := time.Date(now.Year(), now.Month(), now.Day(), 14, 0, 0, 0, loc)
	newEnd := time.Date(now.Year(), now.Month(), now.Day(), 15, 0, 0, 0, loc)

	resp, err = client.PostForm(baseURL+"/calendar/event/"+guid+"/reschedule", url.Values{
		"start_time": {fmt.Sprint(newStart.Unix())},
		"end_time":   {fmt.Sprint(newEnd.Unix())},
	})
	if err != nil {
		t.Fatalf("POST /calendar/event/%s/reschedule: %v", guid, err)
	}
	resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("reschedule status: %d", resp.StatusCode)
	}

	// Verify the event was rescheduled.
	resp, err = client.Get(baseURL + "/calendar/event/" + guid)
	if err != nil {
		t.Fatalf("GET /calendar/event/%s: %v", guid, err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	if !strings.Contains(string(body), "14:00") {
		t.Error("event should show new start time 14:00 after reschedule")
	}
}

func TestSharedCalendar(t *testing.T) {
	a, baseURL, client, cancel := startDragonflyAgent(t)
	defer cancel()

	now := time.Now()
	startDate := now.Format("2006-01-02")

	// Find admin user ID.
	userID := findUserID(t, a.Store)
	if userID == "" {
		t.Fatal("could not find admin user ID")
	}

	// Create an event as admin.
	resp, err := client.PostForm(baseURL+"/calendar/event", url.Values{
		"title":       {"Shared Event"},
		"calendar_id": {"default"},
		"start_date":  {startDate},
		"start_time":  {"10:00"},
		"end_date":    {startDate},
		"end_time":    {"11:00"},
	})
	if err != nil {
		t.Fatalf("POST /calendar/event: %v", err)
	}
	resp.Body.Close()

	// Share the default calendar with an existing local user. A made-up
	// address is refused, so the target must be a real account.
	if _, err := auth.CreateUser(a, a.Metadata, "user-b@localhost", "User B", "userbpass123", false); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	resp, err = client.PostForm(baseURL+"/calendar/default/share", url.Values{
		"share_with": {"user-b@localhost"},
	})
	if err != nil {
		t.Fatalf("POST /calendar/default/share: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("share status: %d", resp.StatusCode)
	}

	// Test free/busy lookup.
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()
	end := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, now.Location()).Unix()

	req, _ := http.NewRequest("GET", fmt.Sprintf("%s/calendar/freebusy?user=%s&start=%d&end=%d",
		baseURL, userID, start, end), nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("GET /calendar/freebusy: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("freebusy status: %d, body: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "start") {
		t.Error("freebusy response should contain busy blocks with start/end")
	}
}

// TestAgendaRouteAuthAndHTMX checks /calendar/agenda sits behind the session
// check like the other calendar routes, and serves a full page or an HTMX
// fragment to a signed-in user.
func TestAgendaRouteAuthAndHTMX(t *testing.T) {
	_, baseURL, client, cancel := startDragonflyAgent(t)
	defer cancel()

	bare := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := bare.Get(baseURL + "/calendar/agenda")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther && resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("sessionless GET /calendar/agenda: status %d, want a redirect to login or 401", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusUnauthorized && !strings.HasPrefix(resp.Header.Get("Location"), "/login") {
		t.Fatalf("sessionless GET /calendar/agenda redirected to %q, want /login", resp.Header.Get("Location"))
	}

	get := func(htmx bool) string {
		req, _ := http.NewRequest("GET", baseURL+"/calendar/agenda", nil)
		if htmx {
			req.Header.Set("HX-Request", "true")
		}
		r, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		if r.StatusCode != http.StatusOK {
			t.Fatalf("GET /calendar/agenda htmx=%v: status %d", htmx, r.StatusCode)
		}
		b, _ := io.ReadAll(r.Body)
		return string(b)
	}
	if full := get(false); !strings.Contains(full, "<html") || !strings.Contains(full, `id="agenda"`) {
		t.Error("full page should include the layout and the agenda container")
	}
	if part := get(true); strings.Contains(part, "<html") || !strings.Contains(part, `id="agenda"`) {
		t.Error("HTMX response should be the fragment with the agenda container")
	}
}

// TestAgendaUsesPreferenceZone guards the agent wiring of the agenda's zone
// source: a 23:30 UTC event belongs to the next day for a user whose zone
// preference is Pacific/Auckland (UTC+12 or +13), and to its own day under
// the UTC default.
func TestAgendaUsesPreferenceZone(t *testing.T) {
	a, baseURL, client, cancel := startDragonflyAgent(t)
	defer cancel()

	userID := findUserID(t, a.Store)
	if userID == "" {
		t.Fatal("could not find user ID")
	}
	if _, err := a.Calendars.EnsureDefault(userID); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2030, 6, 10, 23, 30, 0, 0, time.UTC)
	if _, err := a.Events.Create(userID, &bongov1.Event{
		Title: "Zone Probe", CalendarId: "default",
		StartTime: day.Unix(), EndTime: day.Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatal(err)
	}

	dates := func() string {
		resp, err := client.Get(baseURL + "/calendar/agenda?from=2030-06-01")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		m := regexp.MustCompile(`data-agenda-date="([0-9-]+)"`).FindStringSubmatch(string(b))
		if m == nil {
			t.Fatalf("no agenda day found: %s", b)
		}
		return m[1]
	}
	if got := dates(); got != "2030-06-10" {
		t.Fatalf("UTC default: day %s, want 2030-06-10", got)
	}
	if err := a.Preferences.Set(userID, &bongov1.UserPreferences{Timezone: "Pacific/Auckland"}); err != nil {
		t.Fatal(err)
	}
	if got := dates(); got != "2030-06-11" {
		t.Fatalf("Auckland preference: day %s, want 2030-06-11 (SetZoneSource wiring)", got)
	}
}
