package handlers

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	davcal "github.com/emersion/go-webdav/caldav"

	"github.com/bongo-project/bongo/internal/caldav"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const shareTTL = time.Duration(store.PendingGrantTTL) * time.Second

// clockAt pins the handler clock to the fixture's now.
func (e *inviteEnv) clockAt(t time.Time) { e.h.now = func() time.Time { return t } }

// seedPending writes a pending grant for bob stamped at the given time.
func (e *inviteEnv) seedPending(t *testing.T, calID string, at time.Time) {
	t.Helper()
	if err := e.acl.SetCalendarGrant("alice", calID, "bob", store.GrantLevelFreeBusy, store.GrantStatePending, "alice", at); err != nil {
		t.Fatal(err)
	}
}

func (e *inviteEnv) invitationsPage(who string) string {
	rr := httptest.NewRecorder()
	e.h.ShareInvitations(rr, withRoute(httptest.NewRequest("GET", "/calendar/shares", nil), who, nil))
	return rr.Body.String()
}

func TestExpiredPendingShareIsInvisibleAndNotAcceptable(t *testing.T) {
	for _, tc := range []struct {
		name    string
		age     time.Duration
		visible bool
	}{
		{"at the limit", shareTTL, true},
		{"one second past", shareTTL + time.Second, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newInviteEnv(t)
			e.clockAt(e.now)
			e.seedPending(t, e.calID, e.now.Add(-tc.age))
			if got := strings.Contains(e.invitationsPage("bob"), `hx-post="/calendar/shares/accept"`); got != tc.visible {
				t.Errorf("listed = %v, want %v", got, tc.visible)
			}
			rr := e.answer(true, "bob", e.form())
			if tc.visible {
				if rr.Code != http.StatusOK {
					t.Errorf("accept = %d, want 200", rr.Code)
				}
				return
			}
			if rr.Code != http.StatusNotFound {
				t.Errorf("accept = %d, want 404", rr.Code)
			}
			if got := e.grantState("alice", e.calID, "bob"); got != store.GrantStatePending {
				t.Errorf("state = %q, want pending", got)
			}
			if rr := e.answer(false, "bob", e.form()); rr.Code != http.StatusNotFound {
				t.Errorf("decline = %d, want 404", rr.Code)
			}
		})
	}
}

// An expired grant that slips past the handler's check is still refused by the
// FSM, which judges by the command's own timestamp.
func TestAcceptRaceWithExpiryIsRefusedByFSM(t *testing.T) {
	e := newInviteEnv(t)
	e.seedPending(t, e.calID, e.now.Add(-shareTTL-time.Hour))
	if err := e.acl.AcceptCalendarGrant("alice", e.calID, "bob", e.now); err != nil {
		t.Fatal(err)
	}
	if got := e.grantState("alice", e.calID, "bob"); got != store.GrantStatePending {
		t.Errorf("state = %q, want pending", got)
	}
}

func TestResharingRefreshesExpiredPendingGrant(t *testing.T) {
	e := newInviteEnv(t)
	e.clockAt(e.now)
	e.seedPending(t, e.calID, e.now.Add(-shareTTL-time.Hour))
	// Same level (the default is free/busy): this must still write.
	if rr := e.share("alice", e.calID, "bob@d.test"); rr.Code != http.StatusOK {
		t.Fatalf("re-share = %d %s", rr.Code, rr.Body.String())
	}
	g, _ := e.acl.GetCalendarGrant("alice", e.calID, "bob")
	if g.UpdatedAt != e.now.Unix() || g.State != store.GrantStatePending {
		t.Fatalf("grant after re-share = %+v", g)
	}
	e.drain()
	if e.sender.count() != 1 {
		t.Errorf("invitations = %d, want 1", e.sender.count())
	}
	if rr := e.answer(true, "bob", e.form()); rr.Code != http.StatusOK {
		t.Errorf("accept after re-share = %d", rr.Code)
	}
	// A live grant at the same level still writes and sends nothing.
	e.seedPending(t, e.fullID2(t), e.now.Add(-time.Hour))
	before := e.sender.count()
	e.share("alice", e.calID, "bob@d.test")
	e.drain()
	if e.sender.count() != before {
		t.Error("re-share of a live grant sent mail")
	}
}

