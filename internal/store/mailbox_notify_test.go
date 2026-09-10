package store

import (
	"errors"
	"sync"
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// recordingNotifier captures what the store publishes. The mutex is not
// decorative: notifications are emitted from whichever goroutine performed the
// write, and delivery under -race is what the IMAP tests exercise.
type recordingNotifier struct {
	mu     sync.Mutex
	events []notifyEvent
}

type notifyEvent struct {
	kind    string
	mailbox string
	uid     uint32
	flags   uint32
}

func (r *recordingNotifier) NotifyNewMail(_, mailbox string, uid, _ uint32) {
	r.record(notifyEvent{kind: "newmail", mailbox: mailbox, uid: uid})
}

func (r *recordingNotifier) NotifyFlagChange(_, mailbox string, uid, flags uint32) {
	r.record(notifyEvent{kind: "flagchange", mailbox: mailbox, uid: uid, flags: flags})
}

func (r *recordingNotifier) NotifyExpunge(_, mailbox string, uid uint32) {
	r.record(notifyEvent{kind: "expunge", mailbox: mailbox, uid: uid})
}

func (r *recordingNotifier) record(ev notifyEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *recordingNotifier) drain() []notifyEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := append([]notifyEvent(nil), r.events...)
	r.events = nil
	return out
}

// newNotifyTestEnv builds a store with a recorder attached and one message in
// INBOX. Delivery happens before the recorder is installed so the arrival does
// not appear in the events under test.
func newNotifyTestEnv(t *testing.T) (*MailboxStore, *recordingNotifier, uint32) {
	t.Helper()

	ms := newMailboxTestEnv(t)
	if err := ms.CreateMailbox("u1", "Archive"); err != nil {
		t.Fatalf("CreateMailbox Archive: %v", err)
	}
	uid, err := ms.DeliverMessage("u1", "INBOX", &bongov1.MailMeta{
		Guid:       "guid-1",
		ReceivedAt: 1,
	}, 0)
	if err != nil {
		t.Fatalf("DeliverMessage: %v", err)
	}

	rec := &recordingNotifier{}
	ms.SetNotifier(rec)
	return ms, rec, uid
}

func TestFlagMutationsNotify(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(ms *MailboxStore, uid uint32) error
		wantFlags uint32
	}{
		{
			name: "SetFlags publishes the replacement value",
			mutate: func(ms *MailboxStore, uid uint32) error {
				return ms.SetFlags("u1", "INBOX", uid, FlagSeen|FlagFlagged)
			},
			wantFlags: FlagSeen | FlagFlagged,
		},
		{
			name: "AddFlags publishes the merged value, not the delta",
			mutate: func(ms *MailboxStore, uid uint32) error {
				if err := ms.SetFlags("u1", "INBOX", uid, FlagFlagged); err != nil {
					return err
				}
				return ms.AddFlags("u1", "INBOX", uid, FlagSeen)
			},
			wantFlags: FlagFlagged | FlagSeen,
		},
		{
			name: "RemoveFlags publishes what is left, not what went",
			mutate: func(ms *MailboxStore, uid uint32) error {
				if err := ms.SetFlags("u1", "INBOX", uid, FlagSeen|FlagFlagged); err != nil {
					return err
				}
				return ms.RemoveFlags("u1", "INBOX", uid, FlagSeen)
			},
			wantFlags: FlagFlagged,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ms, rec, uid := newNotifyTestEnv(t)
			if err := tt.mutate(ms, uid); err != nil {
				t.Fatalf("mutate: %v", err)
			}

			events := rec.drain()
			if len(events) == 0 {
				t.Fatal("no notification published; every consumer branch stays dead")
			}
			last := events[len(events)-1]
			if last.kind != "flagchange" {
				t.Fatalf("last event kind = %q, want flagchange", last.kind)
			}
			if last.mailbox != "INBOX" || last.uid != uid {
				t.Errorf("event addressed %s/%d, want INBOX/%d", last.mailbox, last.uid, uid)
			}
			// The absolute value matters: a consumer renders these flags
			// directly, so publishing the argument rather than the result
			// would show a message as unread that the store has just marked
			// read.
			if last.flags != tt.wantFlags {
				t.Errorf("event carried flags %#b, want %#b", last.flags, tt.wantFlags)
			}
		})
	}
}

func TestExpungeNotifiesEveryRemovedUID(t *testing.T) {
	ms, rec, first := newNotifyTestEnv(t)
	second, err := ms.DeliverMessage("u1", "INBOX", &bongov1.MailMeta{Guid: "guid-2", ReceivedAt: 2}, 0)
	if err != nil {
		t.Fatalf("DeliverMessage: %v", err)
	}
	third, err := ms.DeliverMessage("u1", "INBOX", &bongov1.MailMeta{Guid: "guid-3", ReceivedAt: 3}, 0)
	if err != nil {
		t.Fatalf("DeliverMessage: %v", err)
	}

	// Delete the outer two and keep the middle one, so a producer that
	// announced "the mailbox changed" without saying which messages went would
	// have nothing to report the difference with.
	for _, uid := range []uint32{first, third} {
		if err := ms.AddFlags("u1", "INBOX", uid, FlagDeleted); err != nil {
			t.Fatalf("AddFlags \\Deleted on %d: %v", uid, err)
		}
	}
	rec.drain() // discard the flag changes; this test is about the expunge

	if _, err := ms.Expunge("u1", "INBOX"); err != nil {
		t.Fatalf("Expunge: %v", err)
	}

	var gotUIDs []uint32
	for _, ev := range rec.drain() {
		if ev.kind != "expunge" {
			t.Errorf("Expunge published a %q event", ev.kind)
			continue
		}
		if ev.mailbox != "INBOX" {
			t.Errorf("expunge event named mailbox %q, want INBOX", ev.mailbox)
		}
		gotUIDs = append(gotUIDs, ev.uid)
	}

	if len(gotUIDs) != 2 || gotUIDs[0] != first || gotUIDs[1] != third {
		t.Fatalf("expunge events carried UIDs %v, want [%d %d]", gotUIDs, first, third)
	}
	if second == first || second == third {
		t.Fatal("test setup is wrong: the surviving UID collides with a removed one")
	}
}

