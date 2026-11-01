package store

import (
	"errors"
	"testing"
)

// pmEnv seeds a hosted domain d.test with an enabled owner "owner-id", an
// enabled account admin@other.test, and an optional catch-all.
type pmEnv struct {
	ps  *PebbleStore
	as  *AddressStore
	sys *SystemIndex
	r   *RecipientResolver

	role string // postmaster or abuse, for fixtures that name the address
}

func newPMEnv(t *testing.T) *pmEnv {
	t.Helper()
	ps, as, sys, r := newResolverTestEnv(t)
	return &pmEnv{ps: ps, as: as, sys: sys, r: r}
}

func (e *pmEnv) domain(t *testing.T, name string) {
	t.Helper()
	setDomainState(t, e.ps, name, "enabled")
}

func (e *pmEnv) owner(t *testing.T, domain, id string, disabled bool) {
	t.Helper()
	putUserProfile(t, e.ps, id, id+"@"+domain, disabled)
	if err := e.ps.Set(domainOwnerKey(domain), []byte(id)); err != nil {
		t.Fatal(err)
	}
}

func (e *pmEnv) account(t *testing.T, id, addr string) {
	t.Helper()
	insertUser(t, e.sys, id, addr)
	putUserProfile(t, e.ps, id, addr, false)
	putAddress(t, e.as, addr, id, "primary")
}

