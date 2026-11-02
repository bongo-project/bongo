package store

import (
	"reflect"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func deliverAt(t *testing.T, ms *MailboxStore, user, mailbox, guid string, at int64) {
	t.Helper()
	meta := &bongov1.MailMeta{Guid: guid, Subject: guid, ReceivedAt: at}
	if _, err := ms.DeliverMessage(user, mailbox, meta, 0); err != nil {
		t.Fatalf("DeliverMessage %s/%s: %v", mailbox, guid, err)
	}
}

func TestListViewMessagesExcludesAndSortsAcrossFolders(t *testing.T) {
	ms := newMailboxTestEnv(t)
	for _, name := range []string{"INBOX", "Projects", "Archive", "Junk", "Trash", "Sent", "Drafts", "Trash/Old"} {
		if err := ms.CreateMailbox("u1", name); err != nil {
			t.Fatal(err)
		}
	}
	// Scan order is INBOX, Projects, ...: dates deliberately do not follow it.
	deliverAt(t, ms, "u1", "INBOX", "inbox-old", 100)
	deliverAt(t, ms, "u1", "Projects", "proj-new", 500)
	deliverAt(t, ms, "u1", "Archive", "arch-mid", 300)
	deliverAt(t, ms, "u1", "INBOX", "inbox-mid", 400)
	deliverAt(t, ms, "u1", "Junk", "junk", 900)
	deliverAt(t, ms, "u1", "Trash", "trash", 901)
	deliverAt(t, ms, "u1", "Sent", "sent", 902)
	deliverAt(t, ms, "u1", "Drafts", "drafts", 903)
	deliverAt(t, ms, "u1", "Trash/Old", "trash-sub", 904)
	deliverAt(t, ms, "u1", "INBOX", "not-selected", 950)

	set := map[string]bool{}
	for _, g := range []string{"inbox-old", "proj-new", "arch-mid", "inbox-mid", "junk", "trash", "sent", "drafts", "trash-sub"} {
		set[g] = true
	}
	msgs, boxes, total, err := ms.ListViewMessages("u1", set, 0, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for i, m := range msgs {
		got = append(got, boxes[i]+"/"+m.MailGuid)
	}
	want := []string{"Projects/proj-new", "INBOX/inbox-mid", "Archive/arch-mid", "INBOX/inbox-old"}
	if !reflect.DeepEqual(got, want) || total != 4 {
		t.Fatalf("got %v (total %d), want %v", got, total, want)
	}

	// Paging keeps the order and the total.
	msgs, boxes, total, _ = ms.ListViewMessages("u1", set, 1, 2)
	if total != 4 || len(msgs) != 2 || msgs[0].MailGuid != "inbox-mid" || boxes[1] != "Archive" {
		t.Fatalf("page = %v %v total %d", msgs, boxes, total)
	}
	if msgs, _, total, _ = ms.ListViewMessages("u1", set, 10, 2); msgs != nil || total != 4 {
		t.Fatalf("past the end = %v, total %d", msgs, total)
	}
	if msgs, _, total, _ = ms.ListViewMessages("u1", nil, 0, 10); msgs != nil || total != 0 {
		t.Fatalf("empty set = %v, total %d", msgs, total)
	}
}

func TestListViewMessagesSameUIDInTwoFolders(t *testing.T) {
	ms := newMailboxTestEnv(t)
	for _, name := range []string{"INBOX", "Projects"} {
		if err := ms.CreateMailbox("u1", name); err != nil {
			t.Fatal(err)
		}
	}
	deliverAt(t, ms, "u1", "INBOX", "a", 100)
	deliverAt(t, ms, "u1", "Projects", "b", 100)
	msgs, boxes, _, err := ms.ListViewMessages("u1", map[string]bool{"a": true, "b": true}, 0, 10)
	if err != nil || len(msgs) != 2 {
		t.Fatalf("msgs = %v, %v", msgs, err)
	}
	if msgs[0].Uid != 1 || msgs[1].Uid != 1 || boxes[0] == boxes[1] {
		t.Fatalf("both messages must carry UID 1 and their own mailbox: %v %v", msgs, boxes)
	}
	// A tie in date resolves to a fixed order, not scan luck.
	if boxes[0] != "INBOX" || boxes[1] != "Projects" {
		t.Errorf("tie order = %v", boxes)
	}
}

func TestContactAddresses(t *testing.T) {
	dir := t.TempDir()
	ps, err := NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	cs := NewContactStore(ps, &directApplier{store: ps})

	if got, err := cs.Addresses("u1"); err != nil || len(got) != 0 {
		t.Fatalf("no contacts: %v, %v", got, err)
	}
	vcard := "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Two\r\nEMAIL;TYPE=work:Work@Example.com\r\nemail:home@example.com\r\nEMAILX:nope@example.com\r\nNOTE:email:fake@example.com\r\nEND:VCARD\r\n"
	for _, c := range []*bongov1.Contact{
		{DisplayName: "One", Email: "  One@Example.com "},
		{DisplayName: "Two", Email: "two@example.com", VcardRaw: []byte(vcard)},
		{DisplayName: "NoMail"},
	} {
		if _, err := cs.Create("u1", c); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := cs.Create("u2", &bongov1.Contact{Email: "other-user@example.com"}); err != nil {
		t.Fatal(err)
	}
	got, err := cs.Addresses("u1")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"one@example.com": true, "two@example.com": true, "work@example.com": true, "home@example.com": true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("addresses = %v, want %v", got, want)
	}
}

func TestUserAddresses(t *testing.T) {
	m, err := NewMetadataManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	db := m.System.DB()
	for _, q := range []string{
		`INSERT INTO users (id, username) VALUES ('u1', 'Me@Example.com'), ('u2', 'you@example.com')`,
		`INSERT INTO addresses (address, user_id, kind) VALUES ('alias@example.com', 'u1', 'alias'), ('theirs@example.com', 'u2', 'alias')`,
		`INSERT INTO aliases (address, target_user_id, enabled) VALUES ('legacy@example.com', 'u1', 1), ('off@example.com', 'u1', 0)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	got, err := m.UserAddresses("u1")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"me@example.com": true, "alias@example.com": true, "legacy@example.com": true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("u1 addresses = %v, want %v", got, want)
	}
	if got, _ := m.UserAddresses("nobody"); len(got) != 0 {
		t.Errorf("unknown user addresses = %v", got)
	}
}
