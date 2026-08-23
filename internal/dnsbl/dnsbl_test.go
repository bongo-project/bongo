package dnsbl

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// zoneAnswer is a canned response for one exact DNS query name.
type zoneAnswer struct {
	ips []net.IP
	err error
}

// fakeResolver is a map-driven Resolver double, following the mockResolver
// shape in internal/mail/spf_test.go. It never performs network I/O, so no
// test in this package can reach a live DNS query. calls is incremented
// atomically because Check fans out one goroutine per configured zone, so
// this fake is itself called from multiple goroutines concurrently -- a
// plain int++ here would trip -race.
type fakeResolver struct {
	mu      sync.Mutex
	answers map[string]zoneAnswer
	hang    map[string]bool // query names that block until ctx.Done()

	calls atomic.Int64
}

func newFakeResolver() *fakeResolver {
	return &fakeResolver{answers: make(map[string]zoneAnswer), hang: make(map[string]bool)}
}

func (f *fakeResolver) set(name string, ips []net.IP, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers[name] = zoneAnswer{ips: ips, err: err}
}

func (f *fakeResolver) setHang(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hang[name] = true
}

func (f *fakeResolver) LookupIP(ctx context.Context, host string) ([]net.IP, error) {
	f.calls.Add(1)

	f.mu.Lock()
	hang := f.hang[host]
	ans, ok := f.answers[host]
	f.mu.Unlock()

	if hang {
		// Select on ctx.Done() rather than time.Sleep(arbitraryDuration):
		// this fake must respect the shared deadline exactly, or the
		// shared-timeout concurrency test becomes timing-flaky under load.
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	return ans.ips, ans.err
}

func nameFor(t *testing.T, ip net.IP, zone string) string {
	t.Helper()
	nz, ok := normalizeZone(zone)
	if !ok {
		t.Fatalf("bad zone %q", zone)
	}
	name, ok := queryName(ip, nz)
	if !ok {
		t.Fatalf("queryName(%v, %q) failed", ip, nz)
	}
	return name
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestCheck_Listed(t *testing.T) {
	ip := net.ParseIP("203.0.113.5")
	zone := "listed.example"
	r := newFakeResolver()
	r.set(nameFor(t, ip, zone), []net.IP{net.ParseIP("127.0.0.2")}, nil)

	c := NewCheckerWithResolver(r, []string{zone}, time.Second, time.Minute, discardLogger())
	res := c.Check(context.Background(), ip)

	if res.IP != "203.0.113.5" {
		t.Errorf("IP = %q, want 203.0.113.5", res.IP)
	}
	listed := res.ListedZones()
	if len(listed) != 1 {
		t.Fatalf("ListedZones() = %d entries, want 1: %+v", len(listed), res.Zones)
	}
	if listed[0].Zone != zone || listed[0].Code != "127.0.0.2" {
		t.Errorf("listed zone = %+v, want zone=%q code=127.0.0.2", listed[0], zone)
	}
}

func TestCheck_NotListedNXDOMAIN(t *testing.T) {
	ip := net.ParseIP("203.0.113.5")
	zone := "clean.example"
	r := newFakeResolver() // no answer configured -> fakeResolver returns NXDOMAIN

	c := NewCheckerWithResolver(r, []string{zone}, time.Second, time.Minute, discardLogger())
	res := c.Check(context.Background(), ip)

	if len(res.ListedZones()) != 0 {
		t.Errorf("ListedZones() = %+v, want none", res.ListedZones())
	}
	if len(res.Zones) != 1 || res.Zones[0].Listed {
		t.Errorf("Zones = %+v, want one not-listed entry", res.Zones)
	}
}

func TestCheck_ResponseInterpretation(t *testing.T) {
	ip := net.ParseIP("203.0.113.5")

	tests := []struct {
		name       string
		answer     string
		wantListed bool
	}{
		{"127.0.0.2 is a listing", "127.0.0.2", true},
		{"127.0.0.10 is a listing", "127.0.0.10", true},
		{"127.255.255.254 is a policy-error code, not a listing", "127.255.255.254", false},
		{"127.255.255.252 is a policy-error code, not a listing", "127.255.255.252", false},
		{"93.184.216.34 is outside 127/8, not a listing", "93.184.216.34", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			zone := "zone.example"
			r := newFakeResolver()
			r.set(nameFor(t, ip, zone), []net.IP{net.ParseIP(tt.answer)}, nil)

			c := NewCheckerWithResolver(r, []string{zone}, time.Second, time.Minute, discardLogger())
			res := c.Check(context.Background(), ip)

			if len(res.Zones) != 1 {
				t.Fatalf("got %d zones, want 1", len(res.Zones))
			}
			if res.Zones[0].Listed != tt.wantListed {
				t.Errorf("Listed = %v, want %v (answer=%s)", res.Zones[0].Listed, tt.wantListed, tt.answer)
			}
		})
	}
}

func TestCheck_ErrorShapesAllTreatedAsNotListed(t *testing.T) {
	ip := net.ParseIP("203.0.113.5")

	tests := []struct {
		name string
		err  error
	}{
		{"nxdomain", &net.DNSError{Err: "no such host", Name: "x", IsNotFound: true}},
		{"servfail", &net.DNSError{Err: "server misbehaving", Name: "x", IsTemporary: true}},
		{"malformed answer", errors.New("malformed response from nameserver")},
		{"deadline exceeded", context.DeadlineExceeded},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			zone := "zone.example"
			r := newFakeResolver()
			r.set(nameFor(t, ip, zone), nil, tt.err)

			c := NewCheckerWithResolver(r, []string{zone}, time.Second, time.Minute, discardLogger())
			res := c.Check(context.Background(), ip)

			if len(res.Zones) != 1 {
				t.Fatalf("got %d zones, want 1", len(res.Zones))
			}
			if res.Zones[0].Listed {
				t.Errorf("Listed = true, want false for error %v", tt.err)
			}
			if res.Zones[0].Err == nil {
				t.Errorf("Err = nil, want the underlying error recorded (informationally)")
			}
		})
	}
}

