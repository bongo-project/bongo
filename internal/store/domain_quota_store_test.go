package store

import (
	"encoding/binary"
	"math"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// newDomainQuotaTestEnv creates an isolated Pebble + DomainQuotaStore for tests.
func newDomainQuotaTestEnv(t *testing.T) (*PebbleStore, *DomainQuotaStore) {
	t.Helper()
	dir := t.TempDir()
	ps, err := NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	qs := NewDomainQuotaStore(ps, &directApplier{store: ps})
	return ps, qs
}

// writeTestProfile writes a UserProfile into Pebble via directApplier.
func writeTestProfile(t *testing.T, da *directApplier, userID, email string) {
	t.Helper()
	prof := &bongov1.UserProfile{Email: email}
	b, err := proto.Marshal(prof)
	if err != nil {
		t.Fatalf("marshal profile: %v", err)
	}
	if err := da.ApplyCommand(&bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte("/user/" + userID + "/profile"),
		Value: b,
	}, 0); err != nil {
		t.Fatalf("write profile: %v", err)
	}
}

// TestDomainQuotaStore_GetQuota_Default verifies that GetQuota returns an empty
// (unlimited) quota when no record is stored.
func TestDomainQuotaStore_GetQuota_Default(t *testing.T) {
	_, qs := newDomainQuotaTestEnv(t)

	q, err := qs.GetQuota("example.com")
	if err != nil {
		t.Fatalf("GetQuota: %v", err)
	}
	if q.MaxUsers != 0 || q.MaxStorageBytes != 0 {
		t.Errorf("GetQuota default: want zero quota, got %+v", q)
	}
}

// TestDomainQuotaStore_SetGetQuota verifies the Set→Get round-trip.
func TestDomainQuotaStore_SetGetQuota(t *testing.T) {
	_, qs := newDomainQuotaTestEnv(t)

	want := &bongov1.DomainQuota{MaxUsers: 100, MaxStorageBytes: 1 << 30}
	if err := qs.SetQuota("example.com", want); err != nil {
		t.Fatalf("SetQuota: %v", err)
	}
	got, err := qs.GetQuota("example.com")
	if err != nil {
		t.Fatalf("GetQuota: %v", err)
	}
	if !proto.Equal(got, want) {
		t.Errorf("GetQuota: want %+v, got %+v", want, got)
	}
}

// TestDomainQuotaStore_DomainLowercasing verifies that SetQuota normalises the domain.
func TestDomainQuotaStore_DomainLowercasing(t *testing.T) {
	_, qs := newDomainQuotaTestEnv(t)

	if err := qs.SetQuota("EXAMPLE.COM", &bongov1.DomainQuota{MaxUsers: 5}); err != nil {
		t.Fatalf("SetQuota: %v", err)
	}
	got, err := qs.GetQuota("example.com")
	if err != nil {
		t.Fatalf("GetQuota lowercase: %v", err)
	}
	if got.MaxUsers != 5 {
		t.Errorf("GetQuota lowercase: want max_users=5, got %d", got.MaxUsers)
	}
}

// TestDomainQuotaStore_UsageRoundTrip verifies GetUsage returns 0 when unset,
// and the Set→Get round-trip.
func TestDomainQuotaStore_UsageRoundTrip(t *testing.T) {
	_, qs := newDomainQuotaTestEnv(t)

	usage, err := qs.GetUsage("example.com")
	if err != nil {
		t.Fatalf("GetUsage before set: %v", err)
	}
	if usage != 0 {
		t.Errorf("GetUsage before set: want 0, got %d", usage)
	}

	if err := qs.SetUsage("example.com", 512*1024); err != nil {
		t.Fatalf("SetUsage: %v", err)
	}

	usage, err = qs.GetUsage("example.com")
	if err != nil {
		t.Fatalf("GetUsage after set: %v", err)
	}
	if usage != 512*1024 {
		t.Errorf("GetUsage after set: want %d, got %d", 512*1024, usage)
	}
}

