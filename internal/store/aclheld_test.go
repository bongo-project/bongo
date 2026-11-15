package store

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func TestListGrantsHeldPerKind(t *testing.T) {
	e := newMailboxIDEnv(t)
	acl := NewACLStore(e.ps, &serialApplier{inner: &directApplier{store: e.ps}})
	now := time.Unix(10_000_000, 0)
	e.create(t, "o", "Work")
	e.create(t, "o", "Gone")
	e.create(t, "o", "Stale")
	work, _ := e.ms.EnsureMailboxID("o", "Work")
	gone, _ := e.ms.EnsureMailboxID("o", "Gone")
	stale, _ := e.ms.EnsureMailboxID("o", "Stale")
	for _, c := range []string{"cal", "oldcal", "acc"} {
		if err := e.ps.Set(calendarKey("o", c), []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	set := func(kind, res, grantee, level, rights, state string, at time.Time) {
		t.Helper()
		if err := acl.apply(ACLRequest{Op: ACLOpSet, Kind: kind, Owner: "o", Resource: res, Grantee: grantee,
			Level: level, Rights: rights, State: state, GrantedBy: "o", At: at.Unix()}); err != nil {
			t.Fatalf("set %s %s: %v", kind, res, err)
		}
	}
	set(ACLKindCalendar, "cal", GranteeUser("b"), GrantLevelRead, "", GrantStatePending, now)
	set(ACLKindCalendar, "gonecal", GranteeUser("b"), GrantLevelRead, "", GrantStatePending, now)
	set(ACLKindCalendar, "oldcal", GranteeUser("b"), GrantLevelRead, "", GrantStatePending, now.Add(-(PendingGrantTTL+1)*time.Second))
	set(ACLKindMailbox, work, GranteeUser("b"), "", "lrs", GrantStatePending, now)
	set(ACLKindMailbox, gone, GranteeUser("b"), "", "lrs", GrantStatePending, now)
	set(ACLKindMailbox, stale, GranteeUser("b"), "", "lrs", GrantStatePending, now)
	set(ACLKindAddressBook, kindsBookID, GranteeUser("b"), GrantLevelWrite, "", GrantStatePending, now)
	set(ACLKindMailbox, work, GranteeUser("c"), "", "lr", GrantStatePending, now)
	// Groups cannot be granted yet; a stray g: marker must still be ignored.
	if err := e.ps.Set(ACLRevKey(ACLKindMailbox, "o", work, GranteeGroup("staff")), nil); err != nil {
		t.Fatal(err)
	}
	set(ACLKindCalendar, "acc", GranteeUser("b"), GrantLevelWrite, "", GrantStateAccepted, now)

	if err := e.ps.Delete(mailboxMetaKey("o", "Gone")); err != nil {
		t.Fatal(err)
	}
	data, _ := proto.Marshal(&bongov1.MailboxMeta{Name: "Stale", Id: kindsBookID})
	if err := e.ps.Set(mailboxMetaKey("o", "Stale"), data); err != nil {
		t.Fatal(err)
	}

	got, err := acl.ListGrantsHeld("b", GrantStatePending, now)
	if err != nil {
		t.Fatal(err)
	}
	type key struct{ kind, res string }
	want := []key{{ACLKindAddressBook, kindsBookID}, {ACLKindCalendar, "cal"}, {ACLKindMailbox, work}}
	if len(got) != len(want) {
		t.Fatalf("pending = %+v, want %v", got, want)
	}
	for i, w := range want {
		if got[i].Kind != w.kind || got[i].Resource != w.res {
			t.Errorf("row %d = %s/%s, want %s/%s", i, got[i].Kind, got[i].Resource, w.kind, w.res)
		}
	}
	if got[2].Rights != "lrs" || got[1].Level != GrantLevelRead || got[0].Level != GrantLevelWrite {
		t.Errorf("permissions wrong: %+v", got)
	}
	if n, err := acl.CountPendingGrantsHeld("b", now); err != nil || n != 3 {
		t.Errorf("count = %d, %v; want 3", n, err)
	}

	acc, err := acl.ListGrantsHeld("b", GrantStateAccepted, now)
	if err != nil || len(acc) != 1 || acc[0].Resource != "acc" {
		t.Errorf("accepted = %+v, %v", acc, err)
	}
	if other, err := acl.ListGrantsHeld("nobody", GrantStatePending, now); err != nil || len(other) != 0 {
		t.Errorf("other grantee = %+v, %v", other, err)
	}
}

func TestGenericGrantMethods(t *testing.T) {
	e := newMailboxIDEnv(t)
	acl := NewACLStore(e.ps, &serialApplier{inner: &directApplier{store: e.ps}})
	now := time.Unix(1000, 0)
	if err := acl.SetGrantKeepState(ACLKindMailbox, "o", kindsMailboxID, GranteeUser("b"), "", "lr", GrantStatePending, "o", now); err != nil {
		t.Fatal(err)
	}
	if err := acl.AcceptGrant(ACLKindMailbox, "o", kindsMailboxID, GranteeUser("b"), now); err != nil {
		t.Fatal(err)
	}
	// A re-set keeps the accepted state and takes the new rights.
	if err := acl.SetGrantKeepState(ACLKindMailbox, "o", kindsMailboxID, GranteeUser("b"), "", "lrs", GrantStatePending, "o", now); err != nil {
		t.Fatal(err)
	}
	g, _ := acl.GetGrant(ACLKindMailbox, "o", kindsMailboxID, GranteeUser("b"))
	if g == nil || g.State != GrantStateAccepted || g.Rights != "lrs" {
		t.Fatalf("grant = %+v", g)
	}
	if err := acl.RevokeGrant(ACLKindMailbox, "o", kindsMailboxID, GranteeUser("b")); err != nil {
		t.Fatal(err)
	}
	if g, _ := acl.GetGrant(ACLKindMailbox, "o", kindsMailboxID, GranteeUser("b")); g != nil {
		t.Errorf("grant survived revoke: %+v", g)
	}
	if ValidACLKind("task") || !ValidACLKind(ACLKindAddressBook) {
		t.Error("ValidACLKind wrong")
	}
}