// TestCheck_ConcurrentZonesUnderSharedTimeout checks that five zones, THREE
// of which never respond, still cost one timeout's worth of latency rather
// than three, and that the two healthy zones' verdicts survive. Three hanging
// zones is the point of the shape -- with only one, a per-zone-timeout
// implementation (the exact thing the shared timeout rules out) also finishes
// in one timeout and the test passes vacuously.
func TestCheck_ConcurrentZonesUnderSharedTimeout(t *testing.T) {
	ip := net.ParseIP("203.0.113.5")
	const (
		listedZone = "listed.example"
		cleanZone  = "clean.example"
		timeout    = 100 * time.Millisecond
	)
	hangZones := []string{"hang1.example", "hang2.example", "hang3.example"}

	r := newFakeResolver()
	r.set(nameFor(t, ip, listedZone), []net.IP{net.ParseIP("127.0.0.4")}, nil)
	// cleanZone left unset -> NXDOMAIN.
	for _, z := range hangZones {
		r.setHang(nameFor(t, ip, z))
	}

	zones := append([]string{listedZone, cleanZone}, hangZones...)
	c := NewCheckerWithResolver(r, zones, timeout, time.Minute, discardLogger())

	start := time.Now()
	res := c.Check(context.Background(), ip)
	elapsed := time.Since(start)

	// One shared deadline: 3 hanging zones cost one timeout, not three.
	// The 2x allowance is scheduler slack on a loaded CI box, not room for
	// a second serialised timeout.
	if elapsed > 2*timeout {
		t.Errorf("Check took %v, want under %v (one shared timeout, not per-zone)", elapsed, 2*timeout)
	}

	if len(res.Zones) != len(zones) {
		t.Fatalf("got %d zones, want %d: %+v", len(res.Zones), len(zones), res.Zones)
	}

	byZone := make(map[string]ZoneResult, len(zones))
	for _, z := range res.Zones {
		byZone[z.Zone] = z
	}

	if !byZone[listedZone].Listed {
		t.Errorf("listedZone: Listed = false, want true")
	}
	if byZone[cleanZone].Listed {
		t.Errorf("cleanZone: Listed = true, want false")
	}
	for _, z := range hangZones {
		if byZone[z].Listed {
			t.Errorf("%s: Listed = true, want false", z)
		}
		if byZone[z].Err == nil {
			t.Errorf("%s: Err = nil, want context deadline recorded", z)
		}
	}
}