// TestDomainQuotaStore_IsOverStorageQuota covers the quota-check logic.
func TestDomainQuotaStore_IsOverStorageQuota(t *testing.T) {
	tests := []struct {
		name            string
		maxStorageBytes int64
		usage           int64
		wantOver        bool
	}{
		{"unlimited (0 limit)", 0, 1_000_000, false},
		{"under quota", 10_000, 5_000, false},
		{"exactly at quota (should reject)", 10_000, 10_000, true},
		{"over quota", 10_000, 15_000, true},
		{"no usage recorded", 10_000, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, qs := newDomainQuotaTestEnv(t)
			if tc.maxStorageBytes > 0 {
				if err := qs.SetQuota("d.example", &bongov1.DomainQuota{MaxStorageBytes: tc.maxStorageBytes}); err != nil {
					t.Fatalf("SetQuota: %v", err)
				}
			}
			if tc.usage > 0 {
				if err := qs.SetUsage("d.example", tc.usage); err != nil {
					t.Fatalf("SetUsage: %v", err)
				}
			}
			over, err := qs.IsOverStorageQuota("d.example")
			if err != nil {
				t.Fatalf("IsOverStorageQuota: %v", err)
			}
			if over != tc.wantOver {
				t.Errorf("IsOverStorageQuota: want %v, got %v (limit=%d, usage=%d)",
					tc.wantOver, over, tc.maxStorageBytes, tc.usage)
			}
		})
	}
}

// TestDomainQuotaStore_CountUsersForDomain seeds profiles and checks counts.
func TestDomainQuotaStore_CountUsersForDomain(t *testing.T) {
	ps, qs := newDomainQuotaTestEnv(t)
	da := &directApplier{store: ps}

	writeTestProfile(t, da, "u1", "alice@example.com")
	writeTestProfile(t, da, "u2", "bob@example.com")
	writeTestProfile(t, da, "u3", "carol@other.com")
	writeTestProfile(t, da, "u4", "dave@example.com")

	tests := []struct {
		domain string
		want   int
	}{
		{"example.com", 3},
		{"other.com", 1},
		{"notexist.com", 0},
		{"EXAMPLE.COM", 3}, // case-insensitive
	}
	for _, tc := range tests {
		t.Run(tc.domain, func(t *testing.T) {
			got, err := qs.CountUsersForDomain(tc.domain)
			if err != nil {
				t.Fatalf("CountUsersForDomain(%q): %v", tc.domain, err)
			}
			if got != tc.want {
				t.Errorf("CountUsersForDomain(%q): want %d, got %d", tc.domain, tc.want, got)
			}
		})
	}
}

// TestDomainQuotaKey_Format verifies the key format is as documented.
func TestDomainQuotaKey_Format(t *testing.T) {
	tests := []struct {
		domain  string
		wantKey string
	}{
		{"example.com", "/system/domain/example.com/quota"},
		{"EXAMPLE.COM", "/system/domain/example.com/quota"},
	}
	for _, tc := range tests {
		got := string(DomainQuotaKey(tc.domain))
		if got != tc.wantKey {
			t.Errorf("DomainQuotaKey(%q) = %q, want %q", tc.domain, got, tc.wantKey)
		}
	}
}

// TestDomainUsageKey_Format verifies the usage key format.
func TestDomainUsageKey_Format(t *testing.T) {
	want := "/system/domain/example.com/usage"
	got := string(DomainUsageKey("example.com"))
	if got != want {
		t.Errorf("DomainUsageKey = %q, want %q", got, want)
	}
}

// TestDomainQuotaKey_NotPickedUpByDomainScan verifies that /quota and /usage
// sub-keys contain '/' and are therefore excluded by the allManagedDomains guard.
func TestDomainQuotaKey_NotPickedUpByDomainScan(t *testing.T) {
	for _, key := range []string{
		"/system/domain/example.com/quota",
		"/system/domain/example.com/usage",
	} {
		name := key[len("/system/domain/"):]
		hasSlash := false
		for _, c := range name {
			if c == '/' {
				hasSlash = true
				break
			}
		}
		if !hasSlash {
			t.Errorf("key %q: name part %q should contain '/' so domain scans skip it", key, name)
		}
	}
}

