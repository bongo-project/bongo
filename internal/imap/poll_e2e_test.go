package imap

import (
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/bongo-project/bongo/internal/store"
)

const (
	pollTestUsername = "u1@localhost"
	pollTestPassword = "correct-horse"
)

// stubAuthenticator accepts exactly one credential pair. Anything else fails,
// so a test that reaches a selected mailbox really did authenticate.
type stubAuthenticator struct{}

func (stubAuthenticator) Authenticate(username, password string) (string, error) {
	if username == pollTestUsername && password == pollTestPassword {
		return pollTestUser, nil
	}
	return "", errors.New("invalid credentials")
}

// startPollTestServer runs the real IMAP server over a loopback listener. It is
// built through NewServer rather than by assembling an imapserver.Options by
// hand, so the session wiring under test is the wiring that ships.
func startPollTestServer(t *testing.T, ms *store.MailboxStore) string {
	t.Helper()

	blobs, err := store.NewBlobStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(stubAuthenticator{}, nil, blobs, ms, nil, nil, nil, NewNotifier(), nil, true, logger)
	if err := srv.Listen("127.0.0.1:0", "", nil, false); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	go func() { _ = srv.Serve() }()
	t.Cleanup(func() { _ = srv.Close() })

	return srv.PlainAddr()
}

// mailboxUpdates collects the untagged EXISTS and EXPUNGE responses the server
// pushes at a client. The go-imap client dispatches these from its read
// goroutine, so the mutex is load-bearing under -race.
type mailboxUpdates struct {
	mu          sync.Mutex
	numMessages []uint32
	expunged    []uint32
}

func (u *mailboxUpdates) handler() *imapclient.UnilateralDataHandler {
	return &imapclient.UnilateralDataHandler{
		Mailbox: func(data *imapclient.UnilateralDataMailbox) {
			if data.NumMessages == nil {
				return
			}
			u.mu.Lock()
			defer u.mu.Unlock()
			u.numMessages = append(u.numMessages, *data.NumMessages)
		},
		Expunge: func(seqNum uint32) {
			u.mu.Lock()
			defer u.mu.Unlock()
			u.expunged = append(u.expunged, seqNum)
		},
	}
}

func (u *mailboxUpdates) exists() []uint32 {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]uint32(nil), u.numMessages...)
}

func (u *mailboxUpdates) expunges() []uint32 {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]uint32(nil), u.expunged...)
}

// reset discards what has been collected so far, so an assertion can speak
// about the responses to one specific command rather than the whole session.
func (u *mailboxUpdates) reset() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.numMessages = nil
	u.expunged = nil
}

func dialPollTestClient(t *testing.T, addr string, updates *mailboxUpdates) *imapclient.Client {
	t.Helper()

	var opts *imapclient.Options
	if updates != nil {
		opts = &imapclient.Options{UnilateralDataHandler: updates.handler()}
	}
	c, err := imapclient.DialInsecure(addr, opts)
	if err != nil {
		t.Fatalf("DialInsecure(%s): %v", addr, err)
	}
	t.Cleanup(func() { _ = c.Close() })

	if err := c.Login(pollTestUsername, pollTestPassword).Wait(); err != nil {
		t.Fatalf("Login: %v", err)
	}
	return c
}

