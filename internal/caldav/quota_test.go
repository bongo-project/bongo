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
	"google.golang.org/protobuf/proto"
)

// fakeQuota models a domain with a fixed amount of headroom. over forces a
// refusal regardless of size; err forces a checker failure.
type fakeQuota struct {
	over     bool
	err      error
	headroom int64
	useRoom  bool
	lastAdd  int64
	calls    int
}

func (f *fakeQuota) RecipientOverQuota(string) (bool, error) {
	if f.useRoom {
		return f.headroom <= 0, f.err
	}
	return f.over, f.err
}

func (f *fakeQuota) RecipientWouldExceedQuota(_ string, add int64) (bool, error) {
	f.calls++
	f.lastAdd = add
	if f.err != nil {
		return false, f.err
	}
	if f.useRoom {
		return f.headroom <= 0 || add > f.headroom, nil
	}
	return f.over, nil
}

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

func quotaICSBody(uid, summary string) string {
	return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Test//EN\r\nBEGIN:VEVENT\r\nUID:" + uid +
		"\r\nDTSTAMP:20240115T100000Z\r\nDTSTART:20240115T100000Z\r\nSUMMARY:" + summary + "\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
}

func putICS(h http.Handler, userID, guid, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPut, "/caldav/"+userID+"/calendars/default/"+guid+".ics", strings.NewReader(body))
	req.Header.Set("Content-Type", "text/calendar")
	req = req.WithContext(WithUserID(req.Context(), userID))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func okPut(w *httptest.ResponseRecorder) bool {
	return w.Code == http.StatusCreated || w.Code == http.StatusNoContent
}

// storedSize is the byte length of the record in Pebble, the quantity the
// domain usage recompute counts.
func storedSize(t *testing.T, b *Backend, userID, guid string) int64 {
	t.Helper()
	ev, err := b.events.Get(userID, guid)
	if err != nil {
		t.Fatalf("get %s: %v", guid, err)
	}
	return int64(proto.Size(ev))
}

// A create must fit by the size of the stored record, not merely start under
// quota. The headroom is derived from a real stored record, so it is not a
// round number.
func TestPutCalendarObjectQuotaBySize(t *testing.T) {
	b, userID := newTestBackend(t)
	h := &caldav.Handler{Backend: b, Prefix: "/caldav"}
	body := quotaICSBody("s1", "Sized boundary event")

	// Measure with no checker, then remove.
	if w := putICS(h, userID, "s1", body); !okPut(w) {
		t.Fatalf("measure PUT: %d %q", w.Code, w.Body.String())
	}
	size := storedSize(t, b, userID, "s1")
	if err := b.DeleteCalendarObject(WithUserID(context.Background(), userID), "/caldav/"+userID+"/calendars/default/s1.ics"); err != nil {
		t.Fatal(err)
	}

	fq := &fakeQuota{useRoom: true, headroom: size - 1}
	b.SetQuotaChecker(fq)
	if w := putICS(h, userID, "s1", body); w.Code != http.StatusInsufficientStorage {
		t.Fatalf("one byte over headroom: status %d, want 507", w.Code)
	}
	if _, err := b.events.Get(userID, "s1"); err == nil {
		t.Error("event stored despite 507")
	}

	fq.headroom = size
	if w := putICS(h, userID, "s1", body); !okPut(w) {
		t.Fatalf("exact fit: status %d, want 201 (body %q)", w.Code, w.Body.String())
	}
	if got := storedSize(t, b, userID, "s1"); got != fq.headroom {
		t.Errorf("stored size %d, headroom %d: estimate and stored record disagree", got, fq.headroom)
	}
	if fq.lastAdd != size {
		t.Errorf("checker saw %d bytes, want %d", fq.lastAdd, size)
	}
}