// TestCheck_CancelledParentIsNotCached guards the cache-poisoning path: a
// caller whose context is already dead produces an all-timeout Result that
// says nothing about the zones. Caching it would silence DNSBL for that IP
// for a full TTL on the strength of one cancelled request.
func TestCheck_CancelledParentIsNotCached(t *testing.T) {
	ip := net.ParseIP("203.0.113.5")
	const zone = "listed.example"

	r := newFakeResolver()
	r.set(nameFor(t, ip, zone), []net.IP{net.ParseIP("127.0.0.2")}, nil)

	c := NewCheckerWithResolver(r, []string{zone}, time.Second, time.Hour, discardLogger())

	dead, cancel := context.WithCancel(context.Background())
	cancel()
	if res := c.Check(dead, ip); len(res.Zones) != 1 || res.Zones[0].Listed {
		t.Fatalf("cancelled parent: got %+v, want a single not-listed zone", res.Zones)
	}

	// The next healthy call must reach the resolver and see the listing.
	res := c.Check(context.Background(), ip)
	if len(res.Zones) != 1 || !res.Zones[0].Listed {
		t.Errorf("after cancelled parent: got %+v, want the zone listed", res.Zones)
	}
	if r.calls.Load() == 0 {
		t.Errorf("resolver call count = 0: the poisoned Result was served from cache")
	}
}

func TestCheck_CacheHitPerformsNoResolverCall(t *testing.T) {
	ip := net.ParseIP("203.0.113.5")
	zone := "listed.example"
	r := newFakeResolver()
	r.set(nameFor(t, ip, zone), []net.IP{net.ParseIP("127.0.0.2")}, nil)

	c := NewCheckerWithResolver(r, []string{zone}, time.Second, time.Minute, discardLogger())

	res1 := c.Check(context.Background(), ip)
	callsAfterFirst := r.calls.Load()
	if callsAfterFirst != 1 {
		t.Fatalf("calls after first Check = %d, want 1", callsAfterFirst)
	}

	res2 := c.Check(context.Background(), ip)
	callsAfterSecond := r.calls.Load()
	if callsAfterSecond != callsAfterFirst {
		t.Errorf("calls after second Check = %d, want unchanged at %d (cache hit expected)", callsAfterSecond, callsAfterFirst)
	}
	if len(res2.ListedZones()) != len(res1.ListedZones()) {
		t.Errorf("cached result differs from original: %+v vs %+v", res2, res1)
	}
}

func TestCheck_NegativeResultCached(t *testing.T) {
	ip := net.ParseIP("203.0.113.5")
	zone := "clean.example"
	r := newFakeResolver() // NXDOMAIN

	c := NewCheckerWithResolver(r, []string{zone}, time.Second, time.Minute, discardLogger())

	c.Check(context.Background(), ip)
	if got := r.calls.Load(); got != 1 {
		t.Fatalf("calls after first Check = %d, want 1", got)
	}

	c.Check(context.Background(), ip)
	if got := r.calls.Load(); got != 1 {
		t.Errorf("calls after second Check = %d, want still 1 (negative result must be cached too)", got)
	}
}

func TestCheck_CacheExpiresAfterTTL(t *testing.T) {
	ip := net.ParseIP("203.0.113.5")
	zone := "clean.example"
	r := newFakeResolver()

	c := NewCheckerWithResolver(r, []string{zone}, time.Second, 10*time.Millisecond, discardLogger())

	fakeNow := time.Unix(0, 0)
	c.cache.now = func() time.Time { return fakeNow }

	c.Check(context.Background(), ip)
	if got := r.calls.Load(); got != 1 {
		t.Fatalf("calls after first Check = %d, want 1", got)
	}

	// Still within TTL: no new call.
	fakeNow = fakeNow.Add(5 * time.Millisecond)
	c.Check(context.Background(), ip)
	if got := r.calls.Load(); got != 1 {
		t.Fatalf("calls within TTL = %d, want 1", got)
	}

	// Past TTL: must re-query.
	fakeNow = fakeNow.Add(20 * time.Millisecond)
	c.Check(context.Background(), ip)
	if got := r.calls.Load(); got != 2 {
		t.Errorf("calls after TTL expiry = %d, want 2", got)
	}
}

func TestZoneFingerprint(t *testing.T) {
	f1 := zoneFingerprint([]string{"a.example", "b.example"})
	f2 := zoneFingerprint([]string{"b.example", "a.example"})
	if f1 != f2 {
		t.Errorf("fingerprint must be order-independent: %q != %q", f1, f2)
	}

	f3 := zoneFingerprint([]string{"a.example", "c.example"})
	if f1 == f3 {
		t.Errorf("different zone sets must produce different fingerprints")
	}
}

