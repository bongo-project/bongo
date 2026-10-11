package imap

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"

	"github.com/bongo-project/bongo/internal/store"
)

func TestMailboxLookupError(t *testing.T) {
	cause := errors.New("disk on fire")
	notFound := fmt.Errorf("mailbox %q: %w", "Notes", store.ErrNotFound)

	tests := []struct {
		name     string
		target   *imap.Error
		wantCode imap.ResponseCode
	}{
		{"select", errNoSuchMailbox, imap.ResponseCodeNonExistent},
		{"trycreate", errTryCreate, imap.ResponseCodeTryCreate},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := mailboxLookupError("Notes", notFound, tt.target)
			var ie *imap.Error
			if !errors.As(err, &ie) {
				t.Fatalf("got %v, want *imap.Error", err)
			}
			if ie.Type != imap.StatusResponseTypeNo || ie.Code != tt.wantCode {
				t.Errorf("got type %q code %q, want NO %q", ie.Type, ie.Code, tt.wantCode)
			}
			if strings.Contains(ie.Text, "Notes") {
				t.Errorf("status text echoes the client-supplied name: %q", ie.Text)
			}
		})
	}

	for _, target := range []*imap.Error{errNoSuchMailbox, errTryCreate} {
		err := mailboxLookupError("Notes", cause, target)
		var ie *imap.Error
		if errors.As(err, &ie) {
			t.Errorf("store failure was reported as an IMAP status: %v", ie)
		}
		if !errors.Is(err, cause) {
			t.Errorf("cause lost from %v", err)
		}
	}
}

func assertIMAPCode(t *testing.T, err error, code imap.ResponseCode) {
	t.Helper()
	var ie *imap.Error
	if !errors.As(err, &ie) {
		t.Fatalf("got %v (%T), want *imap.Error", err, err)
	}
	if ie.Type != imap.StatusResponseTypeNo || ie.Code != code {
		t.Fatalf("got %s [%s], want NO [%s]", ie.Type, ie.Code, code)
	}
}

func assertNotMissingStatus(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	var ie *imap.Error
	if errors.As(err, &ie) && (ie.Code == imap.ResponseCodeNonExistent || ie.Code == imap.ResponseCodeTryCreate) {
		t.Fatalf("store failure reported as %s: %v", ie.Code, err)
	}
}

func TestMissingMailboxIsNonexistent(t *testing.T) {
	tests := []struct {
		name string
		call func(s *session) error
	}{
		{"select", func(s *session) error { _, e := s.Select("Notes", nil); return e }},
		{"examine", func(s *session) error {
			_, e := s.Select("Notes", &imap.SelectOptions{ReadOnly: true})
			return e
		}},
		{"status", func(s *session) error {
			_, e := s.Status("Notes", &imap.StatusOptions{NumMessages: true})
			return e
		}},
		{"subscribe", func(s *session) error { return s.Subscribe("Notes") }},
		{"unsubscribe", func(s *session) error { return s.Unsubscribe("Notes") }},
		{"rename", func(s *session) error { return s.Rename("Notes", "Other", nil) }},
		{"delete", func(s *session) error { return s.Delete("Notes") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newReadOnlyTestSession(t, false)
			assertIMAPCode(t, tt.call(s), imap.ResponseCodeNonExistent)
		})
	}
}

func TestMissingDestinationIsTryCreate(t *testing.T) {
	uidSet := imap.UIDSetNum(1)
	t.Run("copy", func(t *testing.T) {
		s := newReadOnlyTestSession(t, false)
		_, err := s.Copy(uidSet, "Nowhere")
		assertIMAPCode(t, err, imap.ResponseCodeTryCreate)
	})
	t.Run("move", func(t *testing.T) {
		s := newReadOnlyTestSession(t, false)
		assertIMAPCode(t, s.Move(nil, uidSet, "Nowhere"), imap.ResponseCodeTryCreate)
	})
	t.Run("append", func(t *testing.T) {
		s := newReadOnlyTestSession(t, false)
		blobs, err := store.NewBlobStore(t.TempDir())
		if err != nil {
			t.Fatalf("NewBlobStore: %v", err)
		}
		s.blobs = blobs
		_, err = s.Append("Nowhere", nil, nil)
		assertIMAPCode(t, err, imap.ResponseCodeTryCreate)
		if _, err := s.mailboxes.GetMailbox("u1", "Nowhere"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("APPEND created the missing mailbox: %v", err)
		}
	})
}