// TestNoopReportsMessageAppendedByAnotherConnection is the production incident
// end to end, and the only test that goes through Session.Poll itself rather
// than the helper beneath it. Session.Poll was a stub returning nil on the
// assumption that IDLE was the only channel that mattered; go-imap actually
// calls it after every command (imapserver/conn.go), and that is how a NOOP
// gets its untagged EXISTS.
//
// Thunderbird IDLEs on INBOX and NOOP-polls every other folder. Nothing here
// idles, for the same reason.
func TestNoopReportsMessageAppendedByAnotherConnection(t *testing.T) {
	ms := newPollTestStore(t, "Sent")
	addr := startPollTestServer(t, ms)

	updates := &mailboxUpdates{}
	poller := dialPollTestClient(t, addr, updates)

	selectData, err := poller.Select("Sent", nil).Wait()
	if err != nil {
		t.Fatalf("SELECT Sent: %v", err)
	}
	if selectData.NumMessages != 0 {
		t.Fatalf("SELECT reported %d messages, want 0", selectData.NumMessages)
	}

	// A second connection appends, exactly as the web UI's Sent copy does.
	appender := dialPollTestClient(t, addr, nil)
	raw := "From: u1@localhost\r\n" +
		"To: someone@example.org\r\n" +
		"Subject: appended from another connection\r\n" +
		"\r\n" +
		"body\r\n"
	appendCmd := appender.Append("Sent", int64(len(raw)), nil)
	if _, err := io.WriteString(appendCmd, raw); err != nil {
		t.Fatalf("writing APPEND literal: %v", err)
	}
	if err := appendCmd.Close(); err != nil {
		t.Fatalf("closing APPEND literal: %v", err)
	}
	if _, err := appendCmd.Wait(); err != nil {
		t.Fatalf("APPEND: %v", err)
	}

	// The poller has been told nothing yet.
	if got := updates.exists(); len(got) != 0 {
		t.Fatalf("poller saw EXISTS %v before issuing NOOP, test setup is wrong", got)
	}

	if err := poller.Noop().Wait(); err != nil {
		t.Fatalf("NOOP: %v", err)
	}

	got := updates.exists()
	if len(got) != 1 || got[0] != 1 {
		t.Fatalf("NOOP delivered EXISTS %v, want [1]: the appended message stays "+
			"invisible to the client until it re-SELECTs", got)
	}
}

// TestRepeatedNoopDoesNotRepeatExists is the control. A NOOP loop is what
// Thunderbird does continuously, so re-announcing the same EXISTS every time
// would be a fresh bug rather than a fix.
func TestRepeatedNoopDoesNotRepeatExists(t *testing.T) {
	ms := newPollTestStore(t, "INBOX")
	addr := startPollTestServer(t, ms)

	updates := &mailboxUpdates{}
	poller := dialPollTestClient(t, addr, updates)
	if _, err := poller.Select("INBOX", nil).Wait(); err != nil {
		t.Fatalf("SELECT INBOX: %v", err)
	}

	deliverPollTestMessage(t, ms, "INBOX", "guid-1")

	for i := range 3 {
		if err := poller.Noop().Wait(); err != nil {
			t.Fatalf("NOOP %d: %v", i+1, err)
		}
	}

	got := updates.exists()
	if len(got) != 1 || got[0] != 1 {
		t.Errorf("three NOOPs delivered EXISTS %v, want exactly [1]", got)
	}
}

// selectWithOneMessage is the starting position for the tests below: a mailbox
// holding one message, already SELECTed, with the client's view in step.
func selectWithOneMessage(t *testing.T, mailbox string) (*store.MailboxStore, *imapclient.Client, *mailboxUpdates) {
	t.Helper()

	ms := newPollTestStore(t, mailbox)
	deliverPollTestMessage(t, ms, mailbox, "guid-1")
	addr := startPollTestServer(t, ms)

	updates := &mailboxUpdates{}
	c := dialPollTestClient(t, addr, updates)
	selectData, err := c.Select(mailbox, nil).Wait()
	if err != nil {
		t.Fatalf("SELECT %s: %v", mailbox, err)
	}
	if selectData.NumMessages != 1 {
		t.Fatalf("SELECT reported %d messages, want 1", selectData.NumMessages)
	}
	return ms, c, updates
}

// TestFetchThenNoopStillReportsNewMessage is the production case in the form
// that survived the first implementation. FETCH, STORE and SEARCH each refresh
// the session's server-side message cache, and go-imap runs Poll only after the
// handler returns, so a Poll that diffed against that cache found nothing left
// to report: the pending EXISTS was destroyed rather than deferred, and no
// later NOOP could recover it.
//
// Thunderbird FETCHes on selected folders routinely, so this is the path that
// actually kept the appended message invisible.
func TestFetchThenNoopStillReportsNewMessage(t *testing.T) {
	ms, c, updates := selectWithOneMessage(t, "INBOX")

	// A message arrives from another connection.
	deliverPollTestMessage(t, ms, "INBOX", "guid-2")

	// The client re-fetches the message it already knew about. This is the
	// command that used to swallow the update.
	seq := imap.SeqSet{}
	seq.AddNum(1)
	if _, err := c.Fetch(seq, &imap.FetchOptions{Flags: true}).Collect(); err != nil {
		t.Fatalf("FETCH: %v", err)
	}

	if err := c.Noop().Wait(); err != nil {
		t.Fatalf("NOOP: %v", err)
	}

	if got := updates.exists(); len(got) != 1 || got[0] != 2 {
		t.Fatalf("EXISTS after FETCH then NOOP = %v, want [2]: the FETCH erased the "+
			"pending update instead of deferring it", got)
	}
}

