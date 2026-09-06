package imap

import (
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func TestFlagsToIMAP(t *testing.T) {
	tests := []struct {
		name  string
		flags uint32
		want  []imap.Flag
	}{
		{"no flags", 0, nil},
		{"seen", store.FlagSeen, []imap.Flag{imap.FlagSeen}},
		{"answered", store.FlagAnswered, []imap.Flag{imap.FlagAnswered}},
		{"flagged", store.FlagFlagged, []imap.Flag{imap.FlagFlagged}},
		{"deleted", store.FlagDeleted, []imap.Flag{imap.FlagDeleted}},
		{"draft", store.FlagDraft, []imap.Flag{imap.FlagDraft}},
		{
			"multiple flags",
			store.FlagSeen | store.FlagFlagged | store.FlagAnswered,
			[]imap.Flag{imap.FlagSeen, imap.FlagAnswered, imap.FlagFlagged},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := flagsToIMAP(tt.flags)
			if len(got) != len(tt.want) {
				t.Errorf("flagsToIMAP(%d) returned %d flags, want %d", tt.flags, len(got), len(tt.want))
				return
			}
			gotSet := make(map[imap.Flag]bool)
			for _, f := range got {
				gotSet[f] = true
			}
			for _, w := range tt.want {
				if !gotSet[w] {
					t.Errorf("flagsToIMAP(%d) missing flag %q", tt.flags, w)
				}
			}
		})
	}
}

