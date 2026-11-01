package store

import (
	"slices"
	"testing"
)

func TestCalendarAccessLevel(t *testing.T) {
	e := newACLEnv(t)
	e.calendar(t, "owner", "a")
	e.calendar(t, "owner", "b")
	e.calendar(t, "ownerx", "a")
	e.grant(t, "owner", "a", "fb", GrantLevelFreeBusy, GrantStateAccepted)
	e.grant(t, "owner", "a", "rd", GrantLevelRead, GrantStateAccepted)
	e.grant(t, "owner", "a", "wr", GrantLevelWrite, GrantStateAccepted)
	e.grant(t, "owner", "a", "pend", GrantLevelWrite, GrantStatePending)
	e.grant(t, "owner", "gone", "ghost", GrantLevelWrite, GrantStateAccepted) // calendar never existed
	e.grant(t, "ownerx", "a", "other", GrantLevelWrite, GrantStateAccepted)

	tests := []struct {
		name, owner, cal, who, want string
	}{
		{"freebusy", "owner", "a", "fb", GrantLevelFreeBusy},
		{"read", "owner", "a", "rd", GrantLevelRead},
		{"write", "owner", "a", "wr", GrantLevelWrite},
		{"pending grants nothing", "owner", "a", "pend", ""},
		{"deleted calendar grants nothing", "owner", "gone", "ghost", ""},
		// The cross-calendar case HasCalendarAccess gets wrong.
		{"grant on a does not reach b", "owner", "b", "wr", ""},
		{"grant from another owner does not reach this one", "owner", "a", "other", ""},
		{"stranger", "owner", "a", "nobody", ""},
		{"empty owner", "", "a", "wr", ""},
		{"empty calendar", "owner", "", "wr", ""},
		{"empty grantee", "owner", "a", "", ""},
		{"case differs", "owner", "a", "WR", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := e.acl.CalendarAccessLevel(tt.owner, tt.cal, tt.who)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("level = %q, want %q", got, tt.want)
			}
		})
	}

	// The any-calendar helper says yes for b; the per-calendar one must not.
	if ok, _ := e.acl.HasCalendarAccess("owner", "wr", GrantLevelWrite); !ok {
		t.Fatal("premise: HasCalendarAccess answers for any calendar")
	}
}

func TestLevelSatisfies(t *testing.T) {
	tests := []struct {
		level, need string
		want        bool
	}{
		{GrantLevelWrite, GrantLevelWrite, true},
		{GrantLevelWrite, GrantLevelRead, true},
		{GrantLevelRead, GrantLevelRead, true},
		{GrantLevelRead, GrantLevelWrite, false},
		{GrantLevelFreeBusy, GrantLevelRead, false},
		{"", GrantLevelRead, false},
		{GrantLevelWrite, "admin", false},
	}
	for _, tt := range tests {
		if got := LevelSatisfies(tt.level, tt.need); got != tt.want {
			t.Errorf("LevelSatisfies(%q, %q) = %v, want %v", tt.level, tt.need, got, tt.want)
		}
	}
}

func TestListCalendarGrantsHeld(t *testing.T) {
	e := newACLEnv(t)
	e.calendar(t, "o1", "a")
	e.calendar(t, "o1", "b")
	e.calendar(t, "o2", "a")
	e.calendar(t, "o3", "a")
	e.grant(t, "o1", "b", "me", GrantLevelWrite, GrantStateAccepted)
	e.grant(t, "o1", "a", "me", GrantLevelRead, GrantStateAccepted)
	e.grant(t, "o2", "a", "me", GrantLevelFreeBusy, GrantStateAccepted)
	e.grant(t, "o3", "a", "me", GrantLevelRead, GrantStatePending)
	e.grant(t, "o3", "gone", "me", GrantLevelRead, GrantStateAccepted)
	e.grant(t, "o1", "a", "someone", GrantLevelWrite, GrantStateAccepted)
	e.grant(t, "o1", "a", "me2", GrantLevelWrite, GrantStateAccepted) // grantee prefix of another ID

	got, err := e.acl.ListCalendarGrantsHeld("me")
	if err != nil {
		t.Fatal(err)
	}
	want := []HeldGrant{
		{"o1", "a", GrantLevelRead},
		{"o1", "b", GrantLevelWrite},
		{"o2", "a", GrantLevelFreeBusy},
	}
	if !slices.Equal(got, want) {
		t.Errorf("held = %+v, want %+v", got, want)
	}
	if got, _ := e.acl.ListCalendarGrantsHeld(""); len(got) != 0 {
		t.Errorf("empty grantee listed %v", got)
	}
}

func TestListCalendarGrants(t *testing.T) {
	e := newACLEnv(t)
	e.calendar(t, "o", "a")
	e.calendar(t, "o", "ab")
	e.grant(t, "o", "a", "zed", GrantLevelRead, GrantStateAccepted)
	e.grant(t, "o", "a", "amy", GrantLevelWrite, GrantStatePending)
	e.grant(t, "o", "ab", "other", GrantLevelRead, GrantStateAccepted)

	got, err := e.acl.ListCalendarGrants("o", "a")
	if err != nil {
		t.Fatal(err)
	}
	want := []CalendarGrantEntry{
		{"amy", GrantLevelWrite, GrantStatePending},
		{"zed", GrantLevelRead, GrantStateAccepted},
	}
	if !slices.Equal(got, want) {
		t.Errorf("grants = %+v, want %+v", got, want)
	}
}
