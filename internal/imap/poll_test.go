package imap

import (
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const pollTestUser = "u1"

// errExpungeNotAllowed mirrors the refusal *imapserver.UpdateWriter returns
// when the command in progress forbids expunges (imapserver/conn.go).
var errExpungeNotAllowed = errors.New("EXPUNGE updates are not allowed in this context")

// recordingUpdateWriter stands in for *imapserver.UpdateWriter, which has only
// unexported fields and so cannot be constructed outside the library. It
// reproduces the one behaviour that matters here: WriteExpunge fails when the
// caller passed allowExpunge=false, so a Poll that emits an expunge it should
// have withheld is caught by the returned error as well as by the recording.
type recordingUpdateWriter struct {
	allowExpunge bool
	numMessages  []uint32
	expunged     []uint32
}

func (w *recordingUpdateWriter) WriteNumMessages(n uint32) error {
	w.numMessages = append(w.numMessages, n)
	return nil
}

func (w *recordingUpdateWriter) WriteExpunge(seqNum uint32) error {
	w.expunged = append(w.expunged, seqNum)
	if !w.allowExpunge {
		return errExpungeNotAllowed
	}
	return nil
}

func (w *recordingUpdateWriter) silent() bool {
	return len(w.numMessages) == 0 && len(w.expunged) == 0
}

// newPollTestStore builds a MailboxStore over a fresh Pebble holding one empty
// mailbox. It is shared by every session in a test, which is what makes the
// sessions independent connections to the same server rather than copies.
func newPollTestStore(t *testing.T, mailbox string) *store.MailboxStore {
	t.Helper()

	ps, err := store.NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })

	ms := store.NewMailboxStore(ps, &imapDirectApplier{store: ps})
	if err := ms.CreateMailbox(pollTestUser, mailbox); err != nil {
		t.Fatalf("CreateMailbox(%q): %v", mailbox, err)
	}
	return ms
}

// newPollTestSession opens mailbox through the real Select, so the cached
// message list a Poll diffs against is primed exactly as a client's SELECT
// primes it.
func newPollTestSession(t *testing.T, ms *store.MailboxStore, mailbox string) *session {
	t.Helper()

	s := &session{
		mailboxes: ms,
		logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		userID:    pollTestUser,
	}
	if _, err := s.Select(mailbox, nil); err != nil {
		t.Fatalf("Select(%q): %v", mailbox, err)
	}
	return s
}

// deliverPollTestMessage writes a message into mailbox the way SMTP delivery
// or an APPEND from another connection does: through the shared store, with no
// reference to the session that will later poll for it.
func deliverPollTestMessage(t *testing.T, ms *store.MailboxStore, mailbox, guid string) uint32 {
	t.Helper()

	uid, err := ms.DeliverMessage(pollTestUser, mailbox, &bongov1.MailMeta{
		Guid:       guid,
		Subject:    guid,
		ReceivedAt: time.Now().Unix(),
	}, 0)
	if err != nil {
		t.Fatalf("DeliverMessage(%q): %v", guid, err)
	}
	return uid
}

// expungePollTestMessage removes a message from mailbox, flagging it \Deleted
// first because that is what MailboxStore.Expunge selects on.
func expungePollTestMessage(t *testing.T, ms *store.MailboxStore, mailbox string, uid uint32) {
	t.Helper()

	if err := ms.AddFlags(pollTestUser, mailbox, uid, store.FlagDeleted); err != nil {
		t.Fatalf("AddFlags(uid %d): %v", uid, err)
	}
	if _, err := ms.Expunge(pollTestUser, mailbox); err != nil {
		t.Fatalf("Expunge(uid %d): %v", uid, err)
	}
}

func assertSeqNums(t *testing.T, got, want []uint32, what string) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s = %v, want %v", what, got, want)
		}
	}
}

