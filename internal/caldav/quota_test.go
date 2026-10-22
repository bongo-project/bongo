package caldav

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav/caldav"
)

type fakeQuota struct {
	over bool
	err  error
}

func (f *fakeQuota) RecipientOverQuota(string) (bool, error) { return f.over, f.err }

func quotaTestCalendar(uid string) *ical.Calendar {
	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropProductID, "-//Test//Test//EN")
	ev := ical.NewEvent()
	ev.Props.SetText(ical.PropUID, uid)
	ev.Props.SetText(ical.PropSummary, "Quota")
	ev.Props.SetDateTime(ical.PropDateTimeStart, time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC))
	ev.Props.SetDateTime(ical.PropDateTimeStamp, time.Now().UTC())
	cal.Children = append(cal.Children, ev.Component)
	return cal
}

func TestPutCalendarObjectQuota(t *testing.T) {
	b, userID := newTestBackend(t)
	fq := &fakeQuota{over: true}
	b.SetQuotaChecker(fq)
	ctx := WithUserID(context.Background(), userID)
	path := "/caldav/" + userID + "/calendars/default/q1.ics"

	_, err := b.PutCalendarObject(ctx, path, quotaTestCalendar("q1"), nil)
	if err == nil {
		t.Fatal("PUT over quota: want an error, got nil")
	}
	if !errors.Is(err, ErrStorageQuotaExceeded) {
		t.Errorf("PUT over quota: want ErrStorageQuotaExceeded, got %v", err)
	}
	if _, gerr := b.events.Get(userID, "q1"); gerr == nil {
		t.Error("event stored despite refused PUT")
	}

	// Positive control: same user and path, now under quota.
	fq.over = false
	if _, err := b.PutCalendarObject(ctx, path, quotaTestCalendar("q1"), nil); err != nil {
		t.Fatalf("PUT under quota: %v", err)
	}
	if _, gerr := b.events.Get(userID, "q1"); gerr != nil {
		t.Errorf("event not stored after accepted PUT: %v", gerr)
	}

	// Fail open on a checker error.
	fq.over, fq.err = true, errors.New("pebble unavailable")
	if _, err := b.PutCalendarObject(ctx, "/caldav/"+userID+"/calendars/default/q2.ics", quotaTestCalendar("q2"), nil); err != nil {
		t.Errorf("PUT with failing checker: want success (fail open), got %v", err)
	}

	// DELETE stays allowed over quota so a user can free space.
	fq.over, fq.err = true, nil
	if err := b.DeleteCalendarObject(ctx, path); err != nil {
		t.Errorf("DELETE over quota: %v", err)
	}
}

// The status code is only observable on the wire: go-webdav's HTTPError is
// internal, so drive the real handler.
func TestPutCalendarObjectQuotaHTTP507(t *testing.T) {
	b, userID := newTestBackend(t)
	fq := &fakeQuota{over: true}
	b.SetQuotaChecker(fq)
	h := caldav.Handler{Backend: b, Prefix: "/caldav"}

	body := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Test//EN\r\nBEGIN:VEVENT\r\nUID:h1\r\nDTSTAMP:20240115T100000Z\r\nDTSTART:20240115T100000Z\r\nSUMMARY:Quota\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	put := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, "/caldav/"+userID+"/calendars/default/h1.ics", strings.NewReader(body))
		req.Header.Set("Content-Type", "text/calendar")
		req = req.WithContext(WithUserID(req.Context(), userID))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}

	if w := put(); w.Code != http.StatusInsufficientStorage {
		t.Fatalf("PUT over quota: status %d, want 507 (body %q)", w.Code, w.Body.String())
	}
	if _, err := b.events.Get(userID, "h1"); err == nil {
		t.Error("event stored despite 507")
	}
	fq.over = false
	if w := put(); w.Code != http.StatusCreated && w.Code != http.StatusNoContent {
		t.Fatalf("PUT under quota: status %d, want 201/204 (body %q)", w.Code, w.Body.String())
	}
}