func TestImapFlagsToUint32(t *testing.T) {
	tests := []struct {
		name  string
		flags []imap.Flag
		want  uint32
	}{
		{"empty", nil, 0},
		{"seen", []imap.Flag{imap.FlagSeen}, store.FlagSeen},
		{"answered", []imap.Flag{imap.FlagAnswered}, store.FlagAnswered},
		{"multiple", []imap.Flag{imap.FlagSeen, imap.FlagFlagged}, store.FlagSeen | store.FlagFlagged},
		{"unknown flag ignored", []imap.Flag{"\\Unknown"}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := imapFlagsToUint32(tt.flags)
			if got != tt.want {
				t.Errorf("imapFlagsToUint32() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestHasFlag(t *testing.T) {
	tests := []struct {
		name  string
		flags uint32
		flag  imap.Flag
		want  bool
	}{
		{"seen set", store.FlagSeen, imap.FlagSeen, true},
		{"seen not set", 0, imap.FlagSeen, false},
		{"flagged in multi", store.FlagSeen | store.FlagFlagged, imap.FlagFlagged, true},
		{"draft not in multi", store.FlagSeen | store.FlagFlagged, imap.FlagDraft, false},
		{"unknown flag", store.FlagSeen, "\\Custom", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasFlag(tt.flags, tt.flag)
			if got != tt.want {
				t.Errorf("hasFlag(%d, %q) = %v, want %v", tt.flags, tt.flag, got, tt.want)
			}
		})
	}
}

func TestNumSetContains(t *testing.T) {
	t.Run("SeqSet contains", func(t *testing.T) {
		var set imap.SeqSet
		set.AddNum(5)
		if !numSetContains(set, 5, 0) {
			t.Error("expected SeqSet to contain 5")
		}
		if numSetContains(set, 3, 0) {
			t.Error("expected SeqSet to not contain 3")
		}
	})

	t.Run("UIDSet contains", func(t *testing.T) {
		var set imap.UIDSet
		set.AddNum(42)
		if !numSetContains(set, 0, 42) {
			t.Error("expected UIDSet to contain 42")
		}
		if numSetContains(set, 0, 10) {
			t.Error("expected UIDSet to not contain 10")
		}
	})
}

func TestParseAddress(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		mailbox string
		host    string
		dispNm  string
	}{
		{"plain email", "user@host.com", "user", "host.com", ""},
		{"display name", "\"Alice\" <alice@example.com>", "alice", "example.com", "Alice"},
		{"no quotes", "Bob <bob@test.com>", "bob", "test.com", "Bob"},
		{"local only", "localuser", "localuser", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr := parseAddress(tt.input)
			if addr.Mailbox != tt.mailbox {
				t.Errorf("mailbox = %q, want %q", addr.Mailbox, tt.mailbox)
			}
			if addr.Host != tt.host {
				t.Errorf("host = %q, want %q", addr.Host, tt.host)
			}
			if addr.Name != tt.dispNm {
				t.Errorf("name = %q, want %q", addr.Name, tt.dispNm)
			}
		})
	}
}

func TestParseAddressList(t *testing.T) {
	t.Run("empty string", func(t *testing.T) {
		addrs := parseAddressList("")
		if addrs != nil {
			t.Errorf("expected nil for empty, got %v", addrs)
		}
	})

	t.Run("multiple addresses", func(t *testing.T) {
		addrs := parseAddressList("alice@a.com, bob@b.com")
		if len(addrs) != 2 {
			t.Fatalf("expected 2 addresses, got %d", len(addrs))
		}
		if addrs[0].Mailbox != "alice" {
			t.Errorf("first mailbox = %q, want alice", addrs[0].Mailbox)
		}
		if addrs[1].Mailbox != "bob" {
			t.Errorf("second mailbox = %q, want bob", addrs[1].Mailbox)
		}
	})
}

func TestSessionUIDToSeqNum(t *testing.T) {
	s := &session{
		cachedMsgs: []*bongov1.MailboxMessage{
			{Uid: 10},
			{Uid: 20},
			{Uid: 30},
		},
	}

	tests := []struct {
		uid  uint32
		want uint32
	}{
		{10, 1},
		{20, 2},
		{30, 3},
		{99, 0}, // not found
	}

	for _, tt := range tests {
		got := s.uidToSeqNum(tt.uid)
		if got != tt.want {
			t.Errorf("uidToSeqNum(%d) = %d, want %d", tt.uid, got, tt.want)
		}
	}
}

func TestSessionRemoveFromCache(t *testing.T) {
	s := &session{
		cachedMsgs: []*bongov1.MailboxMessage{
			{Uid: 10},
			{Uid: 20},
			{Uid: 30},
		},
	}

	s.removeFromCache(20)
	if len(s.cachedMsgs) != 2 {
		t.Fatalf("expected 2 messages after remove, got %d", len(s.cachedMsgs))
	}
	if s.cachedMsgs[0].Uid != 10 || s.cachedMsgs[1].Uid != 30 {
		t.Errorf("wrong messages after remove: %v", s.cachedMsgs)
	}

	// Remove non-existent UID.
	s.removeFromCache(99)
	if len(s.cachedMsgs) != 2 {
		t.Errorf("remove of non-existent UID changed length")
	}
}

func TestTagsToIMAPKeywords(t *testing.T) {
	tests := []struct {
		name string
		tags []string
		want []imap.Flag
	}{
		{"nil tags", nil, nil},
		{"empty slice", []string{}, nil},
		{"empty string tag is skipped", []string{""}, nil},
		{"single tag gets $ prefix", []string{"work"}, []imap.Flag{"$work"}},
		{"multiple tags", []string{"work", "urgent"}, []imap.Flag{"$work", "$urgent"}},
		{"tag already has $ prefix", []string{"$Forwarded"}, []imap.Flag{"$Forwarded"}},
		{"tag already has backslash prefix", []string{`\Seen`}, []imap.Flag{`\Seen`}},
		{"mixed tags", []string{"personal", "$MDNSent"}, []imap.Flag{"$personal", "$MDNSent"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tagsToIMAPKeywords(tt.tags)
			if len(got) != len(tt.want) {
				t.Errorf("tagsToIMAPKeywords(%v) = %v, want %v", tt.tags, got, tt.want)
				return
			}
			for i, f := range got {
				if f != tt.want[i] {
					t.Errorf("tagsToIMAPKeywords(%v)[%d] = %q, want %q", tt.tags, i, f, tt.want[i])
				}
			}
		})
	}
}

func TestMessageToIMAPFlags(t *testing.T) {
	tests := []struct {
		name  string
		flags uint32
		tags  []string
		want  []imap.Flag
	}{
		{"no flags no tags", 0, nil, nil},
		{"seen flag only", store.FlagSeen, nil, []imap.Flag{imap.FlagSeen}},
		{"tags only no system flags", 0, []string{"work"}, []imap.Flag{"$work"}},
		{
			"seen flag with tag",
			store.FlagSeen,
			[]string{"work"},
			[]imap.Flag{imap.FlagSeen, "$work"},
		},
		{
			"multiple flags and tags",
			store.FlagSeen | store.FlagFlagged,
			[]string{"urgent", "project-x"},
			[]imap.Flag{imap.FlagSeen, imap.FlagFlagged, "$urgent", "$project-x"},
		},
		{
			"tag with existing $ prefix",
			store.FlagAnswered,
			[]string{"$Forwarded"},
			[]imap.Flag{imap.FlagAnswered, "$Forwarded"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := messageToIMAPFlags(tt.flags, tt.tags)
			if len(got) != len(tt.want) {
				t.Errorf("messageToIMAPFlags(%d, %v) = %v, want %v", tt.flags, tt.tags, got, tt.want)
				return
			}
			gotSet := make(map[imap.Flag]bool, len(got))
			for _, f := range got {
				gotSet[f] = true
			}
			for _, f := range tt.want {
				if !gotSet[f] {
					t.Errorf("messageToIMAPFlags(%d, %v) missing flag %q", tt.flags, tt.tags, f)
				}
			}
		})
	}
}

func TestSessionClose(t *testing.T) {
	s := &session{}
	if err := s.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

// TestSessionSubscribeUnsubscribe used to live here. It asserted that
// Subscribe and Unsubscribe on a bare session returned nil, which was only
// true while both were no-ops -- the bug itself. Subscriptions are now
// persisted and are covered over the wire in subscription_e2e_test.go.

func TestSeparateFlags(t *testing.T) {
	tests := []struct {
		name         string
		flags        []imap.Flag
		wantStd      []imap.Flag
		wantKeywords []string
	}{
		{
			name:         "empty",
			flags:        nil,
			wantStd:      nil,
			wantKeywords: nil,
		},
		{
			name:         "standard flags only",
			flags:        []imap.Flag{imap.FlagSeen, imap.FlagFlagged},
			wantStd:      []imap.Flag{imap.FlagSeen, imap.FlagFlagged},
			wantKeywords: nil,
		},
		{
			name:         "keyword only",
			flags:        []imap.Flag{"$work"},
			wantStd:      nil,
			wantKeywords: []string{"work"},
		},
		{
			name:         "mixed standard and keyword",
			flags:        []imap.Flag{imap.FlagSeen, "$work", "$urgent"},
			wantStd:      []imap.Flag{imap.FlagSeen},
			wantKeywords: []string{"work", "urgent"},
		},
		{
			name:         "keyword is lowercased",
			flags:        []imap.Flag{"$Work", "$URGENT"},
			wantStd:      nil,
			wantKeywords: []string{"work", "urgent"},
		},
		{
			name:         "dollar with no name is skipped",
			flags:        []imap.Flag{"$"},
			wantStd:      nil,
			wantKeywords: nil,
		},
		{
			name:         "no-prefix flag is ignored",
			flags:        []imap.Flag{"custom"},
			wantStd:      nil,
			wantKeywords: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotStd, gotKW := separateFlags(tt.flags)

			if len(gotStd) != len(tt.wantStd) {
				t.Errorf("separateFlags stdFlags = %v, want %v", gotStd, tt.wantStd)
			} else {
				for i, f := range gotStd {
					if f != tt.wantStd[i] {
						t.Errorf("separateFlags stdFlags[%d] = %q, want %q", i, f, tt.wantStd[i])
					}
				}
			}

			if len(gotKW) != len(tt.wantKeywords) {
				t.Errorf("separateFlags keywords = %v, want %v", gotKW, tt.wantKeywords)
			} else {
				for i, kw := range gotKW {
					if kw != tt.wantKeywords[i] {
						t.Errorf("separateFlags keywords[%d] = %q, want %q", i, kw, tt.wantKeywords[i])
					}
				}
			}
		})
	}
}

func TestSessionAppendRequiresBlobs(t *testing.T) {
	// Append without blob store should panic (nil pointer), which we recover from.
	s := &session{}
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic from Append with nil blobs")
		}
	}()
	s.Append("INBOX", nil, nil)
}

