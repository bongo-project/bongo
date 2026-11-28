package greylist

import (
	"fmt"
	"net"
	"os"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

var testSettings = Settings{Delay: 5 * time.Minute, RetryWindow: 4 * time.Hour, PassLifetime: 864 * time.Hour}

func newTestTable(max int) (*Table, *fakeClock) {
	c := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	return New(testSettings, max, c.now), c
}

type recPub struct {
	mu   sync.Mutex
	msgs []pubMsg
}

type pubMsg struct {
	kind byte
	key  Key
	t    int64
}

func (r *recPub) Publish(kind byte, key Key, t int64) {
	r.mu.Lock()
	r.msgs = append(r.msgs, pubMsg{kind, key, t})
	r.mu.Unlock()
}

func (r *recPub) count(kind byte) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, m := range r.msgs {
		if m.kind == kind {
			n++
		}
	}
	return n
}

func TestCheckLifecycle(t *testing.T) {
	ip := net.ParseIP("1.2.3.4")
	tests := []struct {
		name string
		step func(tb *Table, c *fakeClock) bool
		want bool
	}{
		{"new triplet deferred", func(tb *Table, c *fakeClock) bool {
			return tb.Check(ip, "a@x.example", "b@y.example")
		}, false},
		{"retry before delay deferred", func(tb *Table, c *fakeClock) bool {
			tb.Check(ip, "a@x.example", "b@y.example")
			c.advance(4 * time.Minute)
			return tb.Check(ip, "a@x.example", "b@y.example")
		}, false},
		{"early retry does not reset first seen", func(tb *Table, c *fakeClock) bool {
			tb.Check(ip, "a@x.example", "b@y.example")
			c.advance(4 * time.Minute)
			tb.Check(ip, "a@x.example", "b@y.example")
			c.advance(1 * time.Minute)
			return tb.Check(ip, "a@x.example", "b@y.example")
		}, true},
		{"retry at delay passes", func(tb *Table, c *fakeClock) bool {
			tb.Check(ip, "a@x.example", "b@y.example")
			c.advance(5 * time.Minute)
			return tb.Check(ip, "a@x.example", "b@y.example")
		}, true},
		{"retry at window end passes", func(tb *Table, c *fakeClock) bool {
			tb.Check(ip, "a@x.example", "b@y.example")
			c.advance(4 * time.Hour)
			return tb.Check(ip, "a@x.example", "b@y.example")
		}, true},
		{"retry after window resets", func(tb *Table, c *fakeClock) bool {
			tb.Check(ip, "a@x.example", "b@y.example")
			c.advance(4*time.Hour + time.Second)
			return tb.Check(ip, "a@x.example", "b@y.example")
		}, false},
		{"reset restarts the delay", func(tb *Table, c *fakeClock) bool {
			tb.Check(ip, "a@x.example", "b@y.example")
			c.advance(5 * time.Hour)
			tb.Check(ip, "a@x.example", "b@y.example")
			c.advance(time.Minute)
			return tb.Check(ip, "a@x.example", "b@y.example")
		}, false},
		{"case ignored", func(tb *Table, c *fakeClock) bool {
			tb.Check(ip, "A@X.example", "<B@Y.example>")
			c.advance(6 * time.Minute)
			return tb.Check(ip, "a@x.example", "b@y.example")
		}, true},
		{"known network passes other triplet", func(tb *Table, c *fakeClock) bool {
			tb.Check(ip, "a@x.example", "b@y.example")
			c.advance(6 * time.Minute)
			tb.Check(ip, "a@x.example", "b@y.example")
			return tb.Check(ip, "other@z.example", "c@y.example")
		}, true},
		{"same /24 shares the network", func(tb *Table, c *fakeClock) bool {
			tb.Check(ip, "a@x.example", "b@y.example")
			c.advance(6 * time.Minute)
			tb.Check(ip, "a@x.example", "b@y.example")
			return tb.Check(net.ParseIP("1.2.3.99"), "other@z.example", "c@y.example")
		}, true},
		{"other /24 is not known", func(tb *Table, c *fakeClock) bool {
			tb.Check(ip, "a@x.example", "b@y.example")
			c.advance(6 * time.Minute)
			tb.Check(ip, "a@x.example", "b@y.example")
			return tb.Check(net.ParseIP("1.2.4.1"), "other@z.example", "c@y.example")
		}, false},
		{"ipv4-mapped equals ipv4", func(tb *Table, c *fakeClock) bool {
			tb.Check(ip, "a@x.example", "b@y.example")
			c.advance(6 * time.Minute)
			return tb.Check(net.ParseIP("::ffff:1.2.3.4"), "a@x.example", "b@y.example")
		}, true},
		{"same /64 shares the network", func(tb *Table, c *fakeClock) bool {
			a := net.ParseIP("2001:db8:1:2::1")
			tb.Check(a, "a@x.example", "b@y.example")
			c.advance(6 * time.Minute)
			tb.Check(a, "a@x.example", "b@y.example")
			return tb.Check(net.ParseIP("2001:db8:1:2:ffff::9"), "o@z.example", "c@y.example")
		}, true},
		{"other /64 is not known", func(tb *Table, c *fakeClock) bool {
			a := net.ParseIP("2001:db8:1:2::1")
			tb.Check(a, "a@x.example", "b@y.example")
			c.advance(6 * time.Minute)
			tb.Check(a, "a@x.example", "b@y.example")
			return tb.Check(net.ParseIP("2001:db8:1:3::1"), "o@z.example", "c@y.example")
		}, false},
		{"known network expires", func(tb *Table, c *fakeClock) bool {
			tb.Check(ip, "a@x.example", "b@y.example")
			c.advance(6 * time.Minute)
			tb.Check(ip, "a@x.example", "b@y.example")
			c.advance(864*time.Hour + time.Second)
			return tb.Check(ip, "other@z.example", "c@y.example")
		}, false},
		{"known network is extended on use", func(tb *Table, c *fakeClock) bool {
			tb.Check(ip, "a@x.example", "b@y.example")
			c.advance(6 * time.Minute)
			tb.Check(ip, "a@x.example", "b@y.example")
			c.advance(500 * time.Hour)
			tb.Check(ip, "o@z.example", "c@y.example")
			c.advance(500 * time.Hour)
			return tb.Check(ip, "p@z.example", "d@y.example")
		}, true},
		{"nil address passes", func(tb *Table, c *fakeClock) bool {
			return tb.Check(nil, "a@x.example", "b@y.example")
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tb, c := newTestTable(0)
			if got := tt.step(tb, c); got != tt.want {
				t.Fatalf("final Check = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEvictsOldestFirst(t *testing.T) {
	tb, c := newTestTable(3)
	ip := func(n int) net.IP { return net.IPv4(10, byte(n), 0, 1) }
	for i := 1; i <= 3; i++ {
		tb.Check(ip(i), "a@x.example", "b@y.example")
	}
	if tb.Len() != 3 {
		t.Fatalf("Len = %d, want 3", tb.Len())
	}
	tb.Check(ip(4), "a@x.example", "b@y.example") // evicts ip(1)
	if tb.Len() != 3 {
		t.Fatalf("Len = %d after eviction, want 3", tb.Len())
	}
	c.advance(6 * time.Minute)
	if tb.Check(ip(1), "a@x.example", "b@y.example") {
		t.Error("evicted triplet passed; it must start again")
	}
	// ip(2) was evicted by that insert; ip(3) and ip(4) remain.
	if !tb.Check(ip(4), "a@x.example", "b@y.example") {
		t.Error("newest triplet should still pass its retry")
	}
}

func TestSweepRemovesExpired(t *testing.T) {
	tb, c := newTestTable(0)
	for i := range 50 {
		tb.Check(net.IPv4(10, byte(i), 0, 1), "a@x.example", "b@y.example")
	}
	// One network becomes known.
	c.advance(6 * time.Minute)
	tb.Check(net.IPv4(10, 0, 0, 1), "a@x.example", "b@y.example")
	if got := tb.Sweep(); got != 0 {
		t.Fatalf("early sweep removed %d", got)
	}
	c.advance(5 * time.Hour) // pending triplets are now past the window
	if got := tb.Sweep(); got != 49 {
		t.Fatalf("sweep removed %d, want 49 triplets", got)
	}
	if tb.Len() != 1 {
		t.Fatalf("Len = %d, want the known network only", tb.Len())
	}
	c.advance(865 * time.Hour)
	if got := tb.Sweep(); got != 1 || tb.Len() != 0 {
		t.Fatalf("sweep removed %d, Len %d; want 1 and 0", got, tb.Len())
	}
}

func TestPublishesOnCreateAndPass(t *testing.T) {
	tb, c := newTestTable(0)
	pub := &recPub{}
	tb.SetPublisher(pub)
	ip := net.ParseIP("1.2.3.4")
	tb.Check(ip, "a@x.example", "b@y.example")
	if pub.count(KindTriplet) != 1 || pub.count(KindNetwork) != 0 {
		t.Fatalf("after create: %+v", pub.msgs)
	}
	c.advance(6 * time.Minute)
	tb.Check(ip, "a@x.example", "b@y.example")
	if pub.count(KindNetwork) != 1 {
		t.Fatalf("after pass: %+v", pub.msgs)
	}
	// Using the known network again within a day sends nothing.
	c.advance(time.Hour)
	tb.Check(ip, "o@x.example", "b@y.example")
	if pub.count(KindNetwork) != 1 {
		t.Fatalf("refresh within a day was published: %+v", pub.msgs)
	}
	c.advance(24 * time.Hour)
	tb.Check(ip, "o@x.example", "b@y.example")
	if pub.count(KindNetwork) != 2 {
		t.Fatalf("daily refresh not published: %+v", pub.msgs)
	}
}

func TestMerge(t *testing.T) {
	ip := net.ParseIP("1.2.3.4")
	nk, _ := NetworkKey(ip)
	tk := TripletKey(nk, "a@x.example", "b@y.example")
	now := testSettingsNow()

	t.Run("remote first seen lets a local retry pass", func(t *testing.T) {
		tb, c := newTestTable(0)
		if !tb.Merge(KindTriplet, tk, now-600) {
			t.Fatal("merge refused")
		}
		_ = c
		if !tb.Check(ip, "a@x.example", "b@y.example") {
			t.Fatal("retry should pass once a peer saw the triplet 10 minutes ago")
		}
	})
	t.Run("earlier first seen wins, later does not replace", func(t *testing.T) {
		tb, _ := newTestTable(0)
		tb.Check(ip, "a@x.example", "b@y.example") // first seen now
		tb.Merge(KindTriplet, tk, now-600)
		tb.Merge(KindTriplet, tk, now-10)
		tb.mu.Lock()
		got := tb.m[mapKey{KindTriplet, tk}].t
		tb.mu.Unlock()
		if got != now-600 {
			t.Fatalf("first seen = %d, want %d", got, now-600)
		}
	})
	t.Run("known network from a peer passes other triplets", func(t *testing.T) {
		tb, _ := newTestTable(0)
		tb.Merge(KindNetwork, nk, now+3600)
		if !tb.Check(ip, "z@x.example", "q@y.example") {
			t.Fatal("known network from peer should pass")
		}
	})
	t.Run("latest known-until wins", func(t *testing.T) {
		tb, _ := newTestTable(0)
		tb.Merge(KindNetwork, nk, now+7200)
		tb.Merge(KindNetwork, nk, now+3600)
		tb.mu.Lock()
		got := tb.m[mapKey{KindNetwork, nk}].t
		tb.mu.Unlock()
		if got != now+7200 {
			t.Fatalf("until = %d, want %d", got, now+7200)
		}
	})
	rejects := []struct {
		name string
		kind byte
		ts   int64
	}{
		{"first seen in the future", KindTriplet, now + 3600},
		{"first seen older than the window", KindTriplet, now - 5*3600},
		{"known until already past", KindNetwork, now - 1},
		{"known until beyond the lifetime", KindNetwork, now + 865*3600},
		{"unknown kind", 9, now},
	}
	for _, r := range rejects {
		t.Run("rejects "+r.name, func(t *testing.T) {
			tb, _ := newTestTable(0)
			if tb.Merge(r.kind, tk, r.ts) {
				t.Fatal("accepted")
			}
			if tb.Len() != 0 {
				t.Fatalf("Len = %d, want nothing stored", tb.Len())
			}
		})
	}
}

func testSettingsNow() int64 { return 1_800_000_000 }

func TestMergeDoesNotPublish(t *testing.T) {
	tb, _ := newTestTable(0)
	pub := &recPub{}
	tb.SetPublisher(pub)
	tb.Merge(KindNetwork, Key{1}, testSettingsNow()+100)
	if len(pub.msgs) != 0 {
		t.Fatalf("merge republished: %+v", pub.msgs)
	}
}

func TestMergeRespectsCap(t *testing.T) {
	tb, _ := newTestTable(5)
	for i := range 50 {
		tb.Merge(KindNetwork, Key{byte(i)}, testSettingsNow()+100)
	}
	if got := tb.Len(); got < 1 || got > 5 {
		t.Fatalf("Len = %d, want within the cap of 5", got)
	}
}

func TestBucket(t *testing.T) {
	c := &fakeClock{t: time.Unix(1000, 0)}
	b := NewBucket(10, 5, c.now)
	for i := range 5 {
		if !b.Allow() {
			t.Fatalf("burst token %d refused", i)
		}
	}
	if b.Allow() {
		t.Fatal("bucket should be empty")
	}
	c.advance(300 * time.Millisecond) // 3 tokens
	for i := range 3 {
		if !b.Allow() {
			t.Fatalf("refilled token %d refused", i)
		}
	}
	if b.Allow() {
		t.Fatal("bucket should be empty again")
	}
	c.advance(time.Hour)
	n := 0
	for b.Allow() {
		n++
	}
	if n != 5 {
		t.Fatalf("refill capped at burst: got %d, want 5", n)
	}
}

func TestConcurrentUse(t *testing.T) {
	tb, c := newTestTable(1000)
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 500 {
				ip := net.IPv4(10, byte(g), byte(i), 1)
				tb.Check(ip, "a@x.example", "b@y.example")
				tb.Merge(KindNetwork, Key{byte(g), byte(i)}, testSettingsNow()+100)
				if i%100 == 0 {
					tb.Sweep()
				}
			}
		}()
	}
	wg.Wait()
	c.advance(time.Minute)
	if tb.Len() > 1000 {
		t.Fatalf("Len = %d over the cap", tb.Len())
	}
}

func TestDailyRefreshPublishedWhileBusy(t *testing.T) {
	tb, c := newTestTable(0)
	pub := &recPub{}
	tb.SetPublisher(pub)
	ip := net.ParseIP("1.2.3.4")
	tb.Check(ip, "a@x.example", "b@y.example")
	c.advance(6 * time.Minute)
	tb.Check(ip, "a@x.example", "b@y.example") // pass: network published
	// Busy network: used every 12h for three days.
	for range 6 {
		c.advance(12 * time.Hour)
		tb.Check(ip, "o@x.example", "b@y.example")
	}
	pub.mu.Lock()
	defer pub.mu.Unlock()
	var last int64
	n := 0
	for _, m := range pub.msgs {
		if m.kind != KindNetwork {
			continue
		}
		n++
		if last != 0 && m.t-last < int64(refreshEvery/time.Second) {
			t.Errorf("network published twice within 24h: %d then %d", last, m.t)
		}
		last = m.t
	}
	// pass + one per 24h over 72h.
	if n != 4 {
		t.Fatalf("network published %d times, want 4: %+v", n, pub.msgs)
	}
}

func passNetwork(tb *Table, c *fakeClock, ip net.IP) {
	tb.Check(ip, "a@x.example", "b@y.example")
	c.advance(6 * time.Minute)
	tb.Check(ip, "a@x.example", "b@y.example")
}

// An unauthenticated client cycling random envelope senders must not push
// known networks out of the table.
func TestFloodDoesNotEvictKnownNetworks(t *testing.T) {
	tb, c := newTestTable(1000)
	for i := range 50 {
		passNetwork(tb, c, net.IPv4(10, 0, byte(i), 1))
	}
	// One hostile network.
	hostile := net.ParseIP("203.0.113.9")
	for i := range 5000 {
		tb.Check(hostile, fmt.Sprintf("r%d@evil.example", i), "b@y.example")
	}
	// A botnet over many networks.
	for i := range 5000 {
		tb.Check(net.IPv4(198, byte(i>>8), byte(i), 7), "r@evil.example", "b@y.example")
	}
	for i := range 50 {
		if !tb.Check(net.IPv4(10, 0, byte(i), 1), "new@x.example", "other@y.example") {
			t.Fatalf("known network %d was evicted by a flood", i)
		}
	}
	if tb.Len() > 1000 {
		t.Fatalf("Len = %d over the cap", tb.Len())
	}
}

// One network cycling senders cannot push out pending triplets of others.
func TestFloodFromOneNetworkKeepsPendingTriplets(t *testing.T) {
	tb, c := newTestTable(1000)
	legit := net.ParseIP("192.0.2.10")
	tb.Check(legit, "a@x.example", "b@y.example")
	hostile := net.ParseIP("203.0.113.9")
	for i := range 20000 {
		tb.Check(hostile, fmt.Sprintf("r%d@evil.example", i), "b@y.example")
	}
	c.advance(6 * time.Minute)
	if !tb.Check(legit, "a@x.example", "b@y.example") {
		t.Fatal("legitimate pending triplet was pushed out by one network's flood")
	}
	if tb.Len() > 1000 {
		t.Fatalf("Len = %d over the cap", tb.Len())
	}
}

func TestEvictionStaysBoundedUnderChurn(t *testing.T) {
	tb, c := newTestTable(100)
	for i := range 3000 {
		ip := net.IPv4(10, byte(i>>8), byte(i), 1)
		tb.Check(ip, "a@x.example", "b@y.example")
		c.advance(6 * time.Minute)
		tb.Check(ip, "a@x.example", "b@y.example")
		c.advance(time.Second)
		if i%500 == 0 {
			tb.Sweep()
		}
		if tb.Len() > 100 {
			t.Fatalf("Len = %d over the cap", tb.Len())
		}
	}
	tb.mu.Lock()
	defer tb.mu.Unlock()
	if tb.known.n+tb.pending.n != len(tb.m) {
		t.Fatalf("lists hold %d+%d, map %d", tb.known.n, tb.pending.n, len(tb.m))
	}
}

// Worst single-call latency at the cap; informational, run with GREYLIST_STALL=1.
func TestEvictionLatencyAtCap(t *testing.T) {
	if os.Getenv("GREYLIST_STALL") == "" {
		t.Skip("set GREYLIST_STALL=1")
	}
	const capN = 1_000_000
	tb, _ := newTestTable(capN)
	var worst time.Duration
	for i := range 2_500_000 {
		ip := net.IPv4(byte(i>>16), byte(i>>8), byte(i), 1)
		s := fmt.Sprintf("s%d@x.example", i)
		start := time.Now()
		tb.Check(ip, s, "b@y.example")
		if d := time.Since(start); d > worst && i > capN {
			worst = d
		}
	}
	t.Logf("worst Check latency past the cap: %v", worst)
}

func TestSweepInChunks(t *testing.T) {
	tb, c := newTestTable(0)
	n := sweepChunk*2 + 500
	for i := range n {
		tb.Check(net.IPv4(10, byte(i>>8), byte(i), 1), "a@x.example", "b@y.example")
	}
	c.advance(5 * time.Hour)
	if got := tb.Sweep(); got != n {
		t.Fatalf("sweep removed %d, want %d", got, n)
	}
	if tb.Len() != 0 {
		t.Fatalf("Len = %d", tb.Len())
	}
}

// One IPv6 operator rotating /64s inside its /48 shares one budget and cannot
// push out the pending triplets of others.
func TestIPv6RotationWithinSlash48KeepsLegit(t *testing.T) {
	tb, c := newTestTable(2000)
	legit := net.ParseIP("192.0.2.10")
	tb.Check(legit, "a@x.example", "b@y.example")
	for sub := range 60 {
		ip := net.ParseIP(fmt.Sprintf("2001:db8:1:%x::1", sub))
		for i := range 200 {
			tb.Check(ip, fmt.Sprintf("s%d-%d@spam.test", sub, i), "b@y.example")
		}
	}
	c.advance(6 * time.Minute)
	if !tb.Check(legit, "a@x.example", "b@y.example") {
		t.Fatalf("legitimate retry failed after /64 rotation (len %d)", tb.Len())
	}
}

// Networks that used to fold to one id must have separate budgets.
func TestNetworkBudgetsDoNotCollide(t *testing.T) {
	for _, pair := range [][2]string{{"2.3.4.5", "3.3.4.5"}, {"42.1.4.9", "2a01:400:0:2::1"}} {
		tb, c := newTestTable(2000) // 40 pending per network
		a, b := net.ParseIP(pair[0]), net.ParseIP(pair[1])
		for i := range 100 {
			tb.Check(a, fmt.Sprintf("s%d@x.example", i), "b@y.example")
		}
		tb.Check(b, "a@x.example", "b@y.example")
		c.advance(6 * time.Minute)
		if !tb.Check(b, "a@x.example", "b@y.example") {
			t.Errorf("%s shares a budget with %s", pair[1], pair[0])
		}
	}
}

// Passing from many /64s of one /48 cannot flush the known networks.
func TestKnownNetworksCappedPerSlash48(t *testing.T) {
	tb, c := newTestTable(2000)
	passNetwork(tb, c, net.ParseIP("192.0.2.10"))
	for sub := range 200 {
		passNetwork(tb, c, net.ParseIP(fmt.Sprintf("2001:db8:1:%x::1", sub)))
	}
	tb.mu.Lock()
	known := tb.known.n
	tb.mu.Unlock()
	if known > 20 {
		t.Fatalf("one /48 holds %d known networks", known)
	}
	if !tb.Check(net.ParseIP("192.0.2.10"), "new@x.example", "o@y.example") {
		t.Fatal("legitimate known network lost")
	}
}

// Expired triplets stop counting against the budget before the sweep runs.
func TestExpiredTripletsFreeTheBudget(t *testing.T) {
	tb, c := newTestTable(2000)
	ip := net.ParseIP("203.0.113.9")
	for i := range 100 {
		tb.Check(ip, fmt.Sprintf("s%d@x.example", i), "b@y.example")
	}
	c.advance(5 * time.Hour) // past the retry window, no sweep
	tb.Check(ip, "fresh@x.example", "b@y.example")
	c.advance(6 * time.Minute)
	if !tb.Check(ip, "fresh@x.example", "b@y.example") {
		t.Fatal("expired triplets still hold the network budget")
	}
}

// With the pool full, a flood displaces its own entries before older pending
// triplets of small networks.
func TestFullPoolEvictsHeavyNetworksFirst(t *testing.T) {
	tb, c := newTestTable(400) // 8 -> perNet 16; knownCap 200
	var legit []net.IP
	for i := range 5 {
		ip := net.IPv4(192, 0, byte(i), 10)
		legit = append(legit, ip)
		tb.Check(ip, "a@x.example", "b@y.example")
	}
	for n := range 40 { // 40 networks x 16 = 640 > pool
		ip := net.IPv4(198, 51, byte(n), 7)
		for i := range 16 {
			tb.Check(ip, fmt.Sprintf("r%d@evil.example", i), "b@y.example")
		}
	}
	c.advance(6 * time.Minute)
	for i, ip := range legit {
		if !tb.Check(ip, "a@x.example", "b@y.example") {
			t.Errorf("legit %d lost to a flood of heavy networks", i)
		}
	}
}

// A budget group cannot hold more pending triplets than its share, however
// many it sends, and a triplet past the budget is deferred but not recorded.
// The table is large enough that no eviction happens, so only the budget can
// hold the count down. IPv4 groups by /24; one IPv6 /64 gets a quarter of its
// /48's budget, and the /48 as a whole the full budget.
func TestPendingBudgetBoundsGroupCount(t *testing.T) {
	tests := []struct {
		name    string
		ips     []string // senders take turns over these addresses
		perNet  bool     // also bound each IPv6 /64 at a quarter
		wantGrp func(perNet int) int
	}{
		{
			name:    "one IPv4 /24",
			ips:     []string{"203.0.113.9", "203.0.113.200"},
			wantGrp: func(p int) int { return p },
		},
		{
			name:    "one IPv6 /64",
			ips:     []string{"2001:db8:1:1::9", "2001:db8:1:1::ffff"},
			perNet:  true,
			wantGrp: func(p int) int { return max(p/4, 16) },
		},
		{
			name:    "several /64s in one IPv6 /48",
			ips:     []string{"2001:db8:1:1::9", "2001:db8:1:2::9", "2001:db8:1:3::9", "2001:db8:1:4::9", "2001:db8:1:5::9"},
			perNet:  true,
			wantGrp: func(p int) int { return p },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tb, _ := newTestTable(2000)
			const sends = 400
			var refused []Key // triplet keys sent after the group was full
			for i := range sends {
				ip := net.ParseIP(tt.ips[i%len(tt.ips)])
				nk, _ := NetworkKey(ip)
				sender := fmt.Sprintf("r%d@flood.example", i)
				tb.mu.Lock()
				full := tb.pendingFull(nk)
				tb.mu.Unlock()
				if tb.Check(ip, sender, "b@y.example") {
					t.Fatalf("send %d passed on first sight", i)
				}
				if full {
					refused = append(refused, TripletKey(nk, sender, "b@y.example"))
				}
			}

			tb.mu.Lock()
			defer tb.mu.Unlock()
			if len(tb.m) >= tb.max {
				t.Fatalf("table filled up (%d entries); eviction would mask the budget", len(tb.m))
			}
			nk0, _ := NetworkKey(net.ParseIP(tt.ips[0]))
			grp := groupOf(nk0)
			want := tt.wantGrp(tb.perNet)
			if got := tb.grpPending[grp]; got != want {
				t.Errorf("group holds %d pending triplets, want exactly the budget %d", got, want)
			}
			if tt.perNet {
				for _, s := range tt.ips {
					nk, _ := NetworkKey(net.ParseIP(s))
					if got, quarter := tb.netPending[nk], max(tb.perNet/4, 16); got > quarter {
						t.Errorf("%s /64 holds %d pending triplets, over its quarter %d", s, got, quarter)
					}
				}
			}
			if len(refused) == 0 {
				t.Fatal("the budget was never reached; the test proves nothing")
			}
			for _, k := range refused {
				if _, ok := tb.m[mapKey{KindTriplet, k}]; ok {
					t.Fatal("a triplet sent past the budget was recorded")
				}
			}
			if got := tb.pending.n; got != want {
				t.Errorf("pending list holds %d, want %d", got, want)
			}
		})
	}
}
