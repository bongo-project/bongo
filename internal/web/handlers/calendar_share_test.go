package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
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
	ps        *store.PebbleStore
	calendars *store.CalendarStore
	acl       *store.ACLStore
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
	acl := store.NewACLStore(ps, applier)
	fullID, err := calendars.Create("alice", &bongov1.Calendar{Name: "Full"})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 50 {
		filler := "filler-" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		if err := acl.SetCalendarGrant("alice", fullID, filler, store.GrantLevelFreeBusy, store.GrantStateAccepted, "alice", time.Unix(1, 0)); err != nil {
			t.Fatal(err)
		}
	}

	bobCalID, err := calendars.Create("bob", &bongov1.Calendar{Name: "Bob"})
	if err != nil {
		t.Fatal(err)
	}

	h := NewCalendarHandler(events, calendars, nil)
	h.SetACL(acl)
	if withResolver {
		h.SetShareResolver(store.NewRecipientResolver(addrs, ps, nil))
	}
	e := &shareEnv{h: h, ps: ps, calendars: calendars, acl: acl, calID: calID, fullID: fullID, bobCalID: bobCalID,
		now: time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)}
	limiter := mail.NewRateLimiter(20)
	limiter.SetClock(func() time.Time { return e.now })
	h.SetShareLimiter(limiter)
	return e
}

func (e *shareEnv) share(caller, calID, target string) *httptest.ResponseRecorder {
	return e.shareLevel(caller, calID, target, "")
}

func (e *shareEnv) shareLevel(caller, calID, target, level string) *httptest.ResponseRecorder {
	form := url.Values{"share_with": {target}}
	if level != "" {
		form.Set("level", level)
	}
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

// sharedBy lists the grantees of owner's calendar, sorted.
func (e *shareEnv) sharedBy(t *testing.T, owner, calID string) []string {
	t.Helper()
	refs, err := e.acl.ListGrantRefs()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, r := range refs {
		if r.Owner == owner && r.Resource == calID {
			out = append(out, r.Grantee)
		}
	}
	slices.Sort(out)
	return out
}

func (e *shareEnv) shared(t *testing.T, calID string) []string {
	t.Helper()
	return e.sharedBy(t, "alice", calID)
}

func (e *shareEnv) freeBusy(caller, target string) int {
	req := httptest.NewRequest("GET", "/calendar/freebusy?user="+target+"&start=0&end=9999999999", nil)
	req = req.WithContext(middleware.WithUserContext(req.Context(), caller, caller+"@d.test"))
	rr := httptest.NewRecorder()
	e.h.FreeBusy(rr, req)
	return rr.Code
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
		t.Fatalf("grantees = %v, want [bob]", got)
	}
	g, err := e.acl.GetCalendarGrant("alice", e.calID, "bob")
	if err != nil || g == nil {
		t.Fatalf("grant = %v, %v", g, err)
	}
	if g.Level != store.GrantLevelFreeBusy || g.State != store.GrantStateAccepted || g.GrantedBy != "alice" {
		t.Errorf("default grant = %+v, want freebusy/accepted/granted_by alice", g)
	}

	// FreeBusy: bob allowed, an unshared user forbidden.
	if c := e.freeBusy("bob", "alice"); c != http.StatusOK {
		t.Errorf("freebusy as bob = %d, want 200", c)
	}
	if c := e.freeBusy("carol", "alice"); c != http.StatusForbidden {
		t.Errorf("freebusy as carol = %d, want 403", c)
	}
}

func TestShareCalendarLevels(t *testing.T) {
	e := newShareEnv(t, true)
	for _, level := range []string{store.GrantLevelRead, store.GrantLevelWrite} {
		if rr := e.shareLevel("alice", e.calID, "bob@d.test", level); rr.Code != http.StatusOK {
			t.Fatalf("share at %s: status = %d", level, rr.Code)
		}
		g, _ := e.acl.GetCalendarGrant("alice", e.calID, "bob")
		if g == nil || g.Level != level {
			t.Fatalf("grant after %s share = %+v", level, g)
		}
		// Already shared at this level: a repeat is a 200 and writes nothing.
		// A store that refuses every write proves no write was attempted.
		live := e.h.acl
		e.h.acl = store.NewACLStore(e.ps, failingApplier{})
		rr := e.shareLevel("alice", e.calID, "bob@d.test", level)
		e.h.acl = live
		if rr.Code != http.StatusOK {
			t.Fatalf("repeat: status = %d, want 200 with no write", rr.Code)
		}
		if err := e.acl.RevokeCalendarGrant("alice", e.calID, "bob"); err != nil {
			t.Fatal(err)
		}
	}

	// Anything else is refused with nothing stored, and the refusal does not
	// depend on whether the address exists (the level is checked first).
	var first string
	for _, level := range []string{"admin", "READ", "owner", "freebusy,write", "pending"} {
		for _, target := range []string{"bob@d.test", "nobody@d.test", "not an address"} {
			rr := e.shareLevel("alice", e.calID, target, level)
			if rr.Code != http.StatusBadRequest {
				t.Errorf("level %q target %q: status = %d, want 400", level, target, rr.Code)
			}
			if first == "" {
				first = rr.Body.String()
			} else if rr.Body.String() != first {
				t.Errorf("level %q target %q: body %q differs from %q", level, target, rr.Body.String(), first)
			}
		}
	}
	if got := e.shared(t, e.calID); len(got) != 0 {
		t.Errorf("invalid level stored a grant: %v", got)
	}
}

