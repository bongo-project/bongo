package importer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
)

func TestParseDateHeader(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want time.Time
	}{
		{
			"RFC1123Z",
			"Date: Mon, 02 Jan 2006 15:04:05 -0700\r\n\r\nBody",
			time.Date(2006, 1, 2, 15, 4, 5, 0, time.FixedZone("", -7*3600)),
		},
		{
			"no date header",
			"Subject: Test\r\n\r\nBody",
			time.Time{},
		},
		{
			"invalid date",
			"Date: not-a-date\r\n\r\nBody",
			time.Time{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseDateHeader(tt.raw)
			if !got.Equal(tt.want) {
				t.Errorf("parseDateHeader() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMaildirFlags(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		want     []imap.Flag
	}{
		{"seen", "123456.hostname:2,S", []imap.Flag{imap.FlagSeen}},
		{"multiple", "123456.hostname:2,FRS", []imap.Flag{imap.FlagFlagged, imap.FlagAnswered, imap.FlagSeen}},
		{"no flags section", "123456.hostname", nil},
		{"draft", "123456.hostname:2,D", []imap.Flag{imap.FlagDraft}},
		{"trashed", "123456.hostname:2,T", []imap.Flag{imap.FlagDeleted}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := maildirFlags(tt.filename)
			if len(got) != len(tt.want) {
				t.Errorf("maildirFlags(%q) = %v, want %v", tt.filename, got, tt.want)
				return
			}
			gotSet := make(map[imap.Flag]bool)
			for _, f := range got {
				gotSet[f] = true
			}
			for _, w := range tt.want {
				if !gotSet[w] {
					t.Errorf("maildirFlags(%q) missing flag %q", tt.filename, w)
				}
			}
		})
	}
}

func TestMboxSource(t *testing.T) {
	// Create a temp mbox file.
	dir := t.TempDir()
	mboxPath := filepath.Join(dir, "test.mbox")

	content := `From sender@example.com Mon Jan 02 15:04:05 2006
From: sender@example.com
To: recipient@example.com
Subject: First message
Date: Mon, 02 Jan 2006 15:04:05 -0700

Hello, this is the first message.
From sender2@example.com Tue Jan 03 10:00:00 2006
From: sender2@example.com
To: recipient@example.com
Subject: Second message
Date: Tue, 03 Jan 2006 10:00:00 -0700

This is the second message.
`
	os.WriteFile(mboxPath, []byte(content), 0o644)

	src := NewMboxSource([]string{mboxPath}, "INBOX")

	folders, err := src.Folders()
	if err != nil {
		t.Fatalf("Folders: %v", err)
	}
	if len(folders) != 1 || folders[0] != "INBOX" {
		t.Errorf("Folders = %v, want [INBOX]", folders)
	}

	msgCh, errCh := src.Messages(context.Background(), "INBOX")
	var msgs []Message
	for msg := range msgCh {
		msgs = append(msgs, msg)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Messages error: %v", err)
		}
	default:
	}

	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}

	// Check first message has correct subject in raw.
	if got := string(msgs[0].Raw); !contains(got, "Subject: First message") {
		t.Errorf("first message missing subject header")
	}
	if got := string(msgs[1].Raw); !contains(got, "Subject: Second message") {
		t.Errorf("second message missing subject header")
	}
}