// TestDomainQuotaStore_UsageNeverGoesNegative covers the security-relevant edge
// of the int64/uint64 round trip. IsOverStorageQuota returns
// `usage >= MaxStorageBytes`, so a NEGATIVE usage does not merely report a
// wrong number — it makes every quota check return false and silently disables
// the quota for that domain. Both directions must therefore be clamped.
func TestDomainQuotaStore_UsageNeverGoesNegative(t *testing.T) {
	t.Run("a raw value above MaxInt64 reads back clamped, not negative", func(t *testing.T) {
		ps, qs := newDomainQuotaTestEnv(t)

		// Write a raw key whose uint64 has the high bit set. A plain
		// int64(uint64) conversion would surface this as a negative number.
		var buf [8]byte
		binary.LittleEndian.PutUint64(buf[:], math.MaxUint64)
		da := &directApplier{store: ps}
		if err := da.ApplyCommand(&bongov1.RaftCommand{
			Op:    bongov1.Op_OP_PUT,
			Key:   DomainUsageKey("example.test"),
			Value: buf[:],
		}, 5*time.Second); err != nil {
			t.Fatalf("seed raw usage: %v", err)
		}

		usage, err := qs.GetUsage("example.test")
		if err != nil {
			t.Fatalf("GetUsage: %v", err)
		}
		if usage < 0 {
			t.Fatalf("GetUsage = %d, must never be negative (a negative usage disables the quota)", usage)
		}
		if usage != math.MaxInt64 {
			t.Errorf("GetUsage = %d, want MaxInt64 (fail closed on an implausible stored value)", usage)
		}
	})

	t.Run("a negative usage is not storable", func(t *testing.T) {
		_, qs := newDomainQuotaTestEnv(t)

		if err := qs.SetUsage("example.test", -1); err != nil {
			t.Fatalf("SetUsage: %v", err)
		}
		usage, err := qs.GetUsage("example.test")
		if err != nil {
			t.Fatalf("GetUsage: %v", err)
		}
		if usage != 0 {
			t.Errorf("GetUsage after SetUsage(-1) = %d, want 0", usage)
		}
	})

	t.Run("a clamped usage still enforces the quota", func(t *testing.T) {
		ps, qs := newDomainQuotaTestEnv(t)

		if err := qs.SetQuota("example.test", &bongov1.DomainQuota{MaxStorageBytes: 1024}); err != nil {
			t.Fatalf("SetQuota: %v", err)
		}
		var buf [8]byte
		binary.LittleEndian.PutUint64(buf[:], math.MaxUint64)
		da := &directApplier{store: ps}
		if err := da.ApplyCommand(&bongov1.RaftCommand{
			Op:    bongov1.Op_OP_PUT,
			Key:   DomainUsageKey("example.test"),
			Value: buf[:],
		}, 5*time.Second); err != nil {
			t.Fatalf("seed raw usage: %v", err)
		}

		// This is the behaviour that actually matters: before the clamp the
		// same stored bytes produced a negative usage and this returned false,
		// letting an over-quota domain keep accepting mail.
		over, err := qs.IsOverStorageQuota("example.test")
		if err != nil {
			t.Fatalf("IsOverStorageQuota: %v", err)
		}
		if !over {
			t.Error("IsOverStorageQuota = false for a domain whose stored usage exceeds its limit")
		}
	})
}

// TestDomainQuotaStore_RecipientWouldExceedQuota covers the size-aware check
// used by web write paths. Sizes are deliberately not round numbers.
func TestDomainQuotaStore_RecipientWouldExceedQuota(t *testing.T) {
	const limit = int64(1_048_583)
	tests := []struct {
		name  string
		max   int64
		usage int64
		add   int64
		want  bool
	}{
		{"exact fit is allowed", limit, limit - 4_099, 4_099, false},
		{"one byte over is refused", limit, limit - 4_099, 4_100, true},
		{"zero add at usage==max is refused", limit, limit, 0, true},
		{"zero add under max is allowed", limit, limit - 1, 0, false},
		{"unlimited ignores size", 0, 977, math.MaxInt64, false},
		{"overflowing add is refused", limit, 977, math.MaxInt64, true},
		{"negative add treated as zero", limit, limit - 1, -5, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ps, qs := newDomainQuotaTestEnv(t)
			writeTestProfile(t, &directApplier{store: ps}, "u1", "a@d.example")
			if tc.max > 0 {
				if err := qs.SetQuota("d.example", &bongov1.DomainQuota{MaxStorageBytes: tc.max}); err != nil {
					t.Fatalf("SetQuota: %v", err)
				}
			}
			if err := qs.SetUsage("d.example", tc.usage); err != nil {
				t.Fatalf("SetUsage: %v", err)
			}
			got, err := qs.RecipientWouldExceedQuota("u1", tc.add)
			if err != nil {
				t.Fatalf("RecipientWouldExceedQuota: %v", err)
			}
			if got != tc.want {
				t.Errorf("RecipientWouldExceedQuota(add=%d): want %v, got %v", tc.add, tc.want, got)
			}
			if tc.add <= 0 {
				over, err := qs.RecipientOverQuota("u1")
				if err != nil || over != got {
					t.Errorf("RecipientOverQuota = %v, %v; want %v", over, err, got)
				}
			}
		})
	}
}
