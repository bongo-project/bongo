package agent

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/handlers"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const pruneLogMsg = "startup: removed legacy calendar share entries"

// seedFullSharedCalendar writes real profiles for 40 share targets, a
// calendar owned by "owner" holding those 40 plus 10 legacy strings, and
// returns the calendar ID.
func seedFullSharedCalendar(t *testing.T, ps *store.PebbleStore) string {
	t.Helper()
	var entries []string
	for i := range 40 {
		id := fmt.Sprintf("user%02d", i)
		b, err := proto.Marshal(&bongov1.UserProfile{Email: id + "@d.test"})
		if err != nil {
			t.Fatal(err)
		}
		if err := ps.Set([]byte("/user/"+id+"/profile"), b); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, id)
	}
	for i := range 10 {
		entries = append(entries, fmt.Sprintf("legacy%02d@d.test", i))
	}
	b, err := proto.Marshal(&bongov1.Calendar{Guid: "calfull", Name: "Full", SharedWith: entries})
	if err != nil {
		t.Fatal(err)
	}
	if err := ps.Set([]byte("/user/owner/calendar/calfull"), b); err != nil {
		t.Fatal(err)
	}
	return "calfull"
}

func sharedWithCount(t *testing.T, ps *store.PebbleStore) int {
	t.Helper()
	b, err := ps.Get([]byte("/user/owner/calendar/calfull"))
	if err != nil {
		t.Fatal(err)
	}
	var cal bongov1.Calendar
	if err := proto.Unmarshal(b, &cal); err != nil {
		t.Fatal(err)
	}
	return len(cal.SharedWith)
}

func TestPruneLegacyCalendarShares_LeaderRunsAtStartup(t *testing.T) {
	buf := &lockedBuffer{}
	logger := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	a := blobGCLeaderTestAgentSeeded(t, logger, func(a *Agent) {
		seedFullSharedCalendar(t, a.Store)
	})

	// Run() invoked the pass, not this test. The prune dropped the 10 legacy
	// strings and the grants migration then turned the remaining 40 into
	// grants and cleared the field.
	if got := sharedWithCount(t, a.Store); got != 0 {
		t.Fatalf("SharedWith after boot = %d, want 0 (migrated)", got)
	}
	if n, err := a.ACL.CountCalendarGrants("owner", "calfull"); err != nil || n != 40 {
		t.Fatalf("grants after boot = %d, %v; want 40", n, err)
	}
	var line string
	for _, l := range strings.Split(buf.String(), "\n") {
		if strings.Contains(l, pruneLogMsg) {
			line = l
		}
	}
	for _, want := range []string{"calendar=calfull", "removed=10", "user=owner"} {
		if !strings.Contains(line, want) {
			t.Errorf("log line %q lacks %s", line, want)
		}
	}

	// Second run removes nothing and logs nothing more.
	a.pruneLegacyCalendarShares()
	if n := buf.count(pruneLogMsg); n != 1 {
		t.Errorf("prune lines after second run = %d, want 1", n)
	}

	// The formerly full calendar accepts a new share (the cap is 50).
	ca := store.NewCalendarStore(a.Store, a)
	addrs := a.AddressStore
	profile, _ := proto.Marshal(&bongov1.UserProfile{Email: "newbie@d.test"})
	if err := a.Store.Set([]byte("/user/newbie/profile"), profile); err != nil {
		t.Fatal(err)
	}
	if err := addrs.Put("newbie@d.test", &bongov1.AddressRecord{UserId: "newbie", Domain: "d.test", Kind: "primary"}); err != nil {
		t.Fatal(err)
	}
	h := handlers.NewCalendarHandler(store.NewEventStore(a.Store, a), ca, nil)
	h.SetShareResolver(store.NewRecipientResolver(addrs, a.Store, nil))
	h.SetACL(a.ACL)

	form := url.Values{"share_with": {"newbie@d.test"}}
	req := httptest.NewRequest(http.MethodPost, "/calendar/calfull/share", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("calendarID", "calfull")
	ctx := middleware.WithUserContext(req.Context(), "owner", "owner@d.test")
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	h.ShareCalendar(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("share after cleanup = %d (%s)", rr.Code, rr.Body.String())
	}
	if n, err := a.ACL.CountCalendarGrants("owner", "calfull"); err != nil || n != 41 {
		t.Errorf("grants = %d, %v; want 41", n, err)
	}
}

func TestPruneLegacyCalendarShares_FollowerDoesNotRun(t *testing.T) {
	a, applier, buf := newShadowedFollower(t)
	seedFullSharedCalendar(t, a.Store)
	a.pruneLegacyCalendarShares()

	if applier.n != 0 {
		t.Errorf("follower applied %d commands, want 0", applier.n)
	}
	if got := sharedWithCount(t, a.Store); got != 50 {
		t.Errorf("SharedWith = %d, want 50 untouched", got)
	}
	if strings.Contains(buf.String(), "calendar share") {
		t.Errorf("follower logged: %s", buf.String())
	}
}
