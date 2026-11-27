package cluster

import (
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/hashicorp/memberlist"

	"github.com/bongo-project/bongo/internal/greylist"
)

const (
	// greylistQueueMax bounds the broadcasts waiting to be gossiped, per
	// kind: a flood of first-seen triplets cannot prune network announcements.
	greylistQueueMax = 4096
	// greylistOutRate and greylistOutBurst cap entries sent per second, per
	// kind; more stay local only. Triplets and known networks have separate
	// buckets so spam cannot starve the announcements that matter.
	greylistOutRate  = 100
	greylistOutBurst = 1000
	// greylistInRate caps entries accepted per second across all peers, per
	// kind (memberlist does not tell us the sender, so not per peer).
	greylistInRate  = 1000
	greylistInBurst = 1000
	// greylistDropLogEvery is the shortest gap between two drop log lines.
	greylistDropLogEvery = time.Minute
)

// greylistGossip carries the greylisting table over memberlist user messages.
// It exists only for a gossip pool with an encryption key: memberlist then
// authenticates and encrypts every packet, so an outsider cannot mark their
// own network as known. Without a key the table stays local.
type greylistGossip struct {
	table *greylist.Table
	// Index 0 is triplets, 1 is known networks.
	queue  [2]*memberlist.TransmitLimitedQueue
	out    [2]*greylist.Bucket
	in     [2]*greylist.Bucket
	logger *slog.Logger
	now    func() time.Time

	outDropped atomic.Uint64
	inDropped  atomic.Uint64
	lastLog    atomic.Int64 // unix nanos of the last drop log line
}

// kindIndex maps an entry kind to its queue and bucket slot.
func kindIndex(kind byte) int {
	if kind == greylist.KindNetwork {
		return 1
	}
	return 0
}

func newGreylistGossip(table *greylist.Table, numNodes func() int, logger *slog.Logger) *greylistGossip {
	g := &greylistGossip{
		table:  table,
		logger: logger,
		now:    time.Now,
	}
	for i := range g.queue {
		g.queue[i] = &memberlist.TransmitLimitedQueue{NumNodes: numNodes, RetransmitMult: 3}
		g.out[i] = greylist.NewBucket(greylistOutRate, greylistOutBurst, nil)
		g.in[i] = greylist.NewBucket(greylistInRate, greylistInBurst, nil)
	}
	table.SetPublisher(g)
	return g
}

// glBroadcast is one queued entry. A newer update for the same key replaces an
// older one still waiting.
type glBroadcast struct {
	kind byte
	key  greylist.Key
	msg  []byte
}

func (b *glBroadcast) Invalidates(other memberlist.Broadcast) bool {
	o, ok := other.(*glBroadcast)
	return ok && o.kind == b.kind && o.key == b.key
}

// Name makes memberlist replace a queued entry for the same key in constant
// time instead of scanning the queue.
func (b *glBroadcast) Name() string { return string(b.msg[2 : 3+greylist.KeyLen]) }

func (b *glBroadcast) Message() []byte { return b.msg }
func (b *glBroadcast) Finished()       {}

// Publish implements greylist.Publisher.
func (g *greylistGossip) Publish(kind byte, key greylist.Key, t int64) {
	i := kindIndex(kind)
	if !g.out[i].Allow() {
		g.noteDrop(&g.outDropped, "outbound")
		return
	}
	g.queue[i].QueueBroadcast(&glBroadcast{kind: kind, key: key, msg: greylist.Encode(kind, key, t)})
	g.queue[i].Prune(greylistQueueMax)
}

// handle processes a user message from a peer. buf is not retained.
func (g *greylistGossip) handle(buf []byte) {
	if !greylist.IsMessage(buf) {
		return
	}
	kind, key, t, ok := greylist.Decode(buf)
	if !ok {
		return
	}
	if !g.in[kindIndex(kind)].Allow() {
		g.noteDrop(&g.inDropped, "inbound")
		return
	}
	g.table.Merge(kind, key, t)
}

func (g *greylistGossip) broadcasts(overhead, limit int) [][]byte {
	// Known networks first: they are few and worth more than a triplet.
	out := g.queue[1].GetBroadcasts(overhead, limit)
	used := 0
	for _, m := range out {
		used += overhead + len(m)
	}
	return append(out, g.queue[0].GetBroadcasts(overhead, limit-used)...)
}

// noteDrop counts a drop and logs at most once a minute.
func (g *greylistGossip) noteDrop(c *atomic.Uint64, dir string) {
	n := c.Add(1)
	now := g.now().UnixNano()
	last := g.lastLog.Load()
	if now-last < int64(greylistDropLogEvery) || !g.lastLog.CompareAndSwap(last, now) {
		return
	}
	g.logger.Warn("greylist: gossip rate cap reached, dropping entries", "direction", dir, "dropped_total", n)
}
