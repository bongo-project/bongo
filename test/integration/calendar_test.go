//go:build integration

package integration

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestCalendarAndEventCRUD(t *testing.T) {
	_, baseURL, client, cancel := startDragonflyAgent(t)
	defer cancel()

	// GET /calendar should redirect to current month view.
	resp, err := client.Get(baseURL + "/calendar")
	if err != nil {
		t.Fatalf("GET /calendar: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("GET /calendar status: %d", resp.StatusCode)
	}

	// Should contain the calendar grid.
	if !strings.Contains(string(body), "calendar-grid") {
		t.Error("calendar page should contain calendar-grid")
	}

	// Create an event.
	now := time.Now()
	startDate := now.Format("2006-01-02")
	resp, err = client.PostForm(baseURL+"/calendar/event", url.Values{
		"title":       {"Test Meeting"},
		"description": {"Integration test event"},
		"location":    {"Room 101"},
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

	// Verify event appears in month view.
	resp, err = client.Get(baseURL + fmt.Sprintf("/calendar/%d/%d", now.Year(), int(now.Month())))
	if err != nil {
		t.Fatalf("GET /calendar month: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	if !strings.Contains(string(body), "Test Meeting") {
		t.Error("month view should contain the created event")
	}

	// Extract event GUID from event-chip href.
	guid := extractCalendarEventGUID(string(body))
	if guid == "" {
		t.Fatal("could not extract event GUID from response")
	}

	// Update the event.
	resp, err = client.PostForm(baseURL+"/calendar/event/"+guid, url.Values{
		"_method":     {"PUT"},
		"title":       {"Updated Meeting"},
		"description": {"Updated description"},
		"location":    {"Room 202"},
		"calendar_id": {"default"},
		"start_date":  {startDate},
		"start_time":  {"16:00"},
		"end_date":    {startDate},
		"end_time":    {"17:00"},
	})
	if err != nil {
		t.Fatalf("POST /calendar/event/%s (update): %v", guid, err)
	}
	resp.Body.Close()

	// Verify update.
	resp, err = client.Get(baseURL + "/calendar/event/" + guid)
	if err != nil {
		t.Fatalf("GET /calendar/event/%s: %v", guid, err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	if !strings.Contains(string(body), "Updated Meeting") {
		t.Error("event detail should contain updated title")
	}
	if !strings.Contains(string(body), "Room 202") {
		t.Error("event detail should contain updated location")
	}

	// Delete the event.
	req, _ := http.NewRequest(http.MethodDelete, baseURL+"/calendar/event/"+guid, nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("DELETE /calendar/event/%s: %v", guid, err)
	}
	resp.Body.Close()

	// Verify deletion.
	resp, err = client.Get(baseURL + fmt.Sprintf("/calendar/%d/%d", now.Year(), int(now.Month())))
	if err != nil {
		t.Fatalf("GET /calendar month after delete: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	if strings.Contains(string(body), "Updated Meeting") {
		t.Error("month view should not contain deleted event")
	}
}

func TestCalDAVSync(t *testing.T) {
	a, baseURL, client, cancel := startDragonflyAgent(t)
	defer cancel()

	userID := findUserID(t, a.Store)
	if userID == "" {
		t.Fatal("could not find user ID")
	}

	now := time.Now()
	startDate := now.Format("2006-01-02")

	// Create event via web UI.
	resp, err := client.PostForm(baseURL+"/calendar/event", url.Values{
		"title":       {"CalDAV Test Event"},
		"description": {"Sync test"},
		"location":    {"Office"},
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

	// Extract GUID.
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

	// GET event via CalDAV.
	caldavClient := &http.Client{}
	req, _ := http.NewRequest("GET", fmt.Sprintf("%s/caldav/%s/calendars/default/%s.ics", baseURL, userID, guid), nil)
	req.SetBasicAuth("admin@localhost", "admin")
	resp, err = caldavClient.Do(req)
	if err != nil {
		t.Fatalf("CalDAV GET: %v", err)
	}
	icsBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("CalDAV GET status: %d, body: %s", resp.StatusCode, icsBody)
	}
	if !strings.Contains(string(icsBody), "VEVENT") {
		t.Error("CalDAV GET should return iCal with VEVENT")
	}
	if !strings.Contains(string(icsBody), "CalDAV Test Event") {
		t.Error("CalDAV GET should contain event title")
	}

	// PUT a new event via CalDAV.
	dtstart := now.UTC().Format("20060102T150000Z")
	dtend := now.Add(time.Hour).UTC().Format("20060102T150000Z")
	newICS := fmt.Sprintf("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Test//Test//EN\r\nBEGIN:VEVENT\r\nUID:caldav-new-event\r\nSUMMARY:CalDAV Created Event\r\nDTSTART:%s\r\nDTEND:%s\r\nDTSTAMP:%s\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n",
		dtstart, dtend, dtstart)

	req, _ = http.NewRequest("PUT", fmt.Sprintf("%s/caldav/%s/calendars/default/caldav-new-event.ics", baseURL, userID), strings.NewReader(newICS))
	req.SetBasicAuth("admin@localhost", "admin")
	req.Header.Set("Content-Type", "text/calendar")
	resp, err = caldavClient.Do(req)
	if err != nil {
		t.Fatalf("CalDAV PUT: %v", err)
	}
	putBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode >= 400 {
		t.Fatalf("CalDAV PUT status: %d, body: %s", resp.StatusCode, putBody)
	}

	// Verify CalDAV-created event appears in web UI.
	resp, err = client.Get(baseURL + fmt.Sprintf("/calendar/%d/%d", now.Year(), int(now.Month())))
	if err != nil {
		t.Fatalf("GET /calendar after CalDAV PUT: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	if !strings.Contains(string(body), "CalDAV Created Event") {
		t.Error("web UI should show CalDAV-created event")
	}

	// DELETE via CalDAV.
	req, _ = http.NewRequest("DELETE", fmt.Sprintf("%s/caldav/%s/calendars/default/caldav-new-event.ics", baseURL, userID), nil)
	req.SetBasicAuth("admin@localhost", "admin")
	resp, err = caldavClient.Do(req)
	if err != nil {
		t.Fatalf("CalDAV DELETE: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode >= 400 {
		t.Fatalf("CalDAV DELETE status: %d", resp.StatusCode)
	}

	// Verify deleted from web UI.
	resp, err = client.Get(baseURL + fmt.Sprintf("/calendar/%d/%d", now.Year(), int(now.Month())))
	if err != nil {
		t.Fatalf("GET /calendar after CalDAV DELETE: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	if strings.Contains(string(body), "CalDAV Created Event") {
		t.Error("web UI should not show CalDAV-deleted event")
	}
}

func TestICalImportExport(t *testing.T) {
	_, baseURL, client, cancel := startDragonflyAgent(t)
	defer cancel()

	now := time.Now()
	startDate := now.Format("2006-01-02")

	// Create 2 events.
	for i, title := range []string{"Export Event A", "Export Event B"} {
		resp, err := client.PostForm(baseURL+"/calendar/event", url.Values{
			"title":       {title},
			"calendar_id": {"default"},
			"start_date":  {startDate},
			"start_time":  {fmt.Sprintf("%02d:00", 10+i)},
			"end_date":    {startDate},
			"end_time":    {fmt.Sprintf("%02d:00", 11+i)},
		})
		if err != nil {
			t.Fatalf("POST /calendar/event (%s): %v", title, err)
		}
		resp.Body.Close()
	}

	// Export.
	resp, err := client.Get(baseURL + "/calendar/export")
	if err != nil {
		t.Fatalf("GET /calendar/export: %v", err)
	}
	icsData, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/calendar") {
		t.Errorf("export Content-Type: got %q, want text/calendar", ct)
	}
	if !strings.Contains(string(icsData), "Export Event A") || !strings.Contains(string(icsData), "Export Event B") {
		t.Error("exported .ics should contain both events")
	}
	if !strings.Contains(string(icsData), "VEVENT") {
		t.Error("exported .ics should contain VEVENT")
	}

	// Delete all events.
	resp, _ = client.Get(baseURL + fmt.Sprintf("/calendar/%d/%d", now.Year(), int(now.Month())))
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	guids := extractAllCalendarEventGUIDs(string(body))
	for _, guid := range guids {
		req, _ := http.NewRequest(http.MethodDelete, baseURL+"/calendar/event/"+guid, nil)
		resp, _ := client.Do(req)
		resp.Body.Close()
	}

	// Verify empty.
	resp, _ = client.Get(baseURL + fmt.Sprintf("/calendar/%d/%d", now.Year(), int(now.Month())))
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(body), "Export Event A") {
		t.Error("events should be deleted before import test")
	}

	// Import the exported .ics.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("ics", "calendar.ics")
	if err != nil {
		t.Fatalf("creating multipart: %v", err)
	}
	part.Write(icsData)
	mw.Close()

	req, _ := http.NewRequest(http.MethodPost, baseURL+"/calendar/import", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("POST /calendar/import: %v", err)
	}
	resp.Body.Close()

	// Verify imported events appear.
	resp, err = client.Get(baseURL + fmt.Sprintf("/calendar/%d/%d", now.Year(), int(now.Month())))
	if err != nil {
		t.Fatalf("GET /calendar after import: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	if !strings.Contains(string(body), "Export Event A") {
		t.Error("calendar should contain Event A after import")
	}
	if !strings.Contains(string(body), "Export Event B") {
		t.Error("calendar should contain Event B after import")
	}
}

// extractCalendarEventGUID extracts the first event GUID from a calendar page.
// Looks for href="/calendar/event/{guid}" pattern.
func extractCalendarEventGUID(html string) string {
	const marker = `href="/calendar/event/`
	idx := strings.Index(html, marker)
	if idx < 0 {
		return ""
	}
	start := idx + len(marker)
	end := strings.Index(html[start:], `"`)
	if end < 0 {
		return ""
	}
	guid := html[start : start+end]
	// Skip "new" as it's the create form link.
	if guid == "new" || strings.HasPrefix(guid, "new?") {
		// Try finding the next occurrence.
		rest := html[start+end:]
		return extractCalendarEventGUID(rest)
	}
	return guid
}

// extractAllCalendarEventGUIDs extracts all event GUIDs from a calendar page.
func extractAllCalendarEventGUIDs(html string) []string {
	var guids []string
	seen := make(map[string]bool)
	const marker = `href="/calendar/event/`
	for {
		idx := strings.Index(html, marker)
		if idx < 0 {
			break
		}
		start := idx + len(marker)
		end := strings.Index(html[start:], `"`)
		if end < 0 {
			break
		}
		guid := html[start : start+end]
		html = html[start+end:]

		if guid == "new" || strings.HasPrefix(guid, "new?") {
			continue
		}
		if !seen[guid] {
			seen[guid] = true
			guids = append(guids, guid)
		}
	}
	return guids
}