// newCorruptTestSession returns a session whose store holds a mailbox "Bad"
// with an undecodable meta record. The key exists, so only the error mapping
// can tell this apart from a missing mailbox.
func newCorruptTestSession(t *testing.T) *session {
	t.Helper()
	ps, err := store.NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	ms := newDirectMailboxStore(ps)
	if err := ms.CreateMailbox("u1", "INBOX"); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	if err := ps.Set([]byte("/user/u1/mailbox/Bad/meta"), []byte{0xff, 0xff}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if _, err := ms.GetMailbox("u1", "Bad"); err == nil || errors.Is(err, store.ErrNotFound) {
		t.Fatalf("corrupt fixture should fail with a non-not-found error, got %v", err)
	}
	return &session{
		mailboxes:       ms,
		logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		userID:          "u1",
		selectedMailbox: "INBOX",
	}
}

func TestCorruptMailboxMetaIsNotNonexistent(t *testing.T) {
	tests := []struct {
		name string
		call func(s *session) error
	}{
		{"select", func(s *session) error { _, e := s.Select("Bad", nil); return e }},
		{"status", func(s *session) error {
			_, e := s.Status("Bad", &imap.StatusOptions{NumMessages: true})
			return e
		}},
		{"subscribe", func(s *session) error { return s.Subscribe("Bad") }},
		{"rename", func(s *session) error { return s.Rename("Bad", "Other", nil) }},
		{"delete", func(s *session) error { return s.Delete("Bad") }},
		{"copy", func(s *session) error { _, e := s.Copy(imap.UIDSetNum(1), "Bad"); return e }},
		{"move", func(s *session) error { return s.Move(nil, imap.UIDSetNum(1), "Bad") }},
		{"append", func(s *session) error { _, e := s.Append("Bad", nil, nil); return e }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newCorruptTestSession(t)
			assertNotMissingStatus(t, tt.call(s))
		})
	}
}

func TestExistingMailboxStillWorks(t *testing.T) {
	s := newReadOnlyTestSession(t, false)
	if _, err := s.Select("INBOX", nil); err != nil {
		t.Errorf("Select: %v", err)
	}
	if _, err := s.Status("INBOX", &imap.StatusOptions{NumMessages: true}); err != nil {
		t.Errorf("Status: %v", err)
	}
	if err := s.Subscribe("Archive"); err != nil {
		t.Errorf("Subscribe: %v", err)
	}
	if _, err := s.Copy(imap.UIDSetNum(1), "Archive"); err != nil {
		t.Errorf("Copy: %v", err)
	}
	if err := s.Rename("Archive", "Archive2", nil); err != nil {
		t.Errorf("Rename: %v", err)
	}
	if err := s.Delete("Archive2"); err != nil {
		t.Errorf("Delete: %v", err)
	}
}

// TestSelectNonexistentDoesNotLogServerError is the Apple Notes poll end to
// end: SELECT "Notes" on a mailbox that does not exist must be a clean tagged
// NO [NONEXISTENT], and go-imap must not log it as a handler failure.
func TestSelectNonexistentDoesNotLogServerError(t *testing.T) {
	var out syncBuffer
	// imap.command_error is emitted at DEBUG; a higher level would make the
	// absence assertion vacuous.
	logger := slog.New(slog.NewJSONHandler(&out, &slog.HandlerOptions{Level: slog.LevelDebug}))

	ms, ps := newPollTestStore(t, "INBOX")
	addr := startTestServerWithLogger(t, ms, ps, logger)
	c := dialPollTestClient(t, addr, nil)

	_, err := c.Select("Notes", nil).Wait()
	assertIMAPCode(t, err, imap.ResponseCodeNonExistent)

	// A round trip after the SELECT ensures the server has finished handling
	// (and logging) it.
	if err := c.Noop().Wait(); err != nil {
		t.Fatalf("NOOP: %v", err)
	}
	if got := out.String(); strings.Contains(got, "imap.command_error") || strings.Contains(got, "handling SELECT") {
		t.Errorf("not-found SELECT was logged as a server error:\n%s", got)
	}

	// COPY to a missing destination over the wire.
	deliverPollTestMessage(t, ms, "INBOX", "guid-1")
	if _, err := c.Select("INBOX", nil).Wait(); err != nil {
		t.Fatalf("SELECT INBOX: %v", err)
	}
	_, err = c.Copy(imap.UIDSetNum(1), "Nowhere").Wait()
	assertIMAPCode(t, err, imap.ResponseCodeTryCreate)

	// Positive control: a real store failure must still reach the log, proving
	// the sink captures imap.command_error at this level.
	if err := ps.Set([]byte("/user/"+pollTestUser+"/mailbox/Bad/meta"), []byte{0xff, 0xff}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if _, err := c.Select("Bad", nil).Wait(); err == nil {
		t.Fatal("SELECT of a corrupt mailbox succeeded")
	}
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(out.String(), "imap.command_error") {
		if time.Now().After(deadline) {
			t.Fatalf("positive control: no imap.command_error captured:\n%s", out.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
