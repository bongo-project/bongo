package handlers

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/templates"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// scopedEditVia posts a scoped edit whose calendar_id is the key the real form
// carries (the shared key), not the bare calendar ID.
func (e *sharedEnv) scopedEditVia(actor, guid, rid, scope, formCal string) *httptest.ResponseRecorder {
	f := sEventForm(formCal)
	f.Set("owner", sharedOwner)
	f.Set("rrule", "FREQ=WEEKLY")
	f.Set("occurrence", rid)
	f.Set("scope", scope)
	f.Set("title", "Edited series")
	w := httptest.NewRecorder()
	e.h.UpdateEvent(w, withRoute(sFormReq("POST", "/calendar/event/"+guid, f), actor, map[string]string{"guid": guid}))
	return w
}

// otherEvent returns the owner's event whose guid is not in skip.
func (e *sharedEnv) otherEvent(skip ...string) *bongov1.Event {
	e.t.Helper()
	all, _, err := e.events.ListAll(sharedOwner, 0, 0)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, ev := range all {
		if !slices.Contains(skip, ev.Guid) {
			return ev
		}
	}
	return nil
}

func TestSharedScopedEditKeepsRealCalendarID(t *testing.T) {
	sharedKey := templates.SharedKey(sharedOwner, sharedCalA)
	for _, scope := range []string{"this", "following", "all"} {
		t.Run(scope, func(t *testing.T) {
			e := newSharedEnv(t)
			guid, rid := sharedWeekly(e)
			if w := e.scopedEditVia(actWrite, guid, rid, scope, sharedKey); w.Code != http.StatusSeeOther {
				t.Fatalf("edit = %d: %s", w.Code, w.Body.String())
			}
			if n := e.eventCount(actWrite); n != 0 {
				t.Errorf("grantee's own key space holds %d events", n)
			}
			orig := e.event(sharedOwner, guid)
			if orig.CalendarId != sharedCalA {
				t.Errorf("original CalendarId = %q, want %q", orig.CalendarId, sharedCalA)
			}
			if scope != "following" {
				return
			}
			if n := e.eventCount(sharedOwner); n != 4 { // ev-a, ev-b, original, new
				t.Errorf("owner holds %d events, want 4", n)
			}
			if !strings.Contains(orig.Rrule, "UNTIL") {
				t.Errorf("original lacks UNTIL: %q", orig.Rrule)
			}
			ns := e.otherEvent(guid, sharedEvA, sharedEvB)
			if ns == nil || ns.CalendarId != sharedCalA {
				t.Errorf("new series = %+v, want CalendarId %q", ns, sharedCalA)
			}
		})
	}
}

func TestFollowingEditOwnSharedKey(t *testing.T) {
	e := newSharedEnv(t)
	e.mustCal(actWrite, &bongov1.Calendar{Guid: "mine", Name: "Mine"})
	if _, err := e.events.Create(actWrite, &bongov1.Event{
		Guid: "own-weekly", CalendarId: "mine", Title: "Own", Rrule: "FREQ=WEEKLY",
		StartTime: sharedEvAWhn, EndTime: sharedEvAWhn + 3600,
	}); err != nil {
		t.Fatal(err)
	}
	rid := time.Unix(sharedEvAWhn+7*86400, 0).UTC().Format("20060102T150405Z")
	f := sEventForm(templates.SharedKey(actWrite, "mine"))
	f.Set("rrule", "FREQ=WEEKLY")
	f.Set("occurrence", rid)
	f.Set("scope", "following")
	w := httptest.NewRecorder()
	e.h.UpdateEvent(w, withRoute(sFormReq("POST", "/calendar/event/own-weekly", f), actWrite, map[string]string{"guid": "own-weekly"}))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("edit = %d: %s", w.Code, w.Body.String())
	}
	all, _, err := e.events.ListAll(actWrite, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("actor holds %d events, want 2", len(all))
	}
	for _, ev := range all {
		if ev.CalendarId != "mine" {
			t.Errorf("%s CalendarId = %q, want mine", ev.Guid, ev.CalendarId)
		}
	}
}

func TestSharedFollowingEditMove(t *testing.T) {
	destKey := templates.SharedKey(sharedOwner, sharedCalB)
	t.Run("write on destination", func(t *testing.T) {
		e := newSharedEnv(t)
		if err := e.acl.SetCalendarGrant(sharedOwner, sharedCalB, actWrite, store.GrantLevelWrite, store.GrantStateAccepted, sharedOwner, time.Unix(1, 0)); err != nil {
			t.Fatal(err)
		}
		guid, rid := sharedWeekly(e)
		if w := e.scopedEditVia(actWrite, guid, rid, "following", destKey); w.Code != http.StatusSeeOther {
			t.Fatalf("edit = %d: %s", w.Code, w.Body.String())
		}
		ns := e.otherEvent(guid, sharedEvA, sharedEvB)
		if ns == nil || ns.CalendarId != sharedCalB {
			t.Errorf("new series = %+v, want CalendarId %q", ns, sharedCalB)
		}
	})
	for _, level := range []string{store.GrantLevelRead, ""} {
		t.Run("refused with grant "+level, func(t *testing.T) {
			e := newSharedEnv(t)
			if level != "" {
				if err := e.acl.SetCalendarGrant(sharedOwner, sharedCalB, actWrite, level, store.GrantStateAccepted, sharedOwner, time.Unix(1, 0)); err != nil {
					t.Fatal(err)
				}
			}
			guid, rid := sharedWeekly(e)
			before := e.event(sharedOwner, guid)
			w := e.scopedEditVia(actWrite, guid, rid, "following", destKey)
			if w.Code != http.StatusForbidden && w.Code != http.StatusNotFound {
				t.Errorf("edit = %d, want 403 or 404", w.Code)
			}
			if e.app.writes != 0 {
				t.Errorf("refused request wrote %d commands", e.app.writes)
			}
			if after := e.event(sharedOwner, guid); !proto.Equal(before, after) || strings.Contains(after.Rrule, "UNTIL") {
				t.Error("original series changed")
			}
			if e.otherEvent(guid, sharedEvA, sharedEvB) != nil {
				t.Error("a new series was created")
			}
		})
	}
}
