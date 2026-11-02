package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A pending grant is absent from every view and free/busy; accepting makes it
// appear.
func TestPendingShareAppearsOnlyAfterAccept(t *testing.T) {
	for _, view := range []string{"month", "week", "day", "agenda"} {
		t.Run(view, func(t *testing.T) {
			e := newSharedEnv(t)
			if strings.Contains(viewBody(t, e, actPending, view), "Team standup") {
				t.Fatal("pending share is shown")
			}
			if err := e.acl.AcceptCalendarGrant(sharedOwner, sharedCalA, actPending, time.Unix(sharedEvAWhn, 0)); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(viewBody(t, e, actPending, view), "Team standup") {
				t.Error("accepted share is not shown")
			}
		})
	}
	e := newSharedEnv(t)
	fb := func() int {
		req := withRoute(httptest.NewRequest("GET", "/calendar/freebusy?user=alice&start=0&end=9999999999", nil), actPending, nil)
		rr := httptest.NewRecorder()
		e.h.FreeBusy(rr, req)
		return rr.Code
	}
	if got := fb(); got != http.StatusForbidden {
		t.Fatalf("free/busy while pending = %d", got)
	}
	if err := e.acl.AcceptCalendarGrant(sharedOwner, sharedCalA, actPending, time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	if got := fb(); got != http.StatusOK {
		t.Errorf("free/busy after accept = %d", got)
	}
}
