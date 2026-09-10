package cluster

import (
	"sync"
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/store"
)

// The tests here are the ones that were impossible while mailbox notifications
// were emitted by MailboxStore. That producer ran on whichever node the client
// happened to be connected to, so a change made through the leader reached
// nobody idling on a follower. Emission now happens in the apply, and the apply
// runs on every node -- which is only observable by driving an FSM that no
// MailboxStore has ever touched.

// notifyEvent is one published mailbox event.
type notifyEvent struct {
	kind    string
	userID  string
	mailbox string
	uid     uint32
	flags   uint32
	exists  uint32
}

// recordingNotifier captures what an FSM publishes. The mutex is not
// decorative: raft applies on its own goroutine.
type recordingNotifier struct {
	mu     sync.Mutex
	events []notifyEvent
}

func (r *recordingNotifier) NotifyNewMail(userID, mailbox string, uid, existsCount uint32) {
	r.record(notifyEvent{kind: "newmail", userID: userID, mailbox: mailbox, uid: uid, exists: existsCount})
}

func (r *recordingNotifier) NotifyFlagChange(userID, mailbox string, uid, flags uint32) {
	r.record(notifyEvent{kind: "flagchange", userID: userID, mailbox: mailbox, uid: uid, flags: flags})
}

func (r *recordingNotifier) NotifyExpunge(userID, mailbox string, uid uint32) {
	r.record(notifyEvent{kind: "expunge", userID: userID, mailbox: mailbox, uid: uid})
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

const notifyTestUser = "u1"

// TestFSMNotifiesFromARawLogEntry is the narrowest statement of the property:
// a committed log entry, handed straight to an FSM, announces the delivery. No
// MailboxStore exists anywhere in this test, so nothing but the apply path can
// be producing the event.
func TestFSMNotifiesFromARawLogEntry(t *testing.T) {
	env := newFSMTestEnv(t)
	rec := &recordingNotifier{}
	env.fsm.SetNotifier(rec)

	cmd, err := store.BuildDeliverMailCommand(notifyTestUser, "INBOX", &bongov1.MailMeta{
		Guid:       "guid-1",
		Subject:    "delivered by the log",
		ReceivedAt: 1,
	}, 0)
	if err != nil {
		t.Fatalf("BuildDeliverMailCommand: %v", err)
	}
	if res := env.fsm.Apply(makeProtoLog(1, cmd)); res != nil {
		t.Fatalf("Apply: %v", res)
	}

	got := rec.drain()
	if len(got) != 1 {
		t.Fatalf("applying a delivery published %d events, want 1: %+v", len(got), got)
	}
	want := notifyEvent{kind: "newmail", userID: notifyTestUser, mailbox: "INBOX", uid: 1, exists: 1}
	if got[0] != want {
		t.Errorf("published %+v, want %+v", got[0], want)
	}
}

// notifyCluster is a leader that issues mailbox commands and a second,
// independent node that applies the identical committed entries.
//
// The follower has its own Pebble and its own FSM, and no MailboxStore is ever
// pointed at it. Everything it knows arrives as a log entry, which is the whole
// claim: a client idling on a follower has to learn about a change made through
// the leader.
type notifyCluster struct {
	mailboxes *store.MailboxStore
	follower  *FSM
	events    *recordingNotifier
}

// replicatingApplier commits each command to the leader and then to the
// follower, in order, the way a quorum write reaches both.
type replicatingApplier struct {
	leader   *FSM
	follower *FSM
	index    uint64
}

func (a *replicatingApplier) ApplyCommand(cmd *bongov1.RaftCommand, _ time.Duration) error {
	a.index++
	entry := makeProtoLog(a.index, cmd)
	if err, ok := a.leader.Apply(entry).(error); ok {
		return err
	}
	if err, ok := a.follower.Apply(entry).(error); ok {
		return err
	}
	return nil
}

func newNotifyCluster(t *testing.T) *notifyCluster {
	t.Helper()

	leader := newFSMTestEnv(t)
	follower := newFSMTestEnv(t)

	rec := &recordingNotifier{}
	// Only the follower announces. An event recorded here therefore cannot have
	// come from the node the command was issued on.
	follower.fsm.SetNotifier(rec)

	applier := &replicatingApplier{leader: leader.fsm, follower: follower.fsm}
	ms := store.NewMailboxStore(leader.pebble, applier)

	for _, name := range []string{"INBOX", "Archive"} {
		if err := ms.CreateMailbox(notifyTestUser, name); err != nil {
			t.Fatalf("CreateMailbox(%q): %v", name, err)
		}
	}
	if _, err := ms.DeliverMessage(notifyTestUser, "INBOX", &bongov1.MailMeta{
		Guid:       "guid-1",
		Subject:    "seed",
		ReceivedAt: 1,
	}, 0); err != nil {
		t.Fatalf("DeliverMessage: %v", err)
	}
	rec.drain() // the seed delivery is setup, not an assertion

	return &notifyCluster{mailboxes: ms, follower: follower.fsm, events: rec}
}

// TestFollowerFSMNotifiesLocalWatchers covers every event kind a mailbox
// mutation produces. Each case asserts against the FOLLOWER's notifier, so a
// producer that ran on the acting node would leave all of them empty.
func TestFollowerFSMNotifiesLocalWatchers(t *testing.T) {
	tests := []struct {
		name string
		act  func(t *testing.T, ms *store.MailboxStore)
		want []notifyEvent
	}{
		{
			name: "a delivery announces new mail with the new count",
			act: func(t *testing.T, ms *store.MailboxStore) {
				if _, err := ms.DeliverMessage(notifyTestUser, "INBOX", &bongov1.MailMeta{
					Guid: "guid-2", ReceivedAt: 2,
				}, 0); err != nil {
					t.Fatalf("DeliverMessage: %v", err)
				}
			},
			want: []notifyEvent{
				{kind: "newmail", userID: notifyTestUser, mailbox: "INBOX", uid: 2, exists: 2},
			},
		},
		{
			name: "a flag change announces the flags that are now set",
			act: func(t *testing.T, ms *store.MailboxStore) {
				if err := ms.AddFlags(notifyTestUser, "INBOX", 1, store.FlagSeen); err != nil {
					t.Fatalf("AddFlags: %v", err)
				}
			},
			want: []notifyEvent{
				{kind: "flagchange", userID: notifyTestUser, mailbox: "INBOX", uid: 1, flags: store.FlagSeen},
			},
		},
		{
			name: "an expunge announces each removed UID",
			act: func(t *testing.T, ms *store.MailboxStore) {
				if err := ms.AddFlags(notifyTestUser, "INBOX", 1, store.FlagDeleted); err != nil {
					t.Fatalf("AddFlags \\Deleted: %v", err)
				}
				if _, err := ms.Expunge(notifyTestUser, "INBOX"); err != nil {
					t.Fatalf("Expunge: %v", err)
				}
			},
			want: []notifyEvent{
				{kind: "flagchange", userID: notifyTestUser, mailbox: "INBOX", uid: 1, flags: store.FlagDeleted},
				{kind: "expunge", userID: notifyTestUser, mailbox: "INBOX", uid: 1},
			},
		},
		{
			// A move is two events in two mailboxes. A watcher of the
			// destination has no other way to hear about the message: the move
			// writes no delivery record.
			name: "a move announces the removal and the arrival",
			act: func(t *testing.T, ms *store.MailboxStore) {
				if err := ms.MoveMessage(notifyTestUser, "INBOX", 1, "Archive"); err != nil {
					t.Fatalf("MoveMessage: %v", err)
				}
			},
			want: []notifyEvent{
				{kind: "expunge", userID: notifyTestUser, mailbox: "INBOX", uid: 1},
				{kind: "newmail", userID: notifyTestUser, mailbox: "Archive", uid: 1, exists: 1},
			},
		},
		{
			name: "a copy announces the arrival in the destination",
			act: func(t *testing.T, ms *store.MailboxStore) {
				if err := ms.CopyMessage(notifyTestUser, "INBOX", 1, "Archive"); err != nil {
					t.Fatalf("CopyMessage: %v", err)
				}
			},
			want: []notifyEvent{
				{kind: "newmail", userID: notifyTestUser, mailbox: "Archive", uid: 1, exists: 1},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newNotifyCluster(t)

			tt.act(t, c.mailboxes)

			got := c.events.drain()
			if len(got) != len(tt.want) {
				t.Fatalf("the follower published %d events, want %d: %+v", len(got), len(tt.want), got)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("event %d = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestRewritingAMessageWithoutChangingFlagsIsSilent pins the guard that keeps
// the flag branch honest. Keyword and tag updates rewrite the same
// MailboxMessage with the same flags -- IMAP STORE does it on every replace,
// and the web tag handlers write the message key directly -- so treating every
// PUT as a flag change would send an idling client an untagged FETCH that says
// nothing changed.
func TestRewritingAMessageWithoutChangingFlagsIsSilent(t *testing.T) {
	c := newNotifyCluster(t)

	if err := c.mailboxes.AddFlags(notifyTestUser, "INBOX", 1, store.FlagSeen); err != nil {
		t.Fatalf("AddFlags: %v", err)
	}
	if got := c.events.drain(); len(got) != 1 {
		t.Fatalf("the flag change published %d events, want 1: %+v", len(got), got)
	}

	// Same flags, different tags: a keyword update.
	if err := c.mailboxes.WriteMessageTags(notifyTestUser, "INBOX", 1, []string{"work"}); err != nil {
		t.Fatalf("WriteMessageTags: %v", err)
	}
	// And the case the web UI produces on every message open: \Seen re-asserted
	// on a message that already has it.
	if err := c.mailboxes.AddFlags(notifyTestUser, "INBOX", 1, store.FlagSeen); err != nil {
		t.Fatalf("AddFlags (repeat): %v", err)
	}

	if got := c.events.drain(); len(got) != 0 {
		t.Errorf("rewriting a message without changing its flags published %+v, want nothing", got)
	}
}

// TestReplayedLogEntryDoesNotRenotify answers the restart question. raft
// replays committed entries on every start, and an expunge announced a second
// time would tell a client to drop a message it should have kept. The applied
// watermark that keeps Pebble from resurrecting deleted data covers the
// notification too, because the emission is inside the same apply it skips.
func TestReplayedLogEntryDoesNotRenotify(t *testing.T) {
	env := newFSMTestEnv(t)
	rec := &recordingNotifier{}
	env.fsm.SetNotifier(rec)

	cmd, err := store.BuildDeliverMailCommand(notifyTestUser, "INBOX", &bongov1.MailMeta{
		Guid: "guid-1", ReceivedAt: 1,
	}, 0)
	if err != nil {
		t.Fatalf("BuildDeliverMailCommand: %v", err)
	}
	entry := makeProtoLog(7, cmd)
	if res := env.fsm.Apply(entry); res != nil {
		t.Fatalf("first Apply: %v", res)
	}
	if got := rec.drain(); len(got) != 1 {
		t.Fatalf("first apply published %d events, want 1: %+v", len(got), got)
	}

	// The same entry again, exactly as a restart replays it.
	if res := env.fsm.Apply(entry); res != nil {
		t.Fatalf("replayed Apply: %v", res)
	}

	if got := rec.drain(); len(got) != 0 {
		t.Errorf("replaying an already-applied entry published %+v, want nothing: a "+
			"second announcement of an arrival reads as further new mail, and a "+
			"second expunge drops a live message", got)
	}
}
