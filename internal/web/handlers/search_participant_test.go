package handlers

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/store"
)

func participantPath(addrs ...string) string {
	return "/search?" + url.Values{"participant": addrs}.Encode()
}

func (f *viewFixture) expunge(user, mailbox string, uid uint32) {
	f.t.Helper()
	if err := f.mailboxes.SetFlags(user, mailbox, uid, store.FlagDeleted); err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.mailboxes.Expunge(user, mailbox); err != nil {
		f.t.Fatal(err)
	}
}

func TestSearchParticipantAcrossFoldersExceptTrashAndJunk(t *testing.T) {
	f := newViewFixture(t)
	const ann = "ann@friends.test"
	me := []string{"user1@example.com"}
	f.deliver(seedMsg{mailbox: "INBOX", guid: "in", from: ann, to: me, at: 100})
	f.deliver(seedMsg{mailbox: "Sent", guid: "sent", from: "user1@example.com", to: []string{"Ann <" + ann + ">"}, at: 200})
	f.deliver(seedMsg{mailbox: "Drafts", guid: "draft", from: "user1@example.com", to: []string{ann}, at: 300})
	f.deliver(seedMsg{mailbox: "Projects", guid: "proj", from: "x@y.test", to: me, cc: []string{ann}, at: 400})
	f.deliver(seedMsg{mailbox: "Junk", guid: "junk", from: ann, to: me, at: 500})
	f.deliver(seedMsg{mailbox: "Trash", guid: "trash", from: ann, to: me, at: 600})
	f.deliver(seedMsg{mailbox: "Trash/x", guid: "trashsub", from: ann, to: me, at: 700})
	f.deliver(seedMsg{mailbox: "Junk/y", guid: "junksub", from: ann, to: me, at: 800})
	f.deliver(seedMsg{mailbox: "INBOX", guid: "unrelated", from: "other@y.test", to: me, at: 900})
	code, body := f.get(participantPath(ann), "", false)
	if code != http.StatusOK {
		t.Fatalf("status = %d: %s", code, body)
	}
	want := []string{"subj-proj", "subj-draft", "subj-sent", "subj-in"}
	if got := subjectsInOrder(body); !equalStrings(got, want) {
		t.Errorf("subjects = %v, want %v", got, want)
	}
}

func TestSearchParticipantSeveralAddressesOR(t *testing.T) {
	f := newViewFixture(t)
	me := []string{"user1@example.com"}
	f.deliver(seedMsg{mailbox: "INBOX", guid: "a", from: "a@x.test", to: me, at: 100})
	f.deliver(seedMsg{mailbox: "INBOX", guid: "b", from: "b+tag@x.test", to: me, at: 200})
	f.deliver(seedMsg{mailbox: "INBOX", guid: "both", from: "a@x.test", to: []string{"b+tag@x.test"}, at: 300})
	f.deliver(seedMsg{mailbox: "INBOX", guid: "c", from: "c@x.test", to: me, at: 400})
	_, body := f.get(participantPath("A@x.test", "b+tag@x.test"), "", false)
	if got, want := subjectsInOrder(body), []string{"subj-both", "subj-b", "subj-a"}; !equalStrings(got, want) {
		t.Errorf("subjects = %v, want %v (one row for the message carrying both)", got, want)
	}
}

func TestSearchParticipantGroupsByThread(t *testing.T) {
	f := newViewFixture(t)
	f.handler.SetThreadStore(store.NewThreadStore(f.ps, f.mailboxes))
	const ann = "ann@friends.test"
	me := []string{"user1@example.com"}
	f.deliver(seedMsg{mailbox: "Sent", guid: "root", from: "user1@example.com", to: []string{ann}, at: 100, messageID: "<root@x>"})
	f.deliver(seedMsg{mailbox: "INBOX", guid: "r1", from: ann, to: me, at: 200, messageID: "<r1@x>", inReplyTo: "<root@x>"})
	f.deliver(seedMsg{mailbox: "INBOX", guid: "r2", from: ann, to: me, at: 300, messageID: "<r2@x>", inReplyTo: "<root@x>"})
	f.deliver(seedMsg{mailbox: "INBOX", guid: "solo", from: ann, to: me, at: 150, messageID: "<solo@x>"})
	_, body := f.get(participantPath(ann), "", false)
	if got, want := subjectsInOrder(body), []string{"subj-r2", "subj-solo"}; !equalStrings(got, want) {
		t.Errorf("subjects = %v, want one row for the three-message thread and one for the single message: %v", got, want)
	}
}

