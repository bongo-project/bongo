package imap

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// startQuotaTestServer runs the real server. With realQuota the production
// DomainQuotaStore is installed as the APPEND checker, as the agent does.
func startQuotaTestServer(t *testing.T, ms *store.MailboxStore, ps *store.PebbleStore, realQuota bool) string {
	t.Helper()

	blobs, err := store.NewBlobStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}
	notifier := NewNotifier()
	ms.SetNotifier(NewStoreNotifier(notifier))
	srv := NewServer(stubAuthenticator{}, ps, blobs, ms, nil, nil, nil, notifier, nil, true, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if realQuota {
		srv.SetQuotaChecker(store.NewDomainQuotaStore(ps, &imapDirectApplier{store: ps}))
	}
	if err := srv.Listen("127.0.0.1:0", "", nil, false, nil); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	go func() { _ = srv.Serve() }()
	t.Cleanup(func() { _ = srv.Close() })
	return srv.PlainAddr()
}

// seedQuotaUser writes u1's profile with the given quota and usage.
func seedQuotaUser(t *testing.T, ps *store.PebbleStore, quotaBytes, usage int64) {
	t.Helper()
	b, err := proto.Marshal(&bongov1.UserProfile{Email: pollTestUsername, QuotaBytes: quotaBytes})
	if err != nil {
		t.Fatalf("marshal profile: %v", err)
	}
	if err := ps.Set([]byte("/user/"+pollTestUser+"/profile"), b); err != nil {
		t.Fatalf("write profile: %v", err)
	}
	cmds, err := store.BuildUserUsageCommands(map[string]int64{pollTestUser: usage})
	if err != nil {
		t.Fatalf("BuildUserUsageCommands: %v", err)
	}
	for _, c := range cmds {
		if err := store.ApplyUserUsageCommand(ps, c); err != nil {
			t.Fatalf("apply usage: %v", err)
		}
	}
	if got, err := store.GetUserUsage(ps, pollTestUser); err != nil || got != usage {
		t.Fatalf("GetUserUsage = %d, %v; want %d", got, err, usage)
	}
}

// execLines runs a command and returns its untagged lines and the tagged
// status, without failing on a refusal.
func (c *rawIMAPConn) execLines(format string, args ...any) ([]string, string) {
	c.t.Helper()

	c.seq++
	tag := fmt.Sprintf("q%d", c.seq)
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
			return untagged, rest
		}
		untagged = append(untagged, line)
	}
}

// appendLiteral sends a synchronising APPEND and returns the tagged status.
func (c *rawIMAPConn) appendLiteral(mailbox string, msg string) string {
	c.t.Helper()

	c.seq++
	tag := fmt.Sprintf("p%d", c.seq)
	if _, err := fmt.Fprintf(c.conn, "%s APPEND %s {%d}\r\n", tag, mailbox, len(msg)); err != nil {
		c.t.Fatalf("writing APPEND: %v", err)
	}
	line, err := c.r.ReadString('\n')
	if err != nil {
		c.t.Fatalf("reading continuation: %v", err)
	}
	if !strings.HasPrefix(line, "+") {
		// Refused before the literal was sent.
		return strings.TrimSpace(strings.TrimPrefix(line, tag+" "))
	}
	if _, err := fmt.Fprintf(c.conn, "%s\r\n", msg); err != nil {
		c.t.Fatalf("writing literal: %v", err)
	}
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			c.t.Fatalf("reading APPEND response: %v", err)
		}
		if rest, ok := strings.CutPrefix(strings.TrimRight(line, "\r\n"), tag+" "); ok {
			return rest
		}
	}
}

func hasLine(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}
	return false
}

