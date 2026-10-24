package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const exportZonesICS = importHead +
	"BEGIN:VTIMEZONE\r\nTZID:Europe/Berlin\r\nBEGIN:STANDARD\r\nDTSTART:19701025T030000\r\nTZOFFSETFROM:+0200\r\nTZOFFSETTO:+0100\r\nEND:STANDARD\r\nEND:VTIMEZONE\r\n" +
	"BEGIN:VTIMEZONE\r\nTZID:America/New_York\r\nBEGIN:STANDARD\r\nDTSTART:19701101T020000\r\nTZOFFSETFROM:-0400\r\nTZOFFSETTO:-0500\r\nEND:STANDARD\r\nEND:VTIMEZONE\r\n" +
	"BEGIN:VTIMEZONE\r\nTZID:Asia/Tokyo\r\nBEGIN:STANDARD\r\nDTSTART:19700101T000000\r\nTZOFFSETFROM:+0900\r\nTZOFFSETTO:+0900\r\nEND:STANDARD\r\nEND:VTIMEZONE\r\n" +
	"BEGIN:VEVENT\r\nUID:b1@x\r\nDTSTAMP:20261001T000000Z\r\nDTSTART;TZID=Europe/Berlin:20261024T101700\r\nDTEND;TZID=Europe/Berlin:20261024T113100\r\nSUMMARY:BerlinBefore\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nUID:b2@x\r\nDTSTAMP:20261001T000000Z\r\nDTSTART;TZID=Europe/Berlin:20261027T101700\r\nDTEND;TZID=Europe/Berlin:20261027T113100\r\nSUMMARY:BerlinAfter\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nUID:n1@x\r\nDTSTAMP:20261001T000000Z\r\nDTSTART;TZID=America/New_York:20261031T143700\r\nDTEND;TZID=America/New_York:20261031T153700\r\nSUMMARY:NYBefore\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nUID:n2@x\r\nDTSTAMP:20261001T000000Z\r\nDTSTART;TZID=America/New_York:20261102T143700\r\nDTEND;TZID=America/New_York:20261102T153700\r\nSUMMARY:NYAfter\r\nEND:VEVENT\r\n" +
	"END:VCALENDAR\r\n"

func exportICS(t *testing.T, env *quotaEnv) string {
	t.Helper()
	h := NewCalendarHandler(env.events, env.calendars, nil)
	w := httptest.NewRecorder()
	h.ExportCalendar(w, withUserContext(httptest.NewRequest(http.MethodGet, "/calendar/export", nil), quotaUser, quotaEmail))
	if w.Code != http.StatusOK {
		t.Fatalf("export status = %d", w.Code)
	}
	return w.Body.String()
}

func importInto(t *testing.T, env *quotaEnv, body string) {
	t.Helper()
	h := NewCalendarHandler(env.events, env.calendars, nil)
	w := httptest.NewRecorder()
	h.ImportCalendar(w, uploadReq(t, "/calendar/import", "ics", []byte(body), nil))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("import status = %d", w.Code)
	}
}

// zoneCounts returns how many times each TZID is defined by a VTIMEZONE and
// the set of TZIDs referenced by event properties.
func zoneCounts(t *testing.T, ics string) (defined map[string]int, referenced map[string]bool) {
	t.Helper()
	cal, err := ical.NewDecoder(strings.NewReader(ics)).Decode()
	if err != nil {
		t.Fatalf("decode export: %v\n%s", err, ics)
	}
	defined, referenced = map[string]int{}, map[string]bool{}
	for _, c := range cal.Children {
		switch c.Name {
		case ical.CompTimezone:
			defined[c.Props.Get(ical.PropTimezoneID).Value]++
		case ical.CompEvent:
			for _, tz := range referencedTZIDs(c) {
				referenced[tz] = true
			}
		}
	}
	return defined, referenced
}

func TestExportIncludesReferencedTimezonesOnce(t *testing.T) {
	env := newQuotaEnv(t)
	importInto(t, env, exportZonesICS)
	ics := exportICS(t, env)

	defined, referenced := zoneCounts(t, ics)
	for _, tz := range []string{"Europe/Berlin", "America/New_York"} {
		if !referenced[tz] {
			t.Errorf("export does not reference %s:\n%s", tz, ics)
		}
	}
	for tz := range referenced {
		if defined[tz] != 1 {
			t.Errorf("TZID %s defined %d times, want 1", tz, defined[tz])
		}
	}
	if defined["Asia/Tokyo"] != 0 {
		t.Errorf("unreferenced Asia/Tokyo exported:\n%s", ics)
	}
}

