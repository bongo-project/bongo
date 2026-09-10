package imap

import (
	"bytes"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/bongo-project/bongo/internal/store"
)

// These tests all run two connections to one real server. That is deliberate:
// the whole point of a notification is that a change made on one connection
// reaches another, and a test that reaches into a session to call an internal
// helper would pass against an implementation that never puts a byte on the
// wire.

// waitFor polls cond until it holds or the deadline passes. Notifications
// travel through a channel and a second connection's write loop, so there is
// no synchronous moment at which to assert.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// wireLog captures the bytes a client exchanges with the server.
//
// It exists because the client library cannot report every response: an
// ExpungeCommand delivers sequence numbers over a channel whose zero value
// doubles as "no more" (imapclient/expunge.go), so Collect stops at the first
// "* 0 EXPUNGE" and a caller sees nothing at all. Counting untagged responses
// on the wire is the only assertion that cannot be fooled by that.
type wireLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *wireLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *wireLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// untaggedExpunges matches the server's untagged EXPUNGE responses only. The
// client's own "aN EXPUNGE" command line is tagged, so it does not match.
var untaggedExpunges = regexp.MustCompile(`(?m)^\* \d+ EXPUNGE`)

// countUntaggedExpunges reports how many untagged EXPUNGE responses the
// connection has been sent.
func countUntaggedExpunges(l *wireLog) int {
	return len(untaggedExpunges.FindAllString(l.String(), -1))
}

// dialLoggedClient is dialPollTestClient with the raw exchange recorded.
func dialLoggedClient(t *testing.T, addr string) (*imapclient.Client, *wireLog) {
	t.Helper()

	log := &wireLog{}
	c, err := imapclient.DialInsecure(addr, &imapclient.Options{DebugWriter: log})
	if err != nil {
		t.Fatalf("DialInsecure(%s): %v", addr, err)
	}
	t.Cleanup(func() { _ = c.Close() })

	if err := c.Login(pollTestUsername, pollTestPassword).Wait(); err != nil {
		t.Fatalf("Login: %v", err)
	}
	return c, log
}

// hasFlag reports whether flags contains want.
func hasIMAPFlag(flags []imap.Flag, want imap.Flag) bool {
	for _, f := range flags {
		if f == want {
			return true
		}
	}
	return false
}

// deleteAndExpungeByUID is the two-step an IMAP client performs to remove a
// message: mark it \Deleted, then EXPUNGE. Driven over the wire from the
// connection given, so the server-side path is the real one.
func deleteAndExpungeByUID(t *testing.T, c *imapclient.Client, uid uint32) {
	t.Helper()

	if _, err := c.Store(imap.UIDSetNum(imap.UID(uid)), &imap.StoreFlags{
		Op:     imap.StoreFlagsAdd,
		Flags:  []imap.Flag{imap.FlagDeleted},
		Silent: true,
	}, nil).Collect(); err != nil {
		t.Fatalf("STORE \\Deleted on uid %d: %v", uid, err)
	}
	if _, err := c.Expunge().Collect(); err != nil {
		t.Fatalf("EXPUNGE after marking uid %d: %v", uid, err)
	}
}

// divergedIdler is the shared starting position for the translation tests: a
// mailbox of three messages, an observer that SELECTed all three, and a second
// connection that has since removed the first one WITHOUT the observer being
// told.
//
// The divergence is the entire point. Once the observer's view and the
// mailbox's real contents disagree, a sequence number computed from the
// mailbox addresses a different message than the same number computed from the
// observer's view, so a test built on this setup can tell the two apart. A
// setup where the two views agree cannot, and would pass against a producer
// that stamps a sequence number of its own -- which is the bug this guards.
//
// Returns the observer (already IDLEing), its recorder, and the actor.
func divergedIdler(t *testing.T) (*mailboxUpdates, *imapclient.IdleCommand, *imapclient.Client) {
	t.Helper()

	ms, ps := newPollTestStore(t, "INBOX")
	for _, guid := range []string{"guid-1", "guid-2", "guid-3"} {
		deliverPollTestMessage(t, ms, "INBOX", guid)
	}
	addr := startPollTestServer(t, ms, ps)

	observerUpdates := &mailboxUpdates{}
	observer := dialPollTestClient(t, addr, observerUpdates)
	selectData, err := observer.Select("INBOX", nil).Wait()
	if err != nil {
		t.Fatalf("observer SELECT INBOX: %v", err)
	}
	if selectData.NumMessages != 3 {
		t.Fatalf("observer SELECT reported %d messages, want 3", selectData.NumMessages)
	}

	actor := dialPollTestClient(t, addr, nil)
	if _, err := actor.Select("INBOX", nil).Wait(); err != nil {
		t.Fatalf("actor SELECT INBOX: %v", err)
	}

	// The observer is not idling and issues no command, so it is told nothing
	// about this removal and keeps believing the mailbox holds uids 1, 2, 3.
	// The mailbox really holds uids 2 and 3.
	deleteAndExpungeByUID(t, actor, 1)

	if got := observerUpdates.expunges(); len(got) != 0 {
		t.Fatalf("observer saw EXPUNGE %v before idling; the views have not "+
			"diverged and the test cannot distinguish the numberings", got)
	}

	idle, err := observer.Idle()
	if err != nil {
		t.Fatalf("observer IDLE: %v", err)
	}
	t.Cleanup(func() { _ = idle.Close() })

	return observerUpdates, idle, actor
}

