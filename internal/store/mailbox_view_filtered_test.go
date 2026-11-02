package store

import (
	"reflect"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// The predicate runs before paging: a page of 2 out of the unread messages is
// the 2 newest unread ones, not the unread ones among the 2 newest.
func TestListViewMessagesFilteredAppliesPredicateBeforePaging(t *testing.T) {
	ms := newMailboxTestEnv(t)
	if err := ms.CreateMailbox("u1", "INBOX"); err != nil {
		t.Fatal(err)
	}
	set := map[string]bool{}
	for i, g := range []string{"a", "b", "c", "d", "e"} {
		deliverAt(t, ms, "u1", "INBOX", g, int64(100+i))
		set[g] = true
	}
	// Mark the two newest (d, e) as read.
	msgs, err := ms.ListMessages("u1", "INBOX")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs {
		if m.MailGuid == "d" || m.MailGuid == "e" {
			if err := ms.AddFlags("u1", "INBOX", m.Uid, FlagSeen); err != nil {
				t.Fatal(err)
			}
		}
	}
	unread := func(m *bongov1.MailboxMessage) bool { return m.Flags&FlagSeen == 0 }

	got, _, total, err := ms.ListViewMessagesFiltered("u1", set, unread, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	var guids []string
	for _, m := range got {
		guids = append(guids, m.MailGuid)
	}
	if want := []string{"c", "b"}; !reflect.DeepEqual(guids, want) || total != 3 {
		t.Fatalf("got %v (total %d), want %v (total 3)", guids, total, want)
	}

	// A nil predicate is ListViewMessages.
	all, _, allTotal, _ := ms.ListViewMessagesFiltered("u1", set, nil, 0, 10)
	if len(all) != 5 || allTotal != 5 {
		t.Fatalf("nil predicate kept %d (total %d), want 5", len(all), allTotal)
	}
}
