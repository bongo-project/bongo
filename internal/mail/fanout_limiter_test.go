package mail

import (
	"math"
	"testing"
	"time"
)

func TestFanoutLimiter(t *testing.T) {
	start := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	now := start
	l := NewFanoutLimiter(10)
	l.SetClock(func() time.Time { return now })

	if got := l.Remaining("u:a"); got != 10 {
		t.Fatalf("fresh Remaining = %d, want 10", got)
	}
	if !l.AllowN("u:a", 6) {
		t.Fatal("AllowN(6) of 10 refused")
	}
	if l.AllowN("u:a", 5) {
		t.Fatal("AllowN(5) with 4 left allowed")
	}
	if got := l.Remaining("u:a"); got != 4 {
		t.Fatalf("Remaining after a refusal = %d, want 4 (a refusal records nothing)", got)
	}
	if got := l.Remaining("ip:1.2.3.4"); got != 10 {
		t.Errorf("other key Remaining = %d, want 10", got)
	}
	now = start.Add(61 * time.Minute)
	if got := l.Remaining("u:a"); got != 10 {
		t.Errorf("Remaining after the window = %d, want 10", got)
	}
	if !l.AllowN("u:a", 10) {
		t.Error("AllowN after the window refused")
	}
}

func TestFanoutLimiterDisabled(t *testing.T) {
	l := NewFanoutLimiter(0)
	if !l.AllowN("k", 100000) {
		t.Error("disabled limiter refused")
	}
	if l.Remaining("k") != math.MaxInt {
		t.Error("disabled limiter reports a finite Remaining")
	}
	if l.Keys() != 0 {
		t.Error("disabled limiter recorded a key")
	}
}

// Keys that never return are dropped by the periodic sweep, so a stream of
// one-off source IPs does not grow the map without bound.
func TestFanoutLimiterSweepsIdleKeys(t *testing.T) {
	start := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	now := start
	l := NewFanoutLimiter(5000)
	l.SetClock(func() time.Time { return now })
	for i := range 200 {
		if !l.AllowN("ip:10.0.0."+string(rune('a'+i%26))+string(rune('a'+i/26)), 1000) {
			t.Fatal("charge refused")
		}
	}
	if l.Keys() != 200 {
		t.Fatalf("Keys = %d, want 200", l.Keys())
	}
	now = start.Add(2 * time.Hour)
	l.AllowN("ip:fresh", 1)
	if got := l.Keys(); got != 1 {
		t.Errorf("Keys after sweep = %d, want 1", got)
	}
}

// A charge is one entry whatever its size.
func TestFanoutLimiterOneEntryPerCharge(t *testing.T) {
	l := NewFanoutLimiter(5000)
	l.AllowN("k", 1000)
	l.mu.Lock()
	n := len(l.keys["k"])
	l.mu.Unlock()
	if n != 1 {
		t.Errorf("entries = %d, want 1", n)
	}
}
