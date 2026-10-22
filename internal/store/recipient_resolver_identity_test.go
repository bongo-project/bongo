package store

import (
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// putIdentity writes an identity record with the given verified flag.
func putIdentity(t *testing.T, as *AddressStore, addr, userID string, verified bool) {
	t.Helper()
	if err := as.Put(addr, &bongov1.AddressRecord{
		UserId: userID, Domain: domainOf(addr), Kind: "identity", Verified: verified,
	}); err != nil {
		t.Fatalf("putIdentity %q: %v", addr, err)
	}
}

func TestResolve_UnverifiedIdentityDoesNotRoute(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T, ps *PebbleStore, as *AddressStore, sys *SystemIndex)
		addr    string
		want    string
		wantErr bool
	}{
		{
			name: "unverified identity on account without address record does not hijack",
			setup: func(t *testing.T, _ *PebbleStore, as *AddressStore, sys *SystemIndex) {
				insertUser(t, sys, "userB", "b@example.com")
				putIdentity(t, as, "b@example.com", "userA", false)
			},
			addr: "b@example.com", want: "userB",
		},
		{
			name: "unverified identity on external address does not route",
			setup: func(t *testing.T, _ *PebbleStore, as *AddressStore, _ *SystemIndex) {
				putIdentity(t, as, "x@example.org", "userA", false)
			},
			addr: "x@example.org", wantErr: true,
		},
		{
			name: "verified identity on external address routes to owner",
			setup: func(t *testing.T, _ *PebbleStore, as *AddressStore, _ *SystemIndex) {
				putIdentity(t, as, "x@example.org", "userA", true)
			},
			addr: "x@example.org", want: "userA",
		},
		{
			name: "verified identity colliding with an account loses to the account",
			setup: func(t *testing.T, _ *PebbleStore, as *AddressStore, sys *SystemIndex) {
				insertUser(t, sys, "userB", "b@example.com")
				putIdentity(t, as, "b@example.com", "userA", true)
			},
			addr: "b@example.com", want: "userB",
		},
		{
			name: "verified identity beats catch-all",
			setup: func(t *testing.T, ps *PebbleStore, as *AddressStore, _ *SystemIndex) {
				seedCatchAll(t, ps, "example.net", "catcher")
				putIdentity(t, as, "who@example.net", "userA", true)
			},
			addr: "who@example.net", want: "userA",
		},
		{
			name: "unverified identity loses to catch-all",
			setup: func(t *testing.T, ps *PebbleStore, as *AddressStore, _ *SystemIndex) {
				seedCatchAll(t, ps, "example.net", "catcher")
				putIdentity(t, as, "who@example.net", "userA", false)
			},
			addr: "who@example.net", want: "catcher",
		},
		{
			name: "primary and alias records route at tier 1",
			setup: func(t *testing.T, _ *PebbleStore, as *AddressStore, _ *SystemIndex) {
				putAddress(t, as, "p@example.com", "userP", "primary")
				putAddress(t, as, "al@example.com", "userP", "alias")
			},
			addr: "al@example.com", want: "userP",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ps, as, sys, resolver := newResolverTestEnv(t)
			tt.setup(t, ps, as, sys)
			got, err := resolver.ResolveRecipient(tt.addr)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("got %q, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveRecipient: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
