package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

type shareEnv struct {
	h         *CalendarHandler
	calendars *store.CalendarStore
	calID     string
	fullID    string // calendar already holding 50 shares
}

func newShareEnv(t *testing.T, withResolver bool) *shareEnv {
	t.Helper()
	ps, err := store.NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	applier := &directApplierTags{ps: ps}
	events := store.NewEventStore(ps, applier)
	calendars := store.NewCalendarStore(ps, applier)
	addrs := store.NewAddressStore(ps, applier, nil)

	profile := func(id, email string, disabled bool) {
		data, _ := proto.Marshal(&bongov1.UserProfile{Email: email, Disabled: disabled})
		if err := ps.Set([]byte("/user/"+id+"/profile"), data); err != nil {
			t.Fatal(err)
		}
		if err := addrs.Put(email, &bongov1.AddressRecord{UserId: id, Domain: "d.test", Kind: "primary"}); err != nil {
			t.Fatal(err)
		}
	}
	alias := func(addr, id string) {
		if err := addrs.Put(addr, &bongov1.AddressRecord{UserId: id, Domain: "d.test", Kind: "alias"}); err != nil {
			t.Fatal(err)
		}
	}
	profile("alice", "alice@d.test", false)
	profile("bob", "bob@d.test", false)
	profile("carol", "carol@d.test", true)
	profile("dave", "dave@ca.test", false)
	alias("bob.alias@d.test", "bob")
	alias("alice.alias@d.test", "alice")
	if err := ps.Set(store.CatchAllKey("ca.test"), []byte("dave")); err != nil {
		t.Fatal(err)
	}

	calID, err := calendars.Create("alice", &bongov1.Calendar{Name: "Work"})
	if err != nil {
		t.Fatal(err)
	}
	var full []string
	for i := range 50 {
		full = append(full, "filler-"+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	fullID, err := calendars.Create("alice", &bongov1.Calendar{Name: "Full", SharedWith: full})
	if err != nil {
		t.Fatal(err)
	}

	h := NewCalendarHandler(events, calendars, nil)
	if withResolver {
		h.SetShareResolver(store.NewRecipientResolver(addrs, ps, nil))
	}
	return &shareEnv{h: h, calendars: calendars, calID: calID, fullID: fullID}
}

func (e *shareEnv) share(caller, calID, target string) *httptest.ResponseRecorder {
	form := url.Values{"share_with": {target}}
	req := httptest.NewRequest(http.MethodPost, "/calendar/"+calID+"/share", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("calendarID", calID)
	ctx := middleware.WithUserContext(req.Context(), caller, caller+"@d.test")
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	e.h.ShareCalendar(rr, req)
	return rr
}

func (e *shareEnv) shared(t *testing.T, calID string) []string {
	t.Helper()
	cal, err := e.calendars.Get("alice", calID)
	if err != nil {
		t.Fatal(err)
	}
	return cal.SharedWith
}

func TestShareCalendarValidatesTargets(t *testing.T) {
	e := newShareEnv(t, true)

	// Negative cases: byte-identical generic reply, nothing stored.
	var firstBody string
	for _, target := range []string{"nobody@d.test", "carol@d.test", "unknown@ca.test", "not an address", "alice@d.test", "ALICE.ALIAS@d.test"} {
		rr := e.share("alice", e.calID, target)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("share %q: status = %d, want 400", target, rr.Code)
		}
		if firstBody == "" {
			firstBody = rr.Body.String()
		} else if rr.Body.String() != firstBody {
			t.Errorf("share %q: body %q differs from %q", target, rr.Body.String(), firstBody)
		}
	}
	if got := e.shared(t, e.calID); len(got) != 0 {
		t.Fatalf("rejected shares stored: %v", got)
	}
	if rr := e.share("alice", e.calID, ""); rr.Code != http.StatusBadRequest {
		t.Errorf("empty: status = %d, want 400", rr.Code)
	}

	// Positive: mixed case and whitespace; stored value is the user ID.
	if rr := e.share("alice", e.calID, "  Bob@D.test "); rr.Code != http.StatusOK {
		t.Fatalf("share bob: status = %d body %q", rr.Code, rr.Body.String())
	}
	// His alias resolves to the same ID: deduplicated.
	if rr := e.share("alice", e.calID, "bob.alias@d.test"); rr.Code != http.StatusOK {
		t.Fatalf("share bob alias: status = %d", rr.Code)
	}
	if got := e.shared(t, e.calID); len(got) != 1 || got[0] != "bob" {
		t.Fatalf("SharedWith = %v, want [bob]", got)
	}

	// FreeBusy: bob allowed, an unshared user forbidden.
	fb := func(caller string) int {
		req := httptest.NewRequest("GET", "/calendar/freebusy?user=alice&start=0&end=9999999999", nil)
		req = req.WithContext(middleware.WithUserContext(req.Context(), caller, caller+"@d.test"))
		rr := httptest.NewRecorder()
		e.h.FreeBusy(rr, req)
		return rr.Code
	}
	if c := fb("bob"); c != http.StatusOK {
		t.Errorf("freebusy as bob = %d, want 200", c)
	}
	if c := fb("carol"); c != http.StatusForbidden {
		t.Errorf("freebusy as carol = %d, want 403", c)
	}
}

func TestShareCalendarCap(t *testing.T) {
	e := newShareEnv(t, true)
	if got := len(e.shared(t, e.fullID)); got != 50 {
		t.Fatalf("fixture has %d shares, want 50", got)
	}
	if rr := e.share("alice", e.fullID, "bob@d.test"); rr.Code != http.StatusBadRequest {
		t.Errorf("51st share: status = %d, want 400", rr.Code)
	}
	if got := len(e.shared(t, e.fullID)); got != 50 {
		t.Errorf("shares = %d after refused 51st, want 50", got)
	}
	// Control: the same target is accepted on a calendar below the cap.
	if rr := e.share("alice", e.calID, "bob@d.test"); rr.Code != http.StatusOK {
		t.Errorf("share below cap: status = %d, want 200", rr.Code)
	}
}

func TestShareCalendarWithoutResolverUnavailable(t *testing.T) {
	e := newShareEnv(t, false)
	if rr := e.share("alice", e.calID, "bob@d.test"); rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rr.Code)
	}
	if got := e.shared(t, e.calID); len(got) != 0 {
		t.Errorf("stored without resolver: %v", got)
	}
}