func TestCeilKiB(t *testing.T) {
	tests := []struct {
		in, want int64
	}{
		{0, 0},
		{-4, 0},
		{1, 1},
		{1023, 1},
		{1024, 1},
		{1025, 2},
		{1<<63 - 1, 1 << 53},
	}
	for _, tt := range tests {
		if got := ceilKiB(tt.in); got != tt.want {
			t.Errorf("ceilKiB(%d) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestQuotaWithQuota(t *testing.T) {
	ms, ps := newSubscriptionTestStore(t, "INBOX")
	seedQuotaUser(t, ps, 5000, 1500) // 5 KiB limit (4.88 rounded up), 2 KiB used (1.46 rounded up)
	raw := dialRawIMAP(t, startQuotaTestServer(t, ms, ps, false))

	caps, status := raw.execLines("CAPABILITY")
	if !strings.HasPrefix(status, "OK") {
		t.Fatalf("CAPABILITY: %s", status)
	}
	joined := strings.Join(caps, "\n")
	for _, want := range []string{"QUOTA", "QUOTA=RES-STORAGE"} {
		if !strings.Contains(joined, want) {
			t.Errorf("CAPABILITY = %q, want %s", joined, want)
		}
	}
	if strings.Contains(joined, "QUOTASET") {
		t.Errorf("CAPABILITY = %q, must not advertise QUOTASET", joined)
	}

	lines, status := raw.execLines("GETQUOTAROOT INBOX")
	if !strings.HasPrefix(status, "OK") {
		t.Fatalf("GETQUOTAROOT INBOX: %s", status)
	}
	if !hasLine(lines, `* QUOTAROOT INBOX "user"`) || !hasLine(lines, `* QUOTA "user" (STORAGE 2 5)`) {
		t.Errorf("GETQUOTAROOT INBOX = %q", lines)
	}

	lines, status = raw.execLines("GETQUOTA user")
	if !strings.HasPrefix(status, "OK") || !hasLine(lines, `* QUOTA "user" (STORAGE 2 5)`) {
		t.Errorf("GETQUOTA user = %q, %s", lines, status)
	}

	for _, root := range []string{`""`, "other", `"Other Users"`} {
		if _, status := raw.execLines("GETQUOTA %s", root); !strings.HasPrefix(status, "NO") {
			t.Errorf("GETQUOTA %s = %s, want NO", root, status)
		}
	}
	if _, status := raw.execLines("GETQUOTAROOT Missing"); !strings.HasPrefix(status, "NO [NONEXISTENT]") {
		t.Errorf("GETQUOTAROOT Missing = %s, want NO [NONEXISTENT]", status)
	}
	lines, status = raw.execLines(`GETQUOTAROOT "Other Users/bob/INBOX"`)
	if !strings.HasPrefix(status, "OK") || len(lines) != 1 || lines[0] != `* QUOTAROOT "Other Users/bob/INBOX"` {
		t.Errorf("GETQUOTAROOT other user = %q, %s; want no root", lines, status)
	}
	if _, status := raw.execLines("SETQUOTA user (STORAGE 10)"); !strings.HasPrefix(status, "NO [NOPERM]") {
		t.Errorf("SETQUOTA = %s, want NO [NOPERM]", status)
	}
}

func TestQuotaWithoutQuota(t *testing.T) {
	ms, ps := newSubscriptionTestStore(t, "INBOX")
	seedQuotaUser(t, ps, 0, 1500)
	raw := dialRawIMAP(t, startQuotaTestServer(t, ms, ps, false))

	caps, _ := raw.execLines("CAPABILITY")
	if !strings.Contains(strings.Join(caps, "\n"), "QUOTA=RES-STORAGE") {
		t.Errorf("CAPABILITY = %q, want QUOTA=RES-STORAGE even without a user quota", caps)
	}
	lines, status := raw.execLines("GETQUOTAROOT INBOX")
	if !strings.HasPrefix(status, "OK") || len(lines) != 1 || lines[0] != "* QUOTAROOT INBOX" {
		t.Errorf("GETQUOTAROOT INBOX = %q, %s; want a bare QUOTAROOT", lines, status)
	}
	if _, status := raw.execLines("GETQUOTA user"); !strings.HasPrefix(status, "NO [NONEXISTENT]") {
		t.Errorf("GETQUOTA user = %s, want NO [NONEXISTENT]", status)
	}
}

// The library dereferences DELETED and DELETED-STORAGE with no nil check, so a
// compliant client asking for them used to drop the connection.
func TestStatusDeletedItems(t *testing.T) {
	ms, ps := newSubscriptionTestStore(t, "INBOX")
	seedQuotaUser(t, ps, 0, 0)
	sizes := []int64{3000, 100, 2000}
	for i, size := range sizes {
		guid := fmt.Sprintf("g%d", i)
		meta := &bongov1.MailMeta{Guid: guid, RawMessage: &bongov1.BlobRef{Hash: "h" + guid, Size: size}}
		b, err := proto.Marshal(meta)
		if err != nil {
			t.Fatal(err)
		}
		if err := ps.Set([]byte("/user/"+pollTestUser+"/mail/"+guid), b); err != nil {
			t.Fatal(err)
		}
		var flags uint32
		if i != 1 {
			flags = store.FlagDeleted
		}
		if _, err := ms.AppendMessage(pollTestUser, "INBOX", guid, flags, 0); err != nil {
			t.Fatalf("AppendMessage: %v", err)
		}
	}
	raw := dialRawIMAP(t, startQuotaTestServer(t, ms, ps, false))

	// 3000 + 2000 deleted octets: 4.88 KiB, rounded up to 5.
	lines, status := raw.execLines("STATUS INBOX (MESSAGES DELETED DELETED-STORAGE)")
	if !strings.HasPrefix(status, "OK") {
		t.Fatalf("STATUS = %s", status)
	}
	if !hasLine(lines, "* STATUS INBOX (MESSAGES 3 DELETED 2 DELETED-STORAGE 5)") {
		t.Errorf("STATUS = %q", lines)
	}
	// The connection must survive.
	if _, status := raw.execLines("NOOP"); !strings.HasPrefix(status, "OK") {
		t.Errorf("NOOP after STATUS = %s", status)
	}

	// An empty mailbox answers zeros rather than dereferencing nil.
	if _, status := raw.execLines("CREATE Empty"); !strings.HasPrefix(status, "OK") {
		t.Fatalf("CREATE Empty = %s", status)
	}
	lines, status = raw.execLines("STATUS Empty (DELETED DELETED-STORAGE)")
	if !strings.HasPrefix(status, "OK") || !hasLine(lines, `* STATUS "Empty" (DELETED 0 DELETED-STORAGE 0)`) {
		t.Errorf("STATUS Empty = %q, %s", lines, status)
	}
}

func TestAppendPerUserQuotaRefusal(t *testing.T) {
	ms, ps := newSubscriptionTestStore(t, "INBOX")
	// No domain quota is set anywhere: only the user's own limit can refuse.
	seedQuotaUser(t, ps, 2000, 1500)
	raw := dialRawIMAP(t, startQuotaTestServer(t, ms, ps, true))

	pad := func(n int) string {
		head := "From: a@example.test\r\nSubject: s\r\n\r\n"
		return head + strings.Repeat("x", n-len(head))
	}

	// 1500 + 700 > 2000.
	if got := raw.appendLiteral("INBOX", pad(700)); !strings.HasPrefix(got, "NO [OVERQUOTA]") || !strings.Contains(got, "Storage quota exceeded") {
		t.Errorf("large APPEND = %q, want NO [OVERQUOTA] Storage quota exceeded", got)
	}
	// Control: 1500 + 400 fits.
	if got := raw.appendLiteral("INBOX", pad(400)); !strings.HasPrefix(got, "OK") {
		t.Errorf("small APPEND = %q, want OK", got)
	}
}
