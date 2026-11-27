// Package greylist is the in-memory greylisting table: first-seen triplets
// and known client networks, bounded and swept. It never touches Raft or
// Pebble. Sharing between MX nodes is done by the cluster package over
// gossip, through the Publisher interface and Merge.
package greylist

import (
	"crypto/sha256"
	"net"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// DefaultMaxEntries bounds triplets and known networks together.
	DefaultMaxEntries = 1_000_000

	// refreshEvery is how often a known network's refresh is gossiped.
	refreshEvery = 24 * time.Hour
	// futureSlack is how far ahead of the local clock a peer timestamp may be.
	futureSlack = 60 * time.Second
	// sweepChunk is how many removals one sweep step holds the lock for.
	sweepChunk = 10_000
)

// Settings are the greylisting timings.
type Settings struct {
	Delay        time.Duration
	RetryWindow  time.Duration
	PassLifetime time.Duration
}

// Publisher is told about entries other nodes may want. It is called without
// the table lock held.
type Publisher interface {
	Publish(kind byte, key Key, t int64)
}

type mapKey struct {
	kind byte
	key  Key
}

// node is one table entry, linked into the list of its kind.
type node struct {
	t          int64 // first-seen (triplet) or known-until (network), unix seconds
	pub        int64 // when a network was last announced to peers, unix seconds
	prev, next *node
	k          mapKey
	hasNet     bool // pending triplets: net below is set (peer-merged ones carry none)
	net        Key  // pending triplets: the /24 or /64 that sent it
}

// list is an intrusive doubly linked list, oldest at head. Every operation is
// O(1), so eviction never scans.
type list struct {
	head, tail *node
	n          int
}

func (l *list) pushBack(n *node) {
	n.prev, n.next = l.tail, nil
	if l.tail != nil {
		l.tail.next = n
	} else {
		l.head = n
	}
	l.tail = n
	l.n++
}

func (l *list) remove(n *node) {
	if n.prev != nil {
		n.prev.next = n.next
	} else {
		l.head = n.next
	}
	if n.next != nil {
		n.next.prev = n.prev
	} else {
		l.tail = n.prev
	}
	n.prev, n.next = nil, nil
	l.n--
}

func (l *list) moveToBack(n *node) {
	if l.tail == n {
		return
	}
	l.remove(n)
	l.pushBack(n)
}

// Table is the greylisting state of one node. Known networks and pending
// triplets live in separate lists so a flood of one cannot evict the other:
// known networks are capped at half the table, pending triplets take the
// rest, and when the table is full pending triplets go first.
//
// Budgets are counted per budget group: the /24 for IPv4 and the /48 for IPv6
// (a single /64 may use a quarter of its /48's budget). A group may hold at
// most perNet pending triplets and knownPerGroup known networks; past that a
// new triplet is deferred but not recorded, and a new known network passes
// its message but is not recorded. When the pending pool is full the victim
// is the oldest of the first evictSample triplets in the largest group, not
// simply the oldest, so a flood displaces itself first.
//
// Lists are in insertion order, which is expiry order except for entries
// that Merge or a re-put places with a different timestamp; those can outlive
// their expiry by at most one window until the head reaches them, which is
// harmless because every lookup re-checks expiry.
type Table struct {
	settings atomic.Pointer[Settings]
	pub      atomic.Pointer[Publisher]
	nowFn    func() time.Time
	max      int
	knownCap int
	// perNet is the pending-triplet budget of one group; knownPerGroup the
	// known-network budget of one group.
	perNet        int
	knownPerGroup int

	mu         sync.Mutex
	m          map[mapKey]*node
	known      list        // networks, oldest known-until first
	pending    list        // triplets, oldest first-seen first
	netPending map[Key]int // pending triplets per client network (/64 only)
	grpPending map[Key]int // pending triplets per budget group
	grpKnown   map[Key]int
	victimGrp  Key  // group the last eviction took from
	hasVictim  bool // known networks per budget group
}

// New returns a table bounded at limit entries (DefaultMaxEntries when limit <= 0).
// A nil now means time.Now.
func New(s Settings, limit int, now func() time.Time) *Table {
	if limit <= 0 {
		limit = DefaultMaxEntries
	}
	if now == nil {
		now = time.Now
	}
	t := &Table{
		nowFn:      now,
		max:        limit,
		knownCap:   max(limit/2, 1),
		perNet:     max(limit/50, 16),
		m:          make(map[mapKey]*node),
		netPending: make(map[Key]int),
		grpPending: make(map[Key]int),
		grpKnown:   make(map[Key]int),
	}
	t.knownPerGroup = max(t.knownCap/100, 16)
	t.SetSettings(s)
	return t
}

// SetSettings swaps the timings; safe while the table is in use.
func (t *Table) SetSettings(s Settings) { t.settings.Store(&s) }

// SetPublisher registers the gossip publisher. Safe at any time.
func (t *Table) SetPublisher(p Publisher) {
	if p == nil {
		t.pub.Store(nil)
		return
	}
	t.pub.Store(&p)
}

// Len returns the number of entries held.
func (t *Table) Len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.m)
}

