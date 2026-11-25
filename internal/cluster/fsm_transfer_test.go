package cluster

import (
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/store"
)

// fsmApplier commits each command through the FSM, as Raft would.
type fsmApplier struct {
	fsm   *FSM
	index uint64
}

func (a *fsmApplier) ApplyCommand(cmd *bongov1.RaftCommand, _ time.Duration) error {
	a.index++
	if err, ok := a.fsm.Apply(makeProtoLog(a.index, cmd)).(error); ok {
		return err
	}
	return nil
}

// A cross-owner transfer is applied by the FSM: the destination owner's copy is
// indexed (SQLite and Bleve) as a delivery is, and watchers hear of exactly one arrival (and, for
// a MOVE, one expunge on the source).
func TestFSMCrossOwnerTransferIndexesDestinationAndNotifiesOnce(t *testing.T) {
	for _, move := range []bool{false, true} {
		name := "copy"
		if move {
			name = "move"
		}
		t.Run(name, func(t *testing.T) {
			env, sm := newEnvWithGAL(t)
			ms := store.NewMailboxStore(env.pebble, &fsmApplier{fsm: env.fsm})
			for _, c := range [][2]string{{"alice", "INBOX"}, {"bob", "Box"}} {
				if err := ms.CreateMailbox(c[0], c[1]); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := ms.DeliverMessage("alice", "INBOX", &bongov1.MailMeta{
				Guid: "a1", Subject: "xferneedle", ReceivedAt: 10,
				RawMessage: &bongov1.BlobRef{Hash: "deadbeef", Size: 5},
			}, 0); err != nil {
				t.Fatal(err)
			}

			rec := &recordingNotifier{}
			env.fsm.SetNotifier(rec)

			pairs, err := ms.TransferMessages("alice", "INBOX", "bob", "Box", []uint32{1}, move, store.FlagSeen)
			if err != nil {
				t.Fatal(err)
			}
			if len(pairs) != 1 || pairs[0].DstUID != 1 {
				t.Fatalf("pairs = %+v", pairs)
			}

			dst, err := ms.ListMessages("bob", "Box")
			if err != nil {
				t.Fatal(err)
			}
			if len(dst) != 1 {
				t.Fatalf("destination messages = %d, want 1", len(dst))
			}
			dstGUID := dst[0].MailGuid
			if dstGUID == "" || dstGUID == "a1" {
				t.Fatalf("destination guid = %q, want a new guid", dstGUID)
			}

			objs, err := env.metadata.QueryByType("bob", store.DocTypeMail)
			if err != nil {
				t.Fatal(err)
			}
			if len(objs) != 1 || objs[0].GUID != dstGUID {
				t.Fatalf("destination SQLite rows = %+v, want one row for %s", objs, dstGUID)
			}

			hits, err := sm.Search("bob", "xferneedle", 10)
			if err != nil {
				t.Fatal(err)
			}
			if len(hits) != 1 || hits[0].GUID != dstGUID {
				t.Fatalf("destination Bleve hits = %+v, want one hit for %s", hits, dstGUID)
			}

			// The source MailMeta and its index rows are left for the orphan
			// sweep, so only the mailbox listing shows the MOVE.
			src, err := ms.ListMessages("alice", "INBOX")
			if err != nil {
				t.Fatal(err)
			}
			wantSrc := 1
			if move {
				wantSrc = 0
			}
			if len(src) != wantSrc {
				t.Fatalf("source INBOX messages = %d, want %d", len(src), wantSrc)
			}

			var arrivals, expunges int
			for _, ev := range rec.drain() {
				switch {
				case ev.kind == "newmail" && ev.userID == "bob" && ev.mailbox == "Box":
					arrivals++
				case ev.kind == "expunge" && ev.userID == "alice" && ev.mailbox == "INBOX":
					expunges++
				default:
					t.Errorf("unexpected event %+v", ev)
				}
			}
			wantExpunges := 0
			if move {
				wantExpunges = 1
			}
			if arrivals != 1 || expunges != wantExpunges {
				t.Fatalf("arrivals = %d, expunges = %d, want 1 and %d", arrivals, expunges, wantExpunges)
			}
		})
	}
}