func TestMaildirSource(t *testing.T) {
	dir := t.TempDir()

	// Create Maildir structure.
	os.MkdirAll(filepath.Join(dir, "cur"), 0o755)
	os.MkdirAll(filepath.Join(dir, "new"), 0o755)
	os.MkdirAll(filepath.Join(dir, ".Sent", "cur"), 0o755)

	// Add a seen message to cur.
	msg1 := "From: a@b.com\r\nSubject: Test\r\n\r\nBody"
	os.WriteFile(filepath.Join(dir, "cur", "1234.host:2,S"), []byte(msg1), 0o644)

	// Add a new message.
	msg2 := "From: c@d.com\r\nSubject: New\r\n\r\nNew body"
	os.WriteFile(filepath.Join(dir, "new", "5678.host"), []byte(msg2), 0o644)

	// Add a sent message.
	msg3 := "From: me@b.com\r\nSubject: Sent\r\n\r\nSent body"
	os.WriteFile(filepath.Join(dir, ".Sent", "cur", "9012.host:2,S"), []byte(msg3), 0o644)

	src := NewMaildirSource(dir)

	folders, err := src.Folders()
	if err != nil {
		t.Fatalf("Folders: %v", err)
	}

	// Should have INBOX and Sent.
	folderSet := make(map[string]bool)
	for _, f := range folders {
		folderSet[f] = true
	}
	if !folderSet["INBOX"] {
		t.Error("missing INBOX folder")
	}
	if !folderSet["Sent"] {
		t.Error("missing Sent folder")
	}

	// Read INBOX messages.
	msgCh, errCh := src.Messages(context.Background(), "INBOX")
	var inboxMsgs []Message
	for msg := range msgCh {
		inboxMsgs = append(inboxMsgs, msg)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Messages error: %v", err)
		}
	default:
	}

	if len(inboxMsgs) != 2 {
		t.Fatalf("expected 2 INBOX messages, got %d", len(inboxMsgs))
	}
}

func TestEMLSource(t *testing.T) {
	dir := t.TempDir()

	eml := "From: a@b.com\r\nTo: c@d.com\r\nSubject: Test EML\r\nDate: Mon, 02 Jan 2006 15:04:05 -0700\r\n\r\nBody text"
	path := filepath.Join(dir, "test.eml")
	os.WriteFile(path, []byte(eml), 0o644)

	src := NewEMLSource([]string{path}, "INBOX")

	folders, _ := src.Folders()
	if len(folders) != 1 || folders[0] != "INBOX" {
		t.Errorf("Folders = %v, want [INBOX]", folders)
	}

	msgCh, errCh := src.Messages(context.Background(), "INBOX")
	var msgs []Message
	for msg := range msgCh {
		msgs = append(msgs, msg)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Messages error: %v", err)
		}
	default:
	}

	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].InternalDate.IsZero() {
		t.Error("expected non-zero internal date")
	}
}

func TestState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	s, err := NewState(path)
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}

	if s.IsImported("INBOX", "1") {
		t.Error("expected not imported")
	}

	s.MarkImported("INBOX", "1")
	s.MarkImported("INBOX", "2")

	if !s.IsImported("INBOX", "1") {
		t.Error("expected imported after mark")
	}
	if s.ImportedCount("INBOX") != 2 {
		t.Errorf("ImportedCount = %d, want 2", s.ImportedCount("INBOX"))
	}

	// Duplicate mark should be idempotent.
	s.MarkImported("INBOX", "1")
	if s.ImportedCount("INBOX") != 2 {
		t.Errorf("ImportedCount after dup = %d, want 2", s.ImportedCount("INBOX"))
	}

	// Save and reload.
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	s2, err := NewState(path)
	if err != nil {
		t.Fatalf("NewState reload: %v", err)
	}
	if !s2.IsImported("INBOX", "1") {
		t.Error("expected imported after reload")
	}
	if !s2.IsImported("INBOX", "2") {
		t.Error("expected imported 2 after reload")
	}
	if s2.IsImported("INBOX", "3") {
		t.Error("expected 3 not imported")
	}
}

func TestStateInMemory(t *testing.T) {
	s, err := NewState("")
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}

	s.MarkImported("INBOX", "1")
	if !s.IsImported("INBOX", "1") {
		t.Error("expected imported")
	}

	// Save should be a no-op.
	if err := s.Save(); err != nil {
		t.Errorf("Save: %v", err)
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{5 * time.Second, "5.0s"},
		{90 * time.Second, "1m30s"},
	}

	for _, tt := range tests {
		got := FormatDuration(tt.d)
		if got != tt.want {
			t.Errorf("FormatDuration(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
