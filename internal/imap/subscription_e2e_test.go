package imap

import (
	"bufio"
	"fmt"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"google.golang.org/protobuf/encoding/protowire"

	"github.com/bongo-project/bongo/internal/store"
)

// rawIMAPConn drives the server over the wire for commands imapclient does not
// implement. LSUB is one of them: go-imap's client dropped it, but it is a
// mandatory RFC 3501 command that Thunderbird and friends still send, and it is
// the command this change is mostly about. Going through a socket rather than
// calling session.List directly is the point — LSUB only reaches List because
// go-imap translates it into ListOptions{SelectSubscribed: true}, and a test
// that called the helper itself would never exercise that translation.
type rawIMAPConn struct {
	t    *testing.T
	conn net.Conn
	r    *bufio.Reader
	seq  int
}

func dialRawIMAP(t *testing.T, addr string) *rawIMAPConn {
	t.Helper()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("Dial(%s): %v", addr, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	// Without a deadline a protocol mistake hangs the test until the whole
	// package times out, which hides which command went wrong.
	if err := conn.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		t.Fatalf("SetDeadline: %v", err)
	}

	c := &rawIMAPConn{t: t, conn: conn, r: bufio.NewReader(conn)}
	greeting, err := c.r.ReadString('\n')
	if err != nil {
		t.Fatalf("reading greeting: %v", err)
	}
	if !strings.HasPrefix(greeting, "* OK") {
		t.Fatalf("greeting = %q, want an untagged OK", greeting)
	}
	c.exec("LOGIN %s %s", pollTestUsername, pollTestPassword)
	return c
}

// exec runs one command and returns its untagged response lines. A tagged
// response other than OK fails the test, so a caller never asserts over the
// untagged lines of a command the server rejected.
func (c *rawIMAPConn) exec(format string, args ...any) []string {
	c.t.Helper()

	c.seq++
	tag := fmt.Sprintf("t%d", c.seq)
	cmd := fmt.Sprintf(format, args...)
	if _, err := fmt.Fprintf(c.conn, "%s %s\r\n", tag, cmd); err != nil {
		c.t.Fatalf("writing %q: %v", cmd, err)
	}

	var untagged []string
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			c.t.Fatalf("reading response to %q: %v", cmd, err)
		}
		line = strings.TrimRight(line, "\r\n")
		if rest, ok := strings.CutPrefix(line, tag+" "); ok {
			if !strings.HasPrefix(rest, "OK") {
				c.t.Fatalf("%q: %s", cmd, rest)
			}
			return untagged
		}
		untagged = append(untagged, line)
	}
}

// mailboxLines indexes the untagged LIST or LSUB responses by mailbox name. The
// whole line is kept as the value because the attribute assertions need it.
func mailboxLines(t *testing.T, untagged []string, verb string) map[string]string {
	t.Helper()

	out := make(map[string]string)
	for _, line := range untagged {
		if !strings.HasPrefix(line, "* "+verb+" ") {
			continue
		}
		// * LSUB (\HasNoChildren \Subscribed) "/" Archive
		fields := strings.Fields(line)
		name := strings.Trim(fields[len(fields)-1], `"`)
		if name == "" {
			t.Fatalf("could not read a mailbox name out of %q", line)
		}
		out[name] = line
	}
	return out
}

func mailboxNames(t *testing.T, untagged []string, verb string) []string {
	t.Helper()

	names := make([]string, 0, len(untagged))
	for name := range mailboxLines(t, untagged, verb) {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// newSubscriptionTestStore builds a store holding the named mailboxes and hands
// back the Pebble handle too, so a test can plant a record that the current
// code would never write.
func newSubscriptionTestStore(t *testing.T, mailboxes ...string) (*store.MailboxStore, *store.PebbleStore) {
	t.Helper()

	ps, err := store.NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })

	ms := store.NewMailboxStore(ps, &imapDirectApplier{store: ps})
	for _, name := range mailboxes {
		if err := ms.CreateMailbox(pollTestUser, name); err != nil {
			t.Fatalf("CreateMailbox(%q): %v", name, err)
		}
	}
	return ms, ps
}

