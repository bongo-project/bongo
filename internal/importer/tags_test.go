package importer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/emersion/go-imap/v2"
)

func TestExtractGmailLabels(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{
			name: "single user label",
			raw:  "From: a@b.com\r\nX-Gmail-Labels: Work\r\nSubject: Test\r\n\r\nBody",
			want: []string{"work"},
		},
		{
			name: "multiple user labels",
			raw:  "From: a@b.com\r\nX-Gmail-Labels: Work,Personal,Finance\r\nSubject: Test\r\n\r\nBody",
			want: []string{"work", "personal", "finance"},
		},
		{
			name: "system labels excluded",
			raw:  "From: a@b.com\r\nX-Gmail-Labels: Inbox,Sent,Starred,Spam,Drafts,Trash\r\nSubject: Test\r\n\r\nBody",
			want: nil,
		},
		{
			name: "Important excluded",
			raw:  "From: a@b.com\r\nX-Gmail-Labels: Important,Work\r\nSubject: Test\r\n\r\nBody",
			want: []string{"work"},
		},
		{
			name: "Starred excluded becomes flagged flag",
			raw:  "From: a@b.com\r\nX-Gmail-Labels: Starred,Work\r\nSubject: Test\r\n\r\nBody",
			want: []string{"work"},
		},
		{
			name: "no header",
			raw:  "From: a@b.com\r\nSubject: Test\r\n\r\nBody",
			want: nil,
		},
		{
			name: "label with spaces normalised",
			raw:  "From: a@b.com\r\nX-Gmail-Labels: My Projects\r\nSubject: Test\r\n\r\nBody",
			want: []string{"my-projects"},
		},
		{
			name: "deduplication",
			raw:  "From: a@b.com\r\nX-Gmail-Labels: Work,Work,Finance\r\nSubject: Test\r\n\r\nBody",
			want: []string{"work", "finance"},
		},
		{
			name: "all mail excluded",
			raw:  "From: a@b.com\r\nX-Gmail-Labels: All Mail,Work\r\nSubject: Test\r\n\r\nBody",
			want: []string{"work"},
		},
		{
			name: "mixed case system label excluded",
			raw:  "From: a@b.com\r\nX-Gmail-Labels: INBOX,MyTag\r\nSubject: Test\r\n\r\nBody",
			want: []string{"mytag"},
		},
		{
			name: "header case insensitive",
			raw:  "From: a@b.com\r\nx-gmail-labels: Work\r\nSubject: Test\r\n\r\nBody",
			want: []string{"work"},
		},
		{
			name: "folded header",
			raw:  "From: a@b.com\r\nX-Gmail-Labels: Work,\r\n Personal\r\nSubject: Test\r\n\r\nBody",
			want: []string{"work", "personal"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractGmailLabels([]byte(tt.raw))
			if len(got) != len(tt.want) {
				t.Errorf("ExtractGmailLabels() = %v, want %v", got, tt.want)
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("ExtractGmailLabels()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestExtractIMAPKeywords(t *testing.T) {
	tests := []struct {
		name  string
		flags []imap.Flag
		want  []string
	}{
		{
			name:  "no keywords",
			flags: []imap.Flag{imap.FlagSeen, imap.FlagAnswered},
			want:  nil,
		},
		{
			name:  "dollar prefix keyword",
			flags: []imap.Flag{imap.FlagSeen, "$label1"},
			want:  []string{"label1"},
		},
		{
			name:  "multiple keywords",
			flags: []imap.Flag{"$label1", "$label2", imap.FlagFlagged},
			want:  []string{"label1", "label2"},
		},
		{
			name:  "plain keyword without dollar",
			flags: []imap.Flag{"Work", "Finance"},
			want:  []string{"work", "finance"},
		},
		{
			name:  "mixed standard and custom flags",
			flags: []imap.Flag{imap.FlagSeen, imap.FlagDraft, "Project-Alpha"},
			want:  []string{"project-alpha"},
		},
		{
			name:  "empty flags",
			flags: nil,
			want:  nil,
		},
		{
			name:  "all standard flags excluded",
			flags: []imap.Flag{imap.FlagSeen, imap.FlagAnswered, imap.FlagFlagged, imap.FlagDeleted, imap.FlagDraft},
			want:  nil,
		},
		{
			name:  "keyword normalised with underscore",
			flags: []imap.Flag{"My_Tag"},
			want:  []string{"my-tag"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractIMAPKeywords(tt.flags)
			if len(got) != len(tt.want) {
				t.Errorf("ExtractIMAPKeywords(%v) = %v, want %v", tt.flags, got, tt.want)
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("ExtractIMAPKeywords()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestExtractThunderbirdTags(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{
			name: "single label",
			raw:  "From: a@b.com\r\nX-Mozilla-Keys: $label1\r\nSubject: Test\r\n\r\nBody",
			want: []string{"label1"},
		},
		{
			name: "multiple labels",
			raw:  "From: a@b.com\r\nX-Mozilla-Keys: $label1 $label2\r\nSubject: Test\r\n\r\nBody",
			want: []string{"label1", "label2"},
		},
		{
			name: "no header",
			raw:  "From: a@b.com\r\nSubject: Test\r\n\r\nBody",
			want: nil,
		},
		{
			name: "header case insensitive",
			raw:  "From: a@b.com\r\nx-mozilla-keys: $label1\r\nSubject: Test\r\n\r\nBody",
			want: []string{"label1"},
		},
		{
			name: "without dollar prefix",
			raw:  "From: a@b.com\r\nX-Mozilla-Keys: work finance\r\nSubject: Test\r\n\r\nBody",
			want: []string{"work", "finance"},
		},
		{
			name: "keyword normalised",
			raw:  "From: a@b.com\r\nX-Mozilla-Keys: My_Tag\r\nSubject: Test\r\n\r\nBody",
			want: []string{"my-tag"},
		},
		{
			name: "deduplication",
			raw:  "From: a@b.com\r\nX-Mozilla-Keys: $label1 $label1\r\nSubject: Test\r\n\r\nBody",
			want: []string{"label1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractThunderbirdTags([]byte(tt.raw))
			if len(got) != len(tt.want) {
				t.Errorf("ExtractThunderbirdTags() = %v, want %v", got, tt.want)
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("ExtractThunderbirdTags()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestExtractHeaderValue(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		lowerName string
		want      string
	}{
		{
			name:      "present header",
			raw:       "From: a@b.com\r\nX-Gmail-Labels: Work\r\n\r\nBody",
			lowerName: "x-gmail-labels",
			want:      "Work",
		},
		{
			name:      "missing header",
			raw:       "From: a@b.com\r\n\r\nBody",
			lowerName: "x-gmail-labels",
			want:      "",
		},
		{
			name:      "header in body ignored",
			raw:       "From: a@b.com\r\n\r\nX-Gmail-Labels: Work",
			lowerName: "x-gmail-labels",
			want:      "",
		},
		{
			name:      "case insensitive match",
			raw:       "From: a@b.com\r\nX-GMAIL-LABELS: Work\r\n\r\nBody",
			lowerName: "x-gmail-labels",
			want:      "Work",
		},
		{
			name:      "folded header",
			raw:       "From: a@b.com\r\nX-Gmail-Labels: Work,\r\n Finance\r\nSubject: hi\r\n\r\nBody",
			lowerName: "x-gmail-labels",
			want:      "Work, Finance",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractHeaderValue([]byte(tt.raw), tt.lowerName)
			if got != tt.want {
				t.Errorf("extractHeaderValue(%q) = %q, want %q", tt.lowerName, got, tt.want)
			}
		})
	}
}

func TestDiffTags(t *testing.T) {
	tests := []struct {
		name      string
		candidate []string
		existing  [][]string
		want      []string
	}{
		{
			name:      "all new",
			candidate: []string{"work", "finance"},
			existing:  [][]string{},
			want:      []string{"work", "finance"},
		},
		{
			name:      "all already present",
			candidate: []string{"work", "finance"},
			existing:  [][]string{{"work", "finance"}},
			want:      nil,
		},
		{
			name:      "partial overlap",
			candidate: []string{"work", "finance", "receipts"},
			existing:  [][]string{{"work"}},
			want:      []string{"finance", "receipts"},
		},
		{
			name:      "overlap across multiple existing sets",
			candidate: []string{"work", "finance", "receipts"},
			existing:  [][]string{{"work"}, {"finance"}},
			want:      []string{"receipts"},
		},
		{
			name:      "no duplicates in output",
			candidate: []string{"work", "work", "finance"},
			existing:  [][]string{},
			want:      []string{"work", "finance"},
		},
		{
			name:      "empty candidate",
			candidate: nil,
			existing:  [][]string{{"work"}},
			want:      nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := diffTags(tt.candidate, tt.existing...)
			if len(got) != len(tt.want) {
				t.Fatalf("diffTags() = %v, want %v", got, tt.want)
			}
			for i, g := range got {
				if g != tt.want[i] {
					t.Errorf("diffTags()[%d] = %q, want %q", i, g, tt.want[i])
				}
			}
		})
	}
}

func TestMboxSourceExtractsTags(t *testing.T) {
	raw := "From sender@example.com Mon Jan 1 00:00:00 2024\r\n" +
		"From: sender@example.com\r\n" +
		"To: recipient@example.com\r\n" +
		"Subject: Tagged message\r\n" +
		"X-Gmail-Labels: Work,Finance\r\n" +
		"Date: Mon, 01 Jan 2024 00:00:00 +0000\r\n" +
		"\r\n" +
		"Hello world\r\n"

	dir := t.TempDir()
	path := filepath.Join(dir, "test.mbox")
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	src := NewMboxSource([]string{path}, "INBOX")
	msgCh, errCh := src.Messages(context.Background(), "INBOX")
	var msgs []Message
	for m := range msgCh {
		msgs = append(msgs, m)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("Messages error: %v", err)
	}
	if len(msgs) == 0 {
		t.Fatal("expected at least one message")
	}
	tags := msgs[0].Tags
	if len(tags) != 2 {
		t.Fatalf("expected 2 tags, got %v", tags)
	}
	if tags[0] != "work" || tags[1] != "finance" {
		t.Errorf("tags = %v, want [work finance]", tags)
	}
}

func TestMboxSourceSystemLabelsExcluded(t *testing.T) {
	raw := "From sender@example.com Mon Jan 1 00:00:00 2024\r\n" +
		"From: sender@example.com\r\n" +
		"To: recipient@example.com\r\n" +
		"Subject: System labels only\r\n" +
		"X-Gmail-Labels: Inbox,Sent,Starred\r\n" +
		"Date: Mon, 01 Jan 2024 00:00:00 +0000\r\n" +
		"\r\n" +
		"Hello world\r\n"

	dir := t.TempDir()
	path := filepath.Join(dir, "test.mbox")
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	src := NewMboxSource([]string{path}, "INBOX")
	msgCh, errCh := src.Messages(context.Background(), "INBOX")
	var msgs []Message
	for m := range msgCh {
		msgs = append(msgs, m)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("Messages error: %v", err)
	}
	if len(msgs) == 0 {
		t.Fatal("expected at least one message")
	}
	if len(msgs[0].Tags) != 0 {
		t.Errorf("expected no tags, got %v", msgs[0].Tags)
	}
}

func TestEMLSourceExtractsTags(t *testing.T) {
	raw := "From: sender@example.com\r\n" +
		"To: recipient@example.com\r\n" +
		"Subject: Tagged EML\r\n" +
		"X-Mozilla-Keys: $label1 $label2\r\n" +
		"Date: Mon, 01 Jan 2024 00:00:00 +0000\r\n" +
		"\r\n" +
		"Body text\r\n"

	dir := t.TempDir()
	path := filepath.Join(dir, "test.eml")
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	src := NewEMLSource([]string{path}, "INBOX")
	msgCh, errCh := src.Messages(context.Background(), "INBOX")
	var msgs []Message
	for m := range msgCh {
		msgs = append(msgs, m)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("Messages error: %v", err)
	}
	if len(msgs) == 0 {
		t.Fatal("expected at least one message")
	}
	tags := msgs[0].Tags
	if len(tags) != 2 {
		t.Fatalf("expected 2 tags, got %v", tags)
	}
	if tags[0] != "label1" || tags[1] != "label2" {
		t.Errorf("tags = %v, want [label1 label2]", tags)
	}
}
