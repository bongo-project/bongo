package handlers

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// The web mbox import must record Cc and List-Id in MailMeta, as delivery does.
func TestImportMboxRecordsCcAndListID(t *testing.T) {
	const user, email = "u-import-hdr", "me@example.test"
	dir := t.TempDir()
	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	bs, err := store.NewBlobStore(dir + "/blobs")
	if err != nil {
		t.Fatal(err)
	}
	mailboxes := store.NewMailboxStore(ps, &directApplierTags{ps: ps})
	if err := mailboxes.CreateMailbox(user, "INBOX"); err != nil {
		t.Fatal(err)
	}
	h := NewMailHandler(ps, bs, mailboxes, discardLogger())

	mbox := "From a@example.test Mon Jan  1 00:00:00 2024\nFrom: a@example.test\nTo: other@example.test\n" +
		"Cc: Me <me@example.test>\nList-Id: Go Users <Go-Users.Example.org>\nSubject: s\n\nbody\n"
	req := withUserContext(uploadReq(t, "/import", "mbox", []byte(mbox), map[string]string{"mailbox": "INBOX"}), user, email)
	w := httptest.NewRecorder()
	h.ImportMbox(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	msgs, err := mailboxes.ListMessages(user, "INBOX")
	if err != nil || len(msgs) != 1 {
		t.Fatalf("messages = %v, %v", msgs, err)
	}
	val, err := ps.Get([]byte("/user/" + user + "/mail/" + msgs[0].GetMailGuid()))
	if err != nil {
		t.Fatal(err)
	}
	var meta bongov1.MailMeta
	if err := proto.Unmarshal(val, &meta); err != nil {
		t.Fatal(err)
	}
	if want := []string{"Me <me@example.test>"}; !slices.Equal(meta.Cc, want) {
		t.Errorf("Cc = %q, want %q", meta.Cc, want)
	}
	if meta.ListId != "Go Users <Go-Users.Example.org>" {
		t.Errorf("ListId = %q", meta.ListId)
	}
}