func (t *Table) publish(kind byte, k Key, ts int64) {
	if kind == 0 {
		return
	}
	if p := t.pub.Load(); p != nil {
		(*p).Publish(kind, k, ts)
	}
}

// NetworkKey returns the client network key: the /24 for IPv4 (an IPv4-mapped
// IPv6 address counts as IPv4) or the /64 for IPv6. ok is false for a nil or
// invalid address.
func NetworkKey(ip net.IP) (k Key, ok bool) {
	a, ok := netip.AddrFromSlice(ip)
	if !ok {
		return k, false
	}
	a = a.Unmap()
	if a.Is4() {
		b := a.As4()
		copy(k[:3], b[:3])
		k[15] = 4
		return k, true
	}
	b := a.As16()
	copy(k[:8], b[:8])
	k[15] = 6
	return k, true
}

// groupOf returns the budget group of a client network: the network itself
// for IPv4, the /48 for IPv6.
func groupOf(n Key) Key {
	if n[15] != 6 {
		return n
	}
	var g Key
	copy(g[:6], n[:6])
	g[15] = 7
	return g
}

func bare(a string) string {
	a = strings.TrimSpace(a)
	a = strings.TrimPrefix(a, "<")
	a = strings.TrimSuffix(a, ">")
	return strings.ToLower(a)
}

// TripletKey hashes network, sender and recipient into a fixed-size key.
func TripletKey(network Key, sender, rcpt string) Key {
	h := sha256.New()
	h.Write(network[:])
	h.Write([]byte{0})
	h.Write([]byte(bare(sender)))
	h.Write([]byte{0})
	h.Write([]byte(bare(rcpt)))
	var k Key
	copy(k[:], h.Sum(nil))
	return k
}

func (t *Table) listOf(kind byte) *list {
	if kind == KindNetwork {
		return &t.known
	}
	return &t.pending
}

func dec(m map[Key]int, k Key) {
	if m[k] <= 1 {
		delete(m, k)
	} else {
		m[k]--
	}
}

// unlink removes a node everywhere. Caller holds the lock.
func (t *Table) unlink(n *node) {
	delete(t.m, n.k)
	t.listOf(n.k.kind).remove(n)
	if n.k.kind == KindNetwork {
		dec(t.grpKnown, groupOf(n.k.key))
	} else if n.hasNet {
		dec(t.grpPending, groupOf(n.net))
		if n.net[15] == 6 {
			dec(t.netPending, n.net)
		}
	}
}

// reapExpired removes up to limit expired nodes from the head of l.
func (t *Table) reapExpired(l *list, limit int) {
	now := t.nowFn().Unix()
	window := int64(t.settings.Load().RetryWindow / time.Second)
	for range limit {
		if l.head == nil || !expired(l.head.k, l.head, now, window) {
			return
		}
		t.unlink(l.head)
	}
}

const (
	// reapChunk bounds the expired nodes dropped when a budget is hit.
	reapChunk = 64
	// evictSample is how many head triplets are compared when the pool is
	// full.
	evictSample = 16
)

// pendingFull reports whether a new triplet from network net would exceed a
// budget.
func (t *Table) pendingFull(net Key) bool {
	if t.grpPending[groupOf(net)] >= t.perNet {
		return true
	}
	return net[15] == 6 && t.netPending[net] >= max(t.perNet/4, 16)
}

// evictOne frees one slot, preferring the pending pool. Of the first
// evictSample triplets it takes one from the group evicted from last (so a
// flooding group is drained rather than nibbled), else the one whose group
// holds the most pending triplets, the oldest on a tie. Caller holds the lock.
func (t *Table) evictOne() {
	if t.pending.head == nil {
		t.unlink(t.known.head)
		return
	}
	victim, best := t.pending.head, -1
	n := t.pending.head
	for range evictSample {
		if n == nil {
			break
		}
		c := 0
		if n.hasNet {
			g := groupOf(n.net)
			if t.hasVictim && g == t.victimGrp {
				victim = n
				break
			}
			c = t.grpPending[g]
		}
		if c > best {
			victim, best = n, c
		}
		n = n.next
	}
	t.hasVictim = victim.hasNet
	if victim.hasNet {
		t.victimGrp = groupOf(victim.net)
	}
	t.unlink(victim)
}

// put inserts or overwrites an entry, evicting when full. For a new triplet
// net is the sending network (hasNet false for one learned from a peer). It
// reports false when the entry is refused because its group already holds its
// share. Caller holds the lock.
func (t *Table) put(k mapKey, ts int64, net Key, hasNet bool) bool {
	if n, ok := t.m[k]; ok {
		n.t = ts
		t.listOf(k.kind).moveToBack(n)
		return true
	}
	if k.kind == KindNetwork {
		g := groupOf(k.key)
		if t.grpKnown[g] >= t.knownPerGroup {
			t.reapExpired(&t.known, reapChunk)
			if t.grpKnown[g] >= t.knownPerGroup {
				return false
			}
		}
		for t.known.n >= t.knownCap {
			t.unlink(t.known.head)
		}
	} else if hasNet && t.pendingFull(net) {
		t.reapExpired(&t.pending, reapChunk)
		if t.pendingFull(net) {
			return false
		}
	}
	for len(t.m) >= t.max {
		t.evictOne()
	}
	n := &node{k: k, t: ts}
	if k.kind == KindNetwork {
		// A network we create is announced (or came from a peer that did).
		n.pub = t.nowFn().Unix()
		t.grpKnown[groupOf(k.key)]++
	} else if hasNet {
		n.hasNet, n.net = true, net
		t.grpPending[groupOf(net)]++
		if net[15] == 6 {
			t.netPending[net]++
		}
	}
	t.m[k] = n
	t.listOf(k.kind).pushBack(n)
	return true
}