// TestPollReportsMessageAppendedByAnotherConnection is the production failure,
// observed on 2026-09-06: a message appended to a user's Sent folder was stored
// correctly -- MailMeta, mailbox entry, index all present -- but Thunderbird
// never displayed it until a restart forced a fresh SELECT. Thunderbird IDLEs
// on INBOX and NOOP-polls every other folder, and go-imap delivers a NOOP's
// untagged EXISTS through Session.Poll, which was a stub returning nil.
func TestPollReportsMessageAppendedByAnotherConnection(t *testing.T) {
	ms := newPollTestStore(t, "Sent")

	// The polling client selects Sent while it is still empty.
	poller := newPollTestSession(t, ms, "Sent")
	if len(poller.cachedMsgs) != 0 {
		t.Fatalf("Sent holds %d messages at SELECT, want 0", len(poller.cachedMsgs))
	}

	// Another connection appends. This session never touches the poller.
	appender := newPollTestSession(t, ms, "Sent")
	deliverPollTestMessage(t, ms, "Sent", "guid-1")
	if len(appender.cachedMsgs) != 0 {
		t.Fatalf("the appending session's cache changed on its own, test setup is wrong")
	}

	w := &recordingUpdateWriter{allowExpunge: true}
	if err := poller.pollUpdates(w, true); err != nil {
		t.Fatalf("pollUpdates: %v", err)
	}

	assertSeqNums(t, w.numMessages, []uint32{1}, "EXISTS responses")
	if len(w.expunged) != 0 {
		t.Errorf("EXPUNGE responses = %v, want none", w.expunged)
	}
}

// TestPollWithNoMailboxSelectedIsSilent covers the authenticated-but-not-
// selected state, which go-imap also polls. mailboxes is left nil so that a
// missing guard dereferences it: a Poll that reaches the store here is a bug
// even if the store would have answered harmlessly.
func TestPollWithNoMailboxSelectedIsSilent(t *testing.T) {
	s := &session{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		userID: pollTestUser,
	}

	w := &recordingUpdateWriter{allowExpunge: true}
	err := callAndRecover(func() error { return s.pollUpdates(w, true) })
	if err != nil {
		t.Fatalf("pollUpdates with no mailbox selected: %v", err)
	}
	if !w.silent() {
		t.Errorf("reported EXISTS %v and EXPUNGE %v with no mailbox selected, want silence",
			w.numMessages, w.expunged)
	}
}

// TestPollRepeatedWithNoChangesIsSilent pins the cache refresh. Without it the
// second poll re-diffs against the message list from SELECT and repeats the
// EXISTS, which clients read as further new mail.
func TestPollRepeatedWithNoChangesIsSilent(t *testing.T) {
	ms := newPollTestStore(t, "Sent")
	s := newPollTestSession(t, ms, "Sent")
	deliverPollTestMessage(t, ms, "Sent", "guid-1")

	first := &recordingUpdateWriter{allowExpunge: true}
	if err := s.pollUpdates(first, true); err != nil {
		t.Fatalf("first pollUpdates: %v", err)
	}
	assertSeqNums(t, first.numMessages, []uint32{1}, "first poll EXISTS responses")

	second := &recordingUpdateWriter{allowExpunge: true}
	if err := s.pollUpdates(second, true); err != nil {
		t.Fatalf("second pollUpdates: %v", err)
	}
	if !second.silent() {
		t.Errorf("second poll reported EXISTS %v and EXPUNGE %v with nothing changed, want silence",
			second.numMessages, second.expunged)
	}
}

// TestPollWritesExpungesHighestSequenceNumberFirst pins the ordering. Each
// EXPUNGE the client applies shifts every higher sequence number down by one,
// so descending order can be applied verbatim; ascending order would leave the
// later numbers in the batch pointing one message too high, and the client
// would drop the wrong message.
//
// The trailing silent poll pins the cache refresh on this path too.
func TestPollWritesExpungesHighestSequenceNumberFirst(t *testing.T) {
	ms := newPollTestStore(t, "INBOX")
	for _, guid := range []string{"guid-1", "guid-2", "guid-3", "guid-4"} {
		deliverPollTestMessage(t, ms, "INBOX", guid)
	}

	s := newPollTestSession(t, ms, "INBOX")
	if len(s.cachedMsgs) != 4 {
		t.Fatalf("INBOX holds %d messages at SELECT, want 4", len(s.cachedMsgs))
	}
	// Sequence numbers below are positions in this list, so pin the mapping.
	for i, want := range []uint32{1, 2, 3, 4} {
		if s.cachedMsgs[i].Uid != want {
			t.Fatalf("cachedMsgs[%d].Uid = %d, want %d", i, s.cachedMsgs[i].Uid, want)
		}
	}

	// Remove sequence numbers 1 and 3, leaving a gap between them so an
	// ascending emission is distinguishable from a descending one.
	expungePollTestMessage(t, ms, "INBOX", 1)
	expungePollTestMessage(t, ms, "INBOX", 3)

	w := &recordingUpdateWriter{allowExpunge: true}
	if err := s.pollUpdates(w, true); err != nil {
		t.Fatalf("pollUpdates: %v", err)
	}
	assertSeqNums(t, w.expunged, []uint32{3, 1}, "EXPUNGE responses")

	// Two of four are gone, so the client's own count already reads 2. A
	// further EXISTS would be noise.
	if len(w.numMessages) != 0 {
		t.Errorf("EXISTS responses = %v, want none: the expunges already left the "+
			"client on the right count", w.numMessages)
	}

	again := &recordingUpdateWriter{allowExpunge: true}
	if err := s.pollUpdates(again, true); err != nil {
		t.Fatalf("second pollUpdates: %v", err)
	}
	if !again.silent() {
		t.Errorf("second poll reported EXISTS %v and EXPUNGE %v, want silence: the "+
			"expunges were already reported", again.numMessages, again.expunged)
	}
}

