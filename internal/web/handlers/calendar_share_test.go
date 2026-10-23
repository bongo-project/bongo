package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/mail"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

type shareEnv struct {
	h         *CalendarHandler
	calendars *store.CalendarStore
	calID     string
	fullID    string // calendar already holding 50 shares
	bobCalID  string // bob's own calendar
	now       time.Time
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

	bobCalID, err := calendars.Create("bob", &bongov1.Calendar{Name: "Bob"})
	if err != nil {
		t.Fatal(err)
	}

	h := NewCalendarHandler(events, calendars, nil)
	if withResolver {
		h.SetShareResolver(store.NewRecipientResolver(addrs, ps, nil))
	}
	e := &shareEnv{h: h, calendars: calendars, calID: calID, fullID: fullID, bobCalID: bobCalID,
		now: time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)}
	limiter := mail.NewRateLimiter(20)
	limiter.SetClock(func() time.Time { return e.now })
	h.SetShareLimiter(limiter)
	return e
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

// spend makes n share requests from caller that all resolve to the same
// harmless 400 (unknown address), to use up budget without changing state.
func (e *shareEnv) spend(t *testing.T, caller string, n int) {
	t.Helper()
	for i := range n {
		if rr := e.share(caller, e.calID, "nobody@d.test"); rr.Code != http.StatusBadRequest {
			t.Fatalf("spend %d: status = %d, want 400", i, rr.Code)
		}
	}
}

func TestShareCalendarRateLimitReached(t *testing.T) {
	e := newShareEnv(t, true)
	e.spend(t, "alice", 20)

	rr := e.share("alice", e.calID, "bob@d.test")
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("21st request: status = %d, want 429", rr.Code)
	}
	secs, err := strconv.Atoi(rr.Header().Get("Retry-After"))
	if err != nil || secs < 1 || secs > 3600 {
		t.Errorf("Retry-After = %q, want an integer in 1..3600", rr.Header().Get("Retry-After"))
	}
	if secs != 3600 {
		t.Errorf("Retry-After = %d, want 3600 (all entries logged at the same instant)", secs)
	}
	if got := e.shared(t, e.calID); len(got) != 0 {
		t.Errorf("SharedWith = %v, want empty", got)
	}
}

func TestShareCalendarEveryOutcomeCounts(t *testing.T) {
	e := newShareEnv(t, true)
	steps := []struct {
		cal, target string
		want        int
	}{
		{e.calID, "not an address", http.StatusBadRequest},
		{e.calID, "nobody@d.test", http.StatusBadRequest},
		{e.calID, "alice@d.test", http.StatusBadRequest},
		{e.fullID, "bob@d.test", http.StatusBadRequest},
		{e.calID, "bob@d.test", http.StatusOK},
		{e.calID, "bob@d.test", http.StatusOK}, // repeat is a no-op
		{e.calID, "", http.StatusBadRequest},
		{"no-such-calendar", "bob@d.test", http.StatusNotFound},
	}
	n := 0
	for _, s := range steps {
		if rr := e.share("alice", s.cal, s.target); rr.Code != s.want {
			t.Fatalf("request %d (%q): status = %d, want %d", n, s.target, rr.Code, s.want)
		}
		n++
	}
	e.spend(t, "alice", 20-n)
	if rr := e.share("alice", e.calID, "bob@d.test"); rr.Code != http.StatusTooManyRequests {
		t.Fatalf("21st request: status = %d, want 429", rr.Code)
	}
}

func TestShareCalendarRateLimitKeyedByUser(t *testing.T) {
	e := newShareEnv(t, true)
	e.spend(t, "alice", 20)
	if rr := e.share("alice", e.calID, "bob@d.test"); rr.Code != http.StatusTooManyRequests {
		t.Fatalf("alice: status = %d, want 429", rr.Code)
	}
	if rr := e.share("bob", e.bobCalID, "alice@d.test"); rr.Code != http.StatusOK {
		t.Fatalf("bob while alice limited: status = %d, want 200", rr.Code)
	}
	cal, err := e.calendars.Get("bob", e.bobCalID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cal.SharedWith) != 1 || cal.SharedWith[0] != "alice" {
		t.Errorf("bob SharedWith = %v, want [alice]", cal.SharedWith)
	}
}

func TestShareCalendarRateLimitWindow(t *testing.T) {
	t.Run("window passes", func(t *testing.T) {
		e := newShareEnv(t, true)
		e.spend(t, "alice", 20)
		e.now = e.now.Add(59*time.Minute + 59*time.Second)
		if rr := e.share("alice", e.calID, "bob@d.test"); rr.Code != http.StatusTooManyRequests {
			t.Fatalf("at 59m59s: status = %d, want 429", rr.Code)
		}
		e.now = e.now.Add(2 * time.Second) // 60m01s
		if rr := e.share("alice", e.calID, "bob@d.test"); rr.Code != http.StatusOK {
			t.Fatalf("at 60m01s: status = %d, want 200", rr.Code)
		}
		if got := e.shared(t, e.calID); len(got) != 1 || got[0] != "bob" {
			t.Errorf("SharedWith = %v, want [bob]", got)
		}
	})
	t.Run("rolling", func(t *testing.T) {
		e := newShareEnv(t, true)
		e.spend(t, "alice", 7) // t=0
		e.now = e.now.Add(30*time.Minute + 17*time.Second)
		e.spend(t, "alice", 13) // t=30m17s, budget now exhausted
		if rr := e.share("alice", e.calID, "bob@d.test"); rr.Code != http.StatusTooManyRequests {
			t.Fatalf("exhausted: status = %d, want 429", rr.Code)
		}
		// Only the first seven expire at 60m01s; the other thirteen stay.
		e.now = e.now.Add(29*time.Minute + 45*time.Second) // 60m02s from start
		e.spend(t, "alice", 7)
		if rr := e.share("alice", e.calID, "bob@d.test"); rr.Code != http.StatusTooManyRequests {
			t.Fatalf("after 7 freed slots used: status = %d, want 429", rr.Code)
		}
	})
}

func TestShareCalendarNilLimiterUnlimited(t *testing.T) {
	e := newShareEnv(t, true)
	e.h.SetShareLimiter(nil)
	e.spend(t, "alice", 25)
}