func TestExportWithoutRawIsUTCAndAllDayStaysDate(t *testing.T) {
	env := newQuotaEnv(t)
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 26, 9, 13, 0, 0, berlin)
	end := start.Add(77 * time.Minute)
	if _, err := env.events.Create(quotaUser, &bongov1.Event{
		Title: "Plain", StartTime: start.Unix(), EndTime: end.Unix(), Timezone: "Europe/Berlin",
	}); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	if _, err := env.events.Create(quotaUser, &bongov1.Event{
		Title: "Day", StartTime: day.Unix(), EndTime: day.AddDate(0, 0, 1).Unix(), AllDay: true, Timezone: "Pacific/Kiritimati",
	}); err != nil {
		t.Fatal(err)
	}
	ics := exportICS(t, env)

	for _, want := range []string{
		"DTSTART:" + start.UTC().Format("20060102T150405Z"),
		"DTEND:" + end.UTC().Format("20060102T150405Z"),
		"DTSTART;VALUE=DATE:20261105",
		"DTEND;VALUE=DATE:20261106",
	} {
		if !strings.Contains(ics, want) {
			t.Errorf("export lacks %s:\n%s", want, ics)
		}
	}
	if strings.Contains(ics, "TZID") {
		t.Errorf("export carries a TZID:\n%s", ics)
	}
}

func TestExportReimportKeepsInstants(t *testing.T) {
	src := newQuotaEnv(t)
	importInto(t, src, exportZonesICS)
	berlin, _ := time.LoadLocation("Europe/Berlin")
	plain := time.Date(2026, 10, 25, 2, 41, 0, 0, berlin)
	if _, err := src.events.Create(quotaUser, &bongov1.Event{
		Title: "Plain", StartTime: plain.Unix(), EndTime: plain.Add(39 * time.Minute).Unix(), Timezone: "Europe/Berlin",
	}); err != nil {
		t.Fatal(err)
	}
	ics := exportICS(t, src)

	dst := newQuotaEnv(t)
	importInto(t, dst, ics)

	want, _, _ := src.events.ListAll(quotaUser, 0, 0)
	got, _, _ := dst.events.ListAll(quotaUser, 0, 0)
	if len(want) != 5 || len(got) != len(want) {
		t.Fatalf("events: src %d, dst %d, want 5", len(want), len(got))
	}
	byTitle := map[string]*bongov1.Event{}
	for _, e := range got {
		byTitle[e.Title] = e
	}
	for _, w := range want {
		g := byTitle[w.Title]
		if g == nil {
			t.Errorf("%s missing after re-import", w.Title)
			continue
		}
		if g.StartTime != w.StartTime || g.EndTime != w.EndTime {
			t.Errorf("%s: %d..%d, want %d..%d", w.Title, g.StartTime, g.EndTime, w.StartTime, w.EndTime)
		}
	}
}

func TestExportConflictingTimezoneKeepsFirst(t *testing.T) {
	env := newQuotaEnv(t)
	raw := func(offset string) []byte {
		cal := importHead +
			"BEGIN:VTIMEZONE\r\nTZID:Europe/Berlin\r\nBEGIN:STANDARD\r\nDTSTART:19701025T030000\r\nTZOFFSETFROM:+0200\r\nTZOFFSETTO:" + offset + "\r\nEND:STANDARD\r\nEND:VTIMEZONE\r\n" +
			"BEGIN:VEVENT\r\nUID:u" + offset + "@x\r\nDTSTAMP:20261001T000000Z\r\nDTSTART;TZID=Europe/Berlin:20261027T101700\r\nSUMMARY:S" + offset + "\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
		return []byte(cal)
	}
	// ListAll orders by key, so fix the GUIDs to make "first" deterministic.
	for guid, off := range map[string]string{"a-first": "+0100", "b-second": "+0145"} {
		if _, err := env.events.Create(quotaUser, &bongov1.Event{Guid: guid, Title: guid, IcalRaw: raw(off)}); err != nil {
			t.Fatal(err)
		}
	}
	ics := exportICS(t, env)
	defined, _ := zoneCounts(t, ics)
	if defined["Europe/Berlin"] != 1 {
		t.Fatalf("Europe/Berlin defined %d times:\n%s", defined["Europe/Berlin"], ics)
	}
	if !strings.Contains(ics, "TZOFFSETTO:+0100") || strings.Contains(ics, "TZOFFSETTO:+0145") {
		t.Errorf("expected the first definition to win:\n%s", ics)
	}
}