// fullID2 makes a second calendar for alice, so a test can seed another grant.
func (e *inviteEnv) fullID2(t *testing.T) string {
	t.Helper()
	id, err := e.calendars.Create("alice", &bongov1.Calendar{Name: "Spare"})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// Fifty grants of which one is an expired pending grant leave room for a new
// grantee; fifty live ones do not.
func TestExpiredPendingGrantsDoNotCountTowardsCap(t *testing.T) {
	for _, tc := range []struct {
		name    string
		expired int
		want    int
	}{
		{"one expired frees a slot", 1, http.StatusOK},
		{"none expired is full", 0, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newInviteEnv(t)
			e.clockAt(e.now)
			calID := e.fullID2(t)
			for i := range maxCalendarShares {
				state, at := store.GrantStateAccepted, e.now.Add(-2*shareTTL)
				if i < tc.expired {
					state = store.GrantStatePending
				} else if i%2 == 0 {
					state, at = store.GrantStatePending, e.now.Add(-time.Hour)
				}
				who := "filler-" + string(rune('a'+i%26)) + string(rune('a'+i/26))
				if err := e.acl.SetCalendarGrant("alice", calID, who, store.GrantLevelFreeBusy, state, "alice", at); err != nil {
					t.Fatal(err)
				}
				if i < tc.expired {
					if err := e.acl.SetCalendarGrant("alice", calID, who, store.GrantLevelFreeBusy, store.GrantStatePending, "alice", e.now.Add(-shareTTL-time.Minute)); err != nil {
						t.Fatal(err)
					}
				}
			}
			if rr := e.share("alice", calID, "bob@d.test"); rr.Code != tc.want {
				t.Errorf("share = %d %s, want %d", rr.Code, rr.Body.String(), tc.want)
			}
		})
	}
}

func (e *inviteEnv) leave(caller string, form url.Values) *httptest.ResponseRecorder {
	req := withRoute(sFormReq(http.MethodPost, "/calendar/shares/leave", form), caller, nil)
	rr := httptest.NewRecorder()
	e.h.LeaveShare(rr, req)
	return rr
}

func TestLeaveShare(t *testing.T) {
	accepted := func(e *inviteEnv, who string) {
		if err := e.acl.SetCalendarGrant("alice", e.calID, who, store.GrantLevelRead, store.GrantStateAccepted, "alice", e.now); err != nil {
			panic(err)
		}
	}
	t.Run("removes only the session user's accepted grant", func(t *testing.T) {
		e := newInviteEnv(t)
		accepted(e, "bob")
		accepted(e, "carol")
		if rr := e.leave("bob", e.form()); rr.Code != http.StatusOK {
			t.Fatalf("leave = %d %s", rr.Code, rr.Body.String())
		} else if rr.Header().Get("HX-Trigger") != "calendarRefresh" {
			t.Errorf("HX-Trigger = %q", rr.Header().Get("HX-Trigger"))
		}
		if e.grantState("alice", e.calID, "bob") != "" {
			t.Error("bob's grant survives")
		}
		if e.grantState("alice", e.calID, "carol") != store.GrantStateAccepted {
			t.Error("carol's grant was removed")
		}
	})
	t.Run("grantee field is ignored", func(t *testing.T) {
		e := newInviteEnv(t)
		accepted(e, "bob")
		// carol holds nothing; the form names bob as grantee and user.
		f := e.form()
		f.Set("grantee", "bob")
		f.Set("user", "bob")
		f.Set("share_with", "bob")
		if rr := e.leave("carol", f); rr.Code != http.StatusNotFound {
			t.Errorf("carol = %d, want 404", rr.Code)
		}
		if e.grantState("alice", e.calID, "bob") != store.GrantStateAccepted {
			t.Error("bob's grant was removed by carol's request")
		}
	})
	t.Run("pending grant is 404 and stays", func(t *testing.T) {
		e := newInviteEnv(t)
		e.clockAt(e.now)
		e.seedPending(t, e.calID, e.now)
		if rr := e.leave("bob", e.form()); rr.Code != http.StatusNotFound {
			t.Errorf("leave pending = %d, want 404", rr.Code)
		}
		if e.grantState("alice", e.calID, "bob") != store.GrantStatePending {
			t.Error("pending grant removed")
		}
	})
	t.Run("malformed is 404", func(t *testing.T) {
		e := newInviteEnv(t)
		accepted(e, "bob")
		for _, f := range []url.Values{{}, {"owner": {"alice"}}, {"owner": {"alice/x"}, "calendar": {e.calID}}} {
			if rr := e.leave("bob", f); rr.Code != http.StatusNotFound {
				t.Errorf("%v = %d", f, rr.Code)
			}
		}
		if e.grantState("alice", e.calID, "bob") != store.GrantStateAccepted {
			t.Error("grant removed by a malformed request")
		}
	})
	t.Run("write failure is 500", func(t *testing.T) {
		e := newInviteEnv(t)
		accepted(e, "bob")
		e.h.acl = store.NewACLStore(e.ps, failingApplier{})
		if rr := e.leave("bob", e.form()); rr.Code != http.StatusInternalServerError {
			t.Errorf("leave = %d, want 500", rr.Code)
		}
	})
	t.Run("page lists accepted shares with a leave form", func(t *testing.T) {
		e := newInviteEnv(t)
		accepted(e, "bob")
		body := e.invitationsPage("bob")
		for _, want := range []string{`hx-post="/calendar/shares/leave"`, "alice@d.test", "Work", "Leave", `name="_csrf"`} {
			if !strings.Contains(body, want) {
				t.Errorf("page lacks %q", want)
			}
		}
		if strings.Contains(e.invitationsPage("carol"), "/calendar/shares/leave") {
			t.Error("another user sees bob's leave form")
		}
	})
}

