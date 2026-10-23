package store

import "testing"

// SRS addresses are handled before the resolver is consulted. They must never
// reach the domain catch-all, or a bounce would run the catch-all owner's rules
// (including forwards) and be stored in their mailbox.
func TestRecipientResolver_SRSNotCatchAll(t *testing.T) {
	ps, _, _, resolver := newResolverTestEnv(t)

	const ownerID = "catchall-owner"
	putUserProfile(t, ps, ownerID, "owner@catchall.example.com", false)
	seedCatchAll(t, ps, "catchall.example.com", ownerID)

	tests := []struct {
		addr    string
		wantErr bool
	}{
		{"SRS0=abcdefgh=aa=example.org=bob@catchall.example.com", true},
		{"srs0=abcdefgh=aa=example.org=bob@catchall.example.com", true},
		{"SRS1=abcdefgh=first.test==x=y=z=w@catchall.example.com", true},
		{"other@catchall.example.com", false},
		{"srsly@catchall.example.com", false},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			id, err := resolver.ResolveRecipient(tt.addr)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("resolved to %q, want error", id)
				}
				return
			}
			if err != nil || id != ownerID {
				t.Fatalf("got (%q, %v), want catch-all owner", id, err)
			}
		})
	}
}

// The reserved no-relay sender is refused at every tier, so neither the
// catch-all owner nor a user, alias or identity holding the local part is
// resolved for it (the rules and antispam stages resolve before deliver runs).
func TestRecipientResolver_NoRelayNeverResolves(t *testing.T) {
	ps, as, _, resolver := newResolverTestEnv(t)

	const ownerID = "catchall-owner"
	putUserProfile(t, ps, ownerID, "owner@catchall.example.com", false)
	seedCatchAll(t, ps, "catchall.example.com", ownerID)
	putUserProfile(t, ps, "squatter", "squatter@catchall.example.com", false)
	putAddress(t, as, "bongo-bounce-sink@squatter.example.com", "squatter", "alias")

	for _, addr := range []string{
		"bongo-bounce-sink@catchall.example.com",
		"BONGO-BOUNCE-SINK@catchall.example.com",
		"<bongo-bounce-sink@catchall.example.com>",
		"bongo-bounce-sink@squatter.example.com",
	} {
		if id, err := resolver.ResolveRecipient(addr); err == nil {
			t.Errorf("%q resolved to %q, want error", addr, id)
		}
	}
	if id, err := resolver.ResolveRecipient("bongo-bounce-sink2@catchall.example.com"); err != nil || id != ownerID {
		t.Errorf("neighbouring local part: got (%q, %v), want catch-all owner", id, err)
	}
}
