package cluster

import (
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/greylist"
)

type testClock struct{ t time.Time }

func (c *testClock) now() time.Time { return c.t }

var glSettings = greylist.Settings{Delay: 5 * time.Minute, RetryWindow: 4 * time.Hour, PassLifetime: 864 * time.Hour}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// glNode starts a gossip node sharing a greylist table. key is the base64
// encryption key, or empty for none.
func glNode(t *testing.T, name, key string, tb *greylist.Table) (*GossipNode, int) {
	t.Helper()
	cfg := config.Defaults()
	cfg.Server.Role = "server"
	cfg.Server.Name = name
	cfg.Server.BindAddr = "127.0.0.1"
	cfg.Cluster.Gossip.Port = freePort(t)
	cfg.Cluster.Gossip.EncryptionKey = key
	n, err := NewGossipNode(cfg, "127.0.0.1:1", "", quietLogger(), WithGreylist(tb))
	if err != nil {
		t.Fatalf("NewGossipNode %s: %v", name, err)
	}
	t.Cleanup(func() { _ = n.Shutdown() })
	return n, cfg.Cluster.Gossip.Port
}

func twoGreylistNodes(t *testing.T, key string) (a, b *greylist.Table, clock *testClock) {
	t.Helper()
	clock = &testClock{t: time.Unix(1_800_000_000, 0)}
	a = greylist.New(glSettings, 0, clock.now)
	b = greylist.New(glSettings, 0, clock.now)
	_, portA := glNode(t, "a", key, a)
	nodeB, _ := glNode(t, "b", key, b)
	if _, err := nodeB.Join([]string{fmt.Sprintf("127.0.0.1:%d", portA)}); err != nil {
		t.Fatalf("join: %v", err)
	}
	return a, b, clock
}