// TestStoreThenNoopStillReportsNewMessage covers the same erasure via STORE.
func TestStoreThenNoopStillReportsNewMessage(t *testing.T) {
	ms, c, updates := selectWithOneMessage(t, "INBOX")

	deliverPollTestMessage(t, ms, "INBOX", "guid-2")

	seq := imap.SeqSet{}
	seq.AddNum(1)
	if _, err := c.Store(seq, &imap.StoreFlags{
		Op:    imap.StoreFlagsAdd,
		Flags: []imap.Flag{imap.FlagSeen},
	}, nil).Collect(); err != nil {
		t.Fatalf("STORE: %v", err)
	}

	if err := c.Noop().Wait(); err != nil {
		t.Fatalf("NOOP: %v", err)
	}

	if got := updates.exists(); len(got) != 1 || got[0] != 2 {
		t.Fatalf("EXISTS after STORE then NOOP = %v, want [2]", got)
	}
}

// TestSearchThenNoopStillReportsNewMessage covers the same erasure via SEARCH.
func TestSearchThenNoopStillReportsNewMessage(t *testing.T) {
	ms, c, updates := selectWithOneMessage(t, "INBOX")

	deliverPollTestMessage(t, ms, "INBOX", "guid-2")

	if _, err := c.Search(&imap.SearchCriteria{
		Flag: []imap.Flag{imap.FlagSeen},
	}, nil).Wait(); err != nil {
		t.Fatalf("SEARCH: %v", err)
	}

	if err := c.Noop().Wait(); err != nil {
		t.Fatalf("NOOP: %v", err)
	}

	if got := updates.exists(); len(got) != 1 || got[0] != 2 {
		t.Fatalf("EXISTS after SEARCH then NOOP = %v, want [2]", got)
	}
}

// TestExpungeThenNoopDoesNotRepeatTheExpunge guards the hazard the split
// introduces. EXPUNGE and MOVE write untagged EXPUNGEs themselves, so they
// change the client's view too. If they do not record that, the next Poll sees
// a message in the client's view that is gone from the mailbox and sends the
// sequence number a second time -- and the client, having already renumbered,
// drops a message it should have kept. Re-reporting a removal is worse than
// missing an arrival.
func TestExpungeThenNoopDoesNotRepeatTheExpunge(t *testing.T) {
	ms := newPollTestStore(t, "INBOX")
	for _, guid := range []string{"guid-1", "guid-2", "guid-3"} {
		deliverPollTestMessage(t, ms, "INBOX", guid)
	}
	addr := startPollTestServer(t, ms)

	updates := &mailboxUpdates{}
	c := dialPollTestClient(t, addr, updates)
	if _, err := c.Select("INBOX", nil).Wait(); err != nil {
		t.Fatalf("SELECT INBOX: %v", err)
	}

	// Flag the first message deleted, then expunge it.
	seq := imap.SeqSet{}
	seq.AddNum(1)
	if _, err := c.Store(seq, &imap.StoreFlags{
		Op:    imap.StoreFlagsAdd,
		Flags: []imap.Flag{imap.FlagDeleted},
	}, nil).Collect(); err != nil {
		t.Fatalf("STORE \\Deleted: %v", err)
	}
	expunged, err := c.Expunge().Collect()
	if err != nil {
		t.Fatalf("EXPUNGE: %v", err)
	}
	// Exactly one. A re-report from the poll that runs after the handler lands
	// inside this same command's response window, not in a later NOOP, so it
	// would show up here as a second sequence number.
	assertSeqNums(t, expunged, []uint32{1}, "EXPUNGE responses")

	for i := range 2 {
		if err := c.Noop().Wait(); err != nil {
			t.Fatalf("NOOP %d: %v", i+1, err)
		}
	}

	// Nothing is deliberately reset here: an update wrongly emitted during the
	// EXPUNGE command is as much a bug as one emitted during the NOOPs, and
	// resetting between the two hides the first.
	if got := updates.expunges(); len(got) != 0 {
		t.Errorf("EXPUNGE was re-sent as unilateral data %v; the client already "+
			"renumbered and would drop the wrong message", got)
	}
	if got := updates.exists(); len(got) != 0 {
		t.Errorf("EXISTS %v sent after EXPUNGE, want none: the mailbox only shrank, "+
			"and the client was already told how", got)
	}
}

