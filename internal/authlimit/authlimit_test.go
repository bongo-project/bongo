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

func TestSucceedOtherAccountDoesNotClearIP(t *testing.T) {
	l := New(5, time.Minute)
	const ip = "10.0.0.2"
	for range 4 {
		l.Fail(ip, "victim@example.com")
	}
	l.Succeed(ip, "attacker@example.com")
	if !l.Fail(ip, "victim@example.com") {
		t.Fatal("attacker success must not reset the IP counter")
	}
	if !l.Blocked(ip) {
		t.Fatal("IP should be blocked")
	}
}

func TestSucceedDoesNotClearSprayAcrossAccounts(t *testing.T) {
	l := New(5, time.Minute)
	const ip = "10.0.0.3"
	for _, a := range []string{"v1", "v2", "v3", "v1", "v2"} {
		l.Fail(ip, a)
	}
	l.Succeed(ip, "attacker")
	if !l.Blocked(ip) {
		t.Fatal("spray across accounts must stay blocked after an unrelated success")
	}
}

func TestSucceedForgivesOwnFailures(t *testing.T) {
	l := New(5, time.Minute)
	const ip = "10.0.0.4"
	l.Fail(ip, "alice")
	l.Fail(ip, "alice")
	l.Succeed(ip, "alice")
	for i := range 4 {
		if l.Fail(ip, "alice") {
			t.Fatalf("blocked after %d failures post-success, own failures should be forgiven", i+1)
		}
	}
	if l.Blocked(ip) {
		t.Fatal("should not be blocked")
	}
}

func TestSucceedKeepsOthersFailures(t *testing.T) {
	l := New(5, time.Minute)
	const ip = "10.0.0.5"
	l.Fail(ip, "victim")
	l.Fail(ip, "victim")
	l.Fail(ip, "alice")
	l.Fail(ip, "alice")
	l.Succeed(ip, "alice") // leaves 2 victim failures
	l.Fail(ip, "victim")
	l.Fail(ip, "victim")
	if l.Blocked(ip) {
		t.Fatal("4 unforgiven failures must not block at 5")
	}
	if !l.Fail(ip, "victim") {
		t.Fatal("5th unforgiven failure must block")
	}
}

func TestSucceedNormalisesAccount(t *testing.T) {
	l := New(3, time.Minute)
	const ip = "10.0.0.6"
	l.Fail(ip, " Alice@X ")
	l.Fail(ip, "Alice@X")
	l.Succeed(ip, "alice@x")
	l.Fail(ip, "alice@x")
	if l.Fail(ip, "alice@x") {
		t.Fatal("case/whitespace variants should be forgiven together")
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
	l.Fail("x", "a")
	l.Succeed("x", "a") // must not panic

	real := New(1, time.Minute)
	if real.Record("") || real.Blocked("") || real.Fail("", "a") {
		t.Fatal("empty key must be ignored")
	}
}

// A pair from an earlier window must not be subtracted from a fresh IP window.
func TestSucceedIgnoresStalePair(t *testing.T) {
	l := New(5, time.Minute)
	now := time.Unix(1_000_000, 0)
	l.nowFn = func() time.Time { return now }

	l.Fail("203.0.113.9", "attacker")
	now = now.Add(2 * time.Minute)

	for range 4 {
		l.Fail("203.0.113.9", "victim")
	}
	l.Succeed("203.0.113.9", "attacker")
	if !l.Fail("203.0.113.9", "victim") {
		t.Fatal("stale attacker pair must not buy the victim extra guesses")
	}
}

// The pair key is scoped to the IP: a success from one address must neither
// consume nor forgive the same account's failures recorded at another.
func TestSucceedIsScopedToIP(t *testing.T) {
	const ipA, ipB = "10.0.1.1", "10.0.1.2"
	l := New(3, time.Minute)
	l.Fail(ipA, "alice")
	l.Fail(ipA, "alice")
	l.Succeed(ipB, "alice") // must not touch ipA's pair
	l.Succeed(ipA, "alice") // still has its own two failures to forgive
	l.Fail(ipA, "alice")
	if l.Fail(ipA, "alice") {
		t.Fatal("ipA's own success should forgive ipA's failures after another IP's success")
	}

	// The inverse: A's failures and success leave B's count intact.
	l = New(3, time.Minute)
	l.Fail(ipB, "alice")
	l.Fail(ipB, "alice")
	l.Fail(ipA, "alice")
	l.Succeed(ipA, "alice")
	if !l.Fail(ipB, "alice") {
		t.Fatal("success at one IP must not forgive another IP's failures")
	}
}

// The account is part of the pair key: a success under one name must not
// forgive failures recorded under a different name from the same IP.
func TestSucceedIsScopedToAccount(t *testing.T) {
	l := New(3, time.Minute)
	const ip = "10.0.1.3"
	l.Fail(ip, "alice")
	l.Fail(ip, "alice")
	l.Succeed(ip, "bob") // bob has no failures: nothing to forgive
	if !l.Fail(ip, "carol") {
		t.Fatal("success under another account must not forgive alice's failures")
	}
}
