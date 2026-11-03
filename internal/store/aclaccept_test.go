package store

import (
	"slices"
	"testing"
	"time"
)

func TestACLAcceptIsConditional(t *testing.T) {
	cases := []struct {
		name      string
		seed      string // "", pending or accepted
		wantState string // "" means no grant
		wantAt    int64
	}{
		{"absent plans nothing", "", "", 0},
		{"pending becomes accepted", GrantStatePending, GrantStateAccepted, 500},
		{"accepted is left alone", GrantStateAccepted, GrantStateAccepted, 1000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newACLEnv(t)
			if tc.seed != "" {
				e.grant(t, "a", "c", "b", GrantLevelWrite, tc.seed)
			}
			if err := e.acl.AcceptCalendarGrant("a", "c", "b", time.Unix(500, 0)); err != nil {
				t.Fatalf("accept: %v", err)
			}
			g, err := e.acl.GetCalendarGrant("a", "c", "b")
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantState == "" {
				if g != nil {
					t.Fatalf("accept resurrected an absent grant: %+v", g)
				}
				if keys := aclKeys(t, e.ps); len(keys) != 0 {
					t.Errorf("keys written for absent grant: %v", keys)
				}
				return
			}
			if g.State != tc.wantState || g.UpdatedAt != tc.wantAt {
				t.Errorf("grant = %+v", g)
			}
			if g.Level != GrantLevelWrite || g.GrantedBy != "a" || g.CreatedAt != 1000 {
				t.Errorf("accept changed more than state: %+v", g)
			}
			// Replay: a second accept is a no-op.
			if err := e.acl.AcceptCalendarGrant("a", "c", "b", time.Unix(900, 0)); err != nil {
				t.Fatal(err)
			}
			g2, _ := e.acl.GetCalendarGrant("a", "c", "b")
			if g2.UpdatedAt != tc.wantAt {
				t.Errorf("replay changed updated_at to %d", g2.UpdatedAt)
			}
		})
	}
}

func TestACLSetKeepState(t *testing.T) {
	cases := []struct {
		name      string
		seed      string
		payload   string
		wantState string
	}{
		{"keeps pending", GrantStatePending, GrantStateAccepted, GrantStatePending},
		{"keeps accepted", GrantStateAccepted, GrantStatePending, GrantStateAccepted},
		{"absent uses payload pending", "", GrantStatePending, GrantStatePending},
		{"absent uses payload accepted", "", GrantStateAccepted, GrantStateAccepted},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newACLEnv(t)
			if tc.seed != "" {
				e.grant(t, "a", "c", "b", GrantLevelFreeBusy, tc.seed)
			}
			if err := e.acl.SetCalendarGrantKeepState("a", "c", "b", GrantLevelRead, tc.payload, "a", time.Unix(2000, 0)); err != nil {
				t.Fatal(err)
			}
			g, _ := e.acl.GetCalendarGrant("a", "c", "b")
			if g.State != tc.wantState || g.Level != GrantLevelRead {
				t.Errorf("grant = %+v", g)
			}
		})
	}
}

func TestListPendingCalendarGrantsHeld(t *testing.T) {
	e := newACLEnv(t)
	e.calendar(t, "o2", "c2")
	e.calendar(t, "o1", "c1")
	e.calendar(t, "o1", "cacc")
	e.grant(t, "o2", "c2", "me", GrantLevelRead, GrantStatePending)
	e.grant(t, "o1", "c1", "me", GrantLevelWrite, GrantStatePending)
	e.grant(t, "o1", "cacc", "me", GrantLevelRead, GrantStateAccepted)
	e.grant(t, "o1", "gone", "me", GrantLevelRead, GrantStatePending)
	e.grant(t, "o1", "c1", "other", GrantLevelRead, GrantStatePending)

	got, err := e.acl.ListPendingCalendarGrantsHeld("me", time.Unix(2000, 0))
	if err != nil {
		t.Fatal(err)
	}
	want := []HeldGrant{{"o1", "c1", GrantLevelWrite}, {"o2", "c2", GrantLevelRead}}
	if !slices.Equal(got, want) {
		t.Errorf("got %+v want %+v", got, want)
	}
	acc, _ := e.acl.ListCalendarGrantsHeld("me")
	if len(acc) != 1 || acc[0].Calendar != "cacc" {
		t.Errorf("accepted list = %+v", acc)
	}
	if lvl, _ := e.acl.CalendarAccessLevel("o1", "c1", "me"); lvl != "" {
		t.Errorf("pending grant authorises %q", lvl)
	}
}
