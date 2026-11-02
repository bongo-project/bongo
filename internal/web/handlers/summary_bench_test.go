package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cockroachdb/pebble"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// summaryBenchMessages is the INBOX size of the page-load budget: the summary
// page loads in under 300 ms with this many messages in INBOX (US-017).
const summaryBenchMessages = 10000

// seedBigInbox fills INBOX with n messages the way delivery does, so the
// Pebble records and the SQLite index look real: one in
// ten is addressed to the user (60 percent of those unread), one in 25 is
// starred, the rest are a mix of read and unread mail to a list. Only the to-me rows get a
// SQLite row. The newest
// unread to-me message is returned.
func seedBigInbox(tb testing.TB, e *summaryEnv, n int) string {
	tb.Helper()
	if err := e.q.mailboxes.CreateMailbox(quotaUser, "INBOX"); err != nil && !strings.Contains(err.Error(), "exist") {
		tb.Fatal(err)
	}
	mine := map[string]bool{quotaEmail: true}
	newestUnreadToMe := ""
	// Written straight to Pebble in batches, in the key layout DeliverMessage
	// uses: 10,000 Raft applies, each synced, would take minutes.
	batch := e.q.ps.DB().NewBatch()
	defer func() { _ = batch.Close() }()
	base := summaryNow.Unix() - int64(n)
	for i := range n {
		guid := fmt.Sprintf("big-%05d", i)
		subject := fmt.Sprintf("Subject %05d", i)
		meta := &bongov1.MailMeta{
			Guid: guid, Subject: subject, From: "Sender <sender@elsewhere.test>",
			To: []string{"list@elsewhere.test"}, ReceivedAt: base + int64(i),
		}
		var flags uint32
		toMe := i%10 == 0
		if toMe {
			meta.To = []string{quotaEmail}
		}
		switch {
		case toMe && i%5 < 3: // 60 percent of to-me mail is unread
		case i%25 == 0:
			flags = store.FlagFlagged | store.FlagSeen
		case i%3 == 0:
		default:
			flags = store.FlagSeen
		}
		metaVal, err := proto.Marshal(meta)
		if err != nil {
			tb.Fatal(err)
		}
		msgVal, err := proto.Marshal(&bongov1.MailboxMessage{Uid: uint32(i + 1), MailGuid: guid, Flags: flags, InternalDate: meta.ReceivedAt})
		if err != nil {
			tb.Fatal(err)
		}
		if err := batch.Set([]byte("/user/"+quotaUser+"/mail/"+guid), metaVal, nil); err != nil {
			tb.Fatal(err)
		}
		if err := batch.Set([]byte(fmt.Sprintf("/user/%s/mailbox/INBOX/msg/%010d", quotaUser, i+1)), msgVal, nil); err != nil {
			tb.Fatal(err)
		}
		if i%1000 == 999 || i == n-1 {
			if err := batch.Commit(pebble.NoSync); err != nil {
				tb.Fatal(err)
			}
			batch = e.q.ps.DB().NewBatch()
		}
		// Only the to-me rows are indexed: the page asks the index for those
		// alone, and indexing 10,000 rows one by one would only slow the setup.
		if toMe {
			if err := e.meta.IndexMail(quotaUser, guid, subject, meta.From, meta.ReceivedAt, 1); err != nil {
				tb.Fatal(err)
			}
			if err := e.meta.SetMailHeaders(quotaUser, guid, store.MailHeaderFields(meta, mine)); err != nil {
				tb.Fatal(err)
			}
		}
		if toMe && flags&store.FlagSeen == 0 {
			newestUnreadToMe = subject
		}
	}
	return newestUnreadToMe
}

// BenchmarkSummaryPage10kInbox measures the whole handler through httptest,
// sidebar counts included, over a 10,000 message INBOX with a handful of
// invitations and events. ns/op must stay under 300 ms (3e8). It is a
// benchmark and not a test on purpose: a wall-clock assertion would be flaky
// under -race and on shared CI machines.
//
//	go test -run '^$' -bench SummaryPage10kInbox -benchtime 20x ./internal/web/handlers/
func BenchmarkSummaryPage10kInbox(b *testing.B) {
	e := newSummaryEnv(b)
	newest := seedBigInbox(b, e, summaryBenchMessages)
	for i := range 5 {
		e.place(b, quotaUser, icsOpts{uid: fmt.Sprintf("bench-%d@example.org", i), summary: fmt.Sprintf("Bench invite %d", i)}.build("REQUEST"))
	}
	for i := range 20 {
		if _, err := e.q.events.Create(quotaUser, &bongov1.Event{
			Guid: fmt.Sprintf("bench-ev-%d", i), Title: fmt.Sprintf("Bench event %d", i), CalendarId: "default",
			StartTime: summaryNow.Unix() + int64(i+1)*3600, EndTime: summaryNow.Unix() + int64(i+1)*3600 + 1800,
		}); err != nil {
			b.Fatal(err)
		}
	}
	router := e.router()
	req := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/summary", nil)
		r.Header.Set("X-Test-User", quotaUser)
		return r
	}

	// Once, outside the timing: the page must hold what it should, so a fast
	// wrong answer cannot pass.
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req())
	body := w.Body.String()
	if w.Code != http.StatusOK || !strings.Contains(body, newest) || !strings.Contains(body, "Bench invite") || !strings.Contains(body, "Bench event 0") {
		b.Fatalf("status %d; page lacks the newest unread to-me %q, an invitation or an event", w.Code, newest)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req())
		if w.Code != http.StatusOK {
			b.Fatalf("status %d", w.Code)
		}
	}
}