// TestPollReportsBothExpungesAndArrivals covers the mixed case, where the
// remaining count differs from what the expunges alone imply.
func TestPollReportsBothExpungesAndArrivals(t *testing.T) {
	ms := newPollTestStore(t, "INBOX")
	for _, guid := range []string{"guid-1", "guid-2"} {
		deliverPollTestMessage(t, ms, "INBOX", guid)
	}

	s := newPollTestSession(t, ms, "INBOX")
	expungePollTestMessage(t, ms, "INBOX", 1)
	deliverPollTestMessage(t, ms, "INBOX", "guid-3")
	deliverPollTestMessage(t, ms, "INBOX", "guid-4")

	w := &recordingUpdateWriter{allowExpunge: true}
	if err := s.pollUpdates(w, true); err != nil {
		t.Fatalf("pollUpdates: %v", err)
	}
	assertSeqNums(t, w.expunged, []uint32{1}, "EXPUNGE responses")
	// One of two gone leaves the client on 1; the mailbox now holds 3.
	assertSeqNums(t, w.numMessages, []uint32{3}, "EXISTS responses")
}

// TestPollWithoutExpungePermissionWithholdsExpunges covers FETCH, STORE and
// SEARCH, which go-imap polls with allowExpunge=false. RFC 3501 7.4.1 forbids
// renumbering while the client is still resolving the message set it named, and
// the library's UpdateWriter refuses the write outright.
func TestPollWithoutExpungePermissionWithholdsExpunges(t *testing.T) {
	ms := newPollTestStore(t, "INBOX")
	for _, guid := range []string{"guid-1", "guid-2"} {
		deliverPollTestMessage(t, ms, "INBOX", guid)
	}

	s := newPollTestSession(t, ms, "INBOX")
	expungePollTestMessage(t, ms, "INBOX", 1)

	w := &recordingUpdateWriter{allowExpunge: false}
	if err := s.pollUpdates(w, false); err != nil {
		t.Fatalf("pollUpdates: %v", err)
	}
	if len(w.expunged) != 0 {
		t.Errorf("EXPUNGE responses = %v, want none: FETCH, STORE and SEARCH forbid them",
			w.expunged)
	}
	// An EXISTS here would describe a mailbox whose removals the client has not
	// been told about, leaving it with a count it cannot reconcile.
	if len(w.numMessages) != 0 {
		t.Errorf("EXISTS responses = %v, want none while expunges are being withheld",
			w.numMessages)
	}

	// The withheld expunge must survive to the next poll that may report it,
	// which requires the cache to have been left alone above.
	next := &recordingUpdateWriter{allowExpunge: true}
	if err := s.pollUpdates(next, true); err != nil {
		t.Fatalf("follow-up pollUpdates: %v", err)
	}
	assertSeqNums(t, next.expunged, []uint32{1}, "follow-up EXPUNGE responses")
}

// TestPollWithoutExpungePermissionStillReportsArrivals is the control for the
// test above. Withholding expunges must not turn into withholding everything:
// EXISTS is legal at any time, and suppressing it would leave a client that
// only ever issues FETCH blind to new mail.
func TestPollWithoutExpungePermissionStillReportsArrivals(t *testing.T) {
	ms := newPollTestStore(t, "INBOX")
	s := newPollTestSession(t, ms, "INBOX")
	deliverPollTestMessage(t, ms, "INBOX", "guid-1")

	w := &recordingUpdateWriter{allowExpunge: false}
	if err := s.pollUpdates(w, false); err != nil {
		t.Fatalf("pollUpdates: %v", err)
	}
	assertSeqNums(t, w.numMessages, []uint32{1}, "EXISTS responses")
	if len(w.expunged) != 0 {
		t.Errorf("EXPUNGE responses = %v, want none", w.expunged)
	}
}
