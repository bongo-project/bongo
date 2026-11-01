package queue

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// barrierRelay blocks every delivery until limit calls are in flight at once,
// recording the highest concurrency seen. The timeout is the failure path only.
type barrierRelay struct {
	limit   int
	timeout time.Duration

	mu       sync.Mutex
	inflight int
	max      int
	calls    int
	timedOut bool
	reached  chan struct{}
	once     sync.Once
}

func newBarrierRelay(limit int) *barrierRelay {
	return &barrierRelay{limit: limit, timeout: 10 * time.Second, reached: make(chan struct{})}
}

func (r *barrierRelay) Deliver(context.Context, string, []string, []byte) ([]string, error) {
	r.mu.Lock()
	r.inflight++
	r.calls++
	if r.inflight > r.max {
		r.max = r.inflight
	}
	if r.inflight == r.limit {
		r.once.Do(func() { close(r.reached) })
	}
	r.mu.Unlock()

	select {
	case <-r.reached:
	case <-time.After(r.timeout):
		r.mu.Lock()
		r.timedOut = true
		r.mu.Unlock()
	}

	r.mu.Lock()
	r.inflight--
	r.mu.Unlock()
	return nil, nil
}

// TestWorkerConcurrencyLimitsSimultaneousDeliveries: with more pending messages
// than the limit, exactly limit deliveries run at once. The barrier opens only
// when limit callers are in flight, so the limit is reached (not merely
// respected), and a wider semaphore lets more than limit through.
func TestWorkerConcurrencyLimitsSimultaneousDeliveries(t *testing.T) {
	for _, limit := range []int{1, 3} {
		t.Run(fmt.Sprintf("limit %d", limit), func(t *testing.T) {
			worker, qs, blobs := newWorkerTestEnv(t, true)
			relay := newBarrierRelay(limit)
			worker.relay = relay
			worker.concurrency = limit

			hash, err := blobs.Put([]byte("From: alice@example.com\r\nTo: bob@external.com\r\n\r\nHi"))
			if err != nil {
				t.Fatalf("Put: %v", err)
			}
			total := 4*limit + 1
			for i := range total {
				if err := qs.Enqueue(&bongov1.OutboundMessage{
					Id: fmt.Sprintf("m-%d", i), SenderEmail: "alice@example.com",
					Recipients: []string{"bob@external.com"}, BlobHash: hash,
				}); err != nil {
					t.Fatalf("Enqueue: %v", err)
				}
			}

			worker.poll(context.Background())

			relay.mu.Lock()
			defer relay.mu.Unlock()
			if relay.timedOut {
				t.Error("barrier timed out: the limit was never reached")
			}
			if relay.max != limit {
				t.Errorf("max concurrent deliveries = %d, want %d", relay.max, limit)
			}
			if relay.calls != total {
				t.Errorf("relay calls = %d, want %d", relay.calls, total)
			}
			if stats, _ := qs.Stats(); stats.PendingCount != 0 {
				t.Errorf("pending = %d, want 0", stats.PendingCount)
			}
		})
	}
}
