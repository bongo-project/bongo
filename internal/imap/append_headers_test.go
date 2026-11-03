package imap

import (
	"slices"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// APPEND is the path bongo-import uses. The MailMeta it stores must carry Cc
// and List-Id, or imported list mail never reaches the Mailing lists view and
// Cc-to-me mail never reaches To me.
func TestAppendRecordsCcAndListID(t *testing.T) {
	e := newQuotaEnv(t, nil)
	msg := []byte("From: a@example.test\r\nTo: other@example.test,\r\n  second@example.test\r\n" +
		"Cc: Me <me@example.test>, \r\nList-Id: Go Users <Go-Users.Example.org>\r\nSubject: s\r\n\r\nbody\r\n")
	if _, err := e.sess.Append("INBOX", newLiteral(msg), nil); err != nil {
		t.Fatalf("Append: %v", err)
	}
	msgs, err := e.mailboxs.ListMessages("u1", "INBOX")
	if err != nil || len(msgs) != 1 {
		t.Fatalf("messages = %v, %v", msgs, err)
	}
	val, err := e.ps.Get([]byte("/user/u1/mail/" + msgs[0].GetMailGuid()))
	if err != nil {
		t.Fatal(err)
	}
	var meta bongov1.MailMeta
	if err := proto.Unmarshal(val, &meta); err != nil {
		t.Fatal(err)
	}
	if want := []string{"other@example.test", "second@example.test"}; !slices.Equal(meta.To, want) {
		t.Errorf("To = %q, want %q", meta.To, want)
	}
	if want := []string{"Me <me@example.test>"}; !slices.Equal(meta.Cc, want) {
		t.Errorf("Cc = %q, want %q", meta.Cc, want)
	}
	if meta.ListId != "Go Users <Go-Users.Example.org>" {
		t.Errorf("ListId = %q", meta.ListId)
	}
	v := store.MailHeaderFields(&meta, map[string]bool{"me@example.test": true})
	if !v.ToMe || v.ListID != "go-users.example.org" {
		t.Errorf("derived view columns = %+v", v)
	}
}