func TestRecipientResolver_PostmasterTier(t *testing.T) {
	type tc struct {
		name    string
		setup   func(t *testing.T, e *pmEnv)
		account string // postmaster_account
		addr    string // %s replaced by the role
		want    string
		wantErr bool
		wantUna bool // want ErrRecipientUnavailable
	}
	tests := []tc{
		{name: "mixed case delivers to owner",
			setup: func(t *testing.T, e *pmEnv) { e.domain(t, "d.test"); e.owner(t, "d.test", "owner-id", false) },
			addr:  "<%S@D.Test>", want: "owner-id"},
		{name: "no owner uses configured account",
			setup: func(t *testing.T, e *pmEnv) {
				e.domain(t, "d.test")
				e.account(t, "admin-id", "admin@other.test")
			},
			account: "Admin@Other.test", addr: "%s@d.test", want: "admin-id"},
		{name: "no owner and no account is unavailable",
			setup: func(t *testing.T, e *pmEnv) { e.domain(t, "d.test") },
			addr:  "%s@d.test", wantErr: true, wantUna: true},
		{name: "configured account that does not exist is unavailable",
			setup:   func(t *testing.T, e *pmEnv) { e.domain(t, "d.test") },
			account: "ghost@other.test", addr: "%s@d.test", wantErr: true, wantUna: true},
		{name: "owner beats configured account",
			setup: func(t *testing.T, e *pmEnv) {
				e.domain(t, "d.test")
				e.owner(t, "d.test", "owner-id", false)
				e.account(t, "admin-id", "admin@other.test")
			},
			account: "admin@other.test", addr: "%s@d.test", want: "owner-id"},
		{name: "explicit address record wins",
			setup: func(t *testing.T, e *pmEnv) {
				e.domain(t, "d.test")
				e.owner(t, "d.test", "owner-id", false)
				e.account(t, "pm-id", "x@d.test")
				putAddress(t, e.as, e.role+"@d.test", "pm-id", "alias")
			},
			addr: "%s@d.test", want: "pm-id"},
		{name: "legacy alias wins",
			setup: func(t *testing.T, e *pmEnv) {
				e.domain(t, "d.test")
				e.owner(t, "d.test", "owner-id", false)
				e.account(t, "pm-id", "legacy@d.test")
				if err := e.ps.Set([]byte("/system/alias/"+e.role+"@d.test"), []byte("legacy@d.test")); err != nil {
					t.Fatal(err)
				}
			},
			addr: "%s@d.test", want: "pm-id"},
		{name: "tier beats catch-all",
			setup: func(t *testing.T, e *pmEnv) {
				e.domain(t, "d.test")
				e.owner(t, "d.test", "owner-id", false)
				putUserProfile(t, e.ps, "ca-id", "ca@d.test", false)
				seedCatchAll(t, e.ps, "d.test", "ca-id")
			},
			addr: "%s@d.test", want: "owner-id"},
		{name: "no live target falls through to catch-all",
			setup: func(t *testing.T, e *pmEnv) {
				e.domain(t, "d.test")
				putUserProfile(t, e.ps, "ca-id", "ca@d.test", false)
				seedCatchAll(t, e.ps, "d.test", "ca-id")
			},
			addr: "%s@d.test", want: "ca-id"},
		{name: "domain sub-key path is not a hosted domain",
			setup: func(t *testing.T, e *pmEnv) {
				e.domain(t, "d.test")
				e.owner(t, "d.test", "owner-id", false) // /system/domain/d.test/owner exists
				e.account(t, "admin-id", "admin@other.test")
			},
			account: "admin@other.test", addr: "%s@d.test/owner", wantErr: true},
		{name: "unhosted domain is not resolved",
			setup: func(t *testing.T, e *pmEnv) {
				e.owner(t, "d.test", "owner-id", false) // owner key but no domain key
				e.account(t, "admin-id", "admin@other.test")
			},
			account: "admin@other.test", addr: "%s@d.test", wantErr: true},
		{name: "disabled owner falls through to configured account",
			setup: func(t *testing.T, e *pmEnv) {
				e.domain(t, "d.test")
				e.owner(t, "d.test", "owner-id", true)
				e.account(t, "admin-id", "admin@other.test")
			},
			account: "admin@other.test", addr: "%s@d.test", want: "admin-id"},
		{name: "deleted owner falls through to configured account",
			setup: func(t *testing.T, e *pmEnv) {
				e.domain(t, "d.test")
				if err := e.ps.Set(domainOwnerKey("d.test"), []byte("gone-id")); err != nil {
					t.Fatal(err)
				}
				e.account(t, "admin-id", "admin@other.test")
			},
			account: "admin@other.test", addr: "%s@d.test", want: "admin-id"},
		{name: "disabled owner and no account is unavailable",
			setup: func(t *testing.T, e *pmEnv) { e.domain(t, "d.test"); e.owner(t, "d.test", "owner-id", true) },
			addr:  "%s@d.test", wantErr: true, wantUna: true},
		{name: "disabled domain is unavailable",
			setup: func(t *testing.T, e *pmEnv) {
				setDomainState(t, e.ps, "d.test", "disabled")
				e.owner(t, "d.test", "owner-id", false)
			},
			addr: "%s@d.test", wantErr: true, wantUna: true},
		{name: "postmaster2 is unaffected",
			setup: func(t *testing.T, e *pmEnv) { e.domain(t, "d.test"); e.owner(t, "d.test", "owner-id", false) },
			addr:  "%s2@d.test", wantErr: true},
		{name: "x prefix is unaffected",
			setup: func(t *testing.T, e *pmEnv) { e.domain(t, "d.test"); e.owner(t, "d.test", "owner-id", false) },
			addr:  "x%s@d.test", wantErr: true},
		{name: "plus tag is unaffected",
			setup: func(t *testing.T, e *pmEnv) { e.domain(t, "d.test"); e.owner(t, "d.test", "owner-id", false) },
			addr:  "%s+x@d.test", wantErr: true},
	}
	for _, role := range []string{"postmaster", "abuse"} {
		for _, tt := range tests {
			t.Run(role+"/"+tt.name, func(t *testing.T) {
				e := newPMEnv(t)
				e.r.SetPostmasterAccount(tt.account)
				e.role = role
				tt.setup(t, e)
				got, err := e.r.ResolveRecipient(expand(tt.addr, role))
				if tt.wantErr {
					if err == nil {
						t.Fatalf("got %q, want error", got)
					}
					if errors.Is(err, ErrRecipientUnavailable) != tt.wantUna {
						t.Fatalf("err = %v, want ErrRecipientUnavailable = %v", err, tt.wantUna)
					}
					return
				}
				if err != nil || got != tt.want {
					t.Fatalf("got %q, %v; want %q", got, err, tt.want)
				}
			})
		}
	}
}

// expand substitutes the role into a template: %S is capitalised, %s lower.
func expand(tmpl, role string) string {
	out := ""
	for i := 0; i < len(tmpl); i++ {
		if tmpl[i] == '%' && i+1 < len(tmpl) && (tmpl[i+1] == 's' || tmpl[i+1] == 'S') {
			if tmpl[i+1] == 'S' {
				out += string(role[0]-32) + role[1:]
			} else {
				out += role
			}
			i++
			continue
		}
		out += string(tmpl[i])
	}
	return out
}

