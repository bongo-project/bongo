package store

import (
	"errors"
	"slices"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func identityRec(userID, domain string) *bongov1.AddressRecord {
	return &bongov1.AddressRecord{UserId: userID, Domain: domain, Kind: "identity", Verified: true}
}

func putAliasOf(t *testing.T, ps *PebbleStore, alias, target string) {
	t.Helper()
	if err := ps.Set(DomainAliasOfKey(alias), []byte(target)); err != nil {
		t.Fatal(err)
	}
}

func TestDomainAliasOf(t *testing.T) {
	ps, _, _, _ := newResolverTestEnv(t)
	putAliasOf(t, ps, "a.test", "T.Test")
	if err := ps.Set([]byte("/system/domain/slash.test/alias_of"), []byte("x/owner")); err != nil {
		t.Fatal(err)
	}
	if err := ps.Set([]byte("/system/domain/empty.test/alias_of"), []byte("  ")); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name, domain, want string
		wantOK             bool
	}{
		{"lowercases both sides", "A.Test", "t.test", true},
		{"absent", "none.test", "", false},
		{"empty domain", "", "", false},
		{"slash in domain", "a.test/owner", "", false},
		{"slash in stored target", "slash.test", "", false},
		{"empty stored target", "empty.test", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok, err := DomainAliasOf(ps, tc.domain)
			if err != nil || ok != tc.wantOK || got != tc.want {
				t.Fatalf("got (%q, %v, %v), want (%q, %v, nil)", got, ok, err, tc.want, tc.wantOK)
			}
		})
	}
}

func TestDomainAliases(t *testing.T) {
	ps, _, _, _ := newResolverTestEnv(t)
	putAliasOf(t, ps, "b.test", "t.test")
	putAliasOf(t, ps, "a.test", "T.test")
	putAliasOf(t, ps, "c.test", "other.test")
	// A forged sub-key must not be read as an alias of anything.
	if err := ps.Set([]byte("/system/domain/x/alias_of/alias_of"), []byte("t.test")); err != nil {
		t.Fatal(err)
	}
	got, err := DomainAliases(ps, "t.test")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"a.test", "b.test"}) {
		t.Fatalf("got %v", got)
	}
	if got, _ := DomainAliases(ps, "nobody.test"); len(got) != 0 {
		t.Fatalf("got %v, want none", got)
	}
}

// aliasEnv seeds target t.test (owner alice, account alice@t.test) and a
// hosted alias a.test of it.
func aliasEnv(t *testing.T) *pmEnv {
	t.Helper()
	e := newPMEnv(t)
	e.domain(t, "t.test")
	e.domain(t, "a.test")
	putAliasOf(t, e.ps, "a.test", "t.test")
	e.account(t, "alice-id", "alice@t.test")
	return e
}

