package handlers

import (
	"net/http/httptest"
	"testing"

	"github.com/bongo-go/bongo-go/internal/store"
	"github.com/bongo-go/bongo-go/internal/web/middleware"
)

func TestFreeBusyRejectsCrossUser(t *testing.T) {
	dir := t.TempDir()
	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })

	events := store.NewEventStore(ps, nil)
	calendars := store.NewCalendarStore(ps, nil)
	h := NewCalendarHandler(events, calendars, nil)

	do := func(caller, target string) int {
		req := httptest.NewRequest("GET", "/calendar/freebusy?user="+target+"&start=0&end=9999999999", nil)
		req = req.WithContext(middleware.WithUserContext(req.Context(), caller, caller+"@x.com"))
		rr := httptest.NewRecorder()
		h.FreeBusy(rr, req)
		return rr.Code
	}

	// Reading another user's schedule with no share must be forbidden (IDOR).
	if code := do("attacker", "victim"); code != 403 {
		t.Errorf("cross-user free/busy: status = %d, want 403", code)
	}
	// Reading one's own schedule is allowed.
	if code := do("victim", "victim"); code != 200 {
		t.Errorf("own free/busy: status = %d, want 200", code)
	}
}