func TestRecipientResolver_PostmasterNotInAccountLookup(t *testing.T) {
	for _, role := range []string{"postmaster", "abuse"} {
		t.Run(role, func(t *testing.T) {
			e := newPMEnv(t)
			e.domain(t, "d.test")
			e.owner(t, "d.test", "owner-id", false)
			e.account(t, "admin-id", "admin@other.test")
			e.r.SetPostmasterAccount("admin@other.test")
			if got, err := e.r.ResolveAccount(role + "@d.test"); err == nil {
				t.Fatalf("ResolveAccount(%s@d.test) = %q, want error", role, got)
			}
			if got, err := e.r.ResolveRecipient(role + "@d.test"); err != nil || got != "owner-id" {
				t.Fatalf("positive control: got %q, %v", got, err)
			}
		})
	}
}

func TestRecipientResolver_BarePostmaster(t *testing.T) {
	e := newPMEnv(t)
	e.account(t, "admin-id", "admin@other.test")
	if _, err := e.r.ResolveRecipient("postmaster"); err == nil {
		t.Fatal("bare postmaster with no account resolved")
	}
	e.r.SetPostmasterAccount("admin@other.test")
	if got, err := e.r.ResolveRecipient("<Postmaster>"); err != nil || got != "admin-id" {
		t.Fatalf("got %q, %v; want admin-id", got, err)
	}
	if _, err := e.r.ResolveRecipient("abuse"); err == nil {
		t.Fatal("bare abuse resolved")
	}
	if got, err := e.r.ResolveAccount("postmaster"); err == nil {
		t.Fatalf("ResolveAccount(bare postmaster) = %q, want error", got)
	}
}

func TestRecipientResolver_PostmasterAccountIsItselfRole(t *testing.T) {
	// A configured account that is itself postmaster@ some domain must not
	// recurse: strict resolution skips the tier.
	e := newPMEnv(t)
	e.domain(t, "d.test")
	e.r.SetPostmasterAccount("postmaster@d.test")
	if _, err := e.r.ResolveRecipient("abuse@d.test"); !errors.Is(err, ErrRecipientUnavailable) {
		t.Fatalf("err = %v, want ErrRecipientUnavailable and no recursion", err)
	}
}

func TestRecipientResolver_DomainsWithoutPostmaster(t *testing.T) {
	e := newPMEnv(t)
	e.domain(t, "ok.test")
	e.owner(t, "ok.test", "o1", false)
	e.domain(t, "bare.test")
	e.domain(t, "deadowner.test")
	e.owner(t, "deadowner.test", "o2", true)
	setDomainState(t, e.ps, "off.test", "disabled")
	got, err := e.r.DomainsWithoutPostmaster()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "bare.test" || got[1] != "deadowner.test" {
		t.Fatalf("got %v, want [bare.test deadowner.test]", got)
	}
	e.account(t, "admin-id", "admin@other.test")
	e.r.SetPostmasterAccount("admin@other.test")
	if got, _ := e.r.DomainsWithoutPostmaster(); len(got) != 0 {
		t.Fatalf("with fallback account got %v, want none", got)
	}
}

// TestRecipientResolver_DomainsWithoutPostmasterCountsEveryTier checks that the
// startup report asks whether postmaster@ and abuse@ actually resolve, not
// only whether the owner/account tier has a target. An explicit address, a
// legacy alias or a catch-all delivers them, and has done since before the
// tier existed, so such a domain must not be reported as refusing them.
func TestRecipientResolver_DomainsWithoutPostmasterCountsEveryTier(t *testing.T) {
	e := newPMEnv(t)

	e.domain(t, "explicit.test")
	e.account(t, "pm-id", "ops@explicit.test")
	putAddress(t, e.as, "postmaster@explicit.test", "pm-id", "alias")
	putAddress(t, e.as, "abuse@explicit.test", "pm-id", "alias")

	e.domain(t, "legacy.test")
	e.account(t, "lg-id", "ops@legacy.test")
	for _, role := range []string{"postmaster", "abuse"} {
		if err := e.ps.Set([]byte("/system/alias/"+role+"@legacy.test"), []byte("ops@legacy.test")); err != nil {
			t.Fatal(err)
		}
	}

	e.domain(t, "catchall.test")
	putUserProfile(t, e.ps, "ca-id", "ca@catchall.test", false)
	seedCatchAll(t, e.ps, "catchall.test", "ca-id")

	// Only postmaster@ is explicit: abuse@ is still refused, so it is reported.
	e.domain(t, "half.test")
	e.account(t, "hf-id", "ops@half.test")
	putAddress(t, e.as, "postmaster@half.test", "hf-id", "alias")

	e.domain(t, "bare.test")

	got, err := e.r.DomainsWithoutPostmaster()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "bare.test" || got[1] != "half.test" {
		t.Fatalf("got %v, want [bare.test half.test]", got)
	}
}