func TestSessionSearchNoMailbox(t *testing.T) {
	s := &session{}
	_, err := s.Search(0, &imap.SearchCriteria{}, nil)
	if err == nil {
		t.Error("expected error when no mailbox selected")
	}
}

func TestSessionUnselect(t *testing.T) {
	s := &session{
		selectedMailbox: "INBOX",
		cachedMsgs: []*bongov1.MailboxMessage{
			{Uid: 1},
		},
		reportedUIDs: []uint32{1},
	}
	if err := s.Unselect(); err != nil {
		t.Errorf("Unselect() error = %v", err)
	}
	if s.selectedMailbox != "" {
		t.Error("selectedMailbox should be empty after Unselect")
	}
	if s.cachedMsgs != nil {
		t.Error("cachedMsgs should be nil after Unselect")
	}
	// Nothing can currently observe a stale client view across an
	// UNSELECT/CLOSE -- Poll returns early with no mailbox selected, and the
	// next SELECT overwrites it. Pinned anyway so the two views are cleared
	// together, because a future reader of reportedUIDs would inherit a
	// mailbox's worth of UIDs belonging to a mailbox that is no longer open.
	if s.reportedUIDs != nil {
		t.Error("reportedUIDs should be nil after Unselect")
	}
}

func TestEnsureExtendedBodyStructure(t *testing.T) {
	// A single part with no Extended must gain one.
	single := &imap.BodyStructureSinglePart{Type: "text", Subtype: "plain"}
	ensureExtendedBodyStructure(single)
	if single.Extended == nil {
		t.Error("single part Extended still nil after ensureExtendedBodyStructure")
	}

	// A multipart and all of its children must gain Extended recursively.
	multi := &imap.BodyStructureMultiPart{
		Subtype: "mixed",
		Children: []imap.BodyStructure{
			&imap.BodyStructureSinglePart{Type: "text", Subtype: "plain"},
			&imap.BodyStructureSinglePart{Type: "text", Subtype: "html"},
		},
	}
	ensureExtendedBodyStructure(multi)
	if multi.Extended == nil {
		t.Error("multipart Extended still nil")
	}
	for i, child := range multi.Children {
		sp, ok := child.(*imap.BodyStructureSinglePart)
		if !ok || sp.Extended == nil {
			t.Errorf("child %d Extended still nil", i)
		}
	}
}