// TestIdlingClientLearnsOfExpungeInItsOwnNumbering is the headline bug: delete
// a message in one client and another client idling on the same folder is
// never told, so it keeps showing a message that no longer exists.
//
// It also pins WHICH sequence number is correct. The observer believes the
// mailbox is [uid1 uid2 uid3], so uid3 is its message 3. The mailbox really
// holds [uid2 uid3], where uid3 is message 2. A producer that computes a
// sequence number from the mailbox sends 2, the observer drops uid2, and the
// message the user was reading disappears while the deleted one stays.
func TestIdlingClientLearnsOfExpungeInItsOwnNumbering(t *testing.T) {
	updates, idle, actor := divergedIdler(t)

	deleteAndExpungeByUID(t, actor, 3)

	waitFor(t, "the idling client to be sent an EXPUNGE", func() bool {
		return len(updates.expunges()) > 0
	})

	// Snapshot before ending the IDLE: the Poll that follows DONE legitimately
	// reports the FIRST removal, which the observer has still not been told
	// about, and folding that in would hide which number the IDLE delivered.
	got := updates.expunges()
	if err := idle.Close(); err != nil {
		t.Fatalf("ending IDLE: %v", err)
	}

	assertSeqNums(t, got, []uint32{3}, "EXPUNGE delivered during IDLE")
}

// storeSeen issues a STORE of \Seen with op against uid on c.
func storeSeen(t *testing.T, c *imapclient.Client, op imap.StoreFlagsOp, uid uint32) {
	t.Helper()

	if _, err := c.Store(imap.UIDSetNum(imap.UID(uid)), &imap.StoreFlags{
		Op:    op,
		Flags: []imap.Flag{imap.FlagSeen},
		// Silent, so the only untagged FETCH the observer can see is the one
		// the notification produced rather than an echo of the actor's own
		// command.
		Silent: true,
	}, nil).Collect(); err != nil {
		t.Fatalf("STORE (op %v) \\Seen on uid %d: %v", op, uid, err)
	}
}

// TestIdlingClientLearnsOfFlagChange is the "unread counts do not decrease"
// complaint: mark a message read anywhere and nothing else learns, so counts
// diverge between clients.
//
// One case per STORE op, because each lands on a different store method
// (SetFlags, AddFlags, RemoveFlags) and a producer missing from any one of
// them leaves that path silent.
//
// The numbering is diverged for the same reason as the expunge test -- an
// untagged FETCH addresses a message by sequence number too, so a
// producer-computed one relabels another message's flags onto the wrong row.
func TestIdlingClientLearnsOfFlagChange(t *testing.T) {
	tests := []struct {
		name string
		// prep runs BEFORE the assertion and is itself expected to produce an
		// update, so wantUpdates counts it.
		prep        func(t *testing.T, actor *imapclient.Client)
		op          imap.StoreFlagsOp
		wantUpdates int
		wantSeen    bool
	}{
		{
			name:        "replace reaches the observer",
			op:          imap.StoreFlagsSet,
			wantUpdates: 1,
			wantSeen:    true,
		},
		{
			name:        "add reaches the observer",
			op:          imap.StoreFlagsAdd,
			wantUpdates: 1,
			wantSeen:    true,
		},
		{
			// Marking unread again is the case an operator notices as a count
			// that never goes back up.
			name: "remove reaches the observer",
			prep: func(t *testing.T, actor *imapclient.Client) {
				storeSeen(t, actor, imap.StoreFlagsAdd, 3)
			},
			op:          imap.StoreFlagsDel,
			wantUpdates: 2,
			wantSeen:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			updates, idle, actor := divergedIdler(t)

			if tt.prep != nil {
				tt.prep(t, actor)
				waitFor(t, "the observer to be sent the setup flag update", func() bool {
					return len(updates.flags()) == tt.wantUpdates-1
				})
			}

			storeSeen(t, actor, tt.op, 3)

			waitFor(t, "the idling client to be sent a flag update", func() bool {
				return len(updates.flags()) >= tt.wantUpdates
			})

			got := updates.flags()
			if err := idle.Close(); err != nil {
				t.Fatalf("ending IDLE: %v", err)
			}

			if len(got) != tt.wantUpdates {
				t.Fatalf("idling client received %d flag updates, want %d: %+v",
					len(got), tt.wantUpdates, got)
			}
			last := got[len(got)-1]
			if last.seqNum != 3 {
				t.Errorf("flag update addressed sequence number %d, want 3: the observer "+
					"believes the mailbox is [1 2 3] and uid 3 is its third message, "+
					"while the mailbox itself now holds only two", last.seqNum)
			}
			if last.uid != 3 {
				t.Errorf("flag update carried UID %d, want 3", last.uid)
			}
			if hasIMAPFlag(last.flags, imap.FlagSeen) != tt.wantSeen {
				t.Errorf("flag update carried flags %v, want \\Seen present = %v",
					last.flags, tt.wantSeen)
			}
		})
	}
}

