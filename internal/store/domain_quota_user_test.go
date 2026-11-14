package store

import (
	"math"
	"testing"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// writeQuotaProfile writes a profile carrying quota_bytes.
func writeQuotaProfile(t *testing.T, da *directApplier, userID, email string, quotaBytes int64) {
	t.Helper()
	b, err := proto.Marshal(&bongov1.UserProfile{Email: email, QuotaBytes: quotaBytes})
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

func seedUserUsage(t *testing.T, da *directApplier, userID string, bytes int64) {
	t.Helper()
	cmds, err := BuildUserUsageCommands(map[string]int64{userID: bytes})
	if err != nil {
		t.Fatalf("BuildUserUsageCommands: %v", err)
	}
	for _, c := range cmds {
		if err := da.ApplyCommand(c, 0); err != nil {
			t.Fatalf("apply usage: %v", err)
		}
	}
	// A literal-key store would read 0 and pass the under-quota rows vacuously.
	if got, err := GetUserUsage(da.store, userID); err != nil || got != bytes {
		t.Fatalf("GetUserUsage = %d, %v; want %d", got, err, bytes)
	}
}

func TestStorageWouldExceed(t *testing.T) {
	tests := []struct {
		name              string
		limit, usage, add int64
		want              bool
	}{
		{"no limit", 0, 1 << 40, 1 << 40, false},
		{"negative limit is no limit", -5, 10, 10, false},
		{"under", 100, 10, 10, false},
		{"exactly at, add zero", 100, 100, 0, true},
		{"exact fit", 100, 90, 10, false},
		{"one byte over", 100, 90, 11, true},
		{"negative add counts as zero", 100, 90, -50, false},
		{"usage clamped to max", 100, math.MaxInt64, 1, true},
		{"huge limit, huge usage, no overflow", math.MaxInt64, math.MaxInt64 - 1, 2, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := storageWouldExceed(tt.limit, tt.usage, tt.add); got != tt.want {
				t.Errorf("storageWouldExceed(%d,%d,%d) = %v, want %v", tt.limit, tt.usage, tt.add, got, tt.want)
			}
		})
	}
}

func TestRecipientWouldExceedQuotaUserAndDomain(t *testing.T) {
	const (
		userID = "u1"
		domain = "example.com"
	)
	tests := []struct {
		name        string
		userQuota   int64
		userUsage   int64
		domainQuota int64
		domainUsage int64
		add         int64
		noProfile   bool
		want        bool
	}{
		{name: "no quotas", userUsage: 1 << 30, add: 1 << 30},
		{name: "zero user quota with huge usage", userUsage: math.MaxInt64, add: 1},
		{name: "under user quota", userQuota: 1000, userUsage: 400, add: 100},
		{name: "exactly at, add zero", userQuota: 1000, userUsage: 1000, add: 0, want: true},
		{name: "exact fit", userQuota: 1000, userUsage: 900, add: 100},
		{name: "one byte over", userQuota: 1000, userUsage: 900, add: 101, want: true},
		{name: "domain hit while user fine", userQuota: 1000, userUsage: 100, domainQuota: 500, domainUsage: 495, add: 10, want: true},
		{name: "user hit while domain fine", userQuota: 1000, userUsage: 995, domainQuota: 1 << 20, domainUsage: 10, add: 10, want: true},
		{name: "user hit while domain unlimited", userQuota: 1000, userUsage: 995, add: 10, want: true},
		{name: "both fine", userQuota: 1000, userUsage: 100, domainQuota: 5000, domainUsage: 100, add: 10},
		{name: "missing profile", userQuota: 1000, userUsage: 1000, add: 1, noProfile: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ps, qs := newDomainQuotaTestEnv(t)
			da := &directApplier{store: ps}
			if !tt.noProfile {
				writeQuotaProfile(t, da, userID, "alice@"+domain, tt.userQuota)
				seedUserUsage(t, da, userID, tt.userUsage)
			}
			if tt.domainQuota > 0 {
				if err := qs.SetQuota(domain, &bongov1.DomainQuota{MaxStorageBytes: tt.domainQuota}); err != nil {
					t.Fatalf("SetQuota: %v", err)
				}
				if err := qs.SetUsage(domain, tt.domainUsage); err != nil {
					t.Fatalf("SetUsage: %v", err)
				}
			}
			got, err := qs.RecipientWouldExceedQuota(userID, tt.add)
			if err != nil {
				t.Fatalf("RecipientWouldExceedQuota: %v", err)
			}
			if got != tt.want {
				t.Errorf("RecipientWouldExceedQuota = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUserStorageQuota(t *testing.T) {
	ps, _ := newDomainQuotaTestEnv(t)
	da := &directApplier{store: ps}

	if l, u, err := UserStorageQuota(ps, "nobody"); err != nil || l != 0 || u != 0 {
		t.Errorf("missing profile = %d, %d, %v; want 0, 0, nil", l, u, err)
	}
	writeQuotaProfile(t, da, "neg", "n@example.com", -1)
	if l, _, err := UserStorageQuota(ps, "neg"); err != nil || l != 0 {
		t.Errorf("negative limit = %d, %v; want 0", l, err)
	}
	writeQuotaProfile(t, da, "u1", "u@example.com", 2048)
	seedUserUsage(t, da, "u1", 1500)
	if l, u, err := UserStorageQuota(ps, "u1"); err != nil || l != 2048 || u != 1500 {
		t.Errorf("UserStorageQuota = %d, %d, %v; want 2048, 1500", l, u, err)
	}
}
