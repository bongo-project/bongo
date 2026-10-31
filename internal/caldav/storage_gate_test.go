package caldav

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/emersion/go-webdav/caldav"
)

type fakeStorageGate struct{ low bool }

func (g *fakeStorageGate) Low() bool { return g.low }

func TestPutCalendarObjectStorageLowHTTP507(t *testing.T) {
	b, userID := newTestBackend(t)
	gate := &fakeStorageGate{low: true}
	b.SetStorageGate(gate)
	h := caldav.Handler{Backend: b, Prefix: "/caldav"}

	body := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Test//EN\r\nBEGIN:VEVENT\r\nUID:s1\r\nDTSTAMP:20240115T100000Z\r\nDTSTART:20240115T100000Z\r\nSUMMARY:Low\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	put := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, "/caldav/"+userID+"/calendars/default/s1.ics", strings.NewReader(body))
		req.Header.Set("Content-Type", "text/calendar")
		req = req.WithContext(WithUserID(req.Context(), userID))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}

	if w := put(); w.Code != http.StatusInsufficientStorage {
		t.Fatalf("PUT with storage low: status %d, want 507 (body %q)", w.Code, w.Body.String())
	}
	if _, err := b.events.Get(userID, "s1"); err == nil {
		t.Error("event stored despite 507")
	}

	gate.low = false
	if w := put(); w.Code != http.StatusCreated && w.Code != http.StatusNoContent {
		t.Fatalf("PUT with storage ok: status %d (body %q)", w.Code, w.Body.String())
	}

	// DELETE is never gated: it frees space.
	gate.low = true
	if err := b.DeleteCalendarObject(WithUserID(context.Background(), userID), "/caldav/"+userID+"/calendars/default/s1.ics"); err != nil {
		t.Errorf("DELETE with storage low: %v", err)
	}
}
