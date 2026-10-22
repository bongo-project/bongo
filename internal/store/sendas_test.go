package store

import (
	"errors"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/testutil"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func seedSendAsRecords(t *testing.T, as *AddressStore) {
	t.Helper()
	for _, r := range testutil.SendAsRecords {
		rec := &bongov1.AddressRecord{UserId: r.UserID, Kind: r.Kind, CanSend: r.CanSend, Verified: r.Verified}
		if err := as.Put(r.Addr, rec); err != nil {
			t.Fatalf("Put %s: %v", r.Addr, err)
		}
	}
}

func TestCanSendAs(t *testing.T) {
	as := newAddressTestEnv(t)
	seedSendAsRecords(t, as)

	for _, tc := range testutil.SendAsCases {
		if tc.Addr == "" {
			continue
		}
		t.Run(tc.Name, func(t *testing.T) {
			got, err := CanSendAs(as, testutil.SendAsUserID, "Alice@Example.com", tc.Addr)
			if err != nil {
				t.Fatalf("CanSendAs: %v", err)
			}
			if got != tc.Want {
				t.Fatalf("CanSendAs(%q) = %v, want %v", tc.Addr, got, tc.Want)
			}
		})
	}
}

func TestCanSendAsEdges(t *testing.T) {
	as := newAddressTestEnv(t)
	seedSendAsRecords(t, as)

	if ok, _ := CanSendAs(as, "u-alice", "alice@example.com", "  "); ok {
		t.Error("blank address must be refused")
	}
	if ok, _ := CanSendAs(nil, "u-alice", "alice@example.com", "verified@gmail.com"); ok {
		t.Error("nil store must permit only the primary")
	}
	if ok, _ := CanSendAs(nil, "u-alice", "alice@example.com", "ALICE@example.com"); !ok {
		t.Error("nil store must still permit the primary")
	}
	if ok, _ := CanSendAs(as, "u-alice", "", "alice@example.com"); ok {
		t.Error("empty primary must not match anything")
	}
}

func TestSendAsPolicyProfileHandling(t *testing.T) {
	ps, err := NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	as := NewAddressStore(ps, &directApplier{store: ps}, nil)
	pol := &SendAsPolicy{Addresses: as, Pebble: ps}

	// Missing profile: refused without an error, even for a verified
	// identity that is still on file.
	seedSendAsRecords(t, as)
	if ok, err := pol.CanSendAs("u-alice", "verified@gmail.com"); ok || err != nil {
		t.Fatalf("missing profile: ok=%v err=%v, want false,nil", ok, err)
	}

	data, err := proto.Marshal(&bongov1.UserProfile{Email: "Alice@Example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if err := ps.Set([]byte("/user/u-alice/profile"), data); err != nil {
		t.Fatal(err)
	}
	if ok, err := pol.CanSendAs("u-alice", "alice@example.com"); !ok || err != nil {
		t.Fatalf("primary from profile: ok=%v err=%v", ok, err)
	}
	if ok, _ := pol.CanSendAs("u-alice", "verified@gmail.com"); !ok {
		t.Fatal("verified identity should be allowed")
	}

	if err := ps.Set([]byte("/user/u-bad/profile"), []byte{0xff, 0xff}); err != nil {
		t.Fatal(err)
	}
	if _, err := pol.CanSendAs("u-bad", "x@y.example"); err == nil {
		t.Fatal("corrupt profile must return an error (fail closed with retry)")
	}
}

func TestCanSendAsStoreReadErrorFailsClosed(t *testing.T) {
	as := newAddressTestEnv(t)
	// A corrupt record makes AddressStore.Get return an error, not a miss.
	if err := as.pebble.Set(addressKey("broken@gmail.com"), []byte{0xff, 0xff}); err != nil {
		t.Fatal(err)
	}
	ok, err := CanSendAs(as, "u-alice", "alice@example.com", "broken@gmail.com")
	if ok || err == nil {
		t.Fatalf("CanSendAs on unreadable record = (%v, %v), want (false, non-nil)", ok, err)
	}
}

func TestSendAsPolicyProfileReadErrorFailsClosed(t *testing.T) {
	ps, err := NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	as := NewAddressStore(ps, &directApplier{store: ps}, nil)
	seedSendAsRecords(t, as)
	pol := &SendAsPolicy{
		Addresses: as,
		Pebble:    ps,
		get:       func([]byte) ([]byte, error) { return nil, errors.New("pebble get: io failure") },
	}
	// A verified identity is on file, so a fail-open bug would grant it.
	ok, err := pol.CanSendAs("u-alice", "verified@gmail.com")
	if ok || err == nil {
		t.Fatalf("CanSendAs on failing profile read = (%v, %v), want (false, non-nil)", ok, err)
	}
}
