package store

import (
	"errors"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// setDomainState writes the /system/domain/{d} value the admin toggle writes.
func setDomainState(t *testing.T, ps *PebbleStore, domain, state string) {
	t.Helper()
	if err := ps.Set([]byte("/system/domain/"+domain), []byte(state)); err != nil {
		t.Fatalf("set domain %s %s: %v", domain, state, err)
	}
}

// A disabled domain receives no mail through any tier, and re-enabling it
// restores delivery. A user on another, enabled domain is the positive control.
func TestRecipientResolver_DisabledDomain(t *testing.T) {
	ps, as, sys, resolver := newResolverTestEnv(t)

	putUserProfile(t, ps, "u1", "user@off.test", false)
	insertUser(t, sys, "u1", "user@off.test")
	putAddress(t, as, "user@off.test", "u1", "primary")
	putAddress(t, as, "alias@off.test", "u1", "alias")
	seedAlias(t, ps, "legacy@off.test", "user@off.test")
	if err := as.Put("ident@off.test", &bongov1.AddressRecord{UserId: "u1", Domain: "off.test", Kind: "identity", Verified: true}); err != nil {
		t.Fatal(err)
	}
	seedCatchAll(t, ps, "off.test", "u1")

	putUserProfile(t, ps, "u2", "user@on.test", false)
	insertUser(t, sys, "u2", "user@on.test")
	putAddress(t, as, "user@on.test", "u2", "primary")

	setDomainState(t, ps, "off.test", "disabled")
	setDomainState(t, ps, "on.test", "enabled")

	for _, addr := range []string{
		"user@off.test", "USER@Off.Test", "alias@off.test", "legacy@off.test",
		"ident@off.test", "anything@off.test",
	} {
		if _, err := resolver.ResolveRecipient(addr); !errors.Is(err, ErrRecipientUnavailable) {
			t.Errorf("ResolveRecipient(%s) on a disabled domain: err = %v, want ErrRecipientUnavailable", addr, err)
		}
	}
	if id, err := resolver.ResolveRecipient("user@on.test"); err != nil || id != "u2" {
		t.Errorf("ResolveRecipient(user@on.test) = %q, %v; want u2", id, err)
	}

	// Disabling stops mail only: the account still exists for authorisation.
	if id, err := resolver.ResolveAccount("user@off.test"); err != nil || id != "u1" {
		t.Errorf("ResolveAccount(user@off.test) = %q, %v; want u1", id, err)
	}

	setDomainState(t, ps, "off.test", "enabled")
	if id, err := resolver.ResolveRecipient("user@off.test"); err != nil || id != "u1" {
		t.Errorf("after re-enable: ResolveRecipient(user@off.test) = %q, %v; want u1", id, err)
	}
}