func TestShareCalendarWriteFailureIs500(t *testing.T) {
	e := newShareEnv(t, true)
	e.h.acl = store.NewACLStore(e.ps, failingApplier{})
	if rr := e.share("alice", e.calID, "bob@d.test"); rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}
	if got := e.shared(t, e.calID); len(got) != 0 {
		t.Errorf("grant stored despite failure: %v", got)
	}
}

func TestFreeBusyNeedsAnAcceptedGrantOnALiveCalendar(t *testing.T) {
	e := newShareEnv(t, true)
	grant := func(who, level, state string) {
		t.Helper()
		if err := e.acl.SetCalendarGrant("alice", e.calID, who, level, state, "alice", e.now); err != nil {
			t.Fatal(err)
		}
	}
	grant("bob", store.GrantLevelWrite, store.GrantStatePending)
	if c := e.freeBusy("bob", "alice"); c != http.StatusForbidden {
		t.Errorf("pending write grant: freebusy = %d, want 403", c)
	}
	for _, level := range []string{store.GrantLevelFreeBusy, store.GrantLevelRead, store.GrantLevelWrite} {
		grant("bob", level, store.GrantStateAccepted)
		if c := e.freeBusy("bob", "alice"); c != http.StatusOK {
			t.Errorf("accepted %s grant: freebusy = %d, want 200", level, c)
		}
	}
	// A grant on a calendar that no longer exists authorises nothing.
	if err := e.ps.Delete([]byte("/user/alice/calendar/" + e.calID)); err != nil {
		t.Fatal(err)
	}
	if c := e.freeBusy("bob", "alice"); c != http.StatusForbidden {
		t.Errorf("grant on deleted calendar: freebusy = %d, want 403", c)
	}
	// The grants belong to alice: they do not open bob's schedule to alice.
	if c := e.freeBusy("alice", "bob"); c != http.StatusForbidden {
		t.Errorf("reverse direction: freebusy = %d, want 403", c)
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
		t.Errorf("grantees = %v, want empty", got)
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
	if got := e.sharedBy(t, "bob", e.bobCalID); len(got) != 1 || got[0] != "alice" {
		t.Errorf("bob grantees = %v, want [alice]", got)
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
			t.Errorf("grantees = %v, want [bob]", got)
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

func (e *shareEnv) unshare(caller, calID, entry string) *httptest.ResponseRecorder {
	form := url.Values{"share_with": {entry}}
	req := httptest.NewRequest(http.MethodPost, "/calendar/"+calID+"/unshare", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("calendarID", calID)
	ctx := middleware.WithUserContext(req.Context(), caller, caller+"@d.test")
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	e.h.UnshareCalendar(rr, req)
	return rr
}

func TestUnshareCalendar(t *testing.T) {
	tests := []struct {
		name   string
		seed   []string
		entry  string
		want   []string
		status int
		write  bool
	}{
		{"user id", []string{"bob", "dave"}, "bob", []string{"dave"}, http.StatusOK, true},
		{"absent entry", []string{"bob"}, "dave", []string{"bob"}, http.StatusOK, false},
		{"substring is not a match", []string{"bob@d.test"}, "bob", []string{"bob@d.test"}, http.StatusOK, false},
		{"case differs", []string{"bob"}, "BOB", []string{"bob"}, http.StatusOK, false},
		{"whitespace variant is not the grant", []string{"bob"}, " bob ", []string{"bob"}, http.StatusOK, false},
		{"empty entry", []string{"bob"}, "", []string{"bob"}, http.StatusBadRequest, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newShareEnv(t, true)
			for _, who := range tt.seed {
				if err := e.acl.SetCalendarGrant("alice", e.calID, who, store.GrantLevelRead, store.GrantStateAccepted, "alice", e.now); err != nil {
					t.Fatal(err)
				}
			}
			rr := e.unshare("alice", e.calID, tt.entry)
			if rr.Code != tt.status {
				t.Fatalf("status = %d, want %d (%s)", rr.Code, tt.status, rr.Body.String())
			}
			wantTrigger := ""
			if tt.write {
				wantTrigger = "calendarRefresh"
			}
			if got := rr.Header().Get("HX-Trigger"); got != wantTrigger {
				t.Errorf("HX-Trigger = %q, want %q", got, wantTrigger)
			}
			got := e.shared(t, e.calID)
			slices.Sort(tt.want)
			if !slices.Equal(got, tt.want) {
				t.Errorf("grantees = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestUnshareCalendarForeignCalendar(t *testing.T) {
	e := newShareEnv(t, true)
	before := e.shared(t, e.fullID)
	// bob targets alice's calendar ID: the key is scoped to the caller.
	if rr := e.unshare("bob", e.fullID, before[0]); rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
	if got := e.shared(t, e.fullID); !slices.Equal(got, before) {
		t.Errorf("owner's list changed: %q", got)
	}
}

func TestUnshareCalendarWriteFailure(t *testing.T) {
	e := newShareEnv(t, true)
	if rr := e.share("alice", e.calID, "bob@d.test"); rr.Code != http.StatusOK {
		t.Fatalf("share = %d", rr.Code)
	}
	e.h.acl = store.NewACLStore(e.ps, failingApplier{})
	if rr := e.unshare("alice", e.calID, "bob"); rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}
	if got := e.shared(t, e.calID); len(got) != 1 {
		t.Errorf("grant lost despite failed revoke: %v", got)
	}
}

type failingApplier struct{}

func (failingApplier) ApplyCommand(*bongov1.RaftCommand, time.Duration) error {
	return errors.New("raft down")
}

// Sharing again at another level changes the level and reports success only
// because it did; the original creation time and granter stay.
func TestShareCalendarChangesLevelOfExistingGrant(t *testing.T) {
	e := newShareEnv(t, true)
	created := e.now.Add(-48 * time.Hour)
	if err := e.acl.SetCalendarGrant("alice", e.calID, "bob", store.GrantLevelRead, store.GrantStateAccepted, "alice", created); err != nil {
		t.Fatal(err)
	}
	if rr := e.shareLevel("alice", e.calID, "bob@d.test", store.GrantLevelWrite); rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	g, _ := e.acl.GetCalendarGrant("alice", e.calID, "bob")
	if g == nil || g.Level != store.GrantLevelWrite {
		t.Fatalf("level after change = %+v, want write", g)
	}
	if g.CreatedAt != created.Unix() || g.GrantedBy != "alice" {
		t.Errorf("created_at/granted_by not kept: %+v", g)
	}
	if got := e.shared(t, e.calID); len(got) != 1 {
		t.Errorf("grants = %v, want one", got)
	}

	// Same level: no write, so a failing store still answers 200. A different
	// level must write, so the same failing store answers 500 rather than
	// reporting a change that did not happen.
	e.h.acl = store.NewACLStore(e.ps, failingApplier{})
	if rr := e.shareLevel("alice", e.calID, "bob@d.test", store.GrantLevelWrite); rr.Code != http.StatusOK {
		t.Errorf("same level: status = %d, want 200 with no write", rr.Code)
	}
	if rr := e.shareLevel("alice", e.calID, "bob@d.test", store.GrantLevelRead); rr.Code != http.StatusInternalServerError {
		t.Errorf("level change with a failing store: status = %d, want 500", rr.Code)
	}
}

// On a calendar at the cap an existing grantee is not a 51st: their level can
// change, while a new grantee is still refused.
func TestShareCalendarCapDoesNotCountExistingGrantee(t *testing.T) {
	e := newShareEnv(t, true)
	// bob is a new grantee on a full calendar: refused.
	if rr := e.shareLevel("alice", e.fullID, "bob@d.test", store.GrantLevelWrite); rr.Code != http.StatusBadRequest {
		t.Fatalf("new grantee at the cap: status = %d, want 400", rr.Code)
	}
	// Make bob an existing grantee while the calendar stays at exactly the cap.
	if err := e.acl.SetCalendarGrant("alice", e.fullID, "bob", store.GrantLevelFreeBusy, store.GrantStateAccepted, "alice", e.now); err != nil {
		t.Fatal(err)
	}
	if err := e.acl.RevokeCalendarGrant("alice", e.fullID, "filler-aa"); err != nil {
		t.Fatal(err)
	}
	if got := len(e.shared(t, e.fullID)); got != maxCalendarShares {
		t.Fatalf("fixture has %d grants, want %d", got, maxCalendarShares)
	}
	if rr := e.shareLevel("alice", e.fullID, "bob@d.test", store.GrantLevelWrite); rr.Code != http.StatusOK {
		t.Fatalf("existing grantee at the cap: status = %d, want 200", rr.Code)
	}
	if g, _ := e.acl.GetCalendarGrant("alice", e.fullID, "bob"); g == nil || g.Level != store.GrantLevelWrite {
		t.Errorf("level not changed at the cap: %+v", g)
	}
	if got := len(e.shared(t, e.fullID)); got != maxCalendarShares {
		t.Errorf("grants = %d, want %d", got, maxCalendarShares)
	}
}
