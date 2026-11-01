package icalevent

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func vevent(uid, extra string) string {
	return "BEGIN:VEVENT\r\nUID:" + uid + "\r\nDTSTAMP:20260101T000000Z\r\nDTSTART:20260301T100000Z\r\nDTEND:20260301T110000Z\r\nSUMMARY:" + uid + "\r\n" + extra + "END:VEVENT\r\n"
}

func vcal(header string, events ...string) string {
	return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\n" + header + strings.Join(events, "") + "END:VCALENDAR\r\n"
}

func TestParseFeed(t *testing.T) {
	body := vcal("", vevent("a", ""), vevent("b", ""), vevent("a", "RECURRENCE-ID:20260302T100000Z\r\n"))
	f, err := ParseFeed([]byte(body), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Events) != 2 {
		t.Fatalf("got %d events, want 2 (master and override are one)", len(f.Events))
	}
	if f.Events[0].UID != "a" || f.Events[1].UID != "b" {
		t.Fatalf("order: %s %s", f.Events[0].UID, f.Events[1].UID)
	}
	if f.Events[0].Event.Title != "a" {
		t.Fatalf("title = %q", f.Events[0].Event.Title)
	}
	if !bytes.Contains(f.Events[0].Raw, []byte("RECURRENCE-ID")) {
		t.Fatal("override not kept in the stored copy")
	}
}

func TestParseFeedFailures(t *testing.T) {
	tests := []struct {
		name string
		body string
		max  int
		want string
	}{
		{"empty body", "", 10, "no calendar"},
		{"not a calendar", "hello world\r\n", 10, ""},
		{"no events", vcal(""), 10, "no events"},
		{"truncated mid-event", vcal("", vevent("a", "")) + "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nBEGIN:VEVENT\r\nUID:z\r\n", 10, ""},
		{"over cap", vcal("", vevent("a", ""), vevent("b", ""), vevent("c", "")), 2, "too many"},
		{"only uidless", vcal("", "BEGIN:VEVENT\r\nDTSTAMP:20260101T000000Z\r\nDTSTART:20260301T100000Z\r\nSUMMARY:x\r\nEND:VEVENT\r\n"), 10, "no events"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, err := ParseFeed([]byte(tc.body), tc.max)
			if err == nil {
				t.Fatalf("parsed %+v", f)
			}
			if tc.want != "" && !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q lacks %q", err, tc.want)
			}
		})
	}
	_, err := ParseFeed([]byte(vcal("", vevent("a", ""), vevent("b", ""))), 1)
	if !errors.Is(err, ErrFeedTooLarge) {
		t.Fatalf("want ErrFeedTooLarge, got %v", err)
	}
}

func TestParseFeedCapBoundary(t *testing.T) {
	var evs []string
	for i := range 5 {
		evs = append(evs, vevent(fmt.Sprintf("u%d", i), ""))
	}
	if _, err := ParseFeed([]byte(vcal("", evs...)), 5); err != nil {
		t.Fatalf("exactly at the cap: %v", err)
	}
	if _, err := ParseFeed([]byte(vcal("", evs...)), 4); err == nil {
		t.Fatal("one over the cap accepted")
	}
}

func TestParseFeedSkipsUIDless(t *testing.T) {
	body := vcal("", vevent("a", ""), "BEGIN:VEVENT\r\nDTSTAMP:20260101T000000Z\r\nDTSTART:20260301T100000Z\r\nSUMMARY:x\r\nEND:VEVENT\r\n")
	f, err := ParseFeed([]byte(body), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Events) != 1 || f.Skipped != 1 {
		t.Fatalf("events=%d skipped=%d", len(f.Events), f.Skipped)
	}
}

// The same feed must encode to the same bytes every time, whatever the map
// order of the properties, or every refresh would rewrite every event.
func TestParseFeedIsDeterministic(t *testing.T) {
	zone := func(id string) string {
		return "BEGIN:VTIMEZONE\r\nTZID:" + id + "\r\nBEGIN:STANDARD\r\nDTSTART:19700101T000000\r\nTZOFFSETFROM:+0000\r\nTZOFFSETTO:+0000\r\nEND:STANDARD\r\nEND:VTIMEZONE\r\n"
	}
	ev := "BEGIN:VEVENT\r\nUID:tz\r\nDTSTAMP:20260101T000000Z\r\nDTSTART;TZID=Zone/A:20260301T100000\r\nDTEND;TZID=Zone/B:20260301T110000\r\n" +
		"EXDATE;TZID=Zone/C:20260302T100000\r\nRRULE:FREQ=DAILY\r\nSUMMARY:tz\r\nEND:VEVENT\r\n"
	body := vcal(zone("Zone/A")+zone("Zone/B")+zone("Zone/C"), ev)
	first, err := ParseFeed([]byte(body), 10)
	if err != nil {
		t.Fatal(err)
	}
	for range 50 {
		again, err := ParseFeed([]byte(body), 10)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(first.Events[0].Raw, again.Events[0].Raw) {
			t.Fatal("encoding varies between parses")
		}
	}
}

func TestParseFeedRefreshHint(t *testing.T) {
	tests := []struct {
		header string
		want   time.Duration
	}{
		{"", 0},
		{"REFRESH-INTERVAL;VALUE=DURATION:PT12H\r\n", 12 * time.Hour},
		{"X-PUBLISHED-TTL:PT30M\r\n", 30 * time.Minute},
		{"X-PUBLISHED-TTL:nonsense\r\n", 0},
	}
	for _, tc := range tests {
		f, err := ParseFeed([]byte(vcal(tc.header, vevent("a", ""))), 10)
		if err != nil {
			t.Fatal(err)
		}
		if f.RefreshHint != tc.want {
			t.Errorf("%q: hint = %v, want %v", tc.header, f.RefreshHint, tc.want)
		}
	}
}

func TestParseFeedStripsAlarms(t *testing.T) {
	alarm := "BEGIN:VALARM\r\nACTION:DISPLAY\r\nDESCRIPTION:wake\r\nTRIGGER:-PT15M\r\nEND:VALARM\r\n"
	body := vcal("", vevent("a", alarm), vevent("a", "RECURRENCE-ID:20260302T100000Z\r\n"+alarm))
	f, err := ParseFeed([]byte(body), 10)
	if err != nil {
		t.Fatal(err)
	}
	raw := string(f.Events[0].Raw)
	if strings.Contains(raw, "VALARM") || strings.Contains(raw, "TRIGGER") {
		t.Errorf("feed alarm kept in the stored copy:\n%s", raw)
	}
	if !strings.Contains(raw, "RECURRENCE-ID") || !strings.Contains(raw, "SUMMARY:a") {
		t.Errorf("stripping alarms dropped event data:\n%s", raw)
	}
	if f.Events[0].Event.ReminderMinutes != 0 {
		t.Errorf("reminder_minutes = %d, want 0", f.Events[0].Event.ReminderMinutes)
	}
}
