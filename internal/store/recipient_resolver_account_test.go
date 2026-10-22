package store

import (
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func TestRecipientResolver_ResolveAccount(t *testing.T) {
	ps, as, sys, resolver := newResolverTestEnv(t)

	// One shared dataset; live accounts are the positive controls.
	putUserProfile(t, ps, "live", "live@d.test", false)
	insertUser(t, sys, "live", "live@d.test")
	putAddress(t, as, "live@d.test", "live", "primary")
	putAddress(t, as, "livealias@d.test", "live", "alias")
	seedAlias(t, ps, "livelegacy@d.test", "live@d.test")
	if err := as.Put("liveident@d.test", &bongov1.AddressRecord{UserId: "live", Domain: "d.test", Kind: "identity", Verified: true}); err != nil {
		t.Fatal(err)
	}

	putUserProfile(t, ps, "off", "off@d.test", true)
	insertUser(t, sys, "off", "off@d.test")
	putAddress(t, as, "off@d.test", "off", "primary")

	// Catch-all domain owned by a live user: unknown@ca.test is not an account.
	seedCatchAll(t, ps, "ca.test", "live")

	// Corrupt profile: the read fails, so the account must not be accepted.
	if err := ps.Set([]byte("/user/bad/profile"), []byte{0xff, 0xff, 0xff}); err != nil {
		t.Fatal(err)
	}
	putAddress(t, as, "bad@d.test", "bad", "primary")

	tests := []struct {
		name    string
		addr    string
		want    string
		wantErr bool
	}{
		{"primary", "live@d.test", "live", false},
		{"alias", "livealias@d.test", "live", false},
		{"legacy alias", "livelegacy@d.test", "live", false},
		{"mixed case and brackets", "<Live@D.test>", "live", false},
		{"disabled", "off@d.test", "", true},
		{"unknown", "nobody@d.test", "", true},
		{"empty", "", "", true},
		{"catch-all does not name an account", "unknown@ca.test", "", true},
		{"verified identity does not name an account", "liveident@d.test", "", true},
		{"profile read error fails closed", "bad@d.test", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolver.ResolveAccount(tt.addr)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}

	// ResolveRecipient behaviour is unchanged for the same inputs.
	if got, err := resolver.ResolveRecipient("unknown@ca.test"); err != nil || got != "live" {
		t.Errorf("ResolveRecipient catch-all = %q, %v; want live", got, err)
	}
	if got, err := resolver.ResolveRecipient("liveident@d.test"); err != nil || got != "live" {
		t.Errorf("ResolveRecipient identity = %q, %v; want live", got, err)
	}
	if got, err := resolver.ResolveRecipient("bad@d.test"); err != nil || got != "bad" {
		t.Errorf("ResolveRecipient fail-open = %q, %v; want bad", got, err)
	}
}

// A resolver with no profile store cannot check existence or the disabled
// flag, so ResolveAccount must refuse rather than return an unchecked ID.
func TestRecipientResolver_ResolveAccountNoPebble(t *testing.T) {
	ps, as, sys, full := newResolverTestEnv(t)
	putUserProfile(t, ps, "live", "live@d.test", false)
	insertUser(t, sys, "live", "live@d.test")
	putAddress(t, as, "live@d.test", "live", "primary")

	// Positive control: the same dataset resolves with a profile store.
	if got, err := full.ResolveAccount("live@d.test"); err != nil || got != "live" {
		t.Fatalf("control ResolveAccount = %q, %v; want live", got, err)
	}

	for name, r := range map[string]*RecipientResolver{
		"address store only": NewRecipientResolver(as, nil, nil),
		"users table only":   NewRecipientResolver(nil, nil, sys),
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := r.ResolveAccount("live@d.test"); err == nil {
				t.Errorf("ResolveAccount = %q, nil; want error", got)
			}
		})
	}
}