// TestLsubOmitsUnsubscribedMailbox is the reported bug. Subscribe and
// Unsubscribe were no-ops and List ignored SelectSubscribed, so LSUB returned
// every mailbox and an unsubscribed folder came straight back.
func TestLsubOmitsUnsubscribedMailbox(t *testing.T) {
	ms, _ := newSubscriptionTestStore(t, "INBOX", "Archive")
	addr := startPollTestServer(t, ms)

	c := dialPollTestClient(t, addr, nil)
	if err := c.Unsubscribe("Archive").Wait(); err != nil {
		t.Fatalf("UNSUBSCRIBE Archive: %v", err)
	}

	// A second connection, because a subscription that only holds inside the
	// session that made it is not persisted.
	raw := dialRawIMAP(t, addr)

	got := mailboxNames(t, raw.exec(`LSUB "" "*"`), "LSUB")
	if !slices.Equal(got, []string{"INBOX"}) {
		t.Fatalf("LSUB = %v, want [INBOX]: Archive was unsubscribed", got)
	}

	// The mailbox is unsubscribed, not deleted: plain LIST still shows it.
	if got := mailboxNames(t, raw.exec(`LIST "" "*"`), "LIST"); !slices.Equal(got, []string{"Archive", "INBOX"}) {
		t.Fatalf("LIST = %v, want [Archive INBOX]: UNSUBSCRIBE must not hide a mailbox from LIST", got)
	}
}

// TestSubscribeReturnsMailboxToLsub is the other direction, and the one that
// catches a Subscribe left as a no-op while Unsubscribe works.
func TestSubscribeReturnsMailboxToLsub(t *testing.T) {
	ms, _ := newSubscriptionTestStore(t, "INBOX", "Archive")
	addr := startPollTestServer(t, ms)

	raw := dialRawIMAP(t, addr)
	raw.exec("UNSUBSCRIBE Archive")
	if got := mailboxNames(t, raw.exec(`LSUB "" "*"`), "LSUB"); slices.Contains(got, "Archive") {
		t.Fatalf("LSUB = %v after UNSUBSCRIBE, test cannot prove SUBSCRIBE brought it back", got)
	}

	raw.exec("SUBSCRIBE Archive")

	got := mailboxNames(t, dialRawIMAP(t, addr).exec(`LSUB "" "*"`), "LSUB")
	if !slices.Equal(got, []string{"Archive", "INBOX"}) {
		t.Fatalf("LSUB = %v after SUBSCRIBE Archive, want [Archive INBOX]", got)
	}
}

// TestListReturnSubscribedMarksOnlySubscribedMailboxes covers the LIST-EXTENDED
// half: RETURN (SUBSCRIBED) lists everything but flags only what is subscribed.
func TestListReturnSubscribedMarksOnlySubscribedMailboxes(t *testing.T) {
	ms, _ := newSubscriptionTestStore(t, "INBOX", "Archive")
	addr := startPollTestServer(t, ms)

	raw := dialRawIMAP(t, addr)
	raw.exec("UNSUBSCRIBE Archive")

	lines := mailboxLines(t, raw.exec(`LIST "" "*" RETURN (SUBSCRIBED)`), "LIST")
	if len(lines) != 2 {
		t.Fatalf("LIST RETURN (SUBSCRIBED) returned %d mailboxes, want 2: it selects nothing, it only annotates", len(lines))
	}
	if !strings.Contains(lines["INBOX"], string(imap.MailboxAttrSubscribed)) {
		t.Errorf("INBOX line %q is missing %s", lines["INBOX"], imap.MailboxAttrSubscribed)
	}
	if strings.Contains(lines["Archive"], string(imap.MailboxAttrSubscribed)) {
		t.Errorf("Archive line %q carries %s despite being unsubscribed", lines["Archive"], imap.MailboxAttrSubscribed)
	}

	// Without the return option the attribute is not volunteered, so a client
	// that asked for a plain LIST cannot mistake every folder for subscribed.
	plain := mailboxLines(t, raw.exec(`LIST "" "*"`), "LIST")
	for name, line := range plain {
		if strings.Contains(line, string(imap.MailboxAttrSubscribed)) {
			t.Errorf("plain LIST line for %q carries %s: %q", name, imap.MailboxAttrSubscribed, line)
		}
	}
}

