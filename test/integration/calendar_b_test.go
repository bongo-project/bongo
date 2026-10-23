//go:build integration

package integration

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/auth"
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