func TestRecipientResolver_DomainAlias(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T, e *pmEnv)
		addr    string
		strict  bool
		want    string
		wantErr bool
		wantUna bool
	}{
		{name: "alice at alias reaches alice", addr: "Alice@A.Test", want: "alice-id"},
		{name: "strict mode rewrites too", addr: "alice@a.test", strict: true, want: "alice-id"},
		{name: "explicit address on alias wins",
			setup: func(t *testing.T, e *pmEnv) {
				e.account(t, "bob-id", "alice@a.test")
			},
			addr: "alice@a.test", want: "bob-id"},
		{name: "explicit legacy alias on alias domain wins",
			setup: func(t *testing.T, e *pmEnv) {
				e.account(t, "bob-id", "bob@t.test")
				seedAlias(t, e.ps, "alice@a.test", "bob@t.test")
			},
			addr: "alice@a.test", want: "bob-id"},
		{name: "explicit address on alias to disabled user does not fall through",
			setup: func(t *testing.T, e *pmEnv) {
				putUserProfile(t, e.ps, "gone-id", "alice@a.test", true)
				insertUser(t, e.sys, "gone-id", "alice@a.test")
				putAddress(t, e.as, "alice@a.test", "gone-id", "primary")
			},
			addr: "alice@a.test", wantErr: true, wantUna: true},
		{name: "unknown local part without catch-all is rejected", addr: "nobody@a.test", wantErr: true},
		{name: "unknown local part goes to target catch-all",
			setup: func(t *testing.T, e *pmEnv) {
				if err := e.ps.Set(CatchAllKey("t.test"), []byte("alice-id")); err != nil {
					t.Fatal(err)
				}
			},
			addr: "nobody@a.test", want: "alice-id"},
		{name: "alias own catch-all is never consulted",
			setup: func(t *testing.T, e *pmEnv) {
				e.account(t, "bob-id", "bob@t.test")
				if err := e.ps.Set(CatchAllKey("a.test"), []byte("bob-id")); err != nil {
					t.Fatal(err)
				}
			},
			addr: "nobody@a.test", wantErr: true},
		{name: "strict mode never reaches the target catch-all",
			setup: func(t *testing.T, e *pmEnv) {
				if err := e.ps.Set(CatchAllKey("t.test"), []byte("alice-id")); err != nil {
					t.Fatal(err)
				}
			},
			addr: "nobody@a.test", strict: true, wantErr: true},
		{name: "inert alias_of on an unhosted domain does not rewrite",
			setup: func(t *testing.T, e *pmEnv) {
				putAliasOf(t, e.ps, "ghost.test", "t.test")
			},
			addr: "alice@ghost.test", wantErr: true},
		{name: "target not hosted is a miss",
			setup: func(t *testing.T, e *pmEnv) {
				putAliasOf(t, e.ps, "a.test", "missing.test")
				e.account(t, "m-id", "alice@missing.test")
			},
			addr: "alice@a.test", wantErr: true},
		{name: "disabled target is unavailable",
			setup: func(t *testing.T, e *pmEnv) { e.domain(t, "t.test"); setDomainState(t, e.ps, "t.test", "disabled") },
			addr:  "alice@a.test", wantErr: true, wantUna: true},
		{name: "disabled alias is unavailable",
			setup: func(t *testing.T, e *pmEnv) { setDomainState(t, e.ps, "a.test", "disabled") },
			addr:  "alice@a.test", wantErr: true, wantUna: true},
		{name: "chain planted in pebble follows one hop only",
			setup: func(t *testing.T, e *pmEnv) {
				e.domain(t, "c.test")
				e.account(t, "carol-id", "alice@c.test")
				putAliasOf(t, e.ps, "t.test", "c.test")
			},
			addr: "alice@a.test", want: "alice-id"},
		{name: "chain does not reach the second hop for a local part only there",
			setup: func(t *testing.T, e *pmEnv) {
				e.domain(t, "c.test")
				e.account(t, "carol-id", "carol@c.test")
				putAliasOf(t, e.ps, "t.test", "c.test")
			},
			addr: "carol@a.test", wantErr: true},
		{name: "self alias does not loop",
			setup: func(t *testing.T, e *pmEnv) { putAliasOf(t, e.ps, "a.test", "a.test") },
			addr:  "alice@a.test", wantErr: true},
		{name: "stored target with slash is refused",
			setup: func(t *testing.T, e *pmEnv) { putAliasOf(t, e.ps, "a.test", "t.test/owner") },
			addr:  "alice@a.test", wantErr: true},
		{name: "verified identity on alias outranks the rewrite",
			setup: func(t *testing.T, e *pmEnv) {
				e.account(t, "bob-id", "bob@t.test")
				if err := e.as.Put("alice@a.test", identityRec("bob-id", "a.test")); err != nil {
					t.Fatal(err)
				}
			},
			addr: "alice@a.test", want: "bob-id"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := aliasEnv(t)
			if tc.setup != nil {
				tc.setup(t, e)
			}
			var got string
			var err error
			if tc.strict {
				got, err = e.r.ResolveAccount(tc.addr)
			} else {
				got, err = e.r.ResolveRecipient(tc.addr)
			}
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v (got %q)", err, tc.wantErr, got)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			if tc.wantUna != errors.Is(err, ErrRecipientUnavailable) {
				t.Fatalf("unavailable = %v, want %v (err %v)", errors.Is(err, ErrRecipientUnavailable), tc.wantUna, err)
			}
		})
	}
}