func TestCache_SweepRemovesExpiredEntries(t *testing.T) {
	now := time.Unix(0, 0)
	c := newCache([]string{"zone.example"}, time.Millisecond, func() time.Time { return now })
	ip := net.ParseIP("203.0.113.5")
	c.set(c.key(ip), Result{IP: ip.String()})

	now = now.Add(time.Hour) // well past the 1ms TTL

	c.mu.Lock()
	c.sweepLocked()
	n := len(c.entries)
	c.mu.Unlock()

	if n != 0 {
		t.Errorf("sweepLocked left %d entries, want 0", n)
	}
}

func TestCache_BoundedEntryCount(t *testing.T) {
	fixedNow := time.Unix(0, 0)
	c := newCache([]string{"zone.example"}, time.Hour, func() time.Time { return fixedNow })

	for i := 0; i < maxCacheEntries+500; i++ {
		ip := net.IPv4(byte(i>>24), byte(i>>16), byte(i>>8), byte(i)) //nolint:gosec // deterministic test IP generation, not a security-sensitive random value
		c.set(c.key(ip), Result{IP: ip.String()})
	}

	c.mu.RLock()
	n := len(c.entries)
	c.mu.RUnlock()

	if n > maxCacheEntries {
		t.Errorf("cache grew to %d entries, want <= %d (maxCacheEntries)", n, maxCacheEntries)
	}
}

func TestCheck_EmptyZonesPerformsNoLookup(t *testing.T) {
	r := newFakeResolver()
	c := NewCheckerWithResolver(r, nil, time.Second, time.Minute, discardLogger())

	res := c.Check(context.Background(), net.ParseIP("203.0.113.5"))
	if len(res.Zones) != 0 {
		t.Errorf("Zones = %+v, want none", res.Zones)
	}
	if got := r.calls.Load(); got != 0 {
		t.Errorf("calls = %d, want 0", got)
	}
}

func TestCheck_NilIPDoesNotPanic(t *testing.T) {
	r := newFakeResolver()
	c := NewCheckerWithResolver(r, []string{"zone.example"}, time.Second, time.Minute, discardLogger())

	res := c.Check(context.Background(), nil)
	if len(res.Zones) != 0 {
		t.Errorf("Zones = %+v, want none for nil IP", res.Zones)
	}
	if got := r.calls.Load(); got != 0 {
		t.Errorf("calls = %d, want 0 for nil IP", got)
	}
}

// TestCheck_ConcurrentUseIsRaceFree exercises Check from many goroutines
// against the same and different IPs simultaneously, so `go test -race`
// catches any data race in the cache or the zone-collection path.
func TestCheck_ConcurrentUseIsRaceFree(t *testing.T) {
	zone := "zone.example"
	r := newFakeResolver()
	ips := []net.IP{
		net.ParseIP("203.0.113.1"),
		net.ParseIP("203.0.113.2"),
		net.ParseIP("203.0.113.3"),
	}
	r.set(nameFor(t, ips[0], zone), []net.IP{net.ParseIP("127.0.0.2")}, nil)
	// ips[1], ips[2] left unset -> NXDOMAIN.

	c := NewCheckerWithResolver(r, []string{zone}, time.Second, 5*time.Millisecond, discardLogger())

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ip := ips[i%len(ips)]
			c.Check(context.Background(), ip)
		}(i)
	}
	wg.Wait()
}

// TestListingIsLoggedWithIPZoneAndCode pins the logging criterion: each
// listing records the IP, the zone and the returned code at INFO, so an
// operator can answer "why was this junked" from the logs alone. It also
// pins the constraint that goes with it -- the DNSBL path never logs message
// content, because it never sees any.
func TestListingIsLoggedWithIPZoneAndCode(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	ip := net.ParseIP("203.0.113.5")
	const zone = "zen.example-bl.test"

	r := newFakeResolver()
	r.set(nameFor(t, ip, zone), []net.IP{net.ParseIP("127.0.0.4")}, nil)

	c := NewCheckerWithResolver(r, []string{zone}, time.Second, time.Minute, logger)
	if res := c.Check(context.Background(), ip); len(res.ListedZones()) != 1 {
		t.Fatalf("precondition: expected one listing, got %+v", res.Zones)
	}

	out := buf.String()
	for _, want := range []string{"203.0.113.5", zone, "127.0.0.4"} {
		if !strings.Contains(out, want) {
			t.Errorf("INFO log = %q, want it to record %q", out, want)
		}
	}
	if !strings.Contains(out, "level=INFO") {
		t.Errorf("listing was not logged at INFO: %q", out)
	}
}