func TestSearchParticipantOnlyOwnMail(t *testing.T) {
	f := newViewFixture(t)
	const ann = "ann@friends.test"
	f.deliver(seedMsg{user: "user1", mailbox: "INBOX", guid: "mine", from: ann, to: []string{"user1@example.com"}, at: 100})
	f.deliver(seedMsg{user: "user2", mailbox: "INBOX", guid: "bobs", from: ann, to: []string{"user2@example.com"}, at: 200})
	// user2's folder is shared with user1.
	id, err := f.mailboxes.EnsureMailboxID("user2", "INBOX")
	if err != nil {
		t.Fatal(err)
	}
	acl := store.NewACLStore(f.ps, f.applier)
	if err := acl.SetGrant(store.ACLKindMailbox, "user2", id, store.GranteeUser("user1"), "", "lr", store.GrantStateAccepted, "user2", time.Now()); err != nil {
		t.Fatal(err)
	}
	if g, err := acl.GetGrant(store.ACLKindMailbox, "user2", id, store.GranteeUser("user1")); err != nil || g == nil {
		t.Fatalf("the grant was not written: %v %v", g, err)
	}
	for user, want := range map[string]string{"user1": "subj-mine", "user2": "subj-bobs"} {
		_, body := f.get(participantPath(ann), user, false)
		if got := subjectsInOrder(body); !equalStrings(got, []string{want}) {
			t.Errorf("%s sees %v, want only %s", user, got, want)
		}
	}
}

func TestSearchParticipantExpungedNotListed(t *testing.T) {
	f := newViewFixture(t)
	const ann = "ann@friends.test"
	me := []string{"user1@example.com"}
	f.deliver(seedMsg{mailbox: "INBOX", guid: "keep", from: ann, to: me, at: 100})
	uid := f.deliver(seedMsg{mailbox: "INBOX", guid: "gone", from: ann, to: me, at: 200})
	f.expunge("user1", "INBOX", uid)
	// The index still names both messages: only the mailbox says which exist.
	if g, _ := f.metadata.QueryByParticipants("user1", []string{ann}); len(g) != 2 {
		t.Fatalf("index rows = %v, want both: the test would pass vacuously", g)
	}
	_, body := f.get(participantPath(ann), "", false)
	if got := subjectsInOrder(body); !equalStrings(got, []string{"subj-keep"}) {
		t.Errorf("subjects = %v, want only subj-keep (the index row outlives the expunge)", got)
	}
}

func TestSearchParticipantRejectsInvalid(t *testing.T) {
	f := newViewFixture(t)
	var many []string
	for i := range store.MaxParticipantQuery + 1 {
		many = append(many, fmt.Sprintf("p%d@x.test", i))
	}
	for name, path := range map[string]string{
		"empty":    "/search?participant=",
		"garbage":  "/search?participant=not-an-address",
		"too many": participantPath(many...),
	} {
		if code, _ := f.get(path, "", false); code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, code)
		}
		if code, _ := f.post(strings.Replace(path, "/search", "/search/bulk", 1), "", url.Values{"action": {"flag"}, "targets": {"1:INBOX"}}); code != http.StatusBadRequest {
			t.Errorf("%s bulk: status = %d, want 400", name, code)
		}
	}
	// Exactly the cap is fine.
	if code, _ := f.get(participantPath(many[:store.MaxParticipantQuery]...), "", false); code != http.StatusOK {
		t.Errorf("20 addresses = %d, want 200", code)
	}
}

func TestSearchParticipantIgnoresQ(t *testing.T) {
	f := newViewFixture(t)
	f.deliver(seedMsg{mailbox: "INBOX", guid: "a", from: "ann@x.test", to: []string{"user1@example.com"}, at: 100})
	_, body := f.get(participantPath("ann@x.test")+"&q=nothing-matches-this", "", false)
	if got := subjectsInOrder(body); !equalStrings(got, []string{"subj-a"}) {
		t.Errorf("subjects = %v, q must be ignored", got)
	}
}

