package imap

import (
	"testing"

	"github.com/emersion/go-imap/v2"

	"github.com/bongo-go/bongo-go/internal/store"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
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

func TestSessionSubscribeUnsubscribe(t *testing.T) {
	s := &session{}
	if err := s.Subscribe("INBOX"); err != nil {
		t.Errorf("Subscribe() error = %v", err)
	}
	if err := s.Unsubscribe("INBOX"); err != nil {
		t.Errorf("Unsubscribe() error = %v", err)
	}
}

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
}
