package imap

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func TestCreateExistingIsAlreadyExists(t *testing.T) {
	for _, name := range []string{"INBOX", "inbox", "Archive"} {
		t.Run(name, func(t *testing.T) {
			s := newReadOnlyTestSession(t, false)
			assertIMAPCode(t, s.Create(name, nil), imap.ResponseCodeAlreadyExists)
		})
	}

	t.Run("new name succeeds", func(t *testing.T) {
		s := newReadOnlyTestSession(t, false)
		if err := s.Create("Fresh", nil); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if _, err := s.mailboxes.GetMailbox("u1", "Fresh"); err != nil {
			t.Fatalf("mailbox not created: %v", err)
		}
	})
}

func TestCreateLookupFailureIsNotAlreadyExists(t *testing.T) {
	s := newCorruptTestSession(t)
	err := s.Create("Bad", nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	var ie *imap.Error
	if errors.As(err, &ie) {
		t.Fatalf("lookup failure reported as IMAP status %s [%s]", ie.Type, ie.Code)
	}
}

// mailboxState renders a mailbox's meta and messages so before/after can be
// compared exactly.
func mailboxState(t *testing.T, s *session, name string) string {
	t.Helper()
	meta, err := s.mailboxes.GetMailbox(s.userID, name)
	if err != nil {
		t.Fatalf("GetMailbox %s: %v", name, err)
	}
	msgs, err := s.mailboxes.ListMessages(s.userID, name)
	if err != nil {
		t.Fatalf("ListMessages %s: %v", name, err)
	}
	var out strings.Builder
	fmt.Fprintf(&out, "next=%d validity=%d unsub=%v;", meta.UidNext, meta.UidValidity, meta.Unsubscribed)
	for _, m := range msgs {
		fmt.Fprintf(&out, "%d=%s,", m.Uid, m.MailGuid)
	}
	return out.String()
}

func TestRenameOntoExistingIsAlreadyExists(t *testing.T) {
	s := newReadOnlyTestSession(t, false)
	for _, name := range []string{"A", "B"} {
		if err := s.mailboxes.CreateMailbox("u1", name); err != nil {
			t.Fatalf("CreateMailbox %s: %v", name, err)
		}
		if _, err := s.mailboxes.DeliverMessage("u1", name, &bongov1.MailMeta{Guid: "g-" + name, Subject: name}, 0); err != nil {
			t.Fatalf("DeliverMessage %s: %v", name, err)
		}
	}
	names := []string{"INBOX", "A", "B"}
	before := map[string]string{}
	for _, n := range names {
		before[n] = mailboxState(t, s, n)
	}

	for _, target := range []string{"B", "A", "INBOX", "inbox"} {
		t.Run(target, func(t *testing.T) {
			assertIMAPCode(t, s.Rename("A", target, nil), imap.ResponseCodeAlreadyExists)
			for _, n := range names {
				if got := mailboxState(t, s, n); got != before[n] {
					t.Errorf("%s changed: before %q after %q", n, before[n], got)
				}
			}
		})
	}
}

func TestUnsubscribeNonexistentSucceeds(t *testing.T) {
	s := newReadOnlyTestSession(t, false)
	if err := s.Unsubscribe("Notes"); err != nil {
		t.Fatalf("Unsubscribe of missing mailbox: %v", err)
	}
	// SUBSCRIBE keeps its NONEXISTENT answer.
	assertIMAPCode(t, s.Subscribe("Notes"), imap.ResponseCodeNonExistent)
}