// One event whose properties use three different zones: the TZID scan must
// visit every property, not stop at the first one that carries a TZID.
func TestExportDefinesEveryZoneAnEventUses(t *testing.T) {
	tz := func(id string) string {
		return "BEGIN:VTIMEZONE\r\nTZID:" + id + "\r\nBEGIN:STANDARD\r\nDTSTART:19700101T000000\r\nTZOFFSETFROM:+0000\r\nTZOFFSETTO:+0000\r\nEND:STANDARD\r\nEND:VTIMEZONE\r\n"
	}
	ics := importHead + tz("Europe/Berlin") + tz("America/New_York") + tz("Asia/Tokyo") + tz("Australia/Perth") +
		"BEGIN:VEVENT\r\nUID:m1@x\r\nDTSTAMP:20261001T000000Z\r\n" +
		"DTSTART;TZID=Europe/Berlin:20261024T101700\r\nDTEND;TZID=America/New_York:20261024T093100\r\n" +
		"RRULE:FREQ=DAILY;COUNT=5\r\nEXDATE;TZID=Asia/Tokyo:20261026T171700\r\nSUMMARY:Mixed\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"

	env := newQuotaEnv(t)
	importInto(t, env, ics)
	events, _, _ := env.events.ListAll(quotaUser, 0, 0)
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	stored, err := ical.NewDecoder(bytes.NewReader(events[0].IcalRaw)).Decode()
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"Asia/Tokyo", "America/New_York", "Europe/Berlin"}
	for name, text := range map[string]string{"stored ical_raw": string(events[0].IcalRaw), "export": exportICS(t, env)} {
		defined, referenced := zoneCounts(t, text)
		if name == "stored ical_raw" {
			defined = map[string]int{}
			for _, c := range stored.Children {
				if c.Name == ical.CompTimezone {
					defined[c.Props.Get(ical.PropTimezoneID).Value]++
				}
			}
		}
		for _, z := range want {
			if defined[z] != 1 {
				t.Errorf("%s: %s defined %d times, want 1", name, z, defined[z])
			}
			if name == "export" && !referenced[z] {
				t.Errorf("%s: %s not referenced", name, z)
			}
		}
		if defined["Australia/Perth"] != 0 {
			t.Errorf("%s: unreferenced Australia/Perth kept", name)
		}
	}
}

// Raw data written through CalDAV PUT can carry VTIMEZONEs no event uses;
// import strips them, so only a directly stored event exercises the
// export-time filter. Every zone must also precede the first VEVENT.
func TestExportSkipsUnreferencedZonesAndPutsZonesFirst(t *testing.T) {
	tz := func(id string) string {
		return "BEGIN:VTIMEZONE\r\nTZID:" + id + "\r\nBEGIN:STANDARD\r\nDTSTART:19700101T000000\r\nTZOFFSETFROM:+0000\r\nTZOFFSETTO:+0000\r\nEND:STANDARD\r\nEND:VTIMEZONE\r\n"
	}
	raw := importHead + tz("Australia/Perth") + tz("Europe/Berlin") +
		"BEGIN:VEVENT\r\nUID:raw1@x\r\nDTSTAMP:20261001T000000Z\r\nDTSTART;TZID=Europe/Berlin:20261024T101700\r\nDTEND;TZID=Europe/Berlin:20261024T113100\r\nSUMMARY:Raw\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"

	env := newQuotaEnv(t)
	if _, err := env.events.Create(quotaUser, &bongov1.Event{
		Title: "Raw", StartTime: 1792833420, EndTime: 1792837860, IcalRaw: []byte(raw),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	ics := exportICS(t, env)

	if n := strings.Count(ics, "TZID:Europe/Berlin"); n != 1 {
		t.Errorf("Europe/Berlin defined %d times, want 1:\n%s", n, ics)
	}
	if strings.Contains(ics, "Australia/Perth") {
		t.Errorf("unreferenced Australia/Perth exported:\n%s", ics)
	}

	firstEvent := strings.Index(ics, "BEGIN:VEVENT")
	lastZone := strings.LastIndex(ics, "BEGIN:VTIMEZONE")
	if firstEvent < 0 || lastZone < 0 || lastZone > firstEvent {
		t.Errorf("VTIMEZONE must precede the first VEVENT (zone at %d, event at %d):\n%s", lastZone, firstEvent, ics)
	}
}
