package agent

import (
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/queue"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// externalRelayAgent builds an agent with a queue and blob store, hosting
// hosted.test and holding a send-as identity me@gmail.test.
func externalRelayAgent(t *testing.T, withQueue bool, perAddr, global int) (*Agent, *queue.QueueStore) {
	t.Helper()
	ps := bounceCopyTestStore(t)
	rec, err := proto.Marshal(&bongov1.AddressRecord{UserId: "u1", Domain: "gmail.test", Kind: "identity"})
	if err != nil {
		t.Fatal(err)
	}
	seedBounceCopy(t, ps, map[string]string{
		"/system/domain/hosted.test":    "enabled",
		"/system/address/me@gmail.test": string(rec),
	})
	blobs, err := store.NewBlobStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.SMTP.SRSBouncesPerAddressPerHour = perAddr
	cfg.SMTP.SRSBouncesPerHour = global
	a := &Agent{Config: cfg, Store: ps, Blobs: blobs, logger: testLogger()}
	a.AddressStore = store.NewAddressStore(ps, putApplier{ps}, nil)
	var qs *queue.QueueStore
	if withQueue {
		qs = queue.NewQueueStore(ps, putApplier{ps})
		a.QueueStore = qs
	}
	return a, qs
}

// newBounceGenerator is the generator Run hands to the queue worker, so this
// covers what Run wires for reports to senders outside the hosted domains:
// deleting the configureBounceExternalRelay call turns the relay cases red.
func TestNewBounceGenerator_ExternalRelayWiring(t *testing.T) {
	tests := []struct {
		name      string
		withQueue bool
		sender    string
		wantLocal int
		wantQueue int
	}{
		{"hosted domain stays local", true, "alice@hosted.test", 1, 0},
		{"send-as identity on an external domain stays local", true, "me@gmail.test", 1, 0},
		{"unknown external sender is relayed", true, "stranger@gmail.test", 0, 1},
		{"no queue (edge node) keeps the old behaviour", false, "stranger@gmail.test", 1, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a, qs := externalRelayAgent(t, tc.withQueue, 5, 200)
			rec := &bounceRecorder{}
			g := a.newBounceGenerator(rec)
			orig := &bongov1.OutboundMessage{SenderEmail: tc.sender}
			if err := g.GenerateAndDeliver(orig, []string{"bob@remote.test"}, "550 5.1.1 user unknown"); err != nil {
				t.Fatalf("GenerateAndDeliver: %v", err)
			}
			if len(rec.calls) != tc.wantLocal {
				t.Errorf("local deliveries = %d, want %d", len(rec.calls), tc.wantLocal)
			}
			if qs != nil {
				pending, err := qs.ListPending(10)
				if err != nil {
					t.Fatal(err)
				}
				if len(pending) != tc.wantQueue {
					t.Errorf("queued = %d, want %d", len(pending), tc.wantQueue)
				}
			}
		})
	}
}

// The caps come from the SRS bounce settings but are separate limiter
// instances, built even though no SRS codec is configured on this agent.
func TestNewBounceGenerator_ExternalRelayCaps(t *testing.T) {
	t.Run("per address", func(t *testing.T) {
		a, qs := externalRelayAgent(t, true, 2, 200)
		g := a.newBounceGenerator(&bounceRecorder{})
		for range 4 {
			if err := g.GenerateAndDeliver(&bongov1.OutboundMessage{SenderEmail: "stranger@gmail.test"}, []string{"b@remote.test"}, "550 no"); err != nil {
				t.Fatal(err)
			}
		}
		pending, _ := qs.ListPending(10)
		if len(pending) != 2 {
			t.Errorf("queued %d, want the per-address cap of 2", len(pending))
		}
	})
	t.Run("global", func(t *testing.T) {
		a, qs := externalRelayAgent(t, true, 100, 2)
		g := a.newBounceGenerator(&bounceRecorder{})
		for _, s := range []string{"a@x.test", "b@x.test", "c@x.test", "d@x.test"} {
			if err := g.GenerateAndDeliver(&bongov1.OutboundMessage{SenderEmail: s}, []string{"b@remote.test"}, "550 no"); err != nil {
				t.Fatal(err)
			}
		}
		pending, _ := qs.ListPending(10)
		if len(pending) != 2 {
			t.Errorf("queued %d, want the global cap of 2", len(pending))
		}
	})
}
