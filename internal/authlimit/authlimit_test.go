package authlimit

import (
	"testing"
	"time"
)

func TestBlocksAfterMaxFailures(t *testing.T) {
	l := New(3, time.Minute)
	key := "10.0.0.1"

	for i := 0; i < 2; i++ {
		if blocked := l.Record(key); blocked {
			t.Fatalf("blocked after %d failures, want not blocked until 3", i+1)
		}
	}
	if !l.Record(key) {
		t.Fatal("third failure should report blocked")
	}
	if !l.Blocked(key) {
		t.Fatal("key should remain blocked")
	}
}

func TestResetClearsCount(t *testing.T) {
	l := New(3, time.Minute)
	key := "10.0.0.2"
	l.Record(key)
	l.Record(key)
	l.Reset(key)
	if l.Blocked(key) {
		t.Fatal("Reset should clear the counter")
	}
	if l.Record(key) {
		t.Fatal("after Reset, a single failure should not be blocked")
	}
}

func TestWindowExpiry(t *testing.T) {
	l := New(3, time.Minute)
	now := time.Unix(1_000_000, 0)
	l.nowFn = func() time.Time { return now }

	key := "10.0.0.3"
	l.Record(key)
	l.Record(key)
	if !l.Record(key) {
		t.Fatal("should be blocked at 3 failures")
	}

	// Advance past the window: the stale entry is forgotten.
	now = now.Add(2 * time.Minute)
	if l.Blocked(key) {
		t.Fatal("block should expire after the window")
	}
	if l.Record(key) {
		t.Fatal("a fresh window should start after expiry")
	}
}

func TestKeysAreIndependent(t *testing.T) {
	l := New(2, time.Minute)
	l.Record("a")
	if l.Record("a"); !l.Blocked("a") {
		t.Fatal("key a should be blocked")
	}
	if l.Blocked("b") {
		t.Fatal("key b must be unaffected by key a")
	}
}

func TestEmptyKeyAndNilAreNoops(t *testing.T) {
	var l *Limiter // nil
	if l.Blocked("x") || l.Record("x") {
		t.Fatal("nil limiter must report not blocked")
	}
	l.Reset("x") // must not panic

	real := New(1, time.Minute)
	if real.Record("") || real.Blocked("") {
		t.Fatal("empty key must be ignored")
	}
}