// Updates are judged by growth. A non-growing edit is allowed at quota (the web
// rule); growth must fit the headroom exactly.
func TestPutCalendarObjectQuotaUpdateGrowth(t *testing.T) {
	b, userID := newTestBackend(t)
	h := &caldav.Handler{Backend: b, Prefix: "/caldav"}
	if w := putICS(h, userID, "u1", quotaICSBody("u1", "Medium length summary")); !okPut(w) {
		t.Fatalf("seed PUT: %d", w.Code)
	}
	base := storedSize(t, b, userID, "u1")

	fq := &fakeQuota{useRoom: true, headroom: 0}
	b.SetQuotaChecker(fq)

	grown := quotaICSBody("u1", "Medium length summary with 13 more")
	if w := putICS(h, userID, "u1", grown); w.Code != http.StatusInsufficientStorage {
		t.Fatalf("growth at quota: status %d, want 507", w.Code)
	}
	if got := storedSize(t, b, userID, "u1"); got != base {
		t.Errorf("record changed by refused update: %d -> %d", base, got)
	}

	// Identical and shrinking updates are accepted at quota.
	if w := putICS(h, userID, "u1", quotaICSBody("u1", "Medium length summary")); !okPut(w) {
		t.Errorf("identical update at quota: status %d, want accepted", w.Code)
	}
	if w := putICS(h, userID, "u1", quotaICSBody("u1", "Short")); !okPut(w) {
		t.Errorf("shrinking update at quota: status %d, want accepted", w.Code)
	}
	fq.headroom = 1 << 20
	if w := putICS(h, userID, "u1", quotaICSBody("u1", "Medium length summary")); !okPut(w) {
		t.Fatalf("restore: %d", w.Code)
	}

	// Measure the exact growth, then check both sides of it.
	fq.headroom = 1 << 20
	if w := putICS(h, userID, "u1", grown); !okPut(w) {
		t.Fatalf("grow with room: %d", w.Code)
	}
	growth := storedSize(t, b, userID, "u1") - base
	if growth <= 0 {
		t.Fatalf("growth %d, want positive", growth)
	}
	if w := putICS(h, userID, "u1", quotaICSBody("u1", "Medium length summary")); !okPut(w) {
		t.Fatalf("reset: %d", w.Code)
	}

	fq.headroom = growth - 1
	if w := putICS(h, userID, "u1", grown); w.Code != http.StatusInsufficientStorage {
		t.Errorf("growth+1 over headroom: status %d, want 507", w.Code)
	}
	fq.headroom = growth
	if w := putICS(h, userID, "u1", grown); !okPut(w) {
		t.Errorf("growth equal to headroom: status %d, want accepted", w.Code)
	}
}

// A stale If-Match is reported as 412 even when the user is over quota.
func TestPutCalendarObjectPreconditionBeatsQuota(t *testing.T) {
	b, userID := newTestBackend(t)
	h := &caldav.Handler{Backend: b, Prefix: "/caldav"}
	if w := putICS(h, userID, "p1", quotaICSBody("p1", "x")); !okPut(w) {
		t.Fatalf("seed: %d", w.Code)
	}
	b.SetQuotaChecker(&fakeQuota{useRoom: true, headroom: 0})
	req := httptest.NewRequest(http.MethodPut, "/caldav/"+userID+"/calendars/default/p1.ics", strings.NewReader(quotaICSBody("p1", "a much longer summary than before")))
	req.Header.Set("Content-Type", "text/calendar")
	req.Header.Set("If-Match", `"stale"`)
	req = req.WithContext(WithUserID(req.Context(), userID))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusPreconditionFailed {
		t.Errorf("stale If-Match over quota: status %d, want 412", w.Code)
	}
}

// A one-byte growth at quota is refused. Guards the "addBytes <= 0" allow in
// checkQuota against being widened to a small tolerance, which would let a
// client grow a record without limit in small steps. X-NOTE is not extracted
// into an Event field, so only the raw iCalendar grows.
func TestPutCalendarObjectQuotaOneByteGrowth(t *testing.T) {
	b, userID := newTestBackend(t)
	h := &caldav.Handler{Backend: b, Prefix: "/caldav"}
	withNote := func(note string) string {
		return strings.Replace(quotaICSBody("n1", "Note"), "END:VEVENT", "X-NOTE:"+note+"\r\nEND:VEVENT", 1)
	}
	if w := putICS(h, userID, "n1", withNote("abc")); !okPut(w) {
		t.Fatalf("seed PUT: %d", w.Code)
	}
	base := storedSize(t, b, userID, "n1")

	fq := &fakeQuota{useRoom: true, headroom: 0}
	b.SetQuotaChecker(fq)
	if w := putICS(h, userID, "n1", withNote("abcd")); w.Code != http.StatusInsufficientStorage {
		t.Fatalf("one-byte growth at quota: status %d, want 507", w.Code)
	}
	if fq.lastAdd != 1 {
		t.Fatalf("measured growth %d, want exactly 1", fq.lastAdd)
	}
	if got := storedSize(t, b, userID, "n1"); got != base {
		t.Errorf("record changed by refused update: %d -> %d", base, got)
	}
}