func TestRecipientResolver_DomainAliasPostmaster(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T, e *pmEnv)
		account string
		want    string
		wantUna bool
	}{
		{name: "alias owner wins",
			setup: func(t *testing.T, e *pmEnv) {
				e.owner(t, "a.test", "aowner", false)
				e.owner(t, "t.test", "towner", false)
			},
			want: "aowner"},
		{name: "no alias owner reaches target owner",
			setup: func(t *testing.T, e *pmEnv) { e.owner(t, "t.test", "towner", false) },
			want:  "towner"},
		{name: "disabled alias owner falls to target owner",
			setup: func(t *testing.T, e *pmEnv) {
				e.owner(t, "a.test", "aowner", true)
				e.owner(t, "t.test", "towner", false)
			},
			want: "towner"},
		{name: "no live owner reaches configured account",
			setup: func(t *testing.T, e *pmEnv) {
				e.owner(t, "t.test", "towner", true)
				e.account(t, "admin-id", "admin@other.test")
			},
			account: "admin@other.test", want: "admin-id"},
		{name: "nothing live is unavailable", wantUna: true},
		{name: "disabled target is unavailable even with an owner and account",
			setup: func(t *testing.T, e *pmEnv) {
				e.owner(t, "t.test", "towner", false)
				e.account(t, "admin-id", "admin@other.test")
				if err := e.ps.Set([]byte("/system/domain/t.test"), []byte("disabled")); err != nil {
					t.Fatal(err)
				}
			},
			account: "admin@other.test", wantUna: true},
		{name: "explicit postmaster account on target beats target owner",
			setup: func(t *testing.T, e *pmEnv) {
				e.owner(t, "t.test", "towner", false)
				e.account(t, "pm-id", "postmaster@t.test")
			},
			want: "pm-id"},
	}
	for _, role := range []string{"postmaster", "abuse"} {
		for _, tc := range tests {
			t.Run(role+"/"+tc.name, func(t *testing.T) {
				e := aliasEnv(t)
				if tc.setup != nil {
					tc.setup(t, e)
				}
				e.r.SetPostmasterAccount(tc.account)
				addr := role + "@a.test"
				if tc.name == "explicit postmaster account on target beats target owner" && role == "abuse" {
					addr = "postmaster@a.test"
				}
				got, err := e.r.ResolveRecipient(addr)
				if tc.wantUna {
					if !errors.Is(err, ErrRecipientUnavailable) {
						t.Fatalf("got (%q, %v), want unavailable", got, err)
					}
				} else if err != nil || got != tc.want {
					t.Fatalf("got (%q, %v), want %q", got, err, tc.want)
				}
				if has := e.r.HasPostmasterTarget("a.test"); has == tc.wantUna {
					t.Fatalf("HasPostmasterTarget = %v with wantUna %v", has, tc.wantUna)
				}
			})
		}
	}
}

// Quota follows the resolved user's own domain: a message to alice@a.test is
// charged to t.test (the target), and a quota on the alias domain is not
// consulted.
func TestDomainAlias_QuotaChargedToTargetOwner(t *testing.T) {
	e := aliasEnv(t)
	qs := NewDomainQuotaStore(e.ps, &directApplier{store: e.ps})

	id, err := e.r.ResolveRecipient("alice@a.test")
	if err != nil {
		t.Fatal(err)
	}
	const limit = int64(1_048_583)
	if err := qs.SetQuota("t.test", &bongov1.DomainQuota{MaxStorageBytes: limit}); err != nil {
		t.Fatal(err)
	}
	if err := qs.SetUsage("t.test", limit-4_099); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		add  int64
		want bool
	}{{4_099, false}, {4_100, true}} {
		got, err := qs.RecipientWouldExceedQuota(id, tc.add)
		if err != nil || got != tc.want {
			t.Fatalf("add %d: got (%v, %v), want %v", tc.add, got, err, tc.want)
		}
	}
}

// Aliasing grants receipt only: alice receives alice@a.test but may not send
// as it without a verified send-as identity.
func TestDomainAlias_DoesNotGrantSendAs(t *testing.T) {
	e := aliasEnv(t)
	if id, err := e.r.ResolveRecipient("alice@a.test"); err != nil || id != "alice-id" {
		t.Fatalf("receipt: (%q, %v)", id, err)
	}
	sp := &SendAsPolicy{Addresses: e.as, Pebble: e.ps}
	if ok, err := sp.CanSendAs("alice-id", "alice@a.test"); err != nil || ok {
		t.Fatalf("CanSendAs alias address = (%v, %v), want false", ok, err)
	}
	if ok, err := sp.CanSendAs("alice-id", "alice@t.test"); err != nil || !ok {
		t.Fatalf("CanSendAs primary = (%v, %v), want true", ok, err)
	}
}