// davHome answers a Depth 1 PROPFIND on user's CalDAV calendar home, served
// by a CalDAV backend over the same stores the web handlers write.
func davHome(t *testing.T, e *sharedEnv, user string) string {
	t.Helper()
	b := caldav.NewBackend(e.events, e.cals, slog.New(slog.NewTextHandler(io.Discard, nil)))
	b.SetGrants(e.acl)
	return davHomeOf(t, b, user)
}

// davHomeOf runs PROPFIND Depth 1 on user's CalDAV calendar home through b.
func davHomeOf(t *testing.T, b *caldav.Backend, user string) string {
	t.Helper()
	req := httptest.NewRequest("PROPFIND", "/caldav/"+user+"/calendars/",
		strings.NewReader(`<propfind xmlns="DAV:"><prop><displayname/></prop></propfind>`))
	req.Header.Set("Depth", "1")
	req.Header.Set("Content-Type", "application/xml")
	rr := httptest.NewRecorder()
	(&davcal.Handler{Backend: b, Prefix: "/caldav"}).ServeHTTP(rr, req.WithContext(caldav.WithUserID(req.Context(), user)))
	if rr.Code != http.StatusMultiStatus {
		t.Fatalf("PROPFIND home as %s = %d: %s", user, rr.Code, rr.Body.String())
	}
	return rr.Body.String()
}

// After leaving, the calendar is gone from the grantee's views, including the
// CalDAV calendar home, and an event in it is refused; the owner and the other
// grantees keep theirs.
func TestLeaveRemovesSharedCalendarFromViews(t *testing.T) {
	e := newSharedEnv(t)
	sharedSeg := "/calendars/shared-" + sharedOwner + "-" + sharedCalA + "/"
	if !strings.Contains(viewBody(t, e, actRead, "month"), "Team standup") {
		t.Fatal("fixture: shared event not shown before leaving")
	}
	if !strings.Contains(davHome(t, e, actRead), sharedSeg) {
		t.Fatal("fixture: shared collection not in the CalDAV home before leaving")
	}
	form := url.Values{"owner": {sharedOwner}, "calendar": {sharedCalA}}
	rr := httptest.NewRecorder()
	e.h.LeaveShare(rr, withRoute(sFormReq(http.MethodPost, "/calendar/shares/leave", form), actRead, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("leave = %d %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(viewBody(t, e, actRead, "month"), "Team standup") {
		t.Error("left calendar still in the month view")
	}
	if home := davHome(t, e, actRead); strings.Contains(home, sharedSeg) {
		t.Errorf("left calendar still in the CalDAV home listing:\n%s", home)
	}
	if !strings.Contains(davHome(t, e, actWrite), sharedSeg) {
		t.Error("another grantee lost the calendar from the CalDAV home")
	}
	w := httptest.NewRecorder()
	e.h.ShowEvent(w, withRoute(httptest.NewRequest("GET", "/calendar/event/"+sharedEvA+"?owner="+sharedOwner, nil), actRead, map[string]string{"guid": sharedEvA}))
	if w.Code != http.StatusNotFound {
		t.Errorf("event after leaving = %d, want 404", w.Code)
	}
	if !strings.Contains(viewBody(t, e, actWrite, "month"), "Team standup") {
		t.Error("another grantee lost the calendar")
	}
}
