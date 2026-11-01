package store

import (
	"errors"
	"strings"
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func TestPatchSubscriptionKeepsRenameMadeMeanwhile(t *testing.T) {
	cs := newCalendarTestEnv(t)
	guid, err := cs.Create("u1", &bongov1.Calendar{
		Name: "Feed", Color: "#111111",
		Subscription: &bongov1.CalendarSubscription{SourceUrl: "https://example.com/f.ics"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// The worker read the calendar before its fetch...
	stale, _ := cs.Get("u1", guid)
	// ...and the user renamed and recoloured it during the fetch.
	renamed, _ := cs.Get("u1", guid)
	renamed.Name, renamed.Color = "Holidays", "#222222"
	if err := cs.Update("u1", guid, renamed); err != nil {
		t.Fatal(err)
	}
	_ = stale

	if err := cs.PatchSubscription("u1", guid, func(s *bongov1.CalendarSubscription) {
		s.LastError = "boom"
		s.Etag = "\"e\""
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := cs.Get("u1", guid)
	if got.Name != "Holidays" || got.Color != "#222222" {
		t.Fatalf("rename lost: %q %q", got.Name, got.Color)
	}
	if got.Subscription.LastError != "boom" || got.Subscription.Etag != "\"e\"" || got.Subscription.SourceUrl != "https://example.com/f.ics" {
		t.Fatalf("subscription state: %+v", got.Subscription)
	}
}

func TestPatchSubscriptionDoesNotResurrect(t *testing.T) {
	cs := newCalendarTestEnv(t)
	guid, _ := cs.Create("u1", &bongov1.Calendar{Name: "Feed", Subscription: &bongov1.CalendarSubscription{SourceUrl: "https://example.com/f.ics"}})
	if err := cs.Delete("u1", guid); err != nil {
		t.Fatal(err)
	}
	called := false
	err := cs.PatchSubscription("u1", guid, func(*bongov1.CalendarSubscription) { called = true })
	if !errors.Is(err, ErrSubscriptionGone) || called {
		t.Fatalf("err=%v called=%v", err, called)
	}
	if _, err := cs.Get("u1", guid); err == nil {
		t.Fatal("deleted calendar was written back")
	}

	// A plain calendar is not a subscription either.
	plain, _ := cs.Create("u1", &bongov1.Calendar{Name: "Plain"})
	if err := cs.PatchSubscription("u1", plain, func(*bongov1.CalendarSubscription) {}); !errors.Is(err, ErrSubscriptionGone) {
		t.Fatalf("plain calendar: %v", err)
	}
}

func TestCalendarIsSubscribed(t *testing.T) {
	cs := newCalendarTestEnv(t)
	sub, _ := cs.Create("u1", &bongov1.Calendar{Name: "Feed", Subscription: &bongov1.CalendarSubscription{SourceUrl: "https://example.com/f.ics"}})
	plain, _ := cs.Create("u1", &bongov1.Calendar{Name: "Plain"})
	tests := []struct {
		user, cal string
		want      bool
	}{
		{"u1", sub, true},
		{"u1", plain, false},
		{"u1", "", false},
		{"u1", "missing", false},
		{"u2", sub, false},
	}
	for _, tc := range tests {
		got, err := cs.CalendarIsSubscribed(tc.user, tc.cal)
		if err != nil || got != tc.want {
			t.Errorf("%s/%s: got %v, %v; want %v", tc.user, tc.cal, got, err, tc.want)
		}
	}
	list, err := cs.ListSubscribed("u1")
	if err != nil || len(list) != 1 || list[0].Guid != sub {
		t.Fatalf("ListSubscribed = %v, %v", list, err)
	}
}

func TestSubscriptionEventGUID(t *testing.T) {
	a := SubscriptionEventGUID("cal1", "uid-1")
	if a != SubscriptionEventGUID("cal1", "uid-1") {
		t.Fatal("not deterministic")
	}
	if a == SubscriptionEventGUID("cal2", "uid-1") || a == SubscriptionEventGUID("cal1", "uid-2") {
		t.Fatal("collision across calendar or UID")
	}
	if !strings.HasPrefix(a, "sub-") || strings.Contains(a, "uid-1") {
		t.Fatalf("guid %q must be derived, never the raw UID", a)
	}
	// A separator, so ("ab","c") and ("a","bc") differ.
	if SubscriptionEventGUID("ab", "c") == SubscriptionEventGUID("a", "bc") {
		t.Fatal("calendar and UID run together")
	}
	if strings.ContainsAny(SubscriptionEventGUID("c", "x/../../y"), "/.") {
		t.Fatal("guid carries path characters")
	}
}

// patchWhile runs a refresh-state write whose fn blocks until mutate has been
// started on another goroutine, which is how a rename or a token regeneration
// lands in the middle of a refresh. It returns once both have finished.
func patchWhile(t *testing.T, cs *CalendarStore, guid string, mutate func(*bongov1.Calendar) error) {
	t.Helper()
	inside := make(chan struct{})
	release := make(chan struct{})
	patchErr := make(chan error, 1)
	go func() {
		patchErr <- cs.PatchSubscription("u1", guid, func(s *bongov1.CalendarSubscription) {
			close(inside)
			<-release
			s.Etag = "\"e\""
		})
	}()
	<-inside
	mutErr := make(chan error, 1)
	go func() { mutErr <- cs.Mutate("u1", guid, mutate) }()
	time.Sleep(50 * time.Millisecond) // let the second writer reach the lock
	close(release)
	if err := <-patchErr; err != nil {
		t.Fatal(err)
	}
	if err := <-mutErr; err != nil {
		t.Fatal(err)
	}
}

func TestPatchSubscriptionInterleavedWithRename(t *testing.T) {
	cs := newCalendarTestEnv(t)
	guid, _ := cs.Create("u1", &bongov1.Calendar{Name: "Feed", Subscription: &bongov1.CalendarSubscription{SourceUrl: "https://example.com/f.ics"}})
	patchWhile(t, cs, guid, func(c *bongov1.Calendar) error { c.Name = "Holidays"; return nil })
	got, _ := cs.Get("u1", guid)
	if got.Name != "Holidays" || got.Subscription.Etag != "\"e\"" {
		t.Fatalf("a field was lost: name=%q etag=%q", got.Name, got.Subscription.Etag)
	}
}

func TestPatchSubscriptionKeepsRegeneratedPublishToken(t *testing.T) {
	cs := newCalendarTestEnv(t)
	guid, _ := cs.Create("u1", &bongov1.Calendar{
		Name: "Feed", PublishToken: []byte("old"),
		Subscription: &bongov1.CalendarSubscription{SourceUrl: "https://example.com/f.ics"},
	})
	patchWhile(t, cs, guid, func(c *bongov1.Calendar) error { c.PublishToken = []byte("new"); return nil })
	got, _ := cs.Get("u1", guid)
	if string(got.PublishToken) != "new" || got.Subscription.Etag != "\"e\"" {
		t.Fatalf("token=%q etag=%q", got.PublishToken, got.Subscription.Etag)
	}

	// A write after the regeneration also leaves the new token alone.
	if err := cs.PatchSubscription("u1", guid, func(s *bongov1.CalendarSubscription) { s.LastError = "x" }); err != nil {
		t.Fatal(err)
	}
	if got, _ := cs.Get("u1", guid); string(got.PublishToken) != "new" {
		t.Fatalf("token = %q", got.PublishToken)
	}
}
