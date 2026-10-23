package store

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

type countingDirectApplier struct {
	inner *directApplier
	n     int
}

func (c *countingDirectApplier) ApplyCommand(cmd *bongov1.RaftCommand, d time.Duration) error {
	c.n++
	return c.inner.ApplyCommand(cmd, d)
}

type failingProfileSource struct {
	*PebbleStore
	failFor string
}

func (f failingProfileSource) Get(key []byte) ([]byte, error) {
	if string(key) == "/user/"+f.failFor+"/profile" {
		return nil, errors.New("pebble: transient")
	}
	return f.PebbleStore.Get(key)
}

func seedShareCalendar(t *testing.T) (*PebbleStore, *CalendarStore, *countingDirectApplier, string, []string) {
	t.Helper()
	ps, err := NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	applier := &countingDirectApplier{inner: &directApplier{store: ps}}
	cals := NewCalendarStore(ps, applier)

	var entries, legacy []string
	for i := range 40 {
		id := fmt.Sprintf("user%02d", i)
		data, _ := proto.Marshal(&bongov1.UserProfile{Email: id + "@d.test", Disabled: i == 0})
		if err := ps.Set([]byte("/user/"+id+"/profile"), data); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, id)
	}
	for i := range 10 {
		s := fmt.Sprintf("legacy%02d@d.test", i)
		if i == 0 {
			s = "../other/calendar/x"
		}
		if i == 1 {
			s = ""
		}
		legacy = append(legacy, s)
	}
	all := append(append([]string{}, entries...), legacy...)
	guid, err := cals.Create("owner", &bongov1.Calendar{Name: "Full", SharedWith: all})
	if err != nil {
		t.Fatal(err)
	}
	// A calendar with nothing to prune, and an event key that must be ignored.
	if _, err := cals.Create("owner", &bongov1.Calendar{Name: "Clean", SharedWith: []string{"user01"}}); err != nil {
		t.Fatal(err)
	}
	ev, _ := proto.Marshal(&bongov1.Event{Title: "x"})
	if err := ps.Set([]byte("/user/owner/event/e1"), ev); err != nil {
		t.Fatal(err)
	}
	// Not a calendar key, though the value would parse as one: must be ignored.
	decoy, _ := proto.Marshal(&bongov1.Calendar{Guid: "decoy", SharedWith: []string{"legacy@d.test"}})
	if err := ps.Set([]byte("/user/owner/event/decoy"), decoy); err != nil {
		t.Fatal(err)
	}
	// A tag named "profile" makes /user/owner/tag/profile a real key, so an
	// entry "owner/tag" would be kept if the "/" guard were missing.
	if err := ps.Set([]byte("/user/owner/tag/profile"), []byte("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := cals.Create("owner", &bongov1.Calendar{Name: "Steer", SharedWith: []string{"owner/tag"}}); err != nil {
		t.Fatal(err)
	}
	// A deeper key under /calendar/ must not be treated as a calendar.
	deep, _ := proto.Marshal(&bongov1.Calendar{Guid: "deep", SharedWith: []string{"legacy@d.test"}})
	if err := ps.Set([]byte("/user/owner/calendar/x/y"), deep); err != nil {
		t.Fatal(err)
	}
	applier.n = 0
	return ps, cals, applier, guid, entries
}

func TestPruneCalendarShares(t *testing.T) {
	ps, cals, applier, guid, entries := seedShareCalendar(t)

	got, err := PruneCalendarShares(ps, applier)
	if err != nil {
		t.Fatal(err)
	}
	// The Full calendar loses 10; the Steer calendar loses its "owner/tag"
	// entry, which must not resolve to the real /user/owner/tag/profile key.
	removed := map[string]int{}
	for _, p := range got {
		if p.UserID != "owner" {
			t.Fatalf("pruned = %+v, want only owner's calendars", got)
		}
		removed[p.CalendarID] = p.Removed
	}
	if len(got) != 2 || removed[guid] != 10 {
		t.Fatalf("pruned = %+v, want two calendars, 10 removed from %s", got, guid)
	}
	if raw, err := ps.Get([]byte("/user/owner/calendar/x/y")); err != nil {
		t.Fatal(err)
	} else {
		var d bongov1.Calendar
		if err := proto.Unmarshal(raw, &d); err != nil || len(d.SharedWith) != 1 {
			t.Errorf("deeper calendar-prefixed key was rewritten: %v %v", d.SharedWith, err)
		}
	}
	cal, err := cals.Get("owner", guid)
	if err != nil {
		t.Fatal(err)
	}
	if len(cal.SharedWith) != 40 {
		t.Fatalf("SharedWith = %d entries, want 40", len(cal.SharedWith))
	}
	for i, e := range entries {
		if cal.SharedWith[i] != e {
			t.Fatalf("entry %d = %q, want %q (order must be kept)", i, cal.SharedWith[i], e)
		}
	}

	if raw, err := ps.Get([]byte("/user/owner/event/decoy")); err != nil || len(raw) == 0 {
		t.Fatalf("decoy: %v", err)
	} else {
		var d bongov1.Calendar
		if err := proto.Unmarshal(raw, &d); err != nil || len(d.SharedWith) != 1 {
			t.Errorf("non-calendar key was rewritten: %v %v", d.SharedWith, err)
		}
	}

	// Second run: nothing to remove, nothing applied.
	applier.n = 0
	got, err = PruneCalendarShares(ps, applier)
	if err != nil || len(got) != 0 || applier.n != 0 {
		t.Errorf("second run = %+v, %v, applies %d; want none", got, err, applier.n)
	}
}

func TestPruneCalendarSharesKeepsEntryOnReadError(t *testing.T) {
	ps, cals, applier, guid, _ := seedShareCalendar(t)
	// user05 has a profile but its read fails: kept. A legacy entry still goes.
	src := failingProfileSource{PebbleStore: ps, failFor: "user05"}
	got, err := PruneCalendarShares(src, applier)
	if err == nil {
		t.Fatal("want the read error reported")
	}
	if len(got) != 2 {
		t.Fatalf("pruned = %+v, want Full and Steer", got)
	}
	cal, _ := cals.Get("owner", guid)
	if len(cal.SharedWith) != 40 {
		t.Errorf("SharedWith = %d, want 40", len(cal.SharedWith))
	}

	// Now a failing entry that has NO profile on a fresh store: the error must
	// keep it rather than treat it as absent.
	ps2, cals2, applier2, _, _ := seedShareCalendar(t)
	id, err := cals2.Create("owner", &bongov1.Calendar{Name: "Flaky", SharedWith: []string{"ghost"}})
	if err != nil {
		t.Fatal(err)
	}
	applier2.n = 0
	got, err = PruneCalendarShares(failingProfileSource{PebbleStore: ps2, failFor: "ghost"}, applier2)
	if err == nil {
		t.Fatal("want the read error reported")
	}
	for _, g := range got {
		if g.CalendarID == id {
			t.Errorf("flaky calendar was rewritten: %+v", g)
		}
	}
	flaky, _ := cals2.Get("owner", id)
	if len(flaky.SharedWith) != 1 {
		t.Errorf("SharedWith = %q, want ghost kept", flaky.SharedWith)
	}
}

func TestPruneCalendarSharesApplyFailureIsReported(t *testing.T) {
	ps, _, _, _, _ := seedShareCalendar(t)
	_, err := PruneCalendarShares(ps, failApplier{})
	if err == nil {
		t.Fatal("want the apply error reported")
	}
}

type failApplier struct{}

func (failApplier) ApplyCommand(*bongov1.RaftCommand, time.Duration) error {
	return errors.New("raft down")
}
