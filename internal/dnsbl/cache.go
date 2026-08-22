package dnsbl

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"slices"
	"strings"
	"sync"
	"time"
)

// maxCacheEntries caps the per-Checker cache so a spam flood from many
// distinct source IPs cannot grow it without limit -- a memory-exhaustion
// path in a long-lived server process otherwise.
const maxCacheEntries = 10000

// cacheEntry holds one cached Result plus its expiry time.
type cacheEntry struct {
	result  Result
	expires time.Time
}

// cache is a per-(zone-set, IP) TTL cache of DNSBL Results, safe for
// concurrent use by the pipeline's worker pool.
//
// Errored and timed-out zone verdicts are cached with the same TTL as
// successful ones: the alternative -- re-querying on every error -- turns a
// single dead or slow zone into a query storm against a server that is
// already failing, which is worse for delivery latency than serving a stale
// not-listed verdict for up to one TTL.
type cache struct {
	mu          sync.RWMutex
	entries     map[string]cacheEntry
	ttl         time.Duration
	fingerprint string

	// now is injectable for deterministic, sleep-free TTL-expiry tests,
	// following the precedent set by internal/dnssec.Validator.now.
	// Defaults to time.Now.
	now func() time.Time
}

// newCache creates a cache keyed by the given zone set (see zoneFingerprint)
// and TTL. now defaults to time.Now when nil.
func newCache(zones []string, ttl time.Duration, now func() time.Time) *cache {
	if now == nil {
		now = time.Now
	}
	return &cache{
		entries:     make(map[string]cacheEntry),
		ttl:         ttl,
		fingerprint: zoneFingerprint(zones),
		now:         now,
	}
}

// zoneFingerprint hashes the sorted zone set once, at construction, so cache
// keys change automatically when the configured zones change -- a changed
// zone set can never serve a verdict computed against a different set of
// zones.
func zoneFingerprint(zones []string) string {
	sorted := slices.Clone(zones)
	slices.Sort(sorted)
	sum := sha256.Sum256([]byte(strings.Join(sorted, ",")))
	return hex.EncodeToString(sum[:8])
}

// key returns the cache key for ip under this cache's zone-set fingerprint.
func (c *cache) key(ip net.IP) string {
	return c.fingerprint + "|" + ip.String()
}

// get returns the cached Result for key if present and not expired.
func (c *cache) get(key string) (Result, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	ent, ok := c.entries[key]
	if !ok || !c.now().Before(ent.expires) {
		return Result{}, false
	}
	// Hand back a copy of the slice header's contents. A Result returned
	// by value still shares one backing array with every other caller, so
	// a future consumer that sorts or annotates res.Zones in place would
	// corrupt the cached entry and race with concurrent readers.
	res := ent.result
	res.Zones = slices.Clone(ent.result.Zones)
	return res, true
}

// set stores res under key with the cache's TTL, sweeping expired entries
// (and, failing that, dropping the whole map) if the cache is at capacity.
func (c *cache) set(key string, res Result) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= maxCacheEntries {
		c.sweepLocked()
	}
	if len(c.entries) >= maxCacheEntries {
		// Still over cap after sweeping expired entries: a flood of
		// distinct IPs is outrunning the TTL. Drop the whole map rather
		// than grow unbounded -- the worst outcome is a burst of
		// redundant lookups, never unbounded memory growth.
		c.entries = make(map[string]cacheEntry)
	}
	// Clone on the way in as well, so the caller's own copy cannot be
	// mutated out from under the cache after storing.
	stored := res
	stored.Zones = slices.Clone(res.Zones)
	c.entries[key] = cacheEntry{result: stored, expires: c.now().Add(c.ttl)}
}

// sweepLocked deletes expired entries. Callers must hold c.mu for writing.
func (c *cache) sweepLocked() {
	now := c.now()
	for k, ent := range c.entries {
		if !now.Before(ent.expires) {
			delete(c.entries, k)
		}
	}
}
