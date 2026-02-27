package importer

import (
	"sort"
	"testing"

	"github.com/emersion/go-imap/v2"
)

func TestGmailFolderMap(t *testing.T) {
	attrs := map[string][]imap.MailboxAttr{
		"INBOX":                  {},
		"[Gmail]":                {imap.MailboxAttrNoSelect},
		"[Gmail]/Sent Mail":      {imap.MailboxAttrSent},
		"[Gmail]/Drafts":         {imap.MailboxAttrDrafts},
		"[Gmail]/Trash":          {imap.MailboxAttrTrash},
		"[Gmail]/Spam":           {imap.MailboxAttrJunk},
		"[Gmail]/Starred":        {imap.MailboxAttrFlagged},
		"[Gmail]/All Mail":       {imap.MailboxAttrAll},
		"[Gmail]/Important":      {imap.MailboxAttrImportant},
		"Work":                   {},
		"Personal":               {},
	}

	folderMap, exclude, lastFolders := GmailFolderMap(attrs)

	// Check mappings.
	wantMap := map[string]string{
		"[Gmail]/Sent Mail": "Sent",
		"[Gmail]/Drafts":    "Drafts",
		"[Gmail]/Trash":     "Trash",
		"[Gmail]/Spam":      "Junk",
		"[Gmail]/Starred":   "Starred",
		"[Gmail]/All Mail":  "Archive",
	}

	for src, dst := range wantMap {
		if got, ok := folderMap[src]; !ok {
			t.Errorf("missing mapping for %q", src)
		} else if got != dst {
			t.Errorf("folderMap[%q] = %q, want %q", src, got, dst)
		}
	}

	// INBOX and user labels should not be mapped.
	if _, ok := folderMap["INBOX"]; ok {
		t.Error("INBOX should not be mapped")
	}
	if _, ok := folderMap["Work"]; ok {
		t.Error("Work should not be mapped")
	}

	// Check excludes.
	sort.Strings(exclude)
	wantExclude := []string{"[Gmail]", "[Gmail]/Important"}
	sort.Strings(wantExclude)
	if len(exclude) != len(wantExclude) {
		t.Errorf("exclude = %v, want %v", exclude, wantExclude)
	} else {
		for i := range exclude {
			if exclude[i] != wantExclude[i] {
				t.Errorf("exclude[%d] = %q, want %q", i, exclude[i], wantExclude[i])
			}
		}
	}

	// Check lastFolders.
	if len(lastFolders) != 1 || lastFolders[0] != "[Gmail]/All Mail" {
		t.Errorf("lastFolders = %v, want [\"[Gmail]/All Mail\"]", lastFolders)
	}
}

func TestGmailFolderMapGermanLocale(t *testing.T) {
	// German Gmail uses [Google Mail] prefix and German names.
	attrs := map[string][]imap.MailboxAttr{
		"INBOX":                               {},
		"[Google Mail]":                        {imap.MailboxAttrNoSelect},
		"[Google Mail]/Gesendet":               {imap.MailboxAttrSent},
		"[Google Mail]/Entwürfe":             {imap.MailboxAttrDrafts},
		"[Google Mail]/Papierkorb":             {imap.MailboxAttrTrash},
		"[Google Mail]/Alle Nachrichten":       {imap.MailboxAttrAll},
	}

	folderMap, exclude, lastFolders := GmailFolderMap(attrs)

	if folderMap["[Google Mail]/Gesendet"] != "Sent" {
		t.Errorf("German Sent mapping = %q, want Sent", folderMap["[Google Mail]/Gesendet"])
	}
	if folderMap["[Google Mail]/Entwürfe"] != "Drafts" {
		t.Errorf("German Drafts mapping = %q, want Drafts", folderMap["[Google Mail]/Entwürfe"])
	}
	if folderMap["[Google Mail]/Papierkorb"] != "Trash" {
		t.Errorf("German Trash mapping = %q, want Trash", folderMap["[Google Mail]/Papierkorb"])
	}
	if folderMap["[Google Mail]/Alle Nachrichten"] != "Archive" {
		t.Errorf("German All Mail mapping = %q, want Archive", folderMap["[Google Mail]/Alle Nachrichten"])
	}

	// [Google Mail] container should be excluded.
	found := false
	for _, e := range exclude {
		if e == "[Google Mail]" {
			found = true
		}
	}
	if !found {
		t.Error("[Google Mail] should be excluded")
	}

	if len(lastFolders) != 1 || lastFolders[0] != "[Google Mail]/Alle Nachrichten" {
		t.Errorf("lastFolders = %v, want [\"[Google Mail]/Alle Nachrichten\"]", lastFolders)
	}
}

func TestGmailFolderMapNoRemap(t *testing.T) {
	// If the folder is already named correctly, no mapping should be added.
	attrs := map[string][]imap.MailboxAttr{
		"Sent":   {imap.MailboxAttrSent},
		"Drafts": {imap.MailboxAttrDrafts},
	}

	folderMap, _, _ := GmailFolderMap(attrs)

	if len(folderMap) != 0 {
		t.Errorf("folderMap should be empty when names already match, got %v", folderMap)
	}
}

func TestExtractMessageID(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			"normal",
			"From: a@b.com\r\nMessage-ID: <abc123@example.com>\r\nSubject: Test\r\n\r\nBody",
			"<abc123@example.com>",
		},
		{
			"case insensitive",
			"From: a@b.com\r\nmessage-id: <lower@example.com>\r\nSubject: Test\r\n\r\nBody",
			"<lower@example.com>",
		},
		{
			"no message-id",
			"From: a@b.com\r\nSubject: Test\r\n\r\nBody",
			"",
		},
		{
			"empty body",
			"Message-ID: <only-header@example.com>\r\n\r\n",
			"<only-header@example.com>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractMessageID([]byte(tt.raw))
			if got != tt.want {
				t.Errorf("extractMessageID() = %q, want %q", got, tt.want)
			}
		})
	}
}
