package handlers

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
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

func TestFreeBusySharedCalendarAuthorises(t *testing.T) {
	ps, err := store.NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })

	applier := &directApplierTags{ps: ps}
	events := store.NewEventStore(ps, applier)
	calendars := store.NewCalendarStore(ps, applier)
	h := NewCalendarHandler(events, calendars, nil)
	h.SetACL(store.NewACLStore(ps, applier))

	// A share made before grants existed: it reaches free/busy only through
	// the migration, which must keep its meaning.
	if _, err := calendars.Create("victim", &bongov1.Calendar{Name: "Work", SharedWith: []string{"friend"}}); err != nil {
		t.Fatalf("Create calendar: %v", err)
	}
	if _, err := events.Create("victim", &bongov1.Event{Title: "Meeting", StartTime: 1000, EndTime: 2000}); err != nil {
		t.Fatalf("Create event: %v", err)
	}

	do := func(caller, target string) (int, string) {
		req := httptest.NewRequest("GET", "/calendar/freebusy?user="+target+"&start=0&end=9999999999", nil)
		req = req.WithContext(middleware.WithUserContext(req.Context(), caller, caller+"@x.com"))
		rr := httptest.NewRecorder()
		h.FreeBusy(rr, req)
		return rr.Code, rr.Body.String()
	}

	if code, _ := do("friend", "victim"); code != 403 {
		t.Fatalf("unmigrated legacy share: %d, want 403 (free/busy no longer reads shared_with)", code)
	}
	if n, err := store.MigrateCalendarGrants(ps, applier, time.Unix(1000, 0)); err != nil || n != 1 {
		t.Fatalf("MigrateCalendarGrants = %d, %v", n, err)
	}

	ownCode, ownBody := do("victim", "victim")
	if ownCode != 200 || !strings.Contains(ownBody, `"start":1000`) {
		t.Fatalf("own lookup: %d %q", ownCode, ownBody)
	}
	code, body := do("friend", "victim")
	if code != 200 || body != ownBody {
		t.Errorf("shared lookup: %d %q, want 200 %q", code, body, ownBody)
	}
	if code, _ := do("attacker", "victim"); code != 403 {
		t.Errorf("unshared lookup: %d, want 403", code)
	}
	if code, _ := do("friend", "nobody"); code != 403 {
		t.Errorf("unknown target: %d, want 403", code)
	}
}