// Check reports whether a message for this triplet may pass now. false means
// answer 451. A nil or invalid ip passes (nothing to key on).
func (t *Table) Check(ip net.IP, sender, rcpt string) bool {
	nk, ok := NetworkKey(ip)
	if !ok {
		return true
	}
	s := t.settings.Load()
	now := t.nowFn().Unix()
	life := int64(s.PassLifetime / time.Second)
	delay := int64(s.Delay / time.Second)
	window := int64(s.RetryWindow / time.Second)
	netK := mapKey{KindNetwork, nk}
	tripK := mapKey{KindTriplet, TripletKey(nk, sender, rcpt)}

	var pubKind byte
	var pubKey Key
	var pubT int64
	pass := false

	t.mu.Lock()
	if n, ok := t.m[netK]; ok {
		if n.t > now {
			// Known network: pass and extend. The refresh is gossiped at
			// most once every refreshEvery, counted from the last announce.
			if now-n.pub >= int64(refreshEvery/time.Second) {
				pubKind, pubKey, pubT = KindNetwork, nk, now+life
				n.pub = now
			}
			n.t = now + life
			t.known.moveToBack(n)
			t.mu.Unlock()
			t.publish(pubKind, pubKey, pubT)
			return true
		}
		t.unlink(n)
	}
	e, ok := t.m[tripK]
	switch {
	case !ok || now > e.t+window:
		if t.put(tripK, now, nk, true) {
			pubKind, pubKey, pubT = KindTriplet, tripK.key, now
		}
	case now < e.t+delay:
		// Too early: keep first-seen.
	default:
		t.unlink(e)
		// The message passes even when the group's known-network budget
		// refuses the record; the network is then not announced either.
		if t.put(netK, now+life, Key{}, false) {
			pubKind, pubKey, pubT = KindNetwork, nk, now+life
		}
		pass = true
	}
	t.mu.Unlock()
	t.publish(pubKind, pubKey, pubT)
	return pass
}

// Merge applies an entry received from a peer. Values outside the accepted
// range are dropped: a hostile or confused peer cannot plant a far-future
// known network or an ancient first-seen. It reports whether the entry was
// accepted. Nothing is republished.
func (t *Table) Merge(kind byte, key Key, ts int64) bool {
	s := t.settings.Load()
	now := t.nowFn().Unix()
	slack := int64(futureSlack / time.Second)
	window := int64(s.RetryWindow / time.Second)
	switch kind {
	case KindTriplet:
		if ts < now-window || ts > now+slack {
			return false
		}
		k := mapKey{KindTriplet, key}
		t.mu.Lock()
		defer t.mu.Unlock()
		if e, ok := t.m[k]; ok && e.t >= now-window && e.t <= ts {
			return true // ours is earlier and still live
		}
		t.put(k, ts, Key{}, false)
		return true
	case KindNetwork:
		if ts <= now || ts > now+int64(s.PassLifetime/time.Second)+slack {
			return false
		}
		k := mapKey{KindNetwork, key}
		t.mu.Lock()
		defer t.mu.Unlock()
		if e, ok := t.m[k]; ok && e.t >= ts {
			return true
		}
		t.put(k, ts, Key{}, false)
		return true
	}
	return false
}

// sweepList drops expired nodes from the head of l, at most sweepChunk per
// call, and stops at the first live one (lists are in expiry order). It
// reports how many it removed and whether it stopped for the chunk limit.
func (t *Table) sweepList(l *list, now, window int64) (removed int, more bool) {
	for l.head != nil {
		if removed == sweepChunk {
			return removed, true
		}
		n := l.head
		if !expired(n.k, n, now, window) {
			break
		}
		t.unlink(n)
		removed++
	}
	return removed, false
}

func expired(k mapKey, n *node, now, window int64) bool {
	if k.kind == KindNetwork {
		return n.t <= now
	}
	return now > n.t+window
}

// Sweep removes expired entries, holding the lock for at most sweepChunk
// removals at a time, and returns how many were removed.
func (t *Table) Sweep() int {
	s := t.settings.Load()
	window := int64(s.RetryWindow / time.Second)
	total := 0
	for _, l := range []*list{&t.pending, &t.known} {
		for {
			now := t.nowFn().Unix()
			t.mu.Lock()
			n, more := t.sweepList(l, now, window)
			t.mu.Unlock()
			total += n
			if !more {
				break
			}
		}
	}
	return total
}