// TestExpungeNotifiesOtherClientsButNotTheActorTwice is the guard against the
// fix being worse than the bug.
//
// The acting connection writes its own untagged EXPUNGEs inside the EXPUNGE
// command and renumbers its baseline as it does so. If the store's
// notification also reached it, it would apply a second EXPUNGE for a sequence
// number it has already consumed and drop a message it should have kept. So
// the actor must see exactly one, and the observer -- which was told nothing
// until now -- must see exactly one too.
func TestExpungeNotifiesOtherClientsButNotTheActorTwice(t *testing.T) {
	ms, ps := newPollTestStore(t, "INBOX")
	for _, guid := range []string{"guid-1", "guid-2"} {
		deliverPollTestMessage(t, ms, "INBOX", guid)
	}
	addr := startPollTestServer(t, ms, ps)

	observerUpdates := &mailboxUpdates{}
	observer := dialPollTestClient(t, addr, observerUpdates)
	if _, err := observer.Select("INBOX", nil).Wait(); err != nil {
		t.Fatalf("observer SELECT: %v", err)
	}
	idle, err := observer.Idle()
	if err != nil {
		t.Fatalf("observer IDLE: %v", err)
	}

	// The acting connection is read off the wire rather than through the
	// client's parsed responses, so a duplicate cannot hide behind the
	// library's zero-means-done sentinel.
	actor, actorWire := dialLoggedClient(t, addr)
	if _, err := actor.Select("INBOX", nil).Wait(); err != nil {
		t.Fatalf("actor SELECT: %v", err)
	}

	if _, err := actor.Store(imap.UIDSetNum(1), &imap.StoreFlags{
		Op:     imap.StoreFlagsAdd,
		Flags:  []imap.Flag{imap.FlagDeleted},
		Silent: true,
	}, nil).Collect(); err != nil {
		t.Fatalf("actor STORE \\Deleted: %v", err)
	}
	if _, err := actor.Expunge().Collect(); err != nil {
		t.Fatalf("actor EXPUNGE: %v", err)
	}

	waitFor(t, "the observer to be sent an EXPUNGE", func() bool {
		return len(observerUpdates.expunges()) > 0
	})

	// Give a duplicate time to arrive rather than asserting the instant the
	// first one lands: a second EXPUNGE that is merely slower than the first
	// would otherwise slip past. The NOOP also drives a Poll, which is the
	// other way a removal gets re-reported.
	if err := actor.Noop().Wait(); err != nil {
		t.Fatalf("actor NOOP: %v", err)
	}

	observed := observerUpdates.expunges()
	if err := idle.Close(); err != nil {
		t.Fatalf("ending IDLE: %v", err)
	}

	assertSeqNums(t, observed, []uint32{1}, "EXPUNGE delivered to the idling observer")
	if got := countUntaggedExpunges(actorWire); got != 1 {
		t.Errorf("acting client was sent %d untagged EXPUNGE responses, want 1; "+
			"it renumbers on the first, so a second drops a message it should "+
			"have kept.\nwire:\n%s", got, actorWire.String())
	}
}

