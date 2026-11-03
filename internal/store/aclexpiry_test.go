package store

import (
	"slices"
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// A non-round base so a boundary written as a multiple of a day cannot match
// by luck.
const expiryBase = 1_700_000_123

func TestPendingGrantExpired(t *testing.T) {
	cases := []struct {
		name string
		g    *bongov1.Grant
		at   int64
		want bool
	}{
		{"nil", nil, expiryBase * 2, false},
		{"pending at exactly the TTL", &bongov1.Grant{State: GrantStatePending, UpdatedAt: expiryBase}, expiryBase + PendingGrantTTL, false},
		{"pending one second past", &bongov1.Grant{State: GrantStatePending, UpdatedAt: expiryBase}, expiryBase + PendingGrantTTL + 1, true},
		{"pending before its own timestamp", &bongov1.Grant{State: GrantStatePending, UpdatedAt: expiryBase}, expiryBase - 5, false},
		{"accepted never expires", &bongov1.Grant{State: GrantStateAccepted, UpdatedAt: expiryBase}, expiryBase + 10*PendingGrantTTL, false},
		{"falls back to created_at", &bongov1.Grant{State: GrantStatePending, CreatedAt: expiryBase}, expiryBase + PendingGrantTTL + 1, true},
		{"created_at fallback still live", &bongov1.Grant{State: GrantStatePending, CreatedAt: expiryBase}, expiryBase + PendingGrantTTL, false},
		{"no timestamps counts as expired", &bongov1.Grant{State: GrantStatePending}, expiryBase, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := PendingGrantExpired(tc.g, tc.at); got != tc.want {
				t.Errorf("PendingGrantExpired = %v, want %v", got, tc.want)
			}
		})
	}
}

func seedAt(t *testing.T, e *aclEnv, owner, cal, grantee, state string, at int64) {
	t.Helper()
	if err := e.acl.SetCalendarGrant(owner, cal, grantee, GrantLevelRead, state, owner, time.Unix(at, 0)); err != nil {
		t.Fatal(err)
	}
}

// expiryBase is years before the wall clock, so "at the boundary" also fails
// if the FSM judged expiry by time.Now() instead of the command's own At.
func TestACLAcceptExpiry(t *testing.T) {
	cases := []struct {
		name      string
		seedState string
		acceptAt  int64
		want      string
	}{
		{"at the boundary", GrantStatePending, expiryBase + PendingGrantTTL, GrantStateAccepted},
		{"one second past", GrantStatePending, expiryBase + PendingGrantTTL + 1, GrantStatePending},
		{"long past", GrantStatePending, expiryBase + 3*PendingGrantTTL, GrantStatePending},
		{"accepted grant never expires", GrantStateAccepted, expiryBase + 3*PendingGrantTTL, GrantStateAccepted},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newACLEnv(t)
			seedAt(t, e, "a", "c", "b", tc.seedState, expiryBase)
			if err := e.acl.AcceptCalendarGrant("a", "c", "b", time.Unix(tc.acceptAt, 0)); err != nil {
				t.Fatal(err)
			}
			g, _ := e.acl.GetCalendarGrant("a", "c", "b")
			if g == nil || g.State != tc.want {
				t.Errorf("grant = %+v, want state %s", g, tc.want)
			}
		})
	}
}

func TestACLResetRevivesExpiredPending(t *testing.T) {
	e := newACLEnv(t)
	seedAt(t, e, "a", "c", "b", GrantStatePending, expiryBase)
	var late int64 = expiryBase + PendingGrantTTL + 100
	if err := e.acl.AcceptCalendarGrant("a", "c", "b", time.Unix(late, 0)); err != nil {
		t.Fatal(err)
	}
	if g, _ := e.acl.GetCalendarGrant("a", "c", "b"); g.State != GrantStatePending {
		t.Fatalf("expired accept applied: %+v", g)
	}
	if err := e.acl.SetCalendarGrantKeepState("a", "c", "b", GrantLevelRead, GrantStatePending, "a", time.Unix(late, 0)); err != nil {
		t.Fatal(err)
	}
	g, _ := e.acl.GetCalendarGrant("a", "c", "b")
	if g.State != GrantStatePending || g.UpdatedAt != late || g.CreatedAt != expiryBase {
		t.Fatalf("refreshed grant = %+v", g)
	}
	if err := e.acl.AcceptCalendarGrant("a", "c", "b", time.Unix(late+10, 0)); err != nil {
		t.Fatal(err)
	}
	if g, _ := e.acl.GetCalendarGrant("a", "c", "b"); g.State != GrantStateAccepted {
		t.Errorf("accept after refresh: %+v", g)
	}
}

func TestExpiredGrantsSkippedByReads(t *testing.T) {
	e := newACLEnv(t)
	now := time.Unix(expiryBase+PendingGrantTTL+50, 0)
	for _, c := range []string{"live", "old", "acc"} {
		e.calendar(t, "o", c)
	}
	seedAt(t, e, "o", "live", "me", GrantStatePending, expiryBase+60)
	seedAt(t, e, "o", "old", "me", GrantStatePending, expiryBase)
	seedAt(t, e, "o", "acc", "me", GrantStateAccepted, expiryBase)

	got, err := e.acl.ListPendingCalendarGrantsHeld("me", now)
	if err != nil {
		t.Fatal(err)
	}
	want := []HeldGrant{{"o", "live", GrantLevelRead}}
	if !slices.Equal(got, want) {
		t.Errorf("pending list = %+v, want %+v", got, want)
	}

	// Cap count: three grantees on one calendar, one expired.
	seedAt(t, e, "o", "cap", "g1", GrantStatePending, expiryBase)    // expired
	seedAt(t, e, "o", "cap", "g2", GrantStatePending, expiryBase+60) // live
	seedAt(t, e, "o", "cap", "g3", GrantStateAccepted, expiryBase)   // accepted, old
	active, err := e.acl.CountActiveCalendarGrants("o", "cap", now)
	if err != nil || active != 2 {
		t.Errorf("active = %d, %v; want 2", active, err)
	}
	if raw, _ := e.acl.CountCalendarGrants("o", "cap"); raw != 3 {
		t.Errorf("raw count = %d, want 3", raw)
	}
}
