package templates

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestSharedRightsHelpers(t *testing.T) {
	cases := []struct {
		rights                string
		seen, flag, del, read bool
	}{
		{"", false, false, false, false},
		{"l", false, false, false, false},
		{"lr", false, false, false, true},
		{"lrs", true, false, false, true},
		{"lrsw", true, true, false, true},
		{"lrt", false, false, false, true},
		{"lrte", false, false, true, true},
		{"lrswite", true, true, true, true},
		{"lrswipkxtea", true, true, true, true},
	}
	for _, tc := range cases {
		if got := SharedCanSeen(tc.rights); got != tc.seen {
			t.Errorf("SharedCanSeen(%q) = %v", tc.rights, got)
		}
		if got := SharedCanFlag(tc.rights); got != tc.flag {
			t.Errorf("SharedCanFlag(%q) = %v", tc.rights, got)
		}
		if got := SharedCanDelete(tc.rights); got != tc.del {
			t.Errorf("SharedCanDelete(%q) = %v", tc.rights, got)
		}
		if got := SharedCanRead(tc.rights); got != tc.read {
			t.Errorf("SharedCanRead(%q) = %v", tc.rights, got)
		}
	}
}

func TestSharedFolderURLEscapesIDs(t *testing.T) {
	if got := SharedFolderURL("a/b", "0123456789abcdef0123456789abcdef"); got != "/shared/mail/a%2Fb/0123456789abcdef0123456789abcdef" {
		t.Errorf("url = %q", got)
	}
}

func TestSharedSidebarSection(t *testing.T) {
	folders := []SharedMailFolder{
		{OwnerLabel: "alice@d.test", OwnerID: "a", ID: "id1", Name: "Projects", Unread: 3, Counted: true},
		{OwnerLabel: "alice@d.test", OwnerID: "a", ID: "id2", Name: "Lookup", Unread: 9, Counted: false},
		{OwnerLabel: "bob@d.test", OwnerID: "b", ID: "id3", Name: "INBOX"},
	}
	ctx := WithSharedMailFolders(WithPendingShareCount(context.Background(), 2), folders)
	ctx = WithSharedSelected(ctx, "b", "id3")
	var buf bytes.Buffer
	if err := sharedSidebarSection().Render(ctx, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"Shared with me", "Invitations (2)", `href="/shares"`,
		`data-shared-folder="id1"`, `data-shared-unread="id1"`,
		"alice@d.test", "bob@d.test",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q", want)
		}
	}
	if strings.Contains(out, `data-shared-unread="id2"`) {
		t.Error("a folder without the read right shows a count")
	}
	if strings.Contains(out, "data-unread-for") || strings.Contains(out, "data-folder=") {
		t.Error("shared rows use the own-folder attributes")
	}
	if strings.Count(out, `class="active"`) != 1 || !strings.Contains(out, `class="active"`) {
		t.Errorf("want exactly one selected row, got %d", strings.Count(out, `class="active"`))
	}
	if strings.Index(out, "alice@d.test") > strings.Index(out, "bob@d.test") {
		t.Error("owners are not in order")
	}

	// Nothing to show: no section at all.
	buf.Reset()
	if err := sharedSidebarSection().Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(buf.String()) != "" {
		t.Errorf("empty section rendered %q", buf.String())
	}
}