// TestMoveThenNoopDoesNotRepeatTheExpunge is the same guard for MOVE, which
// also writes its own untagged EXPUNGEs.
func TestMoveThenNoopDoesNotRepeatTheExpunge(t *testing.T) {
	ms := newPollTestStore(t, "INBOX")
	if err := ms.CreateMailbox(pollTestUser, "Archive"); err != nil {
		t.Fatalf("CreateMailbox Archive: %v", err)
	}
	for _, guid := range []string{"guid-1", "guid-2"} {
		deliverPollTestMessage(t, ms, "INBOX", guid)
	}
	addr := startPollTestServer(t, ms)

	updates := &mailboxUpdates{}
	c := dialPollTestClient(t, addr, updates)
	if _, err := c.Select("INBOX", nil).Wait(); err != nil {
		t.Fatalf("SELECT INBOX: %v", err)
	}

	seq := imap.SeqSet{}
	seq.AddNum(1)
	if _, err := c.Move(seq, "Archive").Wait(); err != nil {
		t.Fatalf("MOVE: %v", err)
	}

	if err := c.Noop().Wait(); err != nil {
		t.Fatalf("NOOP: %v", err)
	}

	// MOVE's own EXPUNGE arrives here rather than in the MoveCommand: the
	// client routes EXPUNGE to a pending ExpungeCommand and, when there is
	// none, to the unilateral handler (imapclient/expunge.go). So exactly one
	// is correct and two is the bug -- the poll that runs after the handler
	// re-sending a sequence number the client has already applied.
	assertSeqNums(t, updates.expunges(), []uint32{1}, "EXPUNGE responses")
	if got := updates.exists(); len(got) != 0 {
		t.Errorf("EXISTS %v sent after MOVE, want none", got)
	}
}

// TestIdleDeliveredExistsIsNotRepeatedByPoll pins Idle's half of the baseline.
// IDLE announces new mail through the notifier rather than through Poll, so if
// it does not record what it announced, the first command after DONE re-sends
// the same EXISTS. Thunderbird IDLEs on INBOX, so this is its main folder.
func TestIdleDeliveredExistsIsNotRepeatedByPoll(t *testing.T) {
	ms := newPollTestStore(t, "INBOX")
	addr := startPollTestServer(t, ms)

	updates := &mailboxUpdates{}
	poller := dialPollTestClient(t, addr, updates)
	if _, err := poller.Select("INBOX", nil).Wait(); err != nil {
		t.Fatalf("SELECT INBOX: %v", err)
	}

	idle, err := poller.Idle()
	if err != nil {
		t.Fatalf("IDLE: %v", err)
	}

	// Another connection appends. Append fires the notifier the IDLE is on.
	appender := dialPollTestClient(t, addr, nil)
	raw := "From: u1@localhost\r\nSubject: while idling\r\n\r\nbody\r\n"
	appendCmd := appender.Append("INBOX", int64(len(raw)), nil)
	if _, err := io.WriteString(appendCmd, raw); err != nil {
		t.Fatalf("writing APPEND literal: %v", err)
	}
	if err := appendCmd.Close(); err != nil {
		t.Fatalf("closing APPEND literal: %v", err)
	}
	if _, err := appendCmd.Wait(); err != nil {
		t.Fatalf("APPEND: %v", err)
	}

	// Wait for the IDLE to deliver the EXISTS before ending it.
	deadline := time.Now().Add(5 * time.Second)
	for len(updates.exists()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if err := idle.Close(); err != nil {
		t.Fatalf("ending IDLE: %v", err)
	}
	if err := idle.Wait(); err != nil {
		t.Fatalf("IDLE wait: %v", err)
	}

	if got := updates.exists(); len(got) != 1 || got[0] != 1 {
		t.Fatalf("IDLE delivered EXISTS %v, want [1]", got)
	}

	// The client has already been told. A NOOP must not say it again.
	updates.reset()
	if err := poller.Noop().Wait(); err != nil {
		t.Fatalf("NOOP: %v", err)
	}
	if got := updates.exists(); len(got) != 0 {
		t.Errorf("NOOP after an IDLE-delivered EXISTS re-sent EXISTS %v; the client "+
			"reads that as further new mail", got)
	}
}
