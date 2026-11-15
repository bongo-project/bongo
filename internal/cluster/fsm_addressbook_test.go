package cluster

import (
	"bytes"
	"io"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// TestFSMSnapshotKeepsAddressBooks proves a backup and restore carries the book
// record and the contact's addressbook_id. Both ride the plain key copy, so the
// test guards against a future filter that would treat either specially.
func TestFSMSnapshotKeepsAddressBooks(t *testing.T) {
	env := newFSMTestEnv(t)

	const bookID = "0123456789abcdef0123456789abcdef"
	bookVal, err := proto.Marshal(&bongov1.AddressBook{Id: bookID, Name: "Work", CreatedAt: 1, UpdatedAt: 2})
	if err != nil {
		t.Fatal(err)
	}
	contactVal, err := proto.Marshal(&bongov1.Contact{Guid: "c1", DisplayName: "Ada", AddressbookId: bookID})
	if err != nil {
		t.Fatal(err)
	}
	for i, kv := range []struct {
		key string
		val []byte
	}{
		{"/user/u1/addressbook/" + bookID, bookVal},
		{"/user/u1/contact/c1", contactVal},
	} {
		env.fsm.Apply(makeProtoLog(uint64(i+1), &bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: []byte(kv.key), Value: kv.val}))
	}

	snap, err := env.fsm.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Release()
	var buf bytes.Buffer
	if err := snap.Persist(&bufferSink{buf: &buf}); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatal(err)
	}
	defer ps.Close()
	bs, err := store.NewBlobStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	mm, err := store.NewMetadataManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer mm.Close()
	if err := NewFSM(ps, bs, mm, testLogger()).Restore(io.NopCloser(&buf)); err != nil {
		t.Fatal(err)
	}

	books := store.NewAddressBookStore(ps, nil)
	book, err := books.Get("u1", bookID)
	if err != nil || book.Name != "Work" {
		t.Fatalf("restored book = %v, %v", book, err)
	}
	contacts := store.NewContactStore(ps, nil)
	c, err := contacts.Get("u1", "c1")
	if err != nil || c.AddressbookId != bookID {
		t.Fatalf("restored contact = %v, %v; want addressbook_id %s", c, err, bookID)
	}
	if inBook, _, _ := contacts.ListByBook("u1", bookID, 0, 0); len(inBook) != 1 {
		t.Fatalf("restored book lists %d contacts, want 1", len(inBook))
	}
}