func TestBuildBodyStructureIsWritableAsExtended(t *testing.T) {
	// A message that produced the production panic: any built structure, once
	// passed through ensureExtendedBodyStructure, must have Extended populated
	// so go-imap can write it for a BODYSTRUCTURE (extended) request.
	raw := []byte("Subject: t\r\nContent-Type: text/plain\r\n\r\nhello\r\n")
	s := &session{}
	bs := s.buildBodyStructure(raw)
	ensureExtendedBodyStructure(bs)
	if sp, ok := bs.(*imap.BodyStructureSinglePart); ok {
		if sp.Extended == nil {
			t.Error("built single-part structure missing Extended")
		}
	}
}

// imapDirectApplier applies Raft commands straight to Pebble, mirroring the
// FSM's dispatch so synthetic mailbox commands are interpreted rather than
// written through as literal keys.
type imapDirectApplier struct{ store *store.PebbleStore }

func (a *imapDirectApplier) ApplyCommand(cmd *bongov1.RaftCommand, _ time.Duration) error {
	if cmd.Op == bongov1.Op_OP_PUT && store.IsMailboxSyntheticCommand(cmd.Key) {
		_, err := store.ApplyMailboxCommand(a.store, cmd)
		return err
	}
	switch cmd.Op {
	case bongov1.Op_OP_PUT:
		return a.store.Set(cmd.Key, cmd.Value)
	case bongov1.Op_OP_DELETE:
		return a.store.Delete(cmd.Key)
	}
	return nil
}

// newReadOnlyTestSession builds a session over a real MailboxStore holding one
// message in INBOX, opened either read-only (EXAMINE) or read-write (SELECT).
func newReadOnlyTestSession(t *testing.T, readOnly bool) *session {
	t.Helper()

	ps, err := store.NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })

	ms := store.NewMailboxStore(ps, &imapDirectApplier{store: ps})
	if err := ms.CreateMailbox("u1", "INBOX"); err != nil {
		t.Fatalf("CreateMailbox INBOX: %v", err)
	}
	if err := ms.CreateMailbox("u1", "Archive"); err != nil {
		t.Fatalf("CreateMailbox Archive: %v", err)
	}
	if _, err := ms.DeliverMessage("u1", "INBOX", &bongov1.MailMeta{
		Guid:       "guid-1",
		Subject:    "hello",
		ReceivedAt: time.Now().Unix(),
	}, 0); err != nil {
		t.Fatalf("DeliverMessage: %v", err)
	}

	msgs, err := ms.ListMessages("u1", "INBOX")
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}

	return &session{
		mailboxes:       ms,
		logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		userID:          "u1",
		selectedMailbox: "INBOX",
		readOnly:        readOnly,
		cachedMsgs:      msgs,
	}
}