func TestSearchParticipantPaginationKeepsParticipant(t *testing.T) {
	f := newViewFixture(t)
	const addr = "a+b@x.test"
	for i := range 30 {
		f.deliver(seedMsg{mailbox: "INBOX", guid: fmt.Sprintf("p%02d", i), from: addr, to: []string{"user1@example.com"}, at: int64(1000 + i)})
	}
	path := participantPath(addr)
	_, p1 := f.get(path, "", false)
	if got := subjectsInOrder(p1); len(got) != 25 || got[0] != "subj-p29" {
		t.Fatalf("page 1 = %v", got)
	}
	flat := strings.ReplaceAll(p1, "&amp;", "&")
	for _, want := range []string{
		`data-list-url="/search?participant=a%2Bb%40x.test"`,
		`data-bulk-url="/search/bulk?participant=a%2Bb%40x.test"`,
		`href="/search?participant=a%2Bb%40x.test&page=2"`,
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("page 1 lacks %s", want)
		}
	}
	_, p2 := f.get(path+"&page=2", "", false)
	if got := subjectsInOrder(p2); len(got) != 5 || got[0] != "subj-p04" {
		t.Errorf("page 2 = %v", got)
	}
	_, part := f.get(path+"&page=2&partial=1", "", false)
	if got := subjectsInOrder(part); len(got) != 5 {
		t.Errorf("partial page 2 = %v", got)
	}
	if _, inf := f.get(path+"&partial=1", "", false); !strings.Contains(strings.ReplaceAll(inf, "&amp;", "&"), `hx-get="/search?participant=a%2Bb%40x.test&page=2&partial=1"`) {
		t.Errorf("the infinite-scroll sentinel loses the participant: %s", inf)
	}
}

func TestSearchParticipantBulkActsOnRealMailbox(t *testing.T) {
	f := newViewFixture(t)
	const ann = "ann@x.test"
	me := []string{"user1@example.com"}
	f.deliver(seedMsg{mailbox: "INBOX", guid: "x", from: ann, to: me, at: 100})
	f.deliver(seedMsg{mailbox: "Projects", guid: "y", from: ann, to: me, at: 200})
	flagged := func(box string) bool {
		m, err := f.mailboxes.GetMessage("user1", box, 1)
		if err != nil {
			t.Fatal(err)
		}
		return m.Flags&store.FlagFlagged != 0
	}
	_, body := f.get(participantPath(ann), "", false)
	for _, want := range []string{`data-sel-key="1:Projects"`, `data-sel-key="1:INBOX"`, `hx-post="/mail/Projects/1/flags"`} {
		if !strings.Contains(body, want) {
			t.Errorf("rows lack %s", want)
		}
	}
	bulk := "/search/bulk?" + url.Values{"participant": {ann}}.Encode()
	if code, _ := f.post(bulk, "", url.Values{"action": {"flag"}, "uids": {"1"}}); code != http.StatusBadRequest {
		t.Errorf("a bare uid = %d, want 400", code)
	}
	if flagged("INBOX") || flagged("Projects") {
		t.Fatal("an ambiguous uid changed a message")
	}
	if code, b := f.post(bulk, "", url.Values{"action": {"flag"}, "targets": {"1:Projects"}}); code != http.StatusOK {
		t.Fatalf("bulk = %d %s", code, b)
	}
	if flagged("INBOX") || !flagged("Projects") {
		t.Errorf("INBOX=%v Projects=%v, want only Projects", flagged("INBOX"), flagged("Projects"))
	}
}

func TestSearchWithoutParticipantUnchanged(t *testing.T) {
	f := newViewFixture(t)
	f.deliver(seedMsg{mailbox: "INBOX", guid: "a", from: "ann@x.test", to: []string{"user1@example.com"}, at: 100})
	code, body := f.get("/search?q=hello", "", false)
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if strings.Contains(body, "subj-a") {
		t.Error("a plain search must not list the participant view")
	}
}

// An address that BareAddress accepts but that carries markup or whitespace is
// not a participant: the heading must never render it as HTML.
func TestSearchParticipantHeadingEscapesAndDropsUnsafeAddresses(t *testing.T) {
	f := newViewFixture(t)
	f.deliver(seedMsg{mailbox: "INBOX", guid: "a", from: "ann@x.test", to: []string{"user1@example.com"}, at: 100})
	for name, addr := range map[string]string{
		"markup":     "\"<b>x</b>\"@x.test",
		"whitespace": "\"a b\"@x.test",
		"angle":      "a>b@x.test",
	} {
		if code, _ := f.get(participantPath(addr), "", false); code != http.StatusBadRequest {
			t.Errorf("%s alone: status = %d, want 400", name, code)
		}
	}
	// Next to a good address the unsafe one is dropped, and the heading is escaped.
	code, body := f.get(participantPath("\"<b>x</b>\"@x.test", "ann@x.test"), "", false)
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if strings.Contains(body, "<b>x</b>") || strings.Contains(body, "&lt;b&gt;") {
		t.Error("the unsafe address reached the page")
	}
	if !strings.Contains(body, "Conversations with ann@x.test") {
		t.Errorf("heading lacks the safe address")
	}
}
