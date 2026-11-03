package store

import (
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func subscribedCal(guid string) *bongov1.Calendar {
	return &bongov1.Calendar{Guid: guid, Name: "Feed", Subscription: &bongov1.CalendarSubscription{SourceUrl: "https://example.org/feed.ics"}}
}

// A subscription is rewritten from its feed, so it is never the default. The
// GUID sorts before "default", as a generated hex ID usually does.
func TestEnsureDefaultSkipsSubscriptions(t *testing.T) {
	cs := newCalendarTestEnv(t)
	for _, c := range []*bongov1.Calendar{subscribedCal("0aaa11"), {Guid: "default", Name: "Personal"}, NewTasksCalendar()} {
		if err := cs.Update("u", c.Guid, c); err != nil {
			t.Fatal(err)
		}
	}
	if guid, err := cs.EnsureDefault("u"); err != nil || guid != "default" {
		t.Errorf("EnsureDefault = %q, %v; want default", guid, err)
	}
}

func TestEnsureDefaultWithOnlySubscriptionCreatesPersonal(t *testing.T) {
	cs := newCalendarTestEnv(t)
	for _, c := range []*bongov1.Calendar{subscribedCal("0aaa11"), NewTasksCalendar()} {
		if err := cs.Update("u", c.Guid, c); err != nil {
			t.Fatal(err)
		}
	}
	guid, err := cs.EnsureDefault("u")
	if err != nil {
		t.Fatal(err)
	}
	cal, err := cs.Get("u", guid)
	if err != nil || IsSubscribed(cal) || !CalendarAllows(cal, ComponentEvent) {
		t.Fatalf("default = %+v, %v", cal, err)
	}
	if sub, err := cs.Get("u", "0aaa11"); err != nil || !IsSubscribed(sub) {
		t.Errorf("subscription lost: %+v, %v", sub, err)
	}
}

// An existing "default" that cannot be the default is not overwritten.
func TestEnsureDefaultDoesNotOverwriteUnusableDefault(t *testing.T) {
	cs := newCalendarTestEnv(t)
	if err := cs.Update("u", "default", subscribedCal("default")); err != nil {
		t.Fatal(err)
	}
	guid, err := cs.EnsureDefault("u")
	if err != nil {
		t.Fatal(err)
	}
	if guid == "default" {
		t.Fatal("overwrote the existing default key")
	}
	if c, err := cs.Get("u", "default"); err != nil || !IsSubscribed(c) {
		t.Errorf("existing default changed: %+v, %v", c, err)
	}
}