// TestListSelectSubscribedFiltersAndAnnotates pins the RFC 5258 3.1 rule that
// the SUBSCRIBED selection option implies the SUBSCRIBED return option, so the
// mailboxes it selects also carry \Subscribed.
func TestListSelectSubscribedFiltersAndAnnotates(t *testing.T) {
	ms, _ := newSubscriptionTestStore(t, "INBOX", "Archive")
	addr := startPollTestServer(t, ms)

	raw := dialRawIMAP(t, addr)
	raw.exec("UNSUBSCRIBE Archive")

	lines := mailboxLines(t, raw.exec(`LIST (SUBSCRIBED) "" "*"`), "LIST")
	if len(lines) != 1 {
		t.Fatalf("LIST (SUBSCRIBED) returned %v, want INBOX only", lines)
	}
	if !strings.Contains(lines["INBOX"], string(imap.MailboxAttrSubscribed)) {
		t.Errorf("INBOX line %q is missing %s", lines["INBOX"], imap.MailboxAttrSubscribed)
	}
}

func TestSubscribeUnknownMailboxIsRejected(t *testing.T) {
	ms, _ := newSubscriptionTestStore(t, "INBOX")
	addr := startPollTestServer(t, ms)

	c := dialPollTestClient(t, addr, nil)
	if err := c.Subscribe("NoSuchMailbox").Wait(); err == nil {
		t.Fatal("SUBSCRIBE on a mailbox that does not exist succeeded")
	}

	// The failure must be specific to the missing mailbox, not a session that
	// broke and now rejects everything.
	if err := c.Subscribe("INBOX").Wait(); err != nil {
		t.Fatalf("SUBSCRIBE INBOX: %v", err)
	}
}

// TestLegacyMailboxStaysSubscribed is the upgrade-safety test, end to end. A
// mailbox created before this change has a MailboxMeta with no subscription
// field at all, and it must keep behaving exactly as it did: present in LSUB,
// reported as subscribed.
//
// The record is hand-encoded rather than marshalled from a current
// bongov1.MailboxMeta. Marshalling one with Unsubscribed false emits the same
// bytes today, so the test would pass against a field stored in the positive
// sense purely because the test wrote the field it was meant to be missing.
func TestLegacyMailboxStaysSubscribed(t *testing.T) {
	ms, ps := newSubscriptionTestStore(t, "INBOX")
	addr := startPollTestServer(t, ms)

	// Written straight to Pebble, in the key layout MailboxStore uses, because
	// no code path in the current build can produce this record.
	var legacy []byte
	legacy = protowire.AppendTag(legacy, 1, protowire.BytesType)
	legacy = protowire.AppendString(legacy, "Archive")
	legacy = protowire.AppendTag(legacy, 2, protowire.VarintType)
	legacy = protowire.AppendVarint(legacy, 12345)
	legacy = protowire.AppendTag(legacy, 3, protowire.VarintType)
	legacy = protowire.AppendVarint(legacy, 1)
	if err := ps.Set([]byte("/user/"+pollTestUser+"/mailbox/Archive/meta"), legacy); err != nil {
		t.Fatalf("planting legacy mailbox meta: %v", err)
	}

	raw := dialRawIMAP(t, addr)

	got := mailboxNames(t, raw.exec(`LSUB "" "*"`), "LSUB")
	if !slices.Contains(got, "Archive") {
		t.Fatalf("LSUB = %v, want it to include Archive: a mailbox that predates the subscription "+
			"field must stay subscribed, or upgrading empties every client's folder list", got)
	}

	lines := mailboxLines(t, raw.exec(`LIST "" "*" RETURN (SUBSCRIBED)`), "LIST")
	if !strings.Contains(lines["Archive"], string(imap.MailboxAttrSubscribed)) {
		t.Fatalf("legacy mailbox line %q is missing %s", lines["Archive"], imap.MailboxAttrSubscribed)
	}
}
