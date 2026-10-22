package store

import (
	"errors"
	"testing"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// putProfile writes a user profile to Pebble, the durable proof that an
// account exists.
func putUserProfile(t *testing.T, ps *PebbleStore, id, email string, disabled bool) {
	t.Helper()
	data, err := proto.Marshal(&bongov1.UserProfile{Email: email, Disabled: disabled})
	if err != nil {
		t.Fatalf("marshal profile: %v", err)
	}
	if err := ps.Set([]byte("/user/"+id+"/profile"), data); err != nil {
		t.Fatalf("putProfile %q: %v", id, err)
	}
}

func TestRecipientResolver_Liveness(t *testing.T) {
	// One shared dataset: live accounts are positive controls for every tier,
	// dead and disabled accounts are the negatives.
	setup := func(t *testing.T) *RecipientResolver {
		ps, as, sys, resolver := newResolverTestEnv(t)

		// Live user with primary, alias, legacy alias and verified identity.
		putUserProfile(t, ps, "live", "live@d.test", false)
		insertUser(t, sys, "live", "live@d.test")
		putAddress(t, as, "live@d.test", "live", "primary")
		putAddress(t, as, "livealias@d.test", "live", "alias")
		seedAlias(t, ps, "livelegacy@d.test", "live@d.test")
		if err := as.Put("liveident@d.test", &bongov1.AddressRecord{UserId: "live", Domain: "d.test", Kind: "identity", Verified: true}); err != nil {
			t.Fatal(err)
		}

		// Disabled user.
		putUserProfile(t, ps, "off", "off@d.test", true)
		insertUser(t, sys, "off", "off@d.test")
		putAddress(t, as, "off@d.test", "off", "primary")
		putAddress(t, as, "offalias@d.test", "off", "alias")
		seedAlias(t, ps, "offlegacy@d.test", "off@d.test")
		if err := as.Put("offident@d.test", &bongov1.AddressRecord{UserId: "off", Domain: "d.test", Kind: "identity", Verified: true}); err != nil {
			t.Fatal(err)
		}

		// Stale follower: users row enabled, profile disabled (users-table tier only).
		putUserProfile(t, ps, "stale", "stale@d.test", true)
		insertUser(t, sys, "stale", "stale@d.test")

		// Dead user: records exist but no profile.
		insertUser(t, sys, "gone", "gone@d.test")
		putAddress(t, as, "gone@d.test", "gone", "primary")
		putAddress(t, as, "gonealias@d.test", "gone", "alias")
		if err := as.Put("goneident@d.test", &bongov1.AddressRecord{UserId: "gone", Domain: "d.test", Kind: "identity", Verified: true}); err != nil {
			t.Fatal(err)
		}
		seedAlias(t, ps, "gonelegacy@d.test", "gone@d.test")
		insertUser(t, sys, "gone2", "gone2@d.test") // 2b only

		// Stranded legacy alias: target deleted (no profile, no users row,
		// no address record) on a domain with a live catch-all. It must not
		// fall through to the catch-all owner.
		seedAlias(t, ps, "stranded@ca2.test", "deleted@ca2.test")
		seedCatchAll(t, ps, "ca2.test", "live")

		// Catch-alls: live owner, dead owner, disabled owner.
		seedCatchAll(t, ps, "live-ca.test", "live")
		seedCatchAll(t, ps, "dead-ca.test", "gone")
		seedCatchAll(t, ps, "off-ca.test", "off")

		// A disabled user whose domain also has a live catch-all must not leak
		// to the catch-all owner.
		putUserProfile(t, ps, "off3", "off3@leak.test", true)
		insertUser(t, sys, "off3", "off3@leak.test")
		putAddress(t, as, "off3@leak.test", "off3", "primary")
		seedCatchAll(t, ps, "leak.test", "live")
		return resolver
	}

	tests := []struct {
		addr   string
		wantID string // empty => must be ErrRecipientUnavailable
	}{
		{"live@d.test", "live"},
		{"livealias@d.test", "live"},
		{"livelegacy@d.test", "live"},
		{"liveident@d.test", "live"},
		{"anything@live-ca.test", "live"},

		{"off@d.test", ""},
		{"offalias@d.test", ""},
		{"offlegacy@d.test", ""},
		{"offident@d.test", ""},
		{"stale@d.test", ""},
		{"gone@d.test", ""},
		{"gonealias@d.test", ""},
		{"gonelegacy@d.test", ""},
		{"stranded@ca2.test", ""},
		{"goneident@d.test", ""},
		{"gone2@d.test", ""},
		{"x@dead-ca.test", ""},
		{"x@off-ca.test", ""},
		{"off3@leak.test", ""},
	}
	for _, tc := range tests {
		t.Run(tc.addr, func(t *testing.T) {
			resolver := setup(t)
			id, err := resolver.ResolveRecipient(tc.addr)
			if tc.wantID == "" {
				if !errors.Is(err, ErrRecipientUnavailable) {
					t.Fatalf("got id=%q err=%v, want ErrRecipientUnavailable", id, err)
				}
				return
			}
			if err != nil || id != tc.wantID {
				t.Fatalf("got id=%q err=%v, want %q", id, err, tc.wantID)
			}
		})
	}

	t.Run("unknown address stays a plain miss", func(t *testing.T) {
		_, err := setup(t).ResolveRecipient("nobody@d.test")
		if err == nil || errors.Is(err, ErrRecipientUnavailable) {
			t.Fatalf("want plain not-local error, got %v", err)
		}
	})
}

func TestRecipientResolver_ProfileReadErrorFailsOpen(t *testing.T) {
	ps, as, sys, resolver := newResolverTestEnv(t)
	insertUser(t, sys, "u", "u@d.test")
	putAddress(t, as, "u@d.test", "u", "primary")
	// A profile value that cannot be decoded is not proof the user is gone.
	if err := ps.Set([]byte("/user/u/profile"), []byte{0xff, 0xff, 0xff}); err != nil {
		t.Fatal(err)
	}
	id, err := resolver.ResolveRecipient("u@d.test")
	if err != nil || id != "u" {
		t.Fatalf("got %q, %v; want fail-open to u", id, err)
	}
}

func TestRecipientResolver_NilPebbleHonoursUsersFlag(t *testing.T) {
	_, _, sys, _ := newResolverTestEnv(t)
	insertUser(t, sys, "on", "on@d.test")
	insertUser(t, sys, "off", "off@d.test")
	if _, err := sys.DB().Exec("UPDATE users SET enabled = 0 WHERE id = 'off'"); err != nil {
		t.Fatal(err)
	}
	r := NewRecipientResolver(nil, nil, sys)
	if id, err := r.ResolveRecipient("on@d.test"); err != nil || id != "on" {
		t.Fatalf("enabled: got %q, %v", id, err)
	}
	if _, err := r.ResolveRecipient("off@d.test"); !errors.Is(err, ErrRecipientUnavailable) {
		t.Fatalf("disabled with nil pebble: got %v, want ErrRecipientUnavailable", err)
	}
}

// A legacy alias whose target has no users row is decided through the
// address index. The shared dataset has a positive control (live target) and
// the dead and disabled negatives, each on a domain with a live catch-all so
// a fall-through would be visible as a successful route.
func TestRecipientResolver_LegacyAliasAddressIndexFallback(t *testing.T) {
	setup := func(t *testing.T) (*PebbleStore, *RecipientResolver) {
		ps, as, _, resolver := newResolverTestEnv(t)
		seedCatchAll(t, ps, "fb.test", "catcher")
		putUserProfile(t, ps, "catcher", "catcher@other.test", false)

		putUserProfile(t, ps, "fblive", "fblive@fb.test", false)
		putAddress(t, as, "fblive@fb.test", "fblive", "primary")
		seedAlias(t, ps, "alias-live@fb.test", "fblive@fb.test")

		putUserProfile(t, ps, "fboff", "fboff@fb.test", true)
		putAddress(t, as, "fboff@fb.test", "fboff", "primary")
		seedAlias(t, ps, "alias-off@fb.test", "fboff@fb.test")

		putAddress(t, as, "fbgone@fb.test", "fbgone", "primary") // no profile
		seedAlias(t, ps, "alias-gone@fb.test", "fbgone@fb.test")

		// Target address record that cannot be decoded: a read error.
		seedAlias(t, ps, "alias-err@fb.test", "fberr@fb.test")
		if err := ps.Set(addressKey("fberr@fb.test"), []byte{0xff, 0xff, 0xff}); err != nil {
			t.Fatal(err)
		}
		return ps, resolver
	}

	tests := []struct {
		addr   string
		wantID string // empty => must be ErrRecipientUnavailable
	}{
		{"alias-live@fb.test", "fblive"},
		{"alias-off@fb.test", ""},
		{"alias-gone@fb.test", ""},
	}
	for _, tc := range tests {
		t.Run(tc.addr, func(t *testing.T) {
			_, resolver := setup(t)
			id, err := resolver.ResolveRecipient(tc.addr)
			if tc.wantID == "" {
				if !errors.Is(err, ErrRecipientUnavailable) {
					t.Fatalf("got id=%q err=%v, want ErrRecipientUnavailable", id, err)
				}
				return
			}
			if err != nil || id != tc.wantID {
				t.Fatalf("got id=%q err=%v, want %q", id, err, tc.wantID)
			}
		})
	}

	t.Run("address index read error is transient", func(t *testing.T) {
		_, resolver := setup(t)
		id, err := resolver.ResolveRecipient("alias-err@fb.test")
		if err == nil || errors.Is(err, ErrRecipientUnavailable) {
			t.Fatalf("got id=%q err=%v, want a non-nil error that is not ErrRecipientUnavailable", id, err)
		}
	})
}