func TestMoveNotifiesSourceRemovalAndDestinationArrival(t *testing.T) {
	ms, rec, uid := newNotifyTestEnv(t)

	if err := ms.MoveMessage("u1", "INBOX", uid, "Archive"); err != nil {
		t.Fatalf("MoveMessage: %v", err)
	}

	events := rec.drain()
	if len(events) != 2 {
		t.Fatalf("MoveMessage published %d events, want 2 (source removal and "+
			"destination arrival): %+v", len(events), events)
	}
	if events[0].kind != "expunge" || events[0].mailbox != "INBOX" || events[0].uid != uid {
		t.Errorf("first event = %+v, want an expunge of INBOX/%d", events[0], uid)
	}
	// A watcher of Archive has no way to learn about this message otherwise:
	// the move writes no delivery record, so nothing else announces it.
	if events[1].kind != "newmail" || events[1].mailbox != "Archive" {
		t.Errorf("second event = %+v, want a newmail in Archive", events[1])
	}
	// Archive was empty, so the move allocated its first UID. The SSE stream
	// looks the message up by this UID to put a sender and subject on the
	// desktop notification, so an off-by-one here shows the user a blank one.
	dst, err := ms.ListMessages("u1", "Archive")
	if err != nil {
		t.Fatalf("ListMessages Archive: %v", err)
	}
	if len(dst) != 1 {
		t.Fatalf("Archive holds %d messages after the move, want 1", len(dst))
	}
	if events[1].uid != dst[0].Uid {
		t.Errorf("arrival announced UID %d, but the message landed as UID %d",
			events[1].uid, dst[0].Uid)
	}
}

// TestFailedWriteDoesNotNotify keeps the announcement downstream of the write.
// A notification says a change happened; publishing one for a write that Raft
// rejected tells every client a message moved or vanished when it did not, and
// they have no way to find out otherwise.
func TestFailedWriteDoesNotNotify(t *testing.T) {
	ms, rec, uid := newNotifyTestEnv(t)

	// Mark the message \Deleted while writes still work. Without this Expunge
	// finds nothing to remove and returns before it ever reaches the applier,
	// so the case below would be vacuous.
	if err := ms.AddFlags("u1", "INBOX", uid, FlagDeleted); err != nil {
		t.Fatalf("AddFlags \\Deleted: %v", err)
	}
	rec.drain()

	ms.applier = refuseAllApplier{}

	if err := ms.AddFlags("u1", "INBOX", uid, FlagSeen); err == nil {
		t.Fatal("AddFlags succeeded against a refusing applier")
	}
	if err := ms.MoveMessage("u1", "INBOX", uid, "Archive"); err == nil {
		t.Fatal("MoveMessage succeeded against a refusing applier")
	}
	if err := ms.SetFlags("u1", "INBOX", uid, FlagSeen); err == nil {
		t.Fatal("SetFlags succeeded against a refusing applier")
	}
	if err := ms.RemoveFlags("u1", "INBOX", uid, FlagDeleted); err == nil {
		t.Fatal("RemoveFlags succeeded against a refusing applier")
	}
	if _, err := ms.Expunge("u1", "INBOX"); err == nil {
		t.Fatal("Expunge succeeded against a refusing applier")
	}

	if events := rec.drain(); len(events) != 0 {
		t.Errorf("failed writes published %+v, want nothing: the message is still "+
			"there, and a client told otherwise has no way to find out", events)
	}
}

// refuseAllApplier rejects every command, standing in for a Raft write that
// could not reach a quorum. It is distinct from failingApplier above, which
// refuses one specific key so a partial failure can be observed.
type refuseAllApplier struct{}

func (refuseAllApplier) ApplyCommand(*bongov1.RaftCommand, time.Duration) error {
	return errApplyRefused
}

var errApplyRefused = errors.New("apply refused")

// TestMutationsWithoutNotifierStillSucceed pins the graceful degradation. Edge
// nodes and read-only tooling build a MailboxStore with no hub attached, and a
// missing notifier must be silence rather than a panic or a failed write.
func TestMutationsWithoutNotifierStillSucceed(t *testing.T) {
	ms := newMailboxTestEnv(t)
	if err := ms.CreateMailbox("u1", "Archive"); err != nil {
		t.Fatalf("CreateMailbox Archive: %v", err)
	}
	uid, err := ms.DeliverMessage("u1", "INBOX", &bongov1.MailMeta{Guid: "guid-1", ReceivedAt: 1}, 0)
	if err != nil {
		t.Fatalf("DeliverMessage: %v", err)
	}

	if err := ms.AddFlags("u1", "INBOX", uid, FlagSeen); err != nil {
		t.Fatalf("AddFlags: %v", err)
	}
	if err := ms.MoveMessage("u1", "INBOX", uid, "Archive"); err != nil {
		t.Fatalf("MoveMessage: %v", err)
	}
	msgs, err := ms.ListMessages("u1", "Archive")
	if err != nil {
		t.Fatalf("ListMessages Archive: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("Archive holds %d messages after the move, want 1", len(msgs))
	}
}