func TestGreylistSharedOverGossip(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	a, b, clock := twoGreylistNodes(t, key)
	ip := net.ParseIP("192.0.2.10")

	if a.Check(ip, "s@x.example", "r@y.example") {
		t.Fatal("first contact on node A must be deferred")
	}
	deadline := time.Now().Add(15 * time.Second)
	for b.Len() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("node B never received the entry")
		}
		time.Sleep(50 * time.Millisecond)
	}
	clock.t = clock.t.Add(6 * time.Minute)
	if !b.Check(ip, "s@x.example", "r@y.example") {
		t.Fatal("retry reaching node B should pass")
	}
	// B recorded the network as known and tells A.
	deadline = time.Now().Add(15 * time.Second)
	for {
		if a.Check(net.ParseIP("192.0.2.77"), "other@x.example", "r2@y.example") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("known network never reached node A")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestGreylistNotSharedWithoutKey(t *testing.T) {
	a, b, _ := twoGreylistNodes(t, "")
	a.Check(net.ParseIP("192.0.2.10"), "s@x.example", "r@y.example")
	time.Sleep(1500 * time.Millisecond) // several gossip intervals
	if b.Len() != 0 {
		t.Fatalf("node B holds %d entries; nothing may be exchanged without a key", b.Len())
	}
}

func newTestGossip(t *testing.T) (*greylistGossip, *greylist.Table) {
	t.Helper()
	clock := &testClock{t: time.Unix(1_800_000_000, 0)}
	tb := greylist.New(glSettings, 0, clock.now)
	g := newGreylistGossip(tb, func() int { return 3 }, quietLogger())
	return g, tb
}

func TestGreylistHostilePayloads(t *testing.T) {
	g, tb := newTestGossip(t)
	d := &gossipDelegate{greylist: g}
	netKey, _ := greylist.NetworkKey(net.ParseIP("192.0.2.1"))
	valid := greylist.Encode(greylist.KindNetwork, netKey, 1_800_000_100)
	longMsg := append(append([]byte{}, valid...), 0)
	badVersion := append([]byte{}, valid...)
	badVersion[1] = 9
	farFuture := greylist.Encode(greylist.KindNetwork, netKey, 1_800_000_000+10*365*86400)
	stale := greylist.Encode(greylist.KindTriplet, netKey, 1_000)
	for name, msg := range map[string][]byte{
		"nil": nil, "empty": {}, "one byte": {'G'}, "garbage": []byte("hello world, not a greylist message"),
		"too long": longMsg, "future version": badVersion, "far future known": farFuture, "stale first seen": stale,
	} {
		d.NotifyMsg(msg)
		if tb.Len() != 0 {
			t.Fatalf("%s: table holds %d entries", name, tb.Len())
		}
	}
	d.NotifyMsg(valid)
	if tb.Len() != 1 {
		t.Fatalf("valid message not applied: Len = %d", tb.Len())
	}
	// A delegate without a table ignores everything.
	(&gossipDelegate{}).NotifyMsg(valid)
	if (&gossipDelegate{}).GetBroadcasts(0, 1400) != nil {
		t.Fatal("delegate without a table must not broadcast")
	}
}

func TestGreylistInboundRateCap(t *testing.T) {
	g, tb := newTestGossip(t)
	for i := range 3000 {
		g.handle(greylist.Encode(greylist.KindNetwork, greylist.Key{byte(i), byte(i >> 8)}, 1_800_000_100))
	}
	if tb.Len() >= 3000 || tb.Len() < 1000 {
		t.Fatalf("accepted %d of 3000, want about the burst of 1000", tb.Len())
	}
	if g.inDropped.Load() == 0 {
		t.Fatal("no inbound drops counted")
	}
}

func TestGreylistOutboundRateCap(t *testing.T) {
	g, _ := newTestGossip(t)
	for i := range 3000 {
		g.Publish(greylist.KindNetwork, greylist.Key{byte(i), byte(i >> 8)}, 1_800_000_100)
	}
	if g.outDropped.Load() < 1500 {
		t.Fatalf("outbound drops = %d, want most of the excess over the burst", g.outDropped.Load())
	}
}

func TestGreylistQueuePruneAndInvalidate(t *testing.T) {
	g, _ := newTestGossip(t)
	g.out[1] = greylist.NewBucket(1e9, 1e9, nil)
	for i := range 6000 {
		g.Publish(greylist.KindNetwork, greylist.Key{byte(i), byte(i >> 8)}, 1_800_000_100)
	}
	if n := g.queue[1].NumQueued(); n > greylistQueueMax {
		t.Fatalf("queue holds %d, cap %d", n, greylistQueueMax)
	}

	g2, _ := newTestGossip(t)
	// memberlist reuses queue ids when the queue empties during a
	// replacement, so keep an unrelated entry queued.
	g2.Publish(greylist.KindNetwork, greylist.Key{2}, 1_800_000_100)
	for range 5 {
		g2.Publish(greylist.KindTriplet, greylist.Key{1}, 1_800_000_100)
	}
	g2.Publish(greylist.KindNetwork, greylist.Key{1}, 1_800_000_100)
	if n := g2.queue[0].NumQueued() + g2.queue[1].NumQueued(); n != 3 {
		t.Fatalf("queue holds %d, want 3 (duplicates replaced)", n)
	}
	out := g2.broadcasts(2, 1400)
	if len(out) != 3 || len(out[0]) != greylist.MessageLen {
		t.Fatalf("broadcasts = %d messages", len(out))
	}
}

func FuzzGreylistHandle(f *testing.F) {
	f.Add(greylist.Encode(greylist.KindTriplet, greylist.Key{1}, 5))
	f.Add([]byte("G"))
	f.Fuzz(func(t *testing.T, b []byte) {
		clock := &testClock{t: time.Unix(1_800_000_000, 0)}
		tb := greylist.New(glSettings, 100, clock.now)
		g := newGreylistGossip(tb, func() int { return 1 }, quietLogger())
		g.handle(b)
		if tb.Len() > 1 {
			t.Fatalf("one message created %d entries", tb.Len())
		}
	})
}

func TestGreylistNetworkAnnouncementsSurviveTripletFlood(t *testing.T) {
	g, _ := newTestGossip(t)
	for i := range 3000 {
		g.Publish(greylist.KindTriplet, greylist.Key{byte(i), byte(i >> 8), 9}, 1_800_000_100)
	}
	g.Publish(greylist.KindNetwork, greylist.Key{7, 7, 7}, 1_800_000_100)
	found := false
	for _, m := range g.broadcasts(2, 1<<20) {
		if k, _, _, ok := greylist.Decode(m); ok && k == greylist.KindNetwork {
			found = true
		}
	}
	if !found {
		t.Fatal("network announcement lost behind a triplet flood")
	}
}

func TestGreylistInboundNetworksSurviveTripletFlood(t *testing.T) {
	g, tb := newTestGossip(t)
	for i := range 3000 {
		g.handle(greylist.Encode(greylist.KindTriplet, greylist.Key{byte(i), byte(i >> 8), 9}, 1_800_000_000))
	}
	before := tb.Len()
	g.handle(greylist.Encode(greylist.KindNetwork, greylist.Key{7, 7, 7}, 1_800_000_100))
	if tb.Len() != before+1 {
		t.Fatalf("network message dropped behind a triplet flood (len %d -> %d)", before, tb.Len())
	}
}