// TestMoveNotifiesBothMailboxes covers the two-sided event. A move removes a
// message here and creates one there, and a client watching only the
// destination sees nothing at all unless the arrival is announced separately.
func TestMoveNotifiesBothMailboxes(t *testing.T) {
	ms, ps := newPollTestStore(t, "INBOX")
	if err := ms.CreateMailbox(pollTestUser, "Archive"); err != nil {
		t.Fatalf("CreateMailbox Archive: %v", err)
	}
	deliverPollTestMessage(t, ms, "INBOX", "guid-1")
	addr := startPollTestServer(t, ms, ps)

	srcUpdates := &mailboxUpdates{}
	srcWatcher := dialPollTestClient(t, addr, srcUpdates)
	if _, err := srcWatcher.Select("INBOX", nil).Wait(); err != nil {
		t.Fatalf("source watcher SELECT INBOX: %v", err)
	}
	srcIdle, err := srcWatcher.Idle()
	if err != nil {
		t.Fatalf("source watcher IDLE: %v", err)
	}

	dstUpdates := &mailboxUpdates{}
	dstWatcher := dialPollTestClient(t, addr, dstUpdates)
	dstSelect, err := dstWatcher.Select("Archive", nil).Wait()
	if err != nil {
		t.Fatalf("destination watcher SELECT Archive: %v", err)
	}
	if dstSelect.NumMessages != 0 {
		t.Fatalf("Archive holds %d messages at SELECT, want 0", dstSelect.NumMessages)
	}
	dstIdle, err := dstWatcher.Idle()
	if err != nil {
		t.Fatalf("destination watcher IDLE: %v", err)
	}

	actor := dialPollTestClient(t, addr, nil)
	if _, err := actor.Select("INBOX", nil).Wait(); err != nil {
		t.Fatalf("actor SELECT INBOX: %v", err)
	}
	if _, err := actor.Move(imap.UIDSetNum(1), "Archive").Wait(); err != nil {
		t.Fatalf("actor MOVE: %v", err)
	}

	waitFor(t, "the source watcher to be sent an EXPUNGE", func() bool {
		return len(srcUpdates.expunges()) > 0
	})
	waitFor(t, "the destination watcher to be sent an EXISTS", func() bool {
		return len(dstUpdates.exists()) > 0
	})

	srcSeen := srcUpdates.expunges()
	dstSeen := dstUpdates.exists()
	if err := srcIdle.Close(); err != nil {
		t.Fatalf("ending source IDLE: %v", err)
	}
	if err := dstIdle.Close(); err != nil {
		t.Fatalf("ending destination IDLE: %v", err)
	}

	assertSeqNums(t, srcSeen, []uint32{1}, "EXPUNGE delivered to the source watcher")
	assertSeqNums(t, dstSeen, []uint32{1}, "EXISTS delivered to the destination watcher")
}

// TestNotificationForUnknownMessageIsSkipped pins the "sequence number 0"
// answer. A client that has never been told a message exists cannot be sent an
// EXPUNGE or a FETCH for it -- there is no number that addresses it -- and
// reportedSeqNum returns 0 to say so. Writing that zero would be a protocol
// error; the branch must drop the notification instead.
//
// This is the delivery-side idempotence that also stops a repeated
// notification renumbering a client twice for one removal, since dropReported
// takes the UID out of the baseline the first time.
func TestNotificationForUnknownMessageIsSkipped(t *testing.T) {
	ms, ps := newPollTestStore(t, "INBOX")
	addr := startPollTestServer(t, ms, ps)

	updates := &mailboxUpdates{}
	c := dialPollTestClient(t, addr, updates)
	selectData, err := c.Select("INBOX", nil).Wait()
	if err != nil {
		t.Fatalf("SELECT INBOX: %v", err)
	}
	if selectData.NumMessages != 0 {
		t.Fatalf("SELECT reported %d messages, want 0", selectData.NumMessages)
	}
	idle, err := c.Idle()
	if err != nil {
		t.Fatalf("IDLE: %v", err)
	}

	// Delivered through a second store over the same Pebble with no hub
	// attached, so the arrival announces itself to nobody and the message lands
	// without the idling client hearing about it. That is precisely the state
	// the guard is for: the client's view is empty, so no sequence number
	// addresses this message.
	//
	// The detour is necessary because the apply itself now announces arrivals
	// on every node. Delivering through ms -- which the server shares -- would
	// send this client an EXISTS, and a client that has been told the mailbox
	// grew is no longer a client that was never shown the message.
	silent := newDirectMailboxStore(ps)
	uid := deliverPollTestMessage(t, silent, "INBOX", "guid-1")

	if err := ms.SetFlags(pollTestUser, "INBOX", uid, store.FlagDeleted); err != nil {
		t.Fatalf("SetFlags \\Deleted: %v", err)
	}
	if _, err := ms.Expunge(pollTestUser, "INBOX"); err != nil {
		t.Fatalf("Expunge: %v", err)
	}

	// There is nothing to wait for, so let the wrong behaviour have a window
	// in which to show up.
	time.Sleep(200 * time.Millisecond)

	gotExpunges := updates.expunges()
	gotFlags := updates.flags()
	if err := idle.Close(); err != nil {
		t.Fatalf("ending IDLE: %v", err)
	}
	if len(gotExpunges) != 0 {
		t.Errorf("client was sent EXPUNGE %v for a message it was never shown; "+
			"the only sequence number available is 0, which addresses nothing", gotExpunges)
	}
	if len(gotFlags) != 0 {
		t.Errorf("client was sent flag updates %+v for a message it was never shown", gotFlags)
	}
}