// errMutationAttempted stands in for a panic. These commands are called with
// nil response writers, which is fine while the read-only guard returns first
// -- and panics the moment it does not. Recovering turns that into an ordinary
// failure, so each case is judged on "was the command refused?" rather than on
// whether a nil writer happened to blow up, and one case cannot abort the
// others by taking the process down.
var errMutationAttempted = errors.New("mutation was attempted rather than refused")

func callAndRecover(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errMutationAttempted
		}
	}()
	return fn()
}

// TestReadOnlyMailboxRefusesMutations covers EXAMINE.
//
// RFC 3501 6.3.2: EXAMINE is identical to SELECT except that no changes to the
// mailbox's permanent state are permitted. s.readOnly recorded which command
// opened the mailbox but was consulted in exactly one place -- suppressing the
// implicit \Seen on FETCH -- so STORE, EXPUNGE and MOVE all mutated a mailbox
// the client had been told was read-only. go-imap does not enforce it either;
// the library only emits the READ-ONLY code at select time.
func TestReadOnlyMailboxRefusesMutations(t *testing.T) {
	uidSet := imap.UIDSet{}
	uidSet.AddNum(1)

	tests := []struct {
		name string
		call func(s *session) error
	}{
		{"STORE", func(s *session) error {
			return s.Store(nil, uidSet, &imap.StoreFlags{
				Op:    imap.StoreFlagsAdd,
				Flags: []imap.Flag{imap.FlagSeen},
			}, nil)
		}},
		{"EXPUNGE", func(s *session) error {
			return s.Expunge(nil, &uidSet)
		}},
		{"MOVE", func(s *session) error {
			return s.Move(nil, uidSet, "Archive")
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name+" is refused on a read-only mailbox", func(t *testing.T) {
			s := newReadOnlyTestSession(t, true)
			err := callAndRecover(func() error { return tt.call(s) })
			if err == nil {
				t.Fatalf("%s succeeded against a mailbox opened with EXAMINE", tt.name)
			}
			if !errors.Is(err, errMailboxReadOnly) {
				t.Errorf("%s error = %v, want the read-only refusal", tt.name, err)
			}
			// The message must still be there, unmodified.
			msgs, lerr := s.mailboxes.ListMessages("u1", "INBOX")
			if lerr != nil {
				t.Fatalf("ListMessages: %v", lerr)
			}
			if len(msgs) != 1 {
				t.Errorf("INBOX holds %d messages after a refused %s, want 1", len(msgs), tt.name)
			}
		})
	}
}

// TestReadWriteMailboxStillAllowsMutations is the control. Without it, the
// guards above would still pass if they refused every session rather than only
// read-only ones -- which would break ordinary IMAP clients entirely.
func TestReadWriteMailboxStillAllowsMutations(t *testing.T) {
	uidSet := imap.UIDSet{}
	uidSet.AddNum(1)

	s := newReadOnlyTestSession(t, false)
	if err := callAndRecover(func() error { return s.Expunge(nil, &uidSet) }); errors.Is(err, errMailboxReadOnly) {
		t.Error("EXPUNGE was refused as read-only on a mailbox opened with SELECT")
	}
}

// TestCopyIsAllowedFromReadOnlyMailbox pins the deliberate asymmetry with MOVE.
// COPY only writes to the destination and leaves the selected mailbox
// untouched, which EXAMINE permits; MOVE deletes from the source (RFC 6851) and
// must be refused. Gating COPY too would be a plausible-looking overreach that
// breaks a legal client operation.
func TestCopyIsAllowedFromReadOnlyMailbox(t *testing.T) {
	uidSet := imap.UIDSet{}
	uidSet.AddNum(1)

	s := newReadOnlyTestSession(t, true)
	err := callAndRecover(func() error { _, e := s.Copy(uidSet, "Archive"); return e })
	if errors.Is(err, errMailboxReadOnly) {
		t.Error("COPY from a read-only mailbox was refused; it writes only to the " +
			"destination and is permitted under EXAMINE")
	}
}
